package app

import "time"

type Source struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	URL          string     `json:"url,omitempty"`
	Enabled      bool       `json:"enabled"`
	ChannelCount int        `json:"channel_count"`
	LastStatus   string     `json:"last_status"`
	LastError    string     `json:"last_error,omitempty"`
	LastSyncAt   *time.Time `json:"last_sync_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Channel struct {
	ID          int64  `json:"id"`
	SourceID    int64  `json:"source_id"`
	SourceName  string `json:"source_name"`
	SourceKind  string `json:"source_kind"`
	TVGID       string `json:"tvg_id"`
	Name        string `json:"name"`
	Group       string `json:"group"`
	Logo        string `json:"logo"`
	AceStreamID string `json:"acestream_id"`
	StreamType  string `json:"stream_type"`
	Enabled     bool   `json:"enabled"`
}

type Settings struct {
	EngineURL            string `json:"engine_url"`
	PublicBaseURL        string `json:"public_base_url"`
	UpdateInterval       int    `json:"update_interval_hours"`
	MaxStreams           int    `json:"max_streams"`
	StreamIdleSeconds    int    `json:"stream_idle_seconds"`
	HistoryRetentionDays int    `json:"history_retention_days"`
	DiagnosticsEnabled   bool   `json:"diagnostics_enabled"`
}

type SyncJob struct {
	ID         int64      `json:"id"`
	SourceID   int64      `json:"source_id"`
	SourceName string     `json:"source_name"`
	Status     string     `json:"status"`
	Added      int        `json:"added"`
	Updated    int        `json:"updated"`
	Removed    int        `json:"removed"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
