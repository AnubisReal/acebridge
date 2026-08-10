package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type metricsState struct {
	requests        atomic.Uint64
	errors          atomic.Uint64
	syncs           atomic.Uint64
	playbacks       atomic.Uint64
	engineLatencyMs atomic.Uint64
	engineRequests  atomic.Uint64
}

type rateEntry struct {
	window time.Time
	count  int
}
type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
}

func newRateLimiter() *rateLimiter { return &rateLimiter{entries: map[string]rateEntry{}} }
func (l *rateLimiter) allow(key string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e := l.entries[key]
	if e.window.IsZero() || now.Sub(e.window) >= time.Minute {
		e = rateEntry{window: now}
	}
	e.count++
	l.entries[key] = e
	return e.count <= limit
}

type managedStream struct {
	ChannelID       int64     `json:"channel_id"`
	ChannelName     string    `json:"channel_name"`
	ChannelLogo     string    `json:"channel_logo,omitempty"`
	PlaybackURL     string    `json:"-"`
	CommandURL      string    `json:"-"`
	StartedAt       time.Time `json:"started_at"`
	LastAccess      time.Time `json:"last_access"`
	Viewers         int       `json:"viewers"`
	DurationSeconds int64     `json:"duration_seconds"`
	viewerActivity  map[string]time.Time
}

type streamManager struct {
	app      *App
	mu       sync.Mutex
	items    map[int64]*managedStream
	locks    map[int64]*sync.Mutex
	starting int
}

func newStreamManager(app *App) *streamManager {
	return &streamManager{app: app, items: make(map[int64]*managedStream), locks: make(map[int64]*sync.Mutex)}
}

func (m *streamManager) channelLock(id int64) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks[id] == nil {
		m.locks[id] = &sync.Mutex{}
	}
	return m.locks[id]
}

func (m *streamManager) run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.closeAll()
			return
		case <-ticker.C:
			m.reap()
		}
	}
}

func (m *streamManager) reap() {
	settings, _ := m.app.loadSettings(context.Background())
	idle := time.Duration(settings.StreamIdleSeconds) * time.Second
	m.mu.Lock()
	expired := make([]*managedStream, 0)
	for id, stream := range m.items {
		for viewerID, lastSeen := range stream.viewerActivity {
			if time.Since(lastSeen) > idle {
				delete(stream.viewerActivity, viewerID)
			}
		}
		stream.Viewers = len(stream.viewerActivity)
		if time.Since(stream.LastAccess) > idle {
			expired = append(expired, stream)
			delete(m.items, id)
		}
	}
	m.mu.Unlock()
	for _, stream := range expired {
		m.stop(stream)
	}
}

func (m *streamManager) stop(stream *managedStream) {
	if stream.CommandURL == "" {
		return
	}
	req, err := http.NewRequest(http.MethodGet, stream.CommandURL, nil)
	if err != nil {
		return
	}
	resp, err := m.app.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (m *streamManager) closeAll() {
	m.mu.Lock()
	streams := make([]*managedStream, 0, len(m.items))
	for _, stream := range m.items {
		streams = append(streams, stream)
	}
	clear(m.items)
	m.mu.Unlock()

	for _, stream := range streams {
		m.stop(stream)
	}
}
func (m *streamManager) touch(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.items[id]; s != nil {
		s.LastAccess = time.Now()
	}
}

func (m *streamManager) touchViewer(id int64, viewerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	stream := m.items[id]
	if stream == nil {
		return false
	}
	now := time.Now()
	stream.LastAccess = now
	if viewerID != "" {
		if stream.viewerActivity == nil {
			stream.viewerActivity = make(map[string]time.Time)
		}
		stream.viewerActivity[viewerID] = now
		stream.Viewers = len(stream.viewerActivity)
	}
	return true
}

func (m *streamManager) removeViewer(id int64, viewerID string) {
	if viewerID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream := m.items[id]; stream != nil {
		delete(stream.viewerActivity, viewerID)
		stream.Viewers = len(stream.viewerActivity)
	}
}

func (m *streamManager) reserve(limit int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items)+m.starting >= limit {
		return false
	}
	m.starting++
	return true
}

func (m *streamManager) finishReservation() {
	m.mu.Lock()
	if m.starting > 0 {
		m.starting--
	}
	m.mu.Unlock()
}

