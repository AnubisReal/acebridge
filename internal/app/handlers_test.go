package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anubisreal/acebridge/internal/playlist"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

const testM3U = `#EXTM3U
#EXTINF:-1 tvg-id="demo" tvg-name="Demo TV" group-title="General",Demo TV
acestream://0123456789abcdef0123456789abcdef01234567`

func testApp(t *testing.T) *App {
	t.Helper()
	a, err := New(Config{ListenAddr: ":0", DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func perform(handler http.Handler, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestFileImportAndPlaylist(t *testing.T) {
	a := testApp(t)
	handler := a.Routes()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "Demo")
	part, _ := writer.CreateFormFile("file", "demo.m3u")
	_, _ = part.Write([]byte(testM3U))
	_ = writer.Close()

	response := perform(handler, http.MethodPost, "/api/v1/sources/file", &body, writer.FormDataContentType())
	if response.Code != http.StatusCreated {
		t.Fatalf("import status %d: %s", response.Code, response.Body.String())
	}

	response = perform(handler, http.MethodGet, "/api/v1/channels", nil, "")
	var channels []Channel
	if err := json.Unmarshal(response.Body.Bytes(), &channels); err != nil || len(channels) != 1 {
		t.Fatalf("channels: %s", response.Body.String())
	}

	response = perform(handler, http.MethodGet, "/playlist.m3u", nil, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Demo TV") || !strings.Contains(response.Body.String(), "/api/v1/channels/1/stream") {
		t.Fatalf("playlist: %s", response.Body.String())
	}
}

func TestURLJSONSourceImport(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://example.com/hashes.json" {
			t.Fatalf("unexpected source URL: %s", request.URL)
		}
		body := `{"count":1,"hashes":[{"title":"Canal JSON","hash":"0123456789abcdef0123456789abcdef01234567","group":"Deportes","logo":"https://example.com/logo.png","tvg_id":"canal.json"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	a.sourceClient = a.client

	response := perform(a.Routes(), http.MethodPost, "/api/v1/sources/url", strings.NewReader(`{"name":"Catálogo JSON","url":"https://example.com/hashes.json"}`), "application/json")
	if response.Code != http.StatusCreated {
		t.Fatalf("create source status %d: %s", response.Code, response.Body.String())
	}
	var channels []Channel
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response = perform(a.Routes(), http.MethodGet, "/api/v1/channels", nil, "")
		channels = nil
		if json.Unmarshal(response.Body.Bytes(), &channels) == nil && len(channels) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(channels) != 1 || channels[0].Name != "Canal JSON" || channels[0].Group != "Deportes" || channels[0].TVGID != "canal.json" {
		t.Fatalf("unexpected channels: %s", response.Body.String())
	}
}

func TestURLSourceCreationDoesNotWaitForDownload(t *testing.T) {
	a := testApp(t)
	downloadStarted := make(chan struct{}, 1)
	releaseDownload := make(chan struct{})
	defer close(releaseDownload)
	a.client = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		downloadStarted <- struct{}{}
		<-releaseDownload
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(testM3U)), Header: make(http.Header)}, nil
	})}
	a.sourceClient = a.client

	started := time.Now()
	response := perform(a.Routes(), http.MethodPost, "/api/v1/sources/url", strings.NewReader(`{"name":"Fuente lenta","url":"https://example.com/slow.m3u"}`), "application/json")
	if response.Code != http.StatusCreated {
		t.Fatalf("create source status %d: %s", response.Code, response.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("source creation waited for download: %s", elapsed)
	}
	select {
	case <-downloadStarted:
	case <-time.After(time.Second):
		t.Fatal("background source download did not start")
	}
}

func TestSettingsValidation(t *testing.T) {
	a := testApp(t)
	response := perform(a.Routes(), http.MethodPut, "/api/v1/settings", strings.NewReader(`{"engine_url":"file:///tmp/x","public_base_url":"","update_interval_hours":4}`), "application/json")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", response.Code)
	}
}

func TestCreateManualChannel(t *testing.T) {
	a := testApp(t)
	body := `{"name":"Canal privado","acestream_id":"0123456789abcdef0123456789abcdef01234567","group":"Personal","logo":"https://example.com/logo.png"}`
	response := perform(a.Routes(), http.MethodPost, "/api/v1/channels/manual", strings.NewReader(body), "application/json")
	if response.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", response.Code, response.Body.String())
	}
	var channel Channel
	if err := json.Unmarshal(response.Body.Bytes(), &channel); err != nil || channel.Name != "Canal privado" || channel.SourceName != "Canales manuales" {
		t.Fatalf("unexpected channel: %s", response.Body.String())
	}
}

func TestHistoryIncludesCurrentChannelLogo(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Manual','manual');
		INSERT INTO channels(id,source_id,name,logo,acestream_id,source_acestream_id) VALUES (1,1,'Canal Uno','https://example.com/uno.svg','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567');
		INSERT INTO playback_history(channel_id,channel_name,started_at) VALUES (1,'Canal Uno',CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	response := perform(a.Routes(), http.MethodGet, "/api/v1/history", nil, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"channel_logo":"https://example.com/uno.svg"`) {
		t.Fatalf("history response: %s", response.Body.String())
	}
}

func TestHistoryFindsLogoByNameForLegacyEntry(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Manual','manual');
		INSERT INTO channels(id,source_id,name,logo,acestream_id,source_acestream_id) VALUES (1,1,'Canal antiguo **','https://example.com/legacy.svg','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567');
		INSERT INTO playback_history(channel_id,channel_name,started_at) VALUES (NULL,'Canal antiguo *',CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	response := perform(a.Routes(), http.MethodGet, "/api/v1/history", nil, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"channel_logo":"https://example.com/legacy.svg"`) {
		t.Fatalf("legacy history response: %s", response.Body.String())
	}
}

func TestEditedChannelMetadataSurvivesSourceRefresh(t *testing.T) {
	a := testApp(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "Original source")
	part, _ := writer.CreateFormFile("file", "demo.m3u")
	_, _ = part.Write([]byte(testM3U))
	_ = writer.Close()
	created := perform(a.Routes(), http.MethodPost, "/api/v1/sources/file", &body, writer.FormDataContentType())
	if created.Code != http.StatusCreated {
		t.Fatalf("import: %s", created.Body.String())
	}

	updated := perform(a.Routes(), http.MethodPatch, "/api/v1/channels/1", strings.NewReader(`{"name":"Mi nombre","group":"Favoritos","tvg_id":"custom.id","logo":"","acestream_id":"abcdef0123456789abcdef0123456789abcdef01"}`), "application/json")
	if updated.Code != http.StatusOK {
		t.Fatalf("update: %s", updated.Body.String())
	}
	if _, _, _, err := a.replaceChannels(context.Background(), 1, playlist.Parse(testM3U)); err != nil {
		t.Fatal(err)
	}

	var name, group, aceStreamID, sourceAceStreamID string
	var locked int
	if err := a.db.QueryRow(`SELECT name,group_name,metadata_locked,acestream_id,source_acestream_id FROM channels WHERE source_id=1`).Scan(&name, &group, &locked, &aceStreamID, &sourceAceStreamID); err != nil {
		t.Fatal(err)
	}
	if name != "Mi nombre" || group != "Favoritos" || locked != 1 || aceStreamID != "abcdef0123456789abcdef0123456789abcdef01" || sourceAceStreamID != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("edit lost after refresh: %q %q locked=%d ace=%q source=%q", name, group, locked, aceStreamID, sourceAceStreamID)
	}
}

func TestEditSource(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(name,kind,url) VALUES ('Old','url','https://example.com/old.m3u')`)
	if err != nil {
		t.Fatal(err)
	}
	response := perform(a.Routes(), http.MethodPatch, "/api/v1/sources/1", strings.NewReader(`{"name":"New","url":"https://example.com/new.m3u","enabled":false}`), "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("update: %s", response.Body.String())
	}
	var name, sourceURL string
	var enabled int
	if err := a.db.QueryRow(`SELECT name,url,enabled FROM sources WHERE id=1`).Scan(&name, &sourceURL, &enabled); err != nil {
		t.Fatal(err)
	}
	if name != "New" || sourceURL != "https://example.com/new.m3u" || enabled != 0 {
		t.Fatalf("unexpected source: %q %q %d", name, sourceURL, enabled)
	}
}

func TestEngineCheckUsesVersionEndpoint(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/webui/api/service" || request.URL.Query().Get("method") != "get_version" {
			t.Fatalf("unexpected probe URL: %s", request.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"result":{"version":"3.2.4","code":3020400,"platform":"linux"},"error":null}`)), Header: make(http.Header)}, nil
	})}

	response := perform(a.Routes(), http.MethodPost, "/api/v1/engine/check", nil, "")
	var result struct {
		Operational bool   `json:"operational"`
		Version     string `json:"version"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.Operational || result.Version != "3.2.4" {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestEngineCheckRejectsHTTP500(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("error")), Header: make(http.Header)}, nil
	})}

	response := perform(a.Routes(), http.MethodPost, "/api/v1/engine/check", nil, "")
	var result struct {
		Online      bool `json:"online"`
		Operational bool `json:"operational"`
		Status      int  `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.Online || result.Operational || result.Status != 500 {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestChannelStreamProxiesAndRewritesHLS(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(name,kind) VALUES ('Manual','manual')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec(`INSERT INTO channels(source_id,name,acestream_id,source_acestream_id) VALUES (1,'Canal','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567')`)
	if err != nil {
		t.Fatal(err)
	}
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/ace/manifest.m3u8" && request.URL.Query().Get("format") == "json" {
			body := `{"response":{"playback_url":"http://127.0.0.1:6878/ace/m/hash/session.m3u8"},"error":null}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request}, nil
		}
		if request.URL.Path != "/ace/m/hash/session.m3u8" {
			t.Fatalf("unexpected stream request: %s", request.URL)
		}
		body := "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nsegment-1.ts\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/vnd.apple.mpegurl"}}, Request: request}, nil
	})}

	response := perform(a.Routes(), http.MethodGet, "/api/v1/channels/1/stream", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("stream status %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "/api/v1/channels/1/stream?url=") || !strings.Contains(response.Body.String(), "segment-1.ts") {
		t.Fatalf("manifest was not rewritten: %s", response.Body.String())
	}
}

