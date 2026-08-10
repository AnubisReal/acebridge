package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func openDatabase(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "acebridge.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL);
CREATE TABLE IF NOT EXISTS sources (
 id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, kind TEXT NOT NULL,
 url TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
 channel_count INTEGER NOT NULL DEFAULT 0, last_status TEXT NOT NULL DEFAULT 'pending',
 last_error TEXT NOT NULL DEFAULT '', last_sync_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS channels (
 id INTEGER PRIMARY KEY AUTOINCREMENT, source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
 tvg_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, group_name TEXT NOT NULL DEFAULT '', logo TEXT NOT NULL DEFAULT '',
 acestream_id TEXT NOT NULL, source_acestream_id TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, metadata_locked INTEGER NOT NULL DEFAULT 0,
 UNIQUE(source_id, acestream_id)
);
CREATE INDEX IF NOT EXISTS idx_channels_name ON channels(name);
CREATE INDEX IF NOT EXISTS idx_channels_group ON channels(group_name);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT OR IGNORE INTO settings(key,value) VALUES ('engine_url','http://127.0.0.1:6878');
INSERT OR IGNORE INTO settings(key,value) VALUES ('public_base_url','');
INSERT OR IGNORE INTO settings(key,value) VALUES ('update_interval_hours','4');
INSERT OR IGNORE INTO settings(key,value) VALUES ('max_streams','4');
INSERT OR IGNORE INTO settings(key,value) VALUES ('stream_idle_seconds','60');
INSERT OR IGNORE INTO settings(key,value) VALUES ('history_retention_days','90');
INSERT OR IGNORE INTO settings(key,value) VALUES ('diagnostics_enabled','0');
CREATE TABLE IF NOT EXISTS sync_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
 status TEXT NOT NULL, added INTEGER NOT NULL DEFAULT 0, updated INTEGER NOT NULL DEFAULT 0,
 removed INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', started_at DATETIME NOT NULL,
 finished_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_sync_jobs_source ON sync_jobs(source_id,id DESC);
CREATE TABLE IF NOT EXISTS playback_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT, channel_id INTEGER REFERENCES channels(id) ON DELETE SET NULL,
 channel_name TEXT NOT NULL, started_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_playback_history_started ON playback_history(started_at DESC);
CREATE TABLE IF NOT EXISTS channel_diagnostics (
 channel_id INTEGER PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE, status TEXT NOT NULL,
 latency_ms INTEGER NOT NULL DEFAULT 0, resolution TEXT NOT NULL DEFAULT '', codecs TEXT NOT NULL DEFAULT '',
 bitrate INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', checked_at DATETIME NOT NULL
);`
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if err := ensureColumn(tx, "channels", "metadata_locked", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(tx, "channels", "source_acestream_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(tx, "sources", "etag", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(tx, "sources", "last_modified", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE channels SET source_acestream_id=acestream_id WHERE source_acestream_id=''; INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES (1,CURRENT_TIMESTAMP),(2,CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	return tx.Commit()
}

type migrationExecutor interface {
	Query(string, ...any) (*sql.Rows, error)
	Exec(string, ...any) (sql.Result, error)
}

func ensureColumn(db migrationExecutor, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

func scanSource(scanner interface{ Scan(...any) error }) (Source, error) {
	var source Source
	var enabled int
	var lastSync sql.NullTime
	err := scanner.Scan(&source.ID, &source.Name, &source.Kind, &source.URL, &enabled, &source.ChannelCount, &source.LastStatus, &source.LastError, &lastSync, &source.CreatedAt)
	source.Enabled = enabled == 1
	if lastSync.Valid {
		t := lastSync.Time
		source.LastSyncAt = &t
	}
	return source, err
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
