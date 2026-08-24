package server

import "time"

const (
	AnalyticsScopeOrganization = "organization"
	AnalyticsScopeProject      = "project"

	TokenCostSchemaVersion = "1.0"
	TokenCostStatusSuccess = "success"
	TokenCostStatusError   = "error"

	TokenCostIncrementalSnapshot = "snapshot"
	TokenCostIncrementalChanges  = "changes"
	requestLogSequenceName       = "request_logs"
	requestLogCheckpointLockName = "tokenhub:request-log-checkpoint"
)

// AnalyticsSequence is SQLite's transactionally updated request-log checkpoint.
// PostgreSQL stores a portable transaction-ID offset and a one-time history
// migration marker so pg_dump/pg_restore can preserve existing watermarks.
type AnalyticsSequence struct {
	Name            string `gorm:"primaryKey"`
	LastValue       int64  `gorm:"not null"`
	SequenceOffset  int64  `gorm:"not null;default:0"`
	HistoryMigrated bool   `gorm:"not null;default:false"`
}

// AnalyticsCredential is a read-only credential for the versioned analytics
// API. It cannot authenticate to any model gateway endpoint.
type AnalyticsCredential struct {
	ID         string     `json:"id" gorm:"primaryKey"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-" gorm:"uniqueIndex"`
	KeyPrefix  string     `json:"key_prefix"`
	KeySuffix  string     `json:"key_suffix"`
	ScopeType  string     `json:"scope_type" gorm:"index"`
	ProjectID  string     `json:"project_id,omitempty" gorm:"index"`
	Status     string     `json:"status" gorm:"index"`
	CreatedBy  string     `json:"created_by,omitempty" gorm:"index"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty" gorm:"index"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type TokenCostMetrics struct {
	RequestCount      int64   `json:"request_count"`
	ErrorCount        int64   `json:"error_count"`
	InputTokens       int64   `json:"input_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	CacheWriteTokens  int64   `json:"cache_write_input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	ReasoningTokens   int64   `json:"reasoning_output_tokens"`
	TotalTokens       int64   `json:"total_tokens"`
	EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
}

type TokenCostRow struct {
	DedupeKey  string           `json:"dedupe_key"`
	Bucket     string           `json:"bucket,omitempty"`
	RequestID  string           `json:"request_id,omitempty"`
	OccurredAt string           `json:"occurred_at,omitempty"`
	ProjectID  string           `json:"project_id,omitempty"`
	UserID     string           `json:"user_id,omitempty"`
	APIKeyID   string           `json:"api_key_id,omitempty"`
	ProviderID string           `json:"provider_id,omitempty"`
	Model      string           `json:"model,omitempty"`
	Status     string           `json:"status,omitempty"`
	StatusCode int              `json:"status_code,omitempty"`
	Metrics    TokenCostMetrics `json:"metrics"`
}

type TokenCostQueryMetadata struct {
	From            string            `json:"from"`
	To              string            `json:"to"`
	Granularity     string            `json:"granularity"`
	GroupBy         []string          `json:"group_by"`
	Filters         map[string]string `json:"filters"`
	Format          string            `json:"format"`
	Limit           int               `json:"limit"`
	DedupeBy        string            `json:"dedupe_by"`
	CheckpointBy    string            `json:"checkpoint_by"`
	IncrementalMode string            `json:"incremental_mode"`
}

type TokenCostResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	Object        string                 `json:"object"`
	GeneratedAt   string                 `json:"generated_at"`
	Query         TokenCostQueryMetadata `json:"query"`
	Data          []TokenCostRow         `json:"data"`
	HasMore       bool                   `json:"has_more"`
	NextCursor    string                 `json:"next_cursor,omitempty"`
	Watermark     string                 `json:"watermark"`
}

// TokenCostPage keeps the exported rows and their checkpoint together so the
// store can derive both from one consistent database snapshot.
type TokenCostPage struct {
	Rows       []TokenCostRow
	HasMore    bool
	Checkpoint int64
}

type TokenCostQuery struct {
	From               time.Time
	To                 time.Time
	ProjectID          string
	UserID             string
	APIKeyID           string
	ProviderID         string
	Model              string
	Status             string
	Granularity        string
	GroupBy            []string
	Limit              int
	AfterAt            time.Time
	AfterID            string
	Offset             int
	AfterSequence      int64
	ThroughSequence    int64
	ThroughSequenceSet bool
	Incremental        bool
}
