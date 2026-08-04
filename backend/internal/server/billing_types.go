package server

import (
	"context"
	"time"
)

const (
	BillingConnectorAliyun = "aliyun"
	BillingConnectorNewAPI = "newapi"
	BillingConnectorOneAPI = "oneapi"

	BillingSyncRunning   = "running"
	BillingSyncSucceeded = "succeeded"
	BillingSyncFailed    = "failed"
)

// BillingConnector is the persisted configuration for one external billing
// source. Credentials are accepted through Credentials, encrypted into
// CredentialCiphertext by the store, and never serialized back to callers.
type BillingConnector struct {
	ID                      string            `json:"id" gorm:"primaryKey"`
	Name                    string            `json:"name"`
	Type                    string            `json:"type" gorm:"index"`
	BaseURL                 string            `json:"base_url"`
	Status                  string            `json:"status" gorm:"index"`
	ScheduleIntervalMinutes int               `json:"schedule_interval_minutes"`
	Config                  map[string]string `json:"config,omitempty" gorm:"serializer:json"`
	CredentialCiphertext    string            `json:"-" gorm:"type:text"`
	Credentials             map[string]string `json:"-" gorm:"-"`
	CredentialsConfigured   bool              `json:"credentials_configured" gorm:"-"`
	CredentialFields        []string          `json:"credential_fields,omitempty" gorm:"-"`
	Checkpoint              string            `json:"-" gorm:"type:text"`
	LastSyncedThrough       *time.Time        `json:"last_synced_through,omitempty"`
	LastSyncStatus          string            `json:"last_sync_status,omitempty" gorm:"index"`
	LastSyncMessage         string            `json:"last_sync_message,omitempty"`
	LastSyncAt              *time.Time        `json:"last_sync_at,omitempty"`
	NextSyncAt              *time.Time        `json:"next_sync_at,omitempty" gorm:"index"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

type BillingConnectorRequest struct {
	Name                    string            `json:"name"`
	Type                    string            `json:"type"`
	BaseURL                 string            `json:"base_url"`
	Status                  string            `json:"status"`
	ScheduleIntervalMinutes int               `json:"schedule_interval_minutes"`
	Config                  map[string]string `json:"config"`
	Credentials             map[string]string `json:"credentials"`
}

type BillingConnectorPatchRequest struct {
	Name                    *string           `json:"name"`
	BaseURL                 *string           `json:"base_url"`
	Status                  *string           `json:"status"`
	ScheduleIntervalMinutes *int              `json:"schedule_interval_minutes"`
	Config                  map[string]string `json:"config"`
	Credentials             map[string]string `json:"credentials"`
}

// BillingRecord is the provider-neutral billing model consumed by later
// reconciliation and analysis modules. Monetary values are canonical decimal
// strings so source precision is not lost through floating-point conversion.
type BillingRecord struct {
	ID                string            `json:"id" gorm:"primaryKey"`
	ConnectorID       string            `json:"connector_id" gorm:"uniqueIndex:idx_billing_record_source;index"`
	ExternalID        string            `json:"external_id" gorm:"uniqueIndex:idx_billing_record_source"`
	SourceType        string            `json:"source_type" gorm:"index"`
	AccountID         string            `json:"account_id,omitempty" gorm:"index"`
	Service           string            `json:"service,omitempty" gorm:"index"`
	Product           string            `json:"product,omitempty"`
	Model             string            `json:"model,omitempty" gorm:"index"`
	Currency          string            `json:"currency" gorm:"index"`
	GrossAmount       string            `json:"gross_amount"`
	DiscountAmount    string            `json:"discount_amount"`
	TaxAmount         string            `json:"tax_amount"`
	RefundAmount      string            `json:"refund_amount"`
	NetAmount         string            `json:"net_amount"`
	UsageQuantity     int64             `json:"usage_quantity,omitempty"`
	UsageUnit         string            `json:"usage_unit,omitempty"`
	UsageStartAt      time.Time         `json:"usage_start_at" gorm:"index"`
	UsageEndAt        time.Time         `json:"usage_end_at"`
	SourceTimezone    string            `json:"source_timezone"`
	BillingPeriod     string            `json:"billing_period" gorm:"index"`
	ExternalRequestID string            `json:"external_request_id,omitempty" gorm:"index"`
	RawSnapshotID     string            `json:"raw_snapshot_id" gorm:"index"`
	Metadata          map[string]string `json:"metadata,omitempty" gorm:"serializer:json"`
	RawPayload        string            `json:"-" gorm:"-"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

type BillingRawSnapshot struct {
	ID                string    `json:"id" gorm:"primaryKey"`
	ConnectorID       string    `json:"connector_id" gorm:"uniqueIndex:idx_billing_snapshot_source;index"`
	ExternalID        string    `json:"external_id" gorm:"uniqueIndex:idx_billing_snapshot_source"`
	PayloadHash       string    `json:"payload_hash" gorm:"uniqueIndex:idx_billing_snapshot_source"`
	PayloadCiphertext string    `json:"-" gorm:"type:text"`
	CapturedAt        time.Time `json:"captured_at" gorm:"index"`
}

type BillingSyncRun struct {
	ID              string     `json:"id" gorm:"primaryKey"`
	ConnectorID     string     `json:"connector_id" gorm:"index"`
	Trigger         string     `json:"trigger" gorm:"index"`
	Status          string     `json:"status" gorm:"index"`
	RangeStart      time.Time  `json:"range_start"`
	RangeEnd        time.Time  `json:"range_end"`
	CursorStart     string     `json:"cursor_start,omitempty"`
	CursorEnd       string     `json:"cursor_end,omitempty"`
	PagesFetched    int        `json:"pages_fetched"`
	Attempts        int        `json:"attempts"`
	RecordsSeen     int        `json:"records_seen"`
	RecordsInserted int        `json:"records_inserted"`
	RecordsUpdated  int        `json:"records_updated"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	StartedAt       time.Time  `json:"started_at" gorm:"index"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

type BillingSyncRequest struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type BillingFetchRequest struct {
	From     time.Time
	To       time.Time
	Cursor   string
	PageSize int
}

type BillingFetchPage struct {
	Records    []BillingRecord
	NextCursor string
}

type BillingAdapter interface {
	Fetch(ctx context.Context, connector BillingConnector, request BillingFetchRequest) (BillingFetchPage, error)
}

type BillingStore interface {
	CreateBillingConnector(connector BillingConnector) (BillingConnector, error)
	ListBillingConnectors() []BillingConnector
	GetBillingConnector(id string, includeCredentials bool) (BillingConnector, error)
	UpdateBillingConnector(id string, patch BillingConnector) (BillingConnector, error)
	DeleteBillingConnector(id string) error
	StartBillingSyncRun(run BillingSyncRun) (BillingSyncRun, error)
	SaveBillingPage(connectorID string, checkpoint string, records []BillingRecord) (inserted int, updated int, err error)
	FinishBillingSyncRun(run BillingSyncRun) (BillingSyncRun, error)
	ListBillingRecords(connectorID string, limit int) []BillingRecord
	ListBillingSyncRuns(connectorID string, limit int) []BillingSyncRun
	ListDueBillingConnectors(now time.Time, limit int) []BillingConnector
	RecordScheduledBillingAudit(run BillingSyncRun)
}
