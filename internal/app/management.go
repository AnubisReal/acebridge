package app

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anubisreal/acebridge/internal/playlist"
)

func (a *App) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid channel id")
		return
	}
	var kind string
	if err := a.db.QueryRowContext(r.Context(), `SELECT s.kind FROM channels c JOIN sources s ON s.id=c.source_id WHERE c.id=?`, id).Scan(&kind); err != nil {
		writeError(w, 404, "channel not found")
		return
	}
	if kind != "manual" {
		writeError(w, 409, "synchronized channels must be hidden instead of deleted")
		return
	}
	_, err = a.db.ExecContext(r.Context(), `DELETE FROM channels WHERE id=?`, id)
	if err != nil {
		writeError(w, 500, "cannot delete channel")
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE sources SET channel_count=(SELECT COUNT(*) FROM channels WHERE source_id=sources.id) WHERE kind='manual'`)
	w.WriteHeader(204)
}

func (a *App) bulkChannels(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs      []int64 `json:"ids"`
		Enabled  *bool   `json:"enabled,omitempty"`
		Group    *string `json:"group,omitempty"`
		Diagnose bool    `json:"diagnose,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.IDs) == 0 || len(input.IDs) > 1000 {
		writeError(w, 400, "between 1 and 1000 channel ids are required")
		return
	}
	if input.Enabled == nil && input.Group == nil && !input.Diagnose {
		writeError(w, 400, "no bulk change supplied")
		return
	}
	queryArgs := make([]any, len(input.IDs))
	marks := make([]string, len(input.IDs))
	for i, id := range input.IDs {
		queryArgs[i] = id
		marks[i] = "?"
	}
	sets := []string{}
	args := []any{}
	if input.Enabled != nil {
		sets = append(sets, "enabled=?")
		args = append(args, *input.Enabled)
	}
	if input.Group != nil {
		sets = append(sets, "group_name=?", "metadata_locked=1")
		args = append(args, strings.TrimSpace(*input.Group))
	}
	args = append(args, queryArgs...)
	var count int64
	if len(sets) > 0 {
		result, err := a.db.ExecContext(r.Context(), `UPDATE channels SET `+strings.Join(sets, ",")+` WHERE id IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			writeError(w, 500, "cannot update channels")
			return
		}
		count, _ = result.RowsAffected()
	}
	diagnostics := make([]map[string]any, 0)
	if input.Diagnose {
		semaphore := make(chan struct{}, 4)
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, id := range input.IDs {
			wg.Add(1)
			go func(channelID int64) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				started := time.Now()
				info, diagnoseErr := a.ensurePlaybackMode(r.Context(), channelID, false)
				if diagnoseErr == nil && info.NewSession {
					defer a.streams.release(channelID)
				}
				item := map[string]any{"channel_id": channelID, "latency_ms": time.Since(started).Milliseconds(), "status": "healthy"}
				diagnosticError := ""
				if diagnoseErr != nil {
					item["status"] = "error"
					item["error"] = diagnoseErr.Error()
					diagnosticError = diagnoseErr.Error()
				}
				_, _ = a.db.ExecContext(r.Context(), `INSERT INTO channel_diagnostics(channel_id,status,latency_ms,error,checked_at) VALUES (?,?,?,?,?) ON CONFLICT(channel_id) DO UPDATE SET status=excluded.status,latency_ms=excluded.latency_ms,error=excluded.error,checked_at=excluded.checked_at`, channelID, item["status"], item["latency_ms"], diagnosticError, nowUTC())
				mu.Lock()
				diagnostics = append(diagnostics, item)
				mu.Unlock()
			}(id)
		}
		wg.Wait()
	}
	writeJSON(w, 200, map[string]any{"updated": count, "diagnostics": diagnostics})
}

func (a *App) diagnoseChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid channel id")
		return
	}
	started := time.Now()
	var aceID, logo string
	if err := a.db.QueryRowContext(r.Context(), `SELECT acestream_id,logo FROM channels WHERE id=?`, id).Scan(&aceID, &logo); err != nil {
		writeError(w, 404, "channel not found")
		return
	}
	status := "healthy"
	message := ""
	resolution, codecs := "", ""
	bitrate, duplicates := 0, 0
	logoAccessible := true
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*)-1 FROM channels WHERE acestream_id=?`, aceID).Scan(&duplicates)
	if playlist.ExtractAceStreamID(aceID) == "" {
		status = "error"
		message = "invalid AceStream ID"
	}
	if status == "healthy" {
		info, err := a.ensurePlaybackMode(r.Context(), id, false)
		if err != nil {
			status = "error"
			message = err.Error()
		} else {
			if info.NewSession {
				defer a.streams.release(id)
			}
			manifestRequest, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, info.PlaybackURL, nil)
			response, manifestErr := a.client.Do(manifestRequest)
			if manifestErr != nil {
				status, message = "error", "manifest unavailable: "+manifestErr.Error()
			} else {
				data, readErr := readLimitedBody(response, maxManifestSize)
				if readErr != nil || !strings.Contains(string(data), "#EXTM3U") {
					status, message = "error", "invalid HLS manifest"
				} else {
					resolution, codecs, bitrate = manifestDetails(string(data))
				}
			}
		}
	}
	if logo != "" {
		logoRequest, _ := http.NewRequestWithContext(r.Context(), http.MethodHead, logo, nil)
		logoResponse, logoErr := a.sourceClient.Do(logoRequest)
		logoAccessible = logoErr == nil && logoResponse.StatusCode >= 200 && logoResponse.StatusCode < 400
		if logoResponse != nil {
			logoResponse.Body.Close()
		}
	}
	latency := int(time.Since(started).Milliseconds())
	_, _ = a.db.ExecContext(r.Context(), `INSERT INTO channel_diagnostics(channel_id,status,latency_ms,resolution,codecs,bitrate,error,checked_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(channel_id) DO UPDATE SET status=excluded.status,latency_ms=excluded.latency_ms,resolution=excluded.resolution,codecs=excluded.codecs,bitrate=excluded.bitrate,error=excluded.error,checked_at=excluded.checked_at`, id, status, latency, resolution, codecs, bitrate, message, nowUTC())
	writeJSON(w, 200, map[string]any{"channel_id": id, "status": status, "latency_ms": latency, "error": message, "logo": logo, "logo_accessible": logoAccessible, "duplicates": duplicates, "resolution": resolution, "codecs": codecs, "bitrate": bitrate})
}

