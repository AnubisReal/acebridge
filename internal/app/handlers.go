package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/anubisreal/acebridge/internal/playlist"
)

const sourceColumns = `id,name,kind,url,enabled,channel_count,last_status,last_error,last_sync_at,created_at`

func (a *App) health(w http.ResponseWriter, _ *http.Request) {
	if err := a.db.Ping(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy", "service": "acebridge"})
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	var channels, enabled, sources, groups, failed, problems int
	var lastSync *string
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(enabled),0), COUNT(DISTINCT group_name) FROM channels`).Scan(&channels, &enabled, &groups)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM sources`).Scan(&sources)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM sources WHERE last_status='error'`).Scan(&failed)
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM channel_diagnostics WHERE status='error'`).Scan(&problems)
	_ = a.db.QueryRowContext(r.Context(), `SELECT MAX(last_sync_at) FROM sources`).Scan(&lastSync)
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels, "enabled_channels": enabled, "sources": sources, "groups": groups, "failed_sources": failed, "problem_channels": problems, "active_streams": len(a.streams.list()), "last_sync_at": lastSync})
}

func (a *App) listChannels(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := intQuery(r, "limit", 10000, 10000)
	offset := intQuery(r, "offset", 0, 1000000)
	orderBy := "c.group_name,c.name"
	switch r.URL.Query().Get("order") {
	case "name":
		orderBy = "c.name"
	case "source":
		orderBy = "s.name,c.name"
	case "newest":
		orderBy = "c.id DESC"
	}
	baseWhere := `(?='' OR c.name LIKE '%'||?||'%' OR c.group_name LIKE '%'||?||'%') AND (?='' OR c.group_name=?) AND (?='' OR (?='enabled' AND c.enabled=1) OR (?='disabled' AND c.enabled=0) OR (?='problem' AND EXISTS(SELECT 1 FROM channel_diagnostics d WHERE d.channel_id=c.id AND d.status='error')))`
	args := []any{query, query, query, group, group, status, status, status, status}
	var total int
	_ = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM channels c JOIN sources s ON s.id=c.source_id WHERE `+baseWhere, args...).Scan(&total)
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	rows, err := a.db.QueryContext(r.Context(), `SELECT c.id,c.source_id,s.name,s.kind,c.tvg_id,c.name,c.group_name,c.logo,c.acestream_id,c.enabled
FROM channels c JOIN sources s ON s.id=c.source_id
WHERE `+baseWhere+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		writeError(w, 500, "cannot load channels")
		return
	}
	defer rows.Close()
	items := make([]Channel, 0)
	for rows.Next() {
		var item Channel
		var enabled int
		if err := rows.Scan(&item.ID, &item.SourceID, &item.SourceName, &item.SourceKind, &item.TVGID, &item.Name, &item.Group, &item.Logo, &item.AceStreamID, &enabled); err != nil {
			continue
		}
		item.Enabled = enabled == 1
		if item.SourceKind == "redbull_padel" {
			item.StreamType = "hls"
		} else {
			item.StreamType = "acestream"
		}
		items = append(items, item)
	}
	writeJSON(w, 200, items)
}

func (a *App) listChannelGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT group_name,COUNT(*) FROM channels GROUP BY group_name ORDER BY group_name`)
	if err != nil {
		writeError(w, 500, "cannot load channel groups")
		return
	}
	defer rows.Close()
	groups := make([]map[string]any, 0)
	for rows.Next() {
		var name string
		var count int
		if rows.Scan(&name, &count) == nil {
			groups = append(groups, map[string]any{"name": name, "count": count})
		}
	}
	writeJSON(w, 200, groups)
}

