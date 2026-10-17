package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anubisreal/acebridge/internal/playlist"
)

func TestProxyCannotReachInternalDestinations(t *testing.T) {
	a := testApp(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unsafe destination reached the proxy")
		w.WriteHeader(500)
	}))
	defer proxy.Close()
	client, err := a.sourceHTTPClient(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := a.sourceHTTPClient(proxy.URL)
	if client != again {
		t.Fatal("proxy client was not reused")
	}
	for _, raw := range []string{"http://127.0.0.1/", "http://169.254.169.254/", "http://192.168.0.1/", "http://100.64.0.1/", "http://[::1]/", "http://[::ffff:127.0.0.1]/"} {
		response, err := client.Get(raw)
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatalf("accepted private destination %s", raw)
		}
	}
}

func TestGuardedDialPinsPublicIP(t *testing.T) {
	var destination string
	dial := guardedSourceDial(func(_ context.Context, _, address string) (net.Conn, error) {
		destination = address
		return nil, context.Canceled
	}, false)
	_, _ = dial(context.Background(), "tcp", "93.184.216.34:443")
	if destination != "93.184.216.34:443" {
		t.Fatalf("unvalidated destination: %s", destination)
	}
}

func TestIPTVURLRotationPreservesSettings(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'IPTV','url')`)
	if err != nil {
		t.Fatal(err)
	}
	channels := []playlist.Channel{{TVGID: "tdt.test", Name: "Original", Group: "TDT", AceStreamID: "https://old.example/live.m3u8"}}
	if _, _, _, err = a.replaceChannels(context.Background(), 1, channels); err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec(`UPDATE channels SET name='Mi canal',metadata_locked=1,enabled=0 WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	channels[0].AceStreamID = "https://new.example/live.m3u8?token=renewed"
	added, updated, removed, err := a.replaceChannels(context.Background(), 1, channels)
	if err != nil {
		t.Fatal(err)
	}
	var id, enabled int
	var name, address string
	err = a.db.QueryRow(`SELECT id,name,enabled,acestream_id FROM channels WHERE source_id=1`).Scan(&id, &name, &enabled, &address)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || updated != 1 || removed != 0 || id != 1 || enabled != 0 || name != "Mi canal" || address != channels[0].AceStreamID {
		t.Fatalf("rotation lost identity: %d/%d/%d id=%d %s enabled=%d %s", added, updated, removed, id, name, enabled, address)
	}
}

func TestIPTVFormatDetection(t *testing.T) {
	a := testApp(t)
	for _, test := range []struct{ mime, body, want string }{
		{"application/vnd.apple.mpegurl", "#EXTM3U\n", "hls"},
		{"text/plain", "#EXTM3U\n", "hls"},
		{"video/mp2t", "media", "mpegts"},
		{"video/mp4", "media", "native"},
		{"text/html", "<html>Error</html>", ""},
	} {
		a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{test.mime}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
		})}
		got, err := a.detectDirectStreamType(context.Background(), "https://example.com/live", "")
		if got != test.want || (test.want == "" && err == nil) || (test.want != "" && err != nil) {
			t.Fatalf("%s: got %s %v", test.mime, got, err)
		}
	}
}

func TestContinuousIPTVHasNoTotalRequestTimeout(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'IPTV','url'); INSERT INTO channels(id,source_id,name,acestream_id,source_acestream_id) VALUES (1,1,'TS','https://example.com/live.ts','https://example.com/live.ts')`)
	if err != nil {
		t.Fatal(err)
	}
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if _, ok := r.Context().Deadline(); ok {
			t.Error("continuous stream has a total request deadline")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"video/mp2t"}}, Body: io.NopCloser(strings.NewReader("transport stream"))}, nil
	})}
	response := perform(a.Routes(), http.MethodGet, "/api/v1/channels/1/stream", nil, "")
	if response.Code != 200 || response.Body.String() != "transport stream" {
		t.Fatalf("stream failed: %d %s", response.Code, response.Body.String())
	}
}
