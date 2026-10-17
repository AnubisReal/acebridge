package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anubisreal/acebridge/internal/playlist"
)

func TestIPTVProxyImportAndPlayback(t *testing.T) {
	a := testApp(t)
	hits := map[string]int{}
	var connections atomic.Int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch r.URL.Path {
		case "/tv.m3u":
			io.WriteString(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"tdt.demo\",TDT Demo\nhttp://93.184.216.35/master.m3u8\n")
		case "/master.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"http://93.184.216.36/key\"\nhttp://93.184.216.37/segment.ts\n")
		case "/segment.ts", "/key":
			io.WriteString(w, "media")
		case "/redirect":
			w.Header().Set("Location", "http://127.0.0.1/internal")
			w.WriteHeader(http.StatusFound)
		default:
			t.Errorf("unexpected proxy request: %s", r.URL)
			w.WriteHeader(404)
		}
	})
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Error("destination was not pinned through CONNECT")
			w.WriteHeader(400)
			return
		}
		connections.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		reader := bufio.NewReader(conn)
		for {
			req, err := http.ReadRequest(reader)
			if err != nil {
				return
			}
			req.Body.Close()
			rec := httptest.NewRecorder()
			upstream.ServeHTTP(rec, req)
			rec.Header().Set("Content-Length", strconv.Itoa(rec.Body.Len()))
			response := rec.Result()
			response.ContentLength = int64(rec.Body.Len())
			response.Header.Set("Content-Length", strconv.Itoa(rec.Body.Len()))
			response.Write(conn)
			response.Body.Close()
		}
	}))
	defer proxyServer.Close()
	// Use HTTP URLs in this fixture so the test proxy need not implement CONNECT.
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Error("direct network used")
		return nil, context.Canceled
	})}
	_, err := a.db.Exec(`INSERT INTO sources(id,name,kind,url,proxy_url) VALUES (1,'TDT','url','http://93.184.216.34/tv.m3u',?)`, proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Import through the proxy, then substitute HTTP stream addresses for this fixture.
	if err := a.runSyncJob(context.Background(), 1, 0); err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec(`UPDATE channels SET acestream_id='http://93.184.216.35/master.m3u8' WHERE source_id=1`)
	if err != nil {
		t.Fatal(err)
	}
	playback := perform(a.Routes(), http.MethodPost, "/api/v1/channels/1/playback", nil, "")
	if playback.Code != 200 {
		t.Fatal(playback.Body.String())
	}
	var info map[string]string
	json.Unmarshal(playback.Body.Bytes(), &info)
	manifest := perform(a.Routes(), http.MethodGet, info["proxy_url"], nil, "")
	if manifest.Code != 200 {
		t.Fatal(manifest.Body.String())
	}
	// Exercise signed cross-host child resources, including encryption keys.
	for _, line := range strings.Split(manifest.Body.String(), "\n") {
		child := line
		if strings.HasPrefix(line, "#EXT-X-KEY:") {
			child = manifestURIAttribute.FindStringSubmatch(line)[1]
		}
		if strings.HasPrefix(child, "/api/") {
			result := perform(a.Routes(), http.MethodGet, child, nil, "")
			if result.Code != 200 || result.Body.String() != "media" {
				t.Fatalf("child failed: %d %s", result.Code, result.Body.String())
			}
		}
	}
	if !strings.Contains(manifest.Body.String(), "sig=") || !strings.Contains(manifest.Body.String(), "93.184.216.37") {
		t.Fatal(manifest.Body.String())
	}
	unsigned := perform(a.Routes(), http.MethodGet, "/api/v1/channels/1/stream?url=http%3A%2F%2Funrelated.example%2Fsecret", nil, "")
	if unsigned.Code != 400 {
		t.Fatalf("unsigned URL accepted: %d", unsigned.Code)
	}
	if hits["/tv.m3u"] != 1 || hits["/master.m3u8"] != 2 || hits["/key"] != 1 || hits["/segment.ts"] != 1 {
		t.Fatalf("proxy was bypassed: %v", hits)
	}
	client, _ := a.sourceHTTPClient(proxyServer.URL)
	if got := connections.Load(); got != 4 {
		t.Fatalf("expected reuse of master connection; got %d CONNECT tunnels", got)
	}
	if response, err := client.Get("http://93.184.216.34/redirect"); err == nil {
		response.Body.Close()
		t.Fatal("private redirect accepted")
	}
}

func TestParseMixedIPTVAndAceStream(t *testing.T) {
	channels := playlist.Parse("#EXTM3U\n#EXTINF:-1,TDT\nhttps://cdn.example/live.m3u8\n#EXTINF:-1,Ace\nacestream://0123456789abcdef0123456789abcdef01234567\n")
	if len(channels) != 2 || channels[0].AceStreamID != "https://cdn.example/live.m3u8" {
		t.Fatalf("unexpected channels: %+v", channels)
	}
}
