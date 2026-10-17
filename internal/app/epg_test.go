package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const sampleXMLTV = `<?xml version="1.0" encoding="UTF-8"?><tv generator-info-name="test"><channel id="La1.TV"><display-name>La 1</display-name></channel><programme start="20261017120000 +0200" channel="La1.TV"><title>Noticias</title></programme></tv>`

func gzipEPG(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func configureTestEPG(t *testing.T, a *App, raw string) {
	t.Helper()
	response := perform(a.Routes(), http.MethodPut, "/api/v1/epg", strings.NewReader(raw), "application/json")
	if response.Code != 200 {
		t.Fatalf("configure: %d %s", response.Code, response.Body.String())
	}
}

func TestEPGIndependentGzipCacheAndConditionalResponse(t *testing.T) {
	a := testApp(t)
	if r := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, ""); r.Code != 404 {
		t.Fatalf("unconfigured EPG: %d", r.Code)
	}
	compressed := gzipEPG(t, []byte(sampleXMLTV))
	var downloads atomic.Int32
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		downloads.Add(1)
		if request.Header.Get("Accept-Encoding") != "identity" {
			t.Error("gzip size checks could be bypassed by automatic decompression")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(compressed))}, nil
	})}
	configureTestEPG(t, a, `{"enabled":true,"url":"https://example.com/TV.xml.gz","proxy_url":"","cache_hours":6}`)
	first := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	if first.Code != 200 || first.Body.String() != sampleXMLTV || first.Header().Get("Content-Type") != "application/xml; charset=utf-8" {
		t.Fatalf("invalid XML response: %d %s", first.Code, first.Body.String())
	}
	second := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	if second.Body.String() != sampleXMLTV || downloads.Load() != 1 {
		t.Fatalf("cache missed: %d", downloads.Load())
	}
	request := httptest.NewRequest(http.MethodGet, "/epg.xml", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	recorder := httptest.NewRecorder()
	a.Routes().ServeHTTP(recorder, request)
	if recorder.Code != 304 || recorder.Body.Len() != 0 || downloads.Load() != 1 {
		t.Fatalf("conditional response: %d %s", recorder.Code, recorder.Body.String())
	}
	var sources int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sources); err != nil || sources != 0 {
		t.Fatalf("EPG touched channel sources: %d %v", sources, err)
	}
	saved, err := a.loadEPGConfig(context.Background())
	if err != nil || !saved.Enabled || saved.CacheHours != 6 {
		t.Fatalf("config did not persist: %+v %v", saved, err)
	}
}

func TestEPGFailedRefreshPreservesCacheAndBacksOff(t *testing.T) {
	a := testApp(t)
	var requests atomic.Int32
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		n := requests.Add(1)
		status := 200
		body := sampleXMLTV
		if n > 1 {
			status = 503
			body = "unavailable"
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	configureTestEPG(t, a, `{"enabled":true,"url":"https://example.com/TV.xml","proxy_url":"","cache_hours":6}`)
	perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	a.epg.mu.Lock()
	a.epg.fetchedAt = time.Now().Add(-7 * time.Hour)
	a.epg.mu.Unlock()
	stale := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	if stale.Code != 200 || stale.Body.String() != sampleXMLTV || stale.Header().Get("X-AceBridge-EPG-Cache") != "stale" {
		t.Fatalf("stale response failed: %d %s", stale.Code, stale.Body.String())
	}
	perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	if requests.Load() != 2 {
		t.Fatalf("failed requests were not throttled: %d", requests.Load())
	}
	forced := perform(a.Routes(), http.MethodPost, "/api/v1/epg/refresh", nil, "")
	if forced.Code != 502 {
		t.Fatalf("manual refresh hid error: %d", forced.Code)
	}
	a.epg.mu.Lock()
	a.epg.fetchedAt = time.Now().Add(-25 * time.Hour)
	a.epg.mu.Unlock()
	if result := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, ""); result.Code != 502 {
		t.Fatalf("expired stale data served: %d", result.Code)
	}
	configureTestEPG(t, a, `{"enabled":false,"url":"https://other.example/guide.xml","proxy_url":"","cache_hours":6}`)
	if r := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, ""); r.Code != 404 {
		t.Fatalf("disabled EPG leaked previous data: %d", r.Code)
	}
	a.epg.mu.Lock()
	defer a.epg.mu.Unlock()
	if len(a.epg.data) != 0 {
		t.Fatal("provider change did not invalidate cache")
	}
}