func (m *streamManager) invalidate(id int64, playbackURL string) {
	m.mu.Lock()
	stream := m.items[id]
	if stream != nil && (playbackURL == "" || stream.PlaybackURL == playbackURL) {
		delete(m.items, id)
	} else {
		stream = nil
	}
	m.mu.Unlock()
	if stream != nil {
		m.stop(stream)
	}
}
func (m *streamManager) release(id int64) {
	m.mu.Lock()
	stream := m.items[id]
	delete(m.items, id)
	m.mu.Unlock()
	if stream != nil {
		m.stop(stream)
	}
}
func (m *streamManager) list() []managedStream {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]managedStream, 0, len(m.items))
	for _, s := range m.items {
		item := *s
		item.DurationSeconds = int64(time.Since(item.StartedAt).Seconds())
		out = append(out, item)
	}
	return out
}

func (a *App) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "live"})
}
func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	settings, err := a.loadSettings(r.Context())
	if err != nil || strings.TrimSpace(settings.EngineURL) == "" {
		writeError(w, http.StatusServiceUnavailable, "essential configuration is incomplete")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "service": "acebridge"})
}
func (a *App) listStreams(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, a.streams.list())
}
func (a *App) listHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT h.id,h.channel_id,h.channel_name,
		COALESCE(NULLIF(c.logo,''),(SELECT c2.logo FROM channels c2 WHERE LOWER(TRIM(REPLACE(c2.name,'*','')))=LOWER(TRIM(REPLACE(h.channel_name,'*',''))) AND c2.logo<>'' ORDER BY c2.id DESC LIMIT 1),''),
		h.started_at FROM playback_history h LEFT JOIN channels c ON c.id=h.channel_id ORDER BY h.started_at DESC LIMIT 500`)
	if err != nil {
		writeError(w, 500, "cannot load history")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var channelID *int64
		var name, logo string
		var at time.Time
		if rows.Scan(&id, &channelID, &name, &logo, &at) == nil {
			items = append(items, map[string]any{"id": id, "channel_id": channelID, "channel_name": name, "channel_logo": logo, "started_at": at})
		}
	}
	writeJSON(w, 200, items)
}
func (a *App) listSyncJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT j.id,j.source_id,s.name,j.status,j.added,j.updated,j.removed,j.error,j.started_at,j.finished_at FROM sync_jobs j JOIN sources s ON s.id=j.source_id ORDER BY j.id DESC LIMIT 100`)
	if err != nil {
		writeError(w, 500, "cannot load jobs")
		return
	}
	defer rows.Close()
	items := make([]SyncJob, 0)
	for rows.Next() {
		var j SyncJob
		var done *time.Time
		if rows.Scan(&j.ID, &j.SourceID, &j.SourceName, &j.Status, &j.Added, &j.Updated, &j.Removed, &j.Error, &j.StartedAt, &done) == nil {
			j.FinishedAt = done
			items = append(items, j)
		}
	}
	writeJSON(w, 200, items)
}
func (a *App) getSyncJob(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid job id")
		return
	}
	var j SyncJob
	var done *time.Time
	err = a.db.QueryRowContext(r.Context(), `SELECT j.id,j.source_id,s.name,j.status,j.added,j.updated,j.removed,j.error,j.started_at,j.finished_at FROM sync_jobs j JOIN sources s ON s.id=j.source_id WHERE j.id=?`, id).Scan(&j.ID, &j.SourceID, &j.SourceName, &j.Status, &j.Added, &j.Updated, &j.Removed, &j.Error, &j.StartedAt, &done)
	if err != nil {
		writeError(w, 404, "sync job not found")
		return
	}
	j.FinishedAt = done
	writeJSON(w, 200, j)
}
func (a *App) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "acebridge_http_requests_total %d\nacebridge_http_errors_total %d\nacebridge_sync_total %d\nacebridge_playback_total %d\nacebridge_active_streams %d\nacebridge_engine_latency_ms_sum %d\nacebridge_engine_latency_ms_count %d\n", a.metrics.requests.Load(), a.metrics.errors.Load(), a.metrics.syncs.Load(), a.metrics.playbacks.Load(), len(a.streams.list()), a.metrics.engineLatencyMs.Load(), a.metrics.engineRequests.Load())
}

func stableErrorCode(status int) string {
	switch status {
	case 400:
		return "invalid_request"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 429:
		return "rate_limited"
	case 502:
		return "upstream_error"
	default:
		return "internal_error"
	}
}
func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": stableErrorCode(status)})
}

func (a *App) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limited := r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/playback") || strings.HasSuffix(r.URL.Path, "/sync") || strings.HasSuffix(r.URL.Path, "/diagnose"))
		if limited {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			if host == "" {
				host = r.RemoteAddr
			}
			if !a.limiter.allow(host+":"+r.URL.Path, 30) {
				writeError(w, 429, "too many operational requests")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