func (a *App) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid channel id")
		return
	}
	var input struct {
		Enabled     *bool   `json:"enabled,omitempty"`
		Name        *string `json:"name,omitempty"`
		TVGID       *string `json:"tvg_id,omitempty"`
		Group       *string `json:"group,omitempty"`
		Logo        *string `json:"logo,omitempty"`
		AceStreamID *string `json:"acestream_id,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var channel Channel
	var enabled int
	err = a.db.QueryRowContext(r.Context(), `SELECT c.id,c.source_id,s.name,s.kind,c.tvg_id,c.name,c.group_name,c.logo,c.acestream_id,c.enabled FROM channels c JOIN sources s ON s.id=c.source_id WHERE c.id=?`, id).Scan(&channel.ID, &channel.SourceID, &channel.SourceName, &channel.SourceKind, &channel.TVGID, &channel.Name, &channel.Group, &channel.Logo, &channel.AceStreamID, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "channel not found")
		return
	}
	if err != nil {
		writeError(w, 500, "cannot load channel")
		return
	}
	channel.Enabled = enabled == 1
	metadataChanged := input.Name != nil || input.TVGID != nil || input.Group != nil || input.Logo != nil || input.AceStreamID != nil
	if input.Enabled != nil {
		channel.Enabled = *input.Enabled
	}
	if input.Name != nil {
		channel.Name = strings.TrimSpace(*input.Name)
	}
	if input.TVGID != nil {
		channel.TVGID = strings.TrimSpace(*input.TVGID)
	}
	if input.Group != nil {
		channel.Group = strings.TrimSpace(*input.Group)
	}
	if input.Logo != nil {
		channel.Logo = strings.TrimSpace(*input.Logo)
	}
	if input.AceStreamID != nil && channel.SourceKind != "redbull_padel" {
		channel.AceStreamID = playlist.ExtractAceStreamID(strings.TrimSpace(*input.AceStreamID))
		if channel.AceStreamID == "" {
			writeError(w, 400, "a valid 40-character AceStream ID is required")
			return
		}
	}
	if channel.Name == "" {
		writeError(w, 400, "channel name is required")
		return
	}
	if channel.Logo != "" {
		parsed, parseErr := url.Parse(channel.Logo)
		if parseErr != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			writeError(w, 400, "logo must be a valid HTTP URL")
			return
		}
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE channels SET enabled=?,name=?,tvg_id=?,group_name=?,logo=?,acestream_id=?,metadata_locked=CASE WHEN ? THEN 1 ELSE metadata_locked END WHERE id=?`, channel.Enabled, channel.Name, channel.TVGID, channel.Group, channel.Logo, channel.AceStreamID, metadataChanged, id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, 409, "this AceStream ID already exists in the source")
			return
		}
		writeError(w, 500, "cannot update channel")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		writeError(w, 404, "channel not found")
		return
	}
	writeJSON(w, 200, channel)
}

func (a *App) createRedBullPadelSource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "Red Bull Premier Padel"
	}
	var existing int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM sources WHERE kind='redbull_padel'`).Scan(&existing); err != nil {
		writeError(w, 500, "cannot check existing sources")
		return
	}
	if existing > 0 {
		writeError(w, 409, "Red Bull Premier Padel is already configured")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `INSERT INTO sources(name,kind,url) VALUES (?, 'redbull_padel', ?)`, input.Name, redBullEventsURL)
	if err != nil {
		writeError(w, 500, "cannot create source")
		return
	}
	id, _ := result.LastInsertId()
	if _, err := a.queueSyncSource(r.Context(), id); err != nil {
		a.logger.Warn("cannot queue initial Red Bull sync", "source_id", id, "error", err)
	}
	a.sourceByID(w, r, id, http.StatusCreated)
}

func (a *App) updateSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid source id")
		return
	}
	var input struct {
		Name    *string `json:"name,omitempty"`
		URL     *string `json:"url,omitempty"`
		Enabled *bool   `json:"enabled,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	row := a.db.QueryRowContext(r.Context(), `SELECT `+sourceColumns+` FROM sources WHERE id=?`, id)
	source, err := scanSource(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "source not found")
		return
	}
	if err != nil {
		writeError(w, 500, "cannot load source")
		return
	}
	if input.Name != nil {
		source.Name = strings.TrimSpace(*input.Name)
	}
	if input.Enabled != nil {
		source.Enabled = *input.Enabled
	}
	if input.URL != nil {
		if source.Kind != "url" {
			writeError(w, 400, "only URL sources can change their address")
			return
		}
		candidate := strings.TrimSpace(*input.URL)
		parsed, parseErr := url.Parse(candidate)
		if parseErr != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			writeError(w, 400, "a valid HTTP URL is required")
			return
		}
		source.URL = candidate
	}
	if source.Name == "" {
		writeError(w, 400, "source name is required")
		return
	}
	_, err = a.db.ExecContext(r.Context(), `UPDATE sources SET name=?,url=?,enabled=? WHERE id=?`, source.Name, source.URL, source.Enabled, id)
	if err != nil {
		writeError(w, 500, "cannot update source")
		return
	}
	a.sourceByID(w, r, id, 200)
}