func TestEPGProviderNotModified(t *testing.T) {
	a := testApp(t)
	var calls atomic.Int32
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("ETag", `"provider-v1"`)
		if calls.Add(1) > 1 {
			if request.Header.Get("If-None-Match") != `"provider-v1"` {
				t.Error("missing provider validator")
			}
			return &http.Response{StatusCode: 304, Header: header, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(sampleXMLTV))}, nil
	})}
	configureTestEPG(t, a, `{"enabled":true,"url":"https://example.com/guide.xml","cache_hours":1}`)
	perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	a.epg.mu.Lock()
	a.epg.fetchedAt = time.Now().Add(-2 * time.Hour)
	a.epg.mu.Unlock()
	result := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	if result.Code != 200 || result.Body.String() != sampleXMLTV || calls.Load() != 2 {
		t.Fatalf("provider 304 failed: %d %s", result.Code, result.Body.String())
	}
}

func TestEPGValidationAndDecompressionLimits(t *testing.T) {
	good := gzipEPG(t, []byte(sampleXMLTV))
	broken := append([]byte(nil), good...)
	broken[len(broken)-5] ^= 0xff
	for _, test := range []struct {
		name                      string
		data                      []byte
		compressedLimit, xmlLimit int64
	}{
		{"invalid gzip", []byte{0x1f, 0x8b, 0x00}, 4096, 4096},
		{"CRC error", broken, 4096, 4096},
		{"HTML", []byte("<html>blocked</html>"), 4096, 4096},
		{"multiple roots", []byte("<tv></tv><tv></tv>"), 4096, 4096},
		{"truncated XML", []byte("<tv><channel>"), 4096, 4096},
		{"external entity", []byte(`<!DOCTYPE tv [<!ENTITY x SYSTEM "http://127.0.0.1/secret">]><tv>&x;</tv>`), 4096, 4096},
		{"compressed limit", good, 8, 4096},
		{"decompression limit", good, 4096, 32},
		{"XML depth", []byte("<tv>" + strings.Repeat("<x>", 70) + strings.Repeat("</x>", 70) + "</tv>"), 4096, 4096},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readEPGXML(bytes.NewReader(test.data), test.compressedLimit, test.xmlLimit); err == nil {
				t.Fatal("invalid EPG accepted")
			}
		})
	}
	if data, err := readEPGXML(strings.NewReader(sampleXMLTV), 4096, 4096); err != nil || string(data) != sampleXMLTV {
		t.Fatalf("plain XML rejected: %v", err)
	}
}

func TestEPGConcurrentReadersShareOneDownload(t *testing.T) {
	a := testApp(t)
	var downloads atomic.Int32
	a.sourceClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		downloads.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(sampleXMLTV))}, nil
	})}
	configureTestEPG(t, a, `{"enabled":true,"url":"https://example.com/guide.xml","cache_hours":6}`)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
			if result.Code != 200 || result.Body.String() != sampleXMLTV {
				t.Errorf("concurrent request failed: %d", result.Code)
			}
		}()
	}
	wg.Wait()
	if downloads.Load() != 1 {
		t.Fatalf("duplicate downloads: %d", downloads.Load())
	}
}

func TestEPGDoesNotForwardExternalDeclarations(t *testing.T) {
	raw := `<?xml version="1.0"?><!DOCTYPE tv SYSTEM "http://127.0.0.1/private.dtd"><?xml-stylesheet type="text/xsl" href="http://127.0.0.1/secret.xsl"?><tv></tv>`
	result, err := readEPGXML(strings.NewReader(raw), 4096, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "DOCTYPE") || strings.Contains(string(result), "xml-stylesheet") || !strings.Contains(string(result), "<tv>") {
		t.Fatalf("unsafe XML declarations retained: %s", result)
	}
}

func TestEPGBlocksPrivateProvider(t *testing.T) {
	a := testApp(t)
	configureTestEPG(t, a, `{"enabled":true,"url":"http://127.0.0.1/secret.xml","cache_hours":6}`)
	result := perform(a.Routes(), http.MethodGet, "/epg.xml", nil, "")
	if result.Code != 502 || !strings.Contains(result.Body.String(), "source address is not allowed") {
		t.Fatalf("private provider not blocked: %d %s", result.Code, result.Body.String())
	}
}
