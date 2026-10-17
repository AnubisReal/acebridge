package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxEPGDownload = 16 << 20
const maxEPGXML = 64 << 20

type epgConfig struct {
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`
	ProxyURL   string `json:"proxy_url"`
	CacheHours int    `json:"cache_hours"`
}

type epgCache struct {
	refresh    sync.Mutex
	mu         sync.Mutex
	key        string
	data       []byte
	fetchedAt  time.Time
	retryAt    time.Time
	lastError  string
	etag       string
	modified   string
	outputETag string
}

var errEPGDisabled = errors.New("EPG is disabled")

func (a *App) loadEPGConfig(ctx context.Context) (epgConfig, error) {
	config := epgConfig{CacheHours: 6}
	var raw string
	err := a.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='epg_config'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	err = json.Unmarshal([]byte(raw), &config)
	return config, err
}

func (a *App) getEPG(w http.ResponseWriter, r *http.Request) {
	config, err := a.loadEPGConfig(r.Context())
	if err != nil {
		writeError(w, 500, "cannot load EPG configuration")
		return
	}
	a.epg.mu.Lock()
	var cachedAt *time.Time
	if !a.epg.fetchedAt.IsZero() {
		value := a.epg.fetchedAt
		cachedAt = &value
	}
	state := map[string]any{"enabled": config.Enabled, "url": config.URL, "proxy_url": config.ProxyURL, "cache_hours": config.CacheHours, "cached_at": cachedAt, "cached_bytes": len(a.epg.data), "last_error": a.epg.lastError, "output_path": "/epg.xml"}
	a.epg.mu.Unlock()
	writeJSON(w, 200, state)
}

func (a *App) putEPG(w http.ResponseWriter, r *http.Request) {
	var config epgConfig
	if !decodeJSON(w, r, &config) {
		return
	}
	config.URL = strings.TrimSpace(config.URL)
	if config.URL != "" {
		parsed, err := url.Parse(config.URL)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
			writeError(w, 400, "EPG requires a valid HTTP or HTTPS URL")
			return
		}
	}
	if config.Enabled && config.URL == "" {
		writeError(w, 400, "EPG URL is required")
		return
	}
	if config.CacheHours < 1 || config.CacheHours > 168 {
		writeError(w, 400, "EPG cache must be between 1 and 168 hours")
		return
	}
	proxy, err := normalizeSourceProxyURL(config.ProxyURL)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	config.ProxyURL = proxy
	raw, _ := json.Marshal(config)
	// Serialize configuration changes with refreshes so old data cannot replace
	// the cache after the user switches provider or disables the guide.
	a.epg.refresh.Lock()
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO settings(key,value) VALUES ('epg_config',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw))
	if err == nil {
		a.epg.mu.Lock()
		if a.epg.key != string(raw) {
			a.epg.key = string(raw)
			a.epg.data = nil
			a.epg.fetchedAt = time.Time{}
			a.epg.retryAt = time.Time{}
			a.epg.lastError = ""
			a.epg.etag = ""
			a.epg.modified = ""
			a.epg.outputETag = ""
		}
		a.epg.mu.Unlock()
	}
	a.epg.refresh.Unlock()
	if err != nil {
		writeError(w, 500, "cannot save EPG configuration")
		return
	}
	a.getEPG(w, r)
}

type epgSnapshot struct {
	data      []byte
	fetchedAt time.Time
	etag      string
	stale     bool
}

func (a *App) epgData(ctx context.Context, force bool) (epgSnapshot, error) {
	a.epg.refresh.Lock()
	defer a.epg.refresh.Unlock()
	config, err := a.loadEPGConfig(ctx)
	if err != nil {
		return epgSnapshot{}, err
	}
	if !config.Enabled {
		return epgSnapshot{}, errEPGDisabled
	}
	raw, _ := json.Marshal(config)
	a.epg.mu.Lock()
	same := a.epg.key == string(raw)
	if !same {
		a.epg.data = nil
		a.epg.fetchedAt = time.Time{}
		a.epg.retryAt = time.Time{}
		a.epg.lastError = ""
		a.epg.etag = ""
		a.epg.modified = ""
		a.epg.outputETag = ""
	}
	cached := epgSnapshot{data: a.epg.data, fetchedAt: a.epg.fetchedAt, etag: a.epg.outputETag}
	fresh := same && len(cached.data) > 0 && time.Since(cached.fetchedAt) < time.Duration(config.CacheHours)*time.Hour
	backoff := same && time.Now().Before(a.epg.retryAt)
	previousError := a.epg.lastError
	etag, modified := a.epg.etag, a.epg.modified
	a.epg.mu.Unlock()
	if !same {
		cached = epgSnapshot{}
		etag = ""
		modified = ""
	}
	if !force && fresh {
		return cached, nil
	}
	if !force && backoff {
		if len(cached.data) > 0 && time.Since(cached.fetchedAt) < 24*time.Hour {
			cached.stale = true
			return cached, nil
		}
		return epgSnapshot{}, fmt.Errorf("EPG refresh temporarily unavailable: %s", previousError)
	}
	client, err := a.sourceHTTPClient(config.ProxyURL)
	var data []byte
	var responseETag, responseModified string
	if err == nil {
		downloadClient := *client
		downloadClient.Timeout = 90 * time.Second
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, config.URL, nil)
		if requestErr != nil {
			err = requestErr
		} else {
			request.Header.Set("User-Agent", "AceBridge/0.2")
			// Read gzip ourselves so compressed and decompressed sizes are both bounded.
			request.Header.Set("Accept-Encoding", "identity")
			if etag != "" {
				request.Header.Set("If-None-Match", etag)
			}
			if modified != "" {
				request.Header.Set("If-Modified-Since", modified)
			}
			var response *http.Response
			response, err = downloadClient.Do(request)
			if err == nil {
				responseETag, responseModified = response.Header.Get("ETag"), response.Header.Get("Last-Modified")
				if response.StatusCode == http.StatusNotModified && len(cached.data) > 0 {
					data = cached.data
					if responseETag == "" {
						responseETag = etag
					}
					if responseModified == "" {
						responseModified = modified
					}
				} else if response.StatusCode < 200 || response.StatusCode >= 300 {
					err = fmt.Errorf("EPG provider returned %s", response.Status)
				} else {
					data, err = readEPGXML(response.Body, maxEPGDownload, maxEPGXML)
				}
				response.Body.Close()
			}
		}
	}
	a.epg.mu.Lock()
	defer a.epg.mu.Unlock()
	a.epg.key = string(raw)
	if err != nil {
		a.epg.lastError = err.Error()
		a.epg.retryAt = time.Now().Add(time.Minute)
		if !force && len(cached.data) > 0 && time.Since(cached.fetchedAt) < 24*time.Hour {
			cached.stale = true
			return cached, nil
		}
		return epgSnapshot{}, fmt.Errorf("cannot refresh EPG: %w", err)
	}
	a.epg.data = data
	a.epg.fetchedAt = time.Now().UTC()
	a.epg.lastError = ""
	a.epg.retryAt = time.Time{}
	a.epg.etag = responseETag
	a.epg.modified = responseModified
	sum := sha256.Sum256(data)
	a.epg.outputETag = fmt.Sprintf(`"%x"`, sum)
	return epgSnapshot{data: data, fetchedAt: a.epg.fetchedAt, etag: a.epg.outputETag}, nil
}

func readEPGXML(input io.Reader, compressedLimit, xmlLimit int64) ([]byte, error) {
	compressed, err := io.ReadAll(io.LimitReader(input, compressedLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(compressed)) > compressedLimit {
		return nil, errors.New("EPG download exceeds size limit")
	}
	reader := bufio.NewReader(bytes.NewReader(compressed))
	var decoded io.Reader = reader
	if magic, _ := reader.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zipped, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("invalid EPG gzip: %w", err)
		}
		defer zipped.Close()
		decoded = zipped
	}
	data, err := io.ReadAll(io.LimitReader(decoded, xmlLimit+1))
	if err != nil {
		return nil, fmt.Errorf("invalid EPG data: %w", err)
	}
	if int64(len(data)) > xmlLimit {
		return nil, errors.New("EPG XML exceeds size limit")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	type omittedRange struct{ start, end int64 }
	var omitted []omittedRange
	for {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid XMLTV: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || element.Name.Local != "tv" || element.Name.Space != "" {
					return nil, errors.New("EPG must contain one XMLTV tv root")
				}
			}
			depth++
			if depth > 64 {
				return nil, errors.New("EPG XML nesting exceeds limit")
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(element)) != "" {
				return nil, errors.New("invalid content outside XMLTV root")
			}
		case xml.Directive:
			// XMLTV does not need a DTD at runtime. Do not forward declarations
			// that might cause a downstream XML reader to fetch external resources.
			omitted = append(omitted, omittedRange{start, decoder.InputOffset()})
		case xml.ProcInst:
			if element.Target != "xml" {
				omitted = append(omitted, omittedRange{start, decoder.InputOffset()})
			}
		}
	}
	if roots != 1 || depth != 0 {
		return nil, errors.New("incomplete XMLTV document")
	}
	if len(omitted) > 0 {
		var clean bytes.Buffer
		var offset int64
		for _, part := range omitted {
			clean.Write(data[offset:part.start])
			offset = part.end
		}
		clean.Write(data[offset:])
		data = clean.Bytes()
	}
	return data, nil
}

func (a *App) refreshEPG(w http.ResponseWriter, r *http.Request) {
	_, err := a.epgData(r.Context(), true)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	a.getEPG(w, r)
}

func (a *App) serveEPG(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.epgData(r.Context(), false)
	if errors.Is(err, errEPGDisabled) {
		writeError(w, 404, "EPG is not configured or disabled")
		return
	}
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="epg.xml"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", snapshot.etag)
	if snapshot.stale {
		w.Header().Set("X-AceBridge-EPG-Cache", "stale")
	} else {
		w.Header().Set("X-AceBridge-EPG-Cache", "fresh")
	}
	http.ServeContent(w, r, "epg.xml", snapshot.fetchedAt, bytes.NewReader(snapshot.data))
}
