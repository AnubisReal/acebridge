package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anubisreal/acebridge/internal/playlist"
)

func (a *App) syncSource(ctx context.Context, id int64) error {
	lockValue, _ := a.syncLocks.LoadOrStore(id, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	if !lock.TryLock() {
		return errors.New("source synchronization is already running")
	}
	defer lock.Unlock()
	jobID, err := a.startSyncJob(ctx, id, "running")
	if err != nil {
		return err
	}
	return a.runSyncJob(ctx, id, jobID)
}

func (a *App) queueSyncSource(ctx context.Context, id int64) (int64, error) {
	lockValue, _ := a.syncLocks.LoadOrStore(id, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	if !lock.TryLock() {
		return 0, errors.New("source synchronization is already running")
	}
	jobID, err := a.startSyncJob(ctx, id, "queued")
	if err != nil {
		lock.Unlock()
		return 0, err
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer lock.Unlock()
		_, _ = a.db.Exec(`UPDATE sync_jobs SET status='running' WHERE id=?`, jobID)
		_ = a.runSyncJob(context.Background(), id, jobID)
	}()
	return jobID, nil
}

func (a *App) startSyncJob(ctx context.Context, sourceID int64, status string) (int64, error) {
	result, err := a.db.ExecContext(ctx, `INSERT INTO sync_jobs(source_id,status,started_at) VALUES (?,?,?)`, sourceID, status, nowUTC())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (a *App) runSyncJob(ctx context.Context, id, jobID int64) error {
	fail := func(syncErr error) error { a.finishSyncJob(ctx, jobID, 0, 0, 0, syncErr.Error()); return syncErr }
	var kind, sourceURL, etag, lastModified string
	if err := a.db.QueryRowContext(ctx, `SELECT kind,url,etag,last_modified FROM sources WHERE id=?`, id).Scan(&kind, &sourceURL, &etag, &lastModified); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail(errors.New("source not found"))
		}
		return fail(err)
	}
	if kind == "redbull_padel" {
		channels, syncErr := a.fetchRedBullPadelChannels(ctx, sourceURL)
		if syncErr != nil {
			a.markSourceError(ctx, id, syncErr)
			return fail(syncErr)
		}
		added, updated, removed, syncErr := a.replaceChannels(ctx, id, channels)
		if syncErr != nil {
			a.markSourceError(ctx, id, syncErr)
			return fail(syncErr)
		}
		a.finishSyncJob(ctx, jobID, added, updated, removed, "")
		return nil
	}
	if kind != "url" {
		return fail(errors.New("uploaded files cannot be synchronized"))
	}

	fetchURL, err := normalizeSourceFetchURL(sourceURL)
	if err != nil {
		return fail(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return fail(err)
	}
	request.Header.Set("User-Agent", "AceBridge/0.1")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		request.Header.Set("If-Modified-Since", lastModified)
	}
	response, err := a.fetchSourceWithRetry(request)
	if err != nil {
		a.markSourceError(ctx, id, err)
		return fail(fmt.Errorf("cannot download source: %w", err))
	}
	if response.StatusCode == http.StatusNotModified {
		response.Body.Close()
		a.finishSyncJob(ctx, jobID, 0, 0, 0, "")
		_, _ = a.db.ExecContext(ctx, `UPDATE sources SET last_status='healthy',last_error='',last_sync_at=? WHERE id=?`, nowUTC(), id)
		return nil
	}
	data, err := readLimitedBody(response, 10<<20)
	if err != nil {
		a.markSourceError(ctx, id, err)
		return fail(err)
	}
	channels := playlist.Parse(string(data))
	if len(channels) == 0 {
		err = errors.New("no AceStream channels found")
		a.markSourceError(ctx, id, err)
		return fail(err)
	}
	added, updated, removed, err := a.replaceChannels(ctx, id, channels)
	if err != nil {
		a.markSourceError(ctx, id, err)
		a.finishSyncJob(ctx, jobID, 0, 0, 0, err.Error())
		return err
	}
	_, _ = a.db.ExecContext(ctx, `UPDATE sources SET etag=?,last_modified=? WHERE id=?`, response.Header.Get("ETag"), response.Header.Get("Last-Modified"), id)
	a.finishSyncJob(ctx, jobID, added, updated, removed, "")
	return nil
}

func normalizeSourceFetchURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("invalid source URL")
	}
	const suffix = ".ipns.inbrowser.link"
	host := strings.ToLower(parsed.Hostname())
	if !strings.HasSuffix(host, suffix) {
		return parsed.String(), nil
	}
	key := strings.TrimSuffix(host, suffix)
	if key == "" || strings.Contains(key, ".") {
		return "", errors.New("invalid IPNS source URL")
	}
	resolved := &url.URL{Scheme: "https", Host: "ipfs.io", Path: "/ipns/" + key + parsed.Path, RawQuery: parsed.RawQuery}
	return resolved.String(), nil
}

func (a *App) fetchSourceWithRetry(request *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(1<<uint(attempt-1)) * time.Second)
			select {
			case <-request.Context().Done():
				timer.Stop()
				return nil, request.Context().Err()
			case <-timer.C:
			}
		}
		response, err := a.sourceClient.Do(request.Clone(request.Context()))
		if err == nil {
			if response.StatusCode < 500 || attempt == 2 {
				return response, nil
			}
			response.Body.Close()
			lastErr = fmt.Errorf("remote server returned %s", response.Status)
			continue
		}
		lastErr = err
	}
	return nil, lastErr
}

