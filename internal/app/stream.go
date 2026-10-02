package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxManifestSize = 2 << 20

var manifestURIAttribute = regexp.MustCompile(`URI="([^"]+)"`)

func (a *App) startChannelPlayback(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	viewerID := newViewerID()
	info, err := a.ensurePlaybackViewer(r.Context(), id, true, viewerID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"playback_url": info.PlaybackURL,
		"proxy_url":    streamProxyURLForViewer(id, info.PlaybackURL, viewerID),
		"viewer_id":    viewerID,
	})
}

func newViewerID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", value[:])
}

func (a *App) heartbeatViewer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil || !a.streams.touchViewer(id, r.PathValue("viewer")) {
		writeError(w, http.StatusNotFound, "playback session not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) closeViewer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	a.streams.removeViewer(id, r.PathValue("viewer"))
	w.WriteHeader(http.StatusNoContent)
}

type playbackInfo struct {
	PlaybackURL string
	CommandURL  string
	NewSession  bool
}

func (a *App) ensurePlayback(ctx context.Context, id int64) (playbackInfo, error) {
	return a.ensurePlaybackMode(ctx, id, true)
}

func (a *App) ensurePlaybackMode(ctx context.Context, id int64, recordHistory bool) (playbackInfo, error) {
	return a.ensurePlaybackViewer(ctx, id, recordHistory, "")
}

func (a *App) ensurePlaybackViewer(ctx context.Context, id int64, recordHistory bool, viewerID string) (playbackInfo, error) {
	lock := a.streams.channelLock(id)
	lock.Lock()
	defer lock.Unlock()
	a.streams.mu.Lock()
	if s := a.streams.items[id]; s != nil {
		now := time.Now()
		s.LastAccess = now
		if viewerID != "" {
			if s.viewerActivity == nil {
				s.viewerActivity = make(map[string]time.Time)
			}
			s.viewerActivity[viewerID] = now
			s.Viewers = len(s.viewerActivity)
		}
		info := playbackInfo{PlaybackURL: s.PlaybackURL, CommandURL: s.CommandURL}
		a.streams.mu.Unlock()
		return info, nil
	}
	a.streams.mu.Unlock()
	var aceStreamID, name, logo, sourceKind string
	if err := a.db.QueryRowContext(ctx, `SELECT c.acestream_id,c.name,c.logo,s.kind FROM channels c JOIN sources s ON s.id=c.source_id WHERE c.id=?`, id).Scan(&aceStreamID, &name, &logo, &sourceKind); err != nil {
		return playbackInfo{}, errors.New("channel not found")
	}
	settings, err := a.loadSettings(ctx)
	if err != nil {
		return playbackInfo{}, err
	}
	if !a.streams.reserve(settings.MaxStreams) {
		return playbackInfo{}, errors.New("maximum active streams reached")
	}
	defer a.streams.finishReservation()
	viewers := make(map[string]time.Time)
	if viewerID != "" {
		viewers[viewerID] = time.Now()
	}
	if sourceKind == "redbull_padel" {
		playbackURL, playbackErr := a.redBullPlaybackURL(ctx, aceStreamID)
		if playbackErr != nil {
			return playbackInfo{}, playbackErr
		}
		now := time.Now()
		a.streams.mu.Lock()
		a.streams.items[id] = &managedStream{ChannelID: id, ChannelName: name, ChannelLogo: logo, PlaybackURL: playbackURL, StartedAt: now, LastAccess: now, Viewers: len(viewers), viewerActivity: viewers}
		a.streams.mu.Unlock()
		a.metrics.playbacks.Add(1)
		if recordHistory {
			_, _ = a.db.ExecContext(ctx, `INSERT INTO playback_history(channel_id,channel_name,started_at) VALUES (?,?,?)`, id, name, nowUTC())
			_, _ = a.db.ExecContext(ctx, `DELETE FROM playback_history WHERE id NOT IN (SELECT id FROM playback_history ORDER BY started_at DESC LIMIT 500) OR started_at < ?`, nowUTC().AddDate(0, 0, -settings.HistoryRetentionDays))
		}
		return playbackInfo{PlaybackURL: playbackURL, NewSession: true}, nil
	}
	engine, err := url.Parse(settings.EngineURL)
	if err != nil || engine.Host == "" {
		return playbackInfo{}, errors.New("configure a valid Ace Engine URL first")
	}
	startURL, _ := url.Parse(strings.TrimRight(engine.String(), "/") + "/ace/manifest.m3u8")
	q := startURL.Query()
	q.Set("format", "json")
	q.Set("id", aceStreamID)
	startURL.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, startURL.String(), nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AceBridge/0.2")
	client := *a.client
	client.Timeout = 90 * time.Second
	engineStarted := time.Now()
	resp, err := client.Do(req)
	a.metrics.engineRequests.Add(1)
	a.metrics.engineLatencyMs.Add(uint64(time.Since(engineStarted).Milliseconds()))
	if err != nil {
		return playbackInfo{}, fmt.Errorf("could not start Ace Engine playback: %w", err)
	}
	data, err := readLimitedBody(resp, 1<<20)
	if err != nil {
		return playbackInfo{}, err
	}
	var payload struct {
		Response struct {
			PlaybackURL string `json:"playback_url"`
			CommandURL  string `json:"command_url"`
		} `json:"response"`
		Error any `json:"error"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.Response.PlaybackURL == "" {
		return playbackInfo{}, errors.New("invalid playback response from Ace Engine")
	}
	playbackURL, err := engineResourceURL(engine, payload.Response.PlaybackURL)
	if err != nil {
		return playbackInfo{}, err
	}
	commandURL := ""
	if payload.Response.CommandURL != "" {
		commandURL, _ = engineResourceURL(engine, payload.Response.CommandURL)
	}
	now := time.Now()
	a.streams.mu.Lock()
	a.streams.items[id] = &managedStream{ChannelID: id, ChannelName: name, ChannelLogo: logo, PlaybackURL: playbackURL, CommandURL: commandURL, StartedAt: now, LastAccess: now, Viewers: len(viewers), viewerActivity: viewers}
	a.streams.mu.Unlock()
	a.metrics.playbacks.Add(1)
	if recordHistory {
		_, _ = a.db.ExecContext(ctx, `INSERT INTO playback_history(channel_id,channel_name,started_at) VALUES (?,?,?)`, id, name, nowUTC())
		_, _ = a.db.ExecContext(ctx, `DELETE FROM playback_history WHERE id NOT IN (SELECT id FROM playback_history ORDER BY started_at DESC LIMIT 500) OR started_at < ?`, nowUTC().AddDate(0, 0, -settings.HistoryRetentionDays))
	}
	return playbackInfo{PlaybackURL: playbackURL, CommandURL: commandURL, NewSession: true}, nil
}

func engineResourceURL(engine *url.URL, raw string) (string, error) {
	resource, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || resource.Path == "" {
		return "", errors.New("invalid engine resource")
	}
	resource.Scheme = engine.Scheme
	resource.Host = engine.Host
	return resource.String(), nil
}

func (a *App) streamChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	var aceStreamID, sourceKind string
	if err := a.db.QueryRowContext(r.Context(), `SELECT c.acestream_id,s.kind FROM channels c JOIN sources s ON s.id=c.source_id WHERE c.id=?`, id).Scan(&aceStreamID, &sourceKind); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot load Ace Engine settings")
		return
	}
	engine, err := url.Parse(settings.EngineURL)
	if sourceKind != "redbull_padel" && (err != nil || engine.Host == "") {
		writeError(w, http.StatusBadRequest, "configure a valid Ace Engine URL first")
		return
	}

	target := r.URL.Query().Get("url")
	viewerID := r.URL.Query().Get("viewer")
	if target == "" {
		if viewerID == "" {
			viewerID = newViewerID()
		}
		info, playbackErr := a.ensurePlaybackViewer(r.Context(), id, true, viewerID)
		if playbackErr != nil {
			writeError(w, http.StatusBadGateway, playbackErr.Error())
			return
		}
		target = info.PlaybackURL
	}
	a.streams.touchViewer(id, viewerID)
	targetURL, err := url.Parse(target)
	validTarget := sourceKind == "redbull_padel" && allowedRedBullStreamURL(targetURL)
	if sourceKind != "redbull_padel" {
		validTarget = err == nil && sameOrigin(engine, targetURL)
	}
	if err != nil || !validTarget {
		writeError(w, http.StatusBadRequest, "invalid stream resource")
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid stream resource")
		return
	}
	if sourceKind == "redbull_padel" {
		request.Header.Set("User-Agent", redBullUserAgent)
	} else {
		request.Header.Set("User-Agent", "AceBridge/0.1")
	}
	request.Header.Set("Accept", r.Header.Get("Accept"))
	if value := r.Header.Get("Range"); value != "" {
		request.Header.Set("Range", value)
	}
	streamClient := *a.client
	streamClient.Timeout = 90 * time.Second
	response, err := streamClient.Do(request)
	if err != nil {
		a.streams.invalidate(id, target)
		writeError(w, http.StatusBadGateway, "Ace Engine is not responding")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		a.streams.invalidate(id, target)
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Ace Engine returned %s", response.Status))
		return
	}

	responseURL := targetURL
	if response.Request != nil && response.Request.URL != nil {
		responseURL = response.Request.URL
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "mpegurl") || strings.HasSuffix(strings.ToLower(responseURL.Path), ".m3u8") {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, maxManifestSize+1))
		if readErr != nil || len(data) > maxManifestSize {
			writeError(w, http.StatusBadGateway, "invalid HLS manifest")
			return
		}
		manifest := rewriteManifestForViewer(string(data), responseURL, id, viewerID)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, manifest)
		return
	}

	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Cache-Control", "Accept-Ranges"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func rewriteManifestForViewer(manifest string, base *url.URL, channelID int64, viewerID string) string {
	lines := strings.Split(manifest, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			lines[index] = manifestURIAttribute.ReplaceAllStringFunc(line, func(match string) string {
				parts := manifestURIAttribute.FindStringSubmatch(match)
				if len(parts) != 2 {
					return match
				}
				return `URI="` + streamProxyURLForViewer(channelID, resolveStreamURL(base, parts[1]), viewerID) + `"`
			})
			continue
		}
		lines[index] = streamProxyURLForViewer(channelID, resolveStreamURL(base, trimmed), viewerID)
	}
	return strings.Join(lines, "\n")
}

func resolveStreamURL(base *url.URL, raw string) string {
	reference, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return base.ResolveReference(reference).String()
}

func streamProxyURLForViewer(channelID int64, target, viewerID string) string {
	result := fmt.Sprintf("/api/v1/channels/%d/stream?url=%s", channelID, url.QueryEscape(target))
	if viewerID != "" {
		result += "&viewer=" + url.QueryEscape(viewerID)
	}
	return result
}

func sameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