func TestStartChannelPlaybackUsesEnginePlaybackURL(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(name,kind) VALUES ('Manual','manual')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec(`INSERT INTO channels(source_id,name,acestream_id,source_acestream_id) VALUES (1,'Canal','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567')`)
	if err != nil {
		t.Fatal(err)
	}
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasPrefix(request.URL.Path, "/ace/cmd/") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}, Request: request}, nil
		}
		if request.URL.Path != "/ace/manifest.m3u8" || request.URL.Query().Get("format") != "json" || request.URL.Query().Get("id") != "0123456789abcdef0123456789abcdef01234567" {
			t.Fatalf("unexpected playback request: %s", request.URL)
		}
		body := `{"response":{"playback_url":"http://127.0.0.1:6878/ace/m/hash/session.m3u8","command_url":"http://127.0.0.1:6878/ace/cmd/hash/session"},"error":null}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request}, nil
	})}

	response := perform(a.Routes(), http.MethodPost, "/api/v1/channels/1/playback", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("playback status %d: %s", response.Code, response.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["playback_url"] != "http://127.0.0.1:6878/ace/m/hash/session.m3u8" || !strings.Contains(result["proxy_url"], "/api/v1/channels/1/stream?url=") {
		t.Fatalf("unexpected playback result: %#v", result)
	}
}

func TestSynchronizationPreservesIDsAndDeletesMissingChannels(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	_, _ = a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Lista','url')`)
	first := []playlist.Channel{{Name: "Uno", AceStreamID: "0123456789abcdef0123456789abcdef01234567"}, {Name: "Dos", AceStreamID: "1123456789abcdef0123456789abcdef01234567"}}
	if _, _, _, err := a.replaceChannels(context.Background(), 1, first); err != nil {
		t.Fatal(err)
	}
	var preservedID int64
	_ = a.db.QueryRow(`SELECT id FROM channels WHERE name='Uno'`).Scan(&preservedID)
	second := []playlist.Channel{{Name: "Uno actualizado", AceStreamID: "0123456789abcdef0123456789abcdef01234567"}}
	added, updated, removed, err := a.replaceChannels(context.Background(), 1, second)
	if err != nil {
		t.Fatal(err)
	}
	var currentID, count int64
	_ = a.db.QueryRow(`SELECT id FROM channels WHERE name='Uno actualizado'`).Scan(&currentID)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&count)
	if currentID != preservedID || count != 1 || added != 0 || updated != 1 || removed != 1 {
		t.Fatalf("unexpected upsert result id=%d/%d count=%d stats=%d,%d,%d", preservedID, currentID, count, added, updated, removed)
	}
}