var resolutionPattern = regexp.MustCompile(`RESOLUTION=([^,\r\n]+)`)
var codecsPattern = regexp.MustCompile(`CODECS="([^"]+)"`)
var bandwidthPattern = regexp.MustCompile(`BANDWIDTH=([0-9]+)`)

func manifestDetails(manifest string) (resolution, codecs string, bitrate int) {
	if match := resolutionPattern.FindStringSubmatch(manifest); len(match) == 2 {
		resolution = match[1]
	}
	if match := codecsPattern.FindStringSubmatch(manifest); len(match) == 2 {
		codecs = match[1]
	}
	if match := bandwidthPattern.FindStringSubmatch(manifest); len(match) == 2 {
		bitrate, _ = strconv.Atoi(match[1])
	}
	return
}

func (a *App) replaceFileSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid source id")
		return
	}
	var kind string
	if err := a.db.QueryRowContext(r.Context(), `SELECT kind FROM sources WHERE id=?`, id).Scan(&kind); err != nil || kind != "file" {
		writeError(w, 400, "file source not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, 400, "invalid or oversized upload")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err != nil || len(data) > 10<<20 {
		writeError(w, 400, "cannot read file")
		return
	}
	channels := playlist.Parse(string(data))
	if len(channels) == 0 {
		writeError(w, 400, "no AceStream channels found")
		return
	}
	added, updated, removed, err := a.replaceChannels(r.Context(), id, channels)
	if err != nil {
		writeError(w, 500, "cannot replace channels")
		return
	}
	writeJSON(w, 200, map[string]any{"added": added, "updated": updated, "removed": removed})
}

func intQuery(r *http.Request, key string, fallback, max int) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(key))
	if v < 0 {
		v = 0
	}
	if v == 0 {
		v = fallback
	}
	if v > max {
		v = max
	}
	return v
}