func (a *App) createManualChannel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		AceStreamID string `json:"acestream_id"`
		Group       string `json:"group"`
		Logo        string `json:"logo"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.AceStreamID = playlist.ExtractAceStreamID(strings.TrimSpace(input.AceStreamID))
	input.Group = strings.TrimSpace(input.Group)
	input.Logo = strings.TrimSpace(input.Logo)
	if input.Name == "" || input.AceStreamID == "" {
		writeError(w, http.StatusBadRequest, "name and a valid 40-character AceStream ID are required")
		return
	}
	if input.Logo != "" {
		parsed, err := url.Parse(input.Logo)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			writeError(w, http.StatusBadRequest, "logo must be a valid HTTP URL")
			return
		}
	}
	if input.Group == "" {
		input.Group = "Manuales"
	}
	if input.Logo == "" {
		input.Logo = a.matchChannelLogo(r.Context(), "", input.Name)
	}

	var channelID int64
	err := withTx(r.Context(), a.db, func(tx *sql.Tx) error {
		var sourceID int64
		err := tx.QueryRowContext(r.Context(), `SELECT id FROM sources WHERE kind='manual' ORDER BY id LIMIT 1`).Scan(&sourceID)
		if errors.Is(err, sql.ErrNoRows) {
			result, insertErr := tx.ExecContext(r.Context(), `INSERT INTO sources(name,kind,last_status,last_sync_at) VALUES ('Canales manuales','manual','healthy',?)`, nowUTC())
			if insertErr != nil {
				return insertErr
			}
			sourceID, insertErr = result.LastInsertId()
			if insertErr != nil {
				return insertErr
			}
		} else if err != nil {
			return err
		}
		result, err := tx.ExecContext(r.Context(), `INSERT INTO channels(source_id,name,group_name,logo,acestream_id,source_acestream_id) VALUES (?,?,?,?,?,?)`, sourceID, input.Name, input.Group, input.Logo, input.AceStreamID, input.AceStreamID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return errors.New("this AceStream ID already exists in manual channels")
			}
			return err
		}
		channelID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE sources SET channel_count=(SELECT COUNT(*) FROM channels WHERE source_id=?) WHERE id=?`, sourceID, sourceID)
		return err
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	row := a.db.QueryRowContext(r.Context(), `SELECT c.id,c.source_id,s.name,s.kind,c.tvg_id,c.name,c.group_name,c.logo,c.acestream_id,c.enabled FROM channels c JOIN sources s ON s.id=c.source_id WHERE c.id=?`, channelID)
	var channel Channel
	var enabled int
	if err := row.Scan(&channel.ID, &channel.SourceID, &channel.SourceName, &channel.SourceKind, &channel.TVGID, &channel.Name, &channel.Group, &channel.Logo, &channel.AceStreamID, &enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "channel was created but could not be loaded")
		return
	}
	channel.Enabled = enabled == 1
	writeJSON(w, http.StatusCreated, channel)
}

