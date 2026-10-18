package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
)

func TestHLSProxyPreservesFFmpegExtensionsAndUpstreamQueries(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/live/master.m3u8?session=one")
	tests := []struct{ line, target, extension string }{
		{"segment.ts?token=a%2Bb&part=2", "https://cdn.example/live/segment.ts?token=a%2Bb&part=2", ".ts"},
		{"fragment.m4s?token=two", "https://cdn.example/live/fragment.m4s?token=two", ".m4s"},
		{"subtitle.vtt", "https://cdn.example/live/subtitle.vtt", ".vtt"},
		{"#EXT-X-MAP:URI=\"init.mp4?token=three\"", "https://cdn.example/live/init.mp4?token=three", ".mp4"},
		{"#EXT-X-MAP:URI=\"init?id=42\"", "https://cdn.example/live/init?id=42", ".mp4"},
		{"#EXT-X-KEY:METHOD=AES-128,URI=\"https://keys.example/key?id=1\"", "https://keys.example/key?id=1", ".key"},
		{"#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio?track=es\"", "https://cdn.example/live/audio?track=es", ".m3u8"},
		{"#EXT-X-PART:DURATION=0.5,URI=\"part?id=1\"", "https://cdn.example/live/part?id=1", ".m4s"},
		{"segment.jpg?disguised=1", "https://cdn.example/live/segment.jpg?disguised=1", ".ts"},
	}
	for _, test := range tests {
		t.Run(test.extension+test.target, func(t *testing.T) {
			result := rewriteManifestForViewer(test.line, base, 7, "viewer-one")
			resource := result
			if strings.HasPrefix(result, "#") {
				resource = manifestURIAttribute.FindStringSubmatch(result)[1]
			}
			parsed, err := url.Parse(resource)
			if err != nil {
				t.Fatal(err)
			}
			if path.Ext(parsed.Path) != test.extension || parsed.Query().Get("url") != test.target || parsed.Query().Get("viewer") != "viewer-one" {
				t.Fatalf("invalid proxy URL: %s", resource)
			}
		})
	}
	master := rewriteManifestForViewer("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nvideo?quality=720\n", base, 7, "")
	resource := strings.Split(master, "\n")[2]
	parsed, _ := url.Parse(resource)
	if path.Ext(parsed.Path) != ".m3u8" {
		t.Fatalf("variant without extension not rewritten as playlist: %s", resource)
	}
}

func TestNamedIPTVResourcesKeepSignatureAndByteRangeChecks(t *testing.T) {
	a := testApp(t)
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind) VALUES (1,'IPTV','url'); INSERT INTO channels(id,source_id,name,acestream_id,source_acestream_id) VALUES (1,1,'Test','https://example.com/master.m3u8','https://example.com/master.m3u8')`)
	if err != nil {
		t.Fatal(err)
	}
	const target = "https://example.com/segment.ts?token=a%2Bb&part=1"
	calls := 0
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != target || r.Header.Get("Range") != "bytes=10-19" {
			t.Errorf("upstream request was altered: %s range=%s", r.URL, r.Header.Get("Range"))
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": []string{"video/mp2t"}, "Content-Range": []string{"bytes 10-19/100"}}, Body: io.NopCloser(strings.NewReader("0123456789"))}, nil
	})}
	signed := a.signStreamResource(1, streamProxyURLForResource(1, target, "viewer", ".ts"))
	parsed, _ := url.Parse(signed)
	paths := []string{signed, "/api/v1/channels/1/stream?" + parsed.RawQuery}
	for _, raw := range paths {
		req := httptest.NewRequest(http.MethodGet, raw, nil)
		req.Header.Set("Range", "bytes=10-19")
		result := httptest.NewRecorder()
		a.Routes().ServeHTTP(result, req)
		if result.Code != 206 || result.Header().Get("Content-Range") != "bytes 10-19/100" || result.Body.String() != "0123456789" {
			t.Fatalf("resource failed: %d %s", result.Code, result.Body.String())
		}
	}
	query := parsed.Query()
	query.Set("url", "http://127.0.0.1/private")
	parsed.RawQuery = query.Encode()
	if response := perform(a.Routes(), http.MethodGet, parsed.String(), nil, ""); response.Code != 400 {
		t.Fatalf("tampered signature accepted: %d", response.Code)
	}
	if calls != 2 {
		t.Fatalf("invalid requests reached upstream: %d", calls)
	}
}
