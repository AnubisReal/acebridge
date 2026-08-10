package app

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

//go:embed web
var embeddedWeb embed.FS

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type App struct {
	cfg          Config
	db           *sql.DB
	logger       *slog.Logger
	client       *http.Client
	sourceClient *http.Client
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	syncLocks    sync.Map
	streams      *streamManager
	metrics      metricsState
	limiter      *rateLimiter
	logoCatalog  logoCatalogCache
}

func New(cfg Config, logger *slog.Logger) (*App, error) {
	db, err := openDatabase(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{cfg: cfg, db: db, logger: logger, client: newSafeHTTPClient(true), sourceClient: newSafeHTTPClient(cfg.AllowPrivateSources), cancel: cancel}
	a.streams = newStreamManager(a)
	a.limiter = newRateLimiter()
	a.wg.Add(1)
	go a.runScheduler(ctx)
	a.wg.Add(1)
	go a.streams.run(ctx, &a.wg)
	return a, nil
}

func (a *App) Close() error {
	a.cancel()
	a.wg.Wait()
	return a.db.Close()
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /health/live", a.live)
	mux.HandleFunc("GET /health/ready", a.ready)
	mux.HandleFunc("GET /metrics", a.metricsHandler)
	mux.HandleFunc("GET /api/v1/dashboard", a.dashboard)
	mux.HandleFunc("GET /api/v1/channels", a.listChannels)
	mux.HandleFunc("GET /api/v1/channel-groups", a.listChannelGroups)
	mux.HandleFunc("DELETE /api/v1/channels/{id}", a.deleteChannel)
	mux.HandleFunc("POST /api/v1/channels/bulk", a.bulkChannels)
	mux.HandleFunc("POST /api/v1/channels/{id}/diagnose", a.diagnoseChannel)
	mux.HandleFunc("POST /api/v1/channels/manual", a.createManualChannel)
	mux.HandleFunc("PATCH /api/v1/channels/{id}", a.updateChannel)
	mux.HandleFunc("POST /api/v1/channels/{id}/playback", a.startChannelPlayback)
	mux.HandleFunc("POST /api/v1/channels/{id}/viewers/{viewer}/heartbeat", a.heartbeatViewer)
	mux.HandleFunc("DELETE /api/v1/channels/{id}/viewers/{viewer}", a.closeViewer)
	mux.HandleFunc("GET /api/v1/channels/{id}/stream", a.streamChannel)
	mux.HandleFunc("GET /api/v1/sources", a.listSources)
	mux.HandleFunc("GET /api/v1/sync-jobs", a.listSyncJobs)
	mux.HandleFunc("GET /api/v1/sync-jobs/{id}", a.getSyncJob)
	mux.HandleFunc("GET /api/v1/history", a.listHistory)
	mux.HandleFunc("GET /api/v1/streams", a.listStreams)
	mux.HandleFunc("PATCH /api/v1/sources/{id}", a.updateSource)
	mux.HandleFunc("POST /api/v1/sources/url", a.createURLSource)
	mux.HandleFunc("POST /api/v1/sources/redbull-padel", a.createRedBullPadelSource)
	mux.HandleFunc("POST /api/v1/sources/file", a.createFileSource)
	mux.HandleFunc("PUT /api/v1/sources/{id}/file", a.replaceFileSource)
	mux.HandleFunc("POST /api/v1/sources/{id}/sync", a.syncSourceHandler)
	mux.HandleFunc("POST /api/v1/sources/{id}/sync-jobs", a.queueSyncSourceHandler)
	mux.HandleFunc("DELETE /api/v1/sources/{id}", a.deleteSource)
	mux.HandleFunc("GET /api/v1/settings", a.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", a.putSettings)
	mux.HandleFunc("POST /api/v1/engine/check", a.checkEngine)
	mux.HandleFunc("GET /playlist.m3u", a.playlist)

	webFS, _ := fs.Sub(embeddedWeb, "web")
	mux.Handle("/", http.FileServer(http.FS(webFS)))
	return a.recoverer(a.securityHeaders(a.logging(a.rateLimit(mux))))
}

func (a *App) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "error", recovered)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https: http:; style-src 'self'; script-src 'self'; connect-src 'self'; media-src 'self' blob: http: https:; worker-src 'self' blob:")
		next.ServeHTTP(w, r)
	})
}

func (a *App) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := fmt.Sprintf("%x-%x", started.UnixNano(), a.metrics.requests.Add(1))
		w.Header().Set("X-Request-ID", id)
		rec := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		if rec.status >= 400 {
			a.metrics.errors.Add(1)
		}
		a.logger.Info("request", "request_id", id, "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(started).Milliseconds())
	})
}