func (a *App) listSources(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+sourceColumns+` FROM sources ORDER BY created_at DESC`)
	if err != nil {
		writeError(w, 500, "cannot load sources")
		return
	}
	defer rows.Close()
	items := make([]Source, 0)
	for rows.Next() {
		source, err := scanSource(rows)
		if err == nil {
			items = append(items, source)
		}
	}
	writeJSON(w, 200, items)
}

func (a *App) createURLSource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	parsed, err := url.Parse(input.URL)
	if input.Name == "" || err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		writeError(w, 400, "name and a valid HTTP URL are required")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `INSERT INTO sources(name,kind,url) VALUES (?, 'url', ?)`, input.Name, input.URL)
	if err != nil {
		writeError(w, 500, "cannot create source")
		return
	}
	id, _ := result.LastInsertId()
	if _, err := a.queueSyncSource(r.Context(), id); err != nil {
		a.logger.Warn("cannot queue initial source sync", "source_id", id, "error", err)
	}
	a.sourceByID(w, r, id, http.StatusCreated)
}

func (a *App) createFileSource(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, 400, "invalid or oversized upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "M3U, TXT or JSON file is required")
		return
	}
	defer file.Close()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = header.Filename
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if readErr != nil {
		writeError(w, 400, "cannot read file")
		return
	}
	if len(data) > 10<<20 {
		writeError(w, 400, "file exceeds 10 MB")
		return
	}
	channels := playlist.Parse(string(data))
	if len(channels) == 0 {
		writeError(w, 400, "no AceStream channels found")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `INSERT INTO sources(name,kind,last_status,last_sync_at) VALUES (?, 'file','healthy',?)`, name, nowUTC())
	if err != nil {
		writeError(w, 500, "cannot create source")
		return
	}
	id, _ := result.LastInsertId()
	if _, _, _, err := a.replaceChannels(r.Context(), id, channels); err != nil {
		writeError(w, 500, "cannot save channels")
		return
	}
	a.sourceByID(w, r, id, http.StatusCreated)
}

func (a *App) syncSourceHandler(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid source id")
		return
	}
	if err := a.syncSource(r.Context(), id); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	a.sourceByID(w, r, id, http.StatusOK)
}

func (a *App) queueSyncSourceHandler(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid source id")
		return
	}
	jobID, err := a.queueSyncSource(r.Context(), id)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "status": "queued"})
}

func (a *App) deleteSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, 400, "invalid source id")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `DELETE FROM sources WHERE id=?`, id)
	if err != nil {
		writeError(w, 500, "cannot delete source")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		writeError(w, 404, "source not found")
		return
	}
	w.WriteHeader(204)
}

func (a *App) sourceByID(w http.ResponseWriter, r *http.Request, id int64, status int) {
	row := a.db.QueryRowContext(r.Context(), `SELECT `+sourceColumns+` FROM sources WHERE id=?`, id)
	source, err := scanSource(row)
	if err == sql.ErrNoRows {
		writeError(w, 404, "source not found")
		return
	}
	if err != nil {
		writeError(w, 500, "cannot load source")
		return
	}
	writeJSON(w, status, source)
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		writeError(w, 500, "cannot load settings")
		return
	}
	writeJSON(w, 200, settings)
}