func (a *App) finishSyncJob(ctx context.Context, id int64, added, updated, removed int, jobErr string) {
	a.metrics.syncs.Add(1)
	status := "completed"
	if jobErr != "" {
		status = "error"
	}
	_, _ = a.db.ExecContext(ctx, `UPDATE sync_jobs SET status=?,added=?,updated=?,removed=?,error=?,finished_at=? WHERE id=?`, status, added, updated, removed, jobErr, nowUTC(), id)
}

func (a *App) replaceChannels(ctx context.Context, sourceID int64, channels []playlist.Channel) (added, updated, removed int, err error) {
	a.enrichChannelLogos(ctx, channels, true)
	err = withTx(ctx, a.db, func(tx *sql.Tx) error {
		type previousChannel struct {
			id                       int64
			enabled, locked          bool
			tvgID, name, group, logo string
			aceStreamID              string
		}
		existing := make(map[string]previousChannel)
		rows, err := tx.QueryContext(ctx, `SELECT id,source_acestream_id,enabled,metadata_locked,tvg_id,name,group_name,logo,acestream_id FROM channels WHERE source_id=?`, sourceID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var enabled, locked int
			var previous previousChannel
			if rows.Scan(&previous.id, &id, &enabled, &locked, &previous.tvgID, &previous.name, &previous.group, &previous.logo, &previous.aceStreamID) == nil {
				previous.enabled = enabled == 1
				previous.locked = locked == 1
				existing[id] = previous
			}
		}
		rows.Close()
		seen := make(map[string]struct{})
		count := 0
		for _, channel := range channels {
			sourceAceStreamID := channel.AceStreamID
			if _, ok := seen[channel.AceStreamID]; ok {
				continue
			}
			seen[channel.AceStreamID] = struct{}{}
			enabled := true
			locked := false
			if value, ok := existing[channel.AceStreamID]; ok {
				enabled = value.enabled
				locked = value.locked
				if value.locked {
					channel.TVGID = value.tvgID
					channel.Name = value.name
					channel.Group = value.group
					channel.Logo = value.logo
					channel.AceStreamID = value.aceStreamID
				}
				_, err = tx.ExecContext(ctx, `UPDATE channels SET tvg_id=?,name=?,group_name=?,logo=?,acestream_id=?,enabled=?,metadata_locked=? WHERE id=?`, channel.TVGID, channel.Name, channel.Group, channel.Logo, channel.AceStreamID, enabled, locked, value.id)
				updated++
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO channels(source_id,tvg_id,name,group_name,logo,acestream_id,source_acestream_id,enabled,metadata_locked) VALUES (?,?,?,?,?,?,?,?,?)`, sourceID, channel.TVGID, channel.Name, channel.Group, channel.Logo, channel.AceStreamID, sourceAceStreamID, enabled, locked)
				added++
			}
			if err != nil {
				return err
			}
			count++
		}
		for sourceIDKey, previous := range existing {
			if _, ok := seen[sourceIDKey]; !ok {
				if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE id=?`, previous.id); err != nil {
					return err
				}
				removed++
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE sources SET channel_count=?,last_status='healthy',last_error='',last_sync_at=? WHERE id=?`, count, nowUTC(), sourceID)
		return err
	})
	return
}

func (a *App) markSourceError(ctx context.Context, id int64, syncErr error) {
	_, _ = a.db.ExecContext(ctx, `UPDATE sources SET last_status='error',last_error=?,last_sync_at=? WHERE id=?`, syncErr.Error(), nowUTC(), id)
}

func (a *App) loadSettings(ctx context.Context) (Settings, error) {
	settings := Settings{UpdateInterval: 4, MaxStreams: 4, StreamIdleSeconds: 60, HistoryRetentionDays: 90}
	rows, err := a.db.QueryContext(ctx, `SELECT key,value FROM settings`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, err
		}
		switch key {
		case "engine_url":
			settings.EngineURL = value
		case "public_base_url":
			settings.PublicBaseURL = value
		case "update_interval_hours":
			settings.UpdateInterval, _ = strconv.Atoi(value)
		case "max_streams":
			settings.MaxStreams, _ = strconv.Atoi(value)
		case "stream_idle_seconds":
			settings.StreamIdleSeconds, _ = strconv.Atoi(value)
		case "history_retention_days":
			settings.HistoryRetentionDays, _ = strconv.Atoi(value)
		case "diagnostics_enabled":
			settings.DiagnosticsEnabled = value == "1"
		}
	}
	settings.EngineURL = strings.TrimRight(settings.EngineURL, "/")
	return settings, rows.Err()
}

func (a *App) runScheduler(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.syncDueSources(ctx)
		}
	}
}

func (a *App) syncDueSources(ctx context.Context) {
	settings, err := a.loadSettings(ctx)
	if err != nil {
		a.logger.Error("scheduler cannot load settings", "error", err)
		return
	}
	cutoff := nowUTC().Add(-time.Duration(settings.UpdateInterval) * time.Hour)
	redBullCutoff := nowUTC().Add(-5 * time.Minute)
	rows, err := a.db.QueryContext(ctx, `SELECT id FROM sources WHERE enabled=1 AND ((kind='url' AND (last_sync_at IS NULL OR last_sync_at < ?)) OR (kind='redbull_padel' AND (last_sync_at IS NULL OR last_sync_at < ?)))`, cutoff, redBullCutoff)
	if err != nil {
		a.logger.Error("scheduler cannot load sources", "error", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := a.syncSource(ctx, id); err != nil {
			a.logger.Warn("scheduled sync failed", "source_id", id, "error", err)
		}
	}
}