func TestDisabledSourceIsExcludedFromPlaylist(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	_, _ = a.db.Exec(`INSERT INTO sources(id,name,kind,enabled) VALUES (1,'Lista','url',0)`)
	_, _ = a.db.Exec(`INSERT INTO channels(source_id,name,acestream_id,source_acestream_id) VALUES (1,'Oculto','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567')`)
	response := perform(a.Routes(), http.MethodGet, "/playlist.m3u", nil, "")
	if strings.Contains(response.Body.String(), "Oculto") {
		t.Fatalf("disabled source leaked into playlist: %s", response.Body.String())
	}
}

func TestPlaybackSessionIsSharedAndRecordedOnce(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	_, _ = a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Manual','manual')`)
	_, _ = a.db.Exec(`INSERT INTO channels(source_id,name,acestream_id,source_acestream_id) VALUES (1,'Canal','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567')`)
	requests := 0
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		body := `{"response":{"playback_url":"http://127.0.0.1:6878/live/index.m3u8"}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: request}, nil
	})}
	if _, err := a.ensurePlayback(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ensurePlayback(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var history int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM playback_history`).Scan(&history)
	if requests != 1 || history != 1 || len(a.streams.list()) != 1 {
		t.Fatalf("requests=%d history=%d streams=%d", requests, history, len(a.streams.list()))
	}
}