func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var input Settings
	if !decodeJSON(w, r, &input) {
		return
	}
	engine, err := normalizeEngineURL(input.EngineURL)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if input.UpdateInterval < 1 || input.UpdateInterval > 168 {
		writeError(w, 400, "update interval must be between 1 and 168 hours")
		return
	}
	if input.MaxStreams == 0 {
		input.MaxStreams = 4
	}
	if input.StreamIdleSeconds == 0 {
		input.StreamIdleSeconds = 60
	}
	if input.HistoryRetentionDays == 0 {
		input.HistoryRetentionDays = 90
	}
	if input.MaxStreams < 1 || input.MaxStreams > 32 || input.StreamIdleSeconds < 15 || input.StreamIdleSeconds > 3600 || input.HistoryRetentionDays < 1 || input.HistoryRetentionDays > 3650 {
		writeError(w, 400, "invalid operational settings")
		return
	}
	if input.PublicBaseURL != "" {
		parsed, err := url.Parse(strings.TrimRight(input.PublicBaseURL, "/"))
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			writeError(w, 400, "invalid public base URL")
			return
		}
		input.PublicBaseURL = parsed.String()
	}
	err = withTx(r.Context(), a.db, func(tx *sql.Tx) error {
		diagnostics := "0"
		if input.DiagnosticsEnabled {
			diagnostics = "1"
		}
		for key, value := range map[string]string{"engine_url": engine, "public_base_url": input.PublicBaseURL, "update_interval_hours": strconv.Itoa(input.UpdateInterval), "max_streams": strconv.Itoa(input.MaxStreams), "stream_idle_seconds": strconv.Itoa(input.StreamIdleSeconds), "history_retention_days": strconv.Itoa(input.HistoryRetentionDays), "diagnostics_enabled": diagnostics} {
			if _, err := tx.ExecContext(r.Context(), `INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, 500, "cannot save settings")
		return
	}
	input.EngineURL = engine
	writeJSON(w, 200, input)
}

func (a *App) checkEngine(w http.ResponseWriter, r *http.Request) {
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		writeError(w, 500, "cannot load settings")
		return
	}
	probeURL := settings.EngineURL + "/webui/api/service?method=get_version"
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, probeURL, nil)
	response, err := a.client.Do(request)
	if err != nil {
		writeJSON(w, 200, map[string]any{"online": false, "operational": false, "message": "No se pudo conectar con Ace Engine"})
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeJSON(w, 200, map[string]any{
			"online":      true,
			"operational": false,
			"status":      response.StatusCode,
			"message":     fmt.Sprintf("La API de Ace Engine respondió con HTTP %d", response.StatusCode),
		})
		return
	}

	var payload struct {
		Result struct {
			Version  string `json:"version"`
			Code     int    `json:"code"`
			Platform string `json:"platform"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.Error != nil || payload.Result.Version == "" {
		writeJSON(w, 200, map[string]any{
			"online":      true,
			"operational": false,
			"status":      response.StatusCode,
			"message":     "El servidor responde, pero no parece una API compatible de Ace Engine",
		})
		return
	}
	writeJSON(w, 200, map[string]any{
		"online":       true,
		"operational":  true,
		"status":       response.StatusCode,
		"message":      "Ace Engine operativo",
		"version":      payload.Result.Version,
		"platform":     payload.Result.Platform,
		"version_code": payload.Result.Code,
	})
}

func (a *App) playlist(w http.ResponseWriter, r *http.Request) {
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		writeError(w, 500, "cannot load settings")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT c.id,c.tvg_id,c.name,c.group_name,c.logo,c.acestream_id FROM channels c JOIN sources s ON s.id=c.source_id WHERE c.enabled=1 AND s.enabled=1 ORDER BY c.group_name,c.name`)
	if err != nil {
		writeError(w, 500, "cannot generate playlist")
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="acebridge.m3u"`)
	fmt.Fprintln(w, "#EXTM3U")
	base := strings.TrimRight(settings.PublicBaseURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	for rows.Next() {
		var id int64
		var tvgID, name, group, logo, aceID string
		if rows.Scan(&id, &tvgID, &name, &group, &logo, &aceID) != nil {
			continue
		}
		fmt.Fprintf(w, "#EXTINF:-1 tvg-id=%q tvg-name=%q group-title=%q tvg-logo=%q,%s\n%s/api/v1/channels/%d/stream\n", tvgID, name, group, logo, name, base, id)
	}
}