func TestSourceSSRFProtection(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "169.254.169.254", "192.168.1.10"} {
		if !blockedSourceIP(net.ParseIP(raw), false) {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}
	if blockedSourceIP(net.ParseIP("8.8.8.8"), false) {
		t.Fatal("public address was blocked")
	}
}

func TestInactiveStreamIsReleased(t *testing.T) {
	a := testApp(t)
	defer a.Close()
	stops := 0
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		stops++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}, Request: request}, nil
	})}
	a.streams.items[7] = &managedStream{ChannelID: 7, CommandURL: "http://127.0.0.1:6878/ace/cmd/stop", LastAccess: time.Now().Add(-2 * time.Minute)}
	a.streams.reap()
	if len(a.streams.list()) != 0 || stops != 1 {
		t.Fatalf("inactive stream was not released: streams=%d stops=%d", len(a.streams.list()), stops)
	}
}

func TestConcurrentPlaybackRespectsGlobalStreamLimit(t *testing.T) {
	a := testApp(t)
	_, _ = a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Manual','manual');
		INSERT INTO channels(id,source_id,name,acestream_id,source_acestream_id) VALUES
		(1,1,'Uno','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567'),
		(2,1,'Dos','abcdef0123456789abcdef0123456789abcdef01','abcdef0123456789abcdef0123456789abcdef01');
		UPDATE settings SET value='1' WHERE key='max_streams'`)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-release
		body := `{"response":{"playback_url":"http://127.0.0.1:6878/live/index.m3u8"}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: request}, nil
	})}
	firstResult := make(chan error, 1)
	go func() {
		_, err := a.ensurePlayback(context.Background(), 1)
		firstResult <- err
	}()
	<-started
	if _, err := a.ensurePlayback(context.Background(), 2); err == nil || !strings.Contains(err.Error(), "maximum active streams") {
		t.Fatalf("second concurrent stream was not rejected: %v", err)
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
}

func TestViewerLifecycleTracksDistinctViewers(t *testing.T) {
	a := testApp(t)
	_, _ = a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Manual','manual');
		INSERT INTO channels(id,source_id,name,acestream_id,source_acestream_id) VALUES (1,1,'Canal','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567')`)
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"response":{"playback_url":"http://127.0.0.1:6878/live/index.m3u8"}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: request}, nil
	})}
	for range 2 {
		response := perform(a.Routes(), http.MethodPost, "/api/v1/channels/1/playback", nil, "")
		if response.Code != http.StatusOK {
			t.Fatalf("playback: %s", response.Body.String())
		}
	}
	streams := a.streams.list()
	if len(streams) != 1 || streams[0].Viewers != 2 {
		t.Fatalf("unexpected viewers: %#v", streams)
	}
	var result map[string]string
	response := perform(a.Routes(), http.MethodPost, "/api/v1/channels/1/playback", nil, "")
	_ = json.Unmarshal(response.Body.Bytes(), &result)
	closed := perform(a.Routes(), http.MethodDelete, "/api/v1/channels/1/viewers/"+result["viewer_id"], nil, "")
	if closed.Code != http.StatusNoContent || a.streams.list()[0].Viewers != 2 {
		t.Fatalf("viewer was not removed: status=%d viewers=%d", closed.Code, a.streams.list()[0].Viewers)
	}
}

func TestExpiredUpstreamManifestInvalidatesManagedSession(t *testing.T) {
	a := testApp(t)
	_, _ = a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Manual','manual');
		INSERT INTO channels(id,source_id,name,acestream_id,source_acestream_id) VALUES (1,1,'Canal','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567')`)
	starts := 0
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/ace/manifest.m3u8":
			starts++
			body := `{"response":{"playback_url":"http://127.0.0.1:6878/live/index.m3u8"}}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: request}, nil
		case "/live/index.m3u8":
			return &http.Response{StatusCode: http.StatusGone, Status: "410 Gone", Body: io.NopCloser(strings.NewReader("expired")), Header: http.Header{}, Request: request}, nil
		default:
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}, Request: request}, nil
		}
	})}
	started := perform(a.Routes(), http.MethodPost, "/api/v1/channels/1/playback", nil, "")
	var playback map[string]string
	_ = json.Unmarshal(started.Body.Bytes(), &playback)
	failed := perform(a.Routes(), http.MethodGet, playback["proxy_url"], nil, "")
	if failed.Code != http.StatusBadGateway || len(a.streams.list()) != 0 {
		t.Fatalf("expired session was retained: status=%d streams=%d", failed.Code, len(a.streams.list()))
	}
	restarted := perform(a.Routes(), http.MethodPost, "/api/v1/channels/1/playback", nil, "")
	if restarted.Code != http.StatusOK || starts != 2 || len(a.streams.list()) != 1 {
		t.Fatalf("session was not recreated: status=%d starts=%d streams=%d", restarted.Code, starts, len(a.streams.list()))
	}
}

func TestStreamProxyForwardsByteRange(t *testing.T) {
	a := testApp(t)
	_, _ = a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'Manual','manual');
		INSERT INTO channels(id,source_id,name,acestream_id,source_acestream_id) VALUES (1,1,'Canal','0123456789abcdef0123456789abcdef01234567','0123456789abcdef0123456789abcdef01234567')`)
	a.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Range") != "bytes=10-19" {
			t.Errorf("range header not forwarded: %q", request.Header.Get("Range"))
		}
		return &http.Response{StatusCode: http.StatusPartialContent, Body: io.NopCloser(strings.NewReader("0123456789")), Header: http.Header{"Content-Range": []string{"bytes 10-19/100"}}, Request: request}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/channels/1/stream?url=http%3A%2F%2F127.0.0.1%3A6878%2Flive%2Fsegment.m4s", nil)
	request.Header.Set("Range", "bytes=10-19")
	recorder := httptest.NewRecorder()
	a.Routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent || recorder.Header().Get("Content-Range") != "bytes 10-19/100" {
		t.Fatalf("unexpected ranged response: status=%d range=%q", recorder.Code, recorder.Header().Get("Content-Range"))
	}
}
