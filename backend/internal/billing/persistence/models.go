package persistence

import (
	"time"

	"tokenhub/backend/internal/billing"
)

type ConnectorRow struct {
	ID                      string `gorm:"primaryKey"`
	Name                    string
	Type                    string `gorm:"index"`
	BaseURL                 string
	Status                  string `gorm:"index"`
	ScheduleIntervalMinutes int
	Config                  map[string]string `gorm:"serializer:json"`
	CredentialCiphertext    string            `gorm:"type:text"`
	Checkpoint              string            `gorm:"type:text"`
	LastSyncedThrough       *time.Time
	LastSyncStatus          string `gorm:"index"`
	LastSyncMessage         string
	LastSyncAt              *time.Time
	NextSyncAt              *time.Time `gorm:"index"`
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func (ConnectorRow) TableName() string { return "billing_connectors" }

type RecordRow struct {
	ID                string `gorm:"primaryKey"`
	ConnectorID       string `gorm:"uniqueIndex:idx_billing_record_source;index"`
	ExternalID        string `gorm:"uniqueIndex:idx_billing_record_source"`
	SourceType        string `gorm:"index"`
	AccountID         string `gorm:"index"`
	Service           string `gorm:"index"`
	Product           string
	Model             string `gorm:"index"`
	Currency          string `gorm:"index"`
	GrossAmount       string
	DiscountAmount    string
	TaxAmount         string
	RefundAmount      string
	NetAmount         string
	UsageQuantity     int64
	UsageUnit         string
	UsageStartAt      time.Time `gorm:"index"`
	UsageEndAt        time.Time
	SourceTimezone    string
	BillingPeriod     string            `gorm:"index"`
	ExternalRequestID string            `gorm:"index"`
	RawSnapshotID     string            `gorm:"index"`
	Metadata          map[string]string `gorm:"serializer:json"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (RecordRow) TableName() string { return "billing_records" }

type RawSnapshotRow struct {
	ID                string    `gorm:"primaryKey"`
	ConnectorID       string    `gorm:"uniqueIndex:idx_billing_snapshot_source;index"`
	ExternalID        string    `gorm:"uniqueIndex:idx_billing_snapshot_source"`
	PayloadHash       string    `gorm:"uniqueIndex:idx_billing_snapshot_source"`
	PayloadCiphertext string    `gorm:"type:text"`
	CapturedAt        time.Time `gorm:"index"`
}

func (RawSnapshotRow) TableName() string { return "billing_raw_snapshots" }

type SyncRunRow struct {
	ID              string `gorm:"primaryKey"`
	ConnectorID     string `gorm:"index"`
	Trigger         string `gorm:"index"`
	Status          string `gorm:"index"`
	RangeStart      time.Time
	RangeEnd        time.Time
	CursorStart     string
	CursorEnd       string
	PagesFetched    int
	Attempts        int
	RecordsSeen     int
	RecordsInserted int
	RecordsUpdated  int
	ErrorCode       string
	ErrorMessage    string
	StartedAt       time.Time `gorm:"index"`
	FinishedAt      *time.Time
}

func (SyncRunRow) TableName() string { return "billing_sync_runs" }

func Models() []any {
	return []any{&ConnectorRow{}, &RecordRow{}, &RawSnapshotRow{}, &SyncRunRow{}}
}

func connectorRow(connector billing.Connector) ConnectorRow {
	return ConnectorRow{
		ID: connector.ID, Name: connector.Name, Type: connector.Type, BaseURL: connector.BaseURL,
		Status: connector.Status, ScheduleIntervalMinutes: connector.ScheduleIntervalMinutes,
		Config: cloneStringMap(connector.Config), CredentialCiphertext: connector.CredentialCiphertext,
		Checkpoint: connector.Checkpoint, LastSyncedThrough: cloneTimePointer(connector.LastSyncedThrough),
		LastSyncStatus: connector.LastSyncStatus, LastSyncMessage: connector.LastSyncMessage,
		LastSyncAt: cloneTimePointer(connector.LastSyncAt), NextSyncAt: cloneTimePointer(connector.NextSyncAt),
		CreatedAt: connector.CreatedAt, UpdatedAt: connector.UpdatedAt,
	}
}

func domainConnector(row ConnectorRow) billing.Connector {
	return billing.Connector{
		ID: row.ID, Name: row.Name, Type: row.Type, BaseURL: row.BaseURL, Status: row.Status,
		ScheduleIntervalMinutes: row.ScheduleIntervalMinutes, Config: cloneStringMap(row.Config),
		CredentialCiphertext: row.CredentialCiphertext, Checkpoint: row.Checkpoint,
		LastSyncedThrough: cloneTimePointer(row.LastSyncedThrough), LastSyncStatus: row.LastSyncStatus,
		LastSyncMessage: row.LastSyncMessage, LastSyncAt: cloneTimePointer(row.LastSyncAt),
		NextSyncAt: cloneTimePointer(row.NextSyncAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func recordRow(record billing.Record) RecordRow {
	return RecordRow{
		ID: record.ID, ConnectorID: record.ConnectorID, ExternalID: record.ExternalID,
		SourceType: record.SourceType, AccountID: record.AccountID, Service: record.Service,
		Product: record.Product, Model: record.Model, Currency: record.Currency,
		GrossAmount: record.GrossAmount, DiscountAmount: record.DiscountAmount, TaxAmount: record.TaxAmount,
		RefundAmount: record.RefundAmount, NetAmount: record.NetAmount, UsageQuantity: record.UsageQuantity,
		UsageUnit: record.UsageUnit, UsageStartAt: record.UsageStartAt, UsageEndAt: record.UsageEndAt,
		SourceTimezone: record.SourceTimezone, BillingPeriod: record.BillingPeriod,
		ExternalRequestID: record.ExternalRequestID, RawSnapshotID: record.RawSnapshotID,
		Metadata: cloneStringMap(record.Metadata), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func RecordRowFromDomain(record billing.Record) RecordRow { return recordRow(record) }

func domainRecord(row RecordRow) billing.Record {
	return billing.Record{
		ID: row.ID, ConnectorID: row.ConnectorID, ExternalID: row.ExternalID, SourceType: row.SourceType,
		AccountID: row.AccountID, Service: row.Service, Product: row.Product, Model: row.Model,
		Currency: row.Currency, GrossAmount: row.GrossAmount, DiscountAmount: row.DiscountAmount,
		TaxAmount: row.TaxAmount, RefundAmount: row.RefundAmount, NetAmount: row.NetAmount,
		UsageQuantity: row.UsageQuantity, UsageUnit: row.UsageUnit, UsageStartAt: row.UsageStartAt,
		UsageEndAt: row.UsageEndAt, SourceTimezone: row.SourceTimezone, BillingPeriod: row.BillingPeriod,
		ExternalRequestID: row.ExternalRequestID, RawSnapshotID: row.RawSnapshotID,
		Metadata: cloneStringMap(row.Metadata), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func DomainRecord(row RecordRow) billing.Record { return domainRecord(row) }

func syncRunRow(run billing.SyncRun) SyncRunRow {
	return SyncRunRow{
		ID: run.ID, ConnectorID: run.ConnectorID, Trigger: run.Trigger, Status: run.Status,
		RangeStart: run.RangeStart, RangeEnd: run.RangeEnd, CursorStart: run.CursorStart, CursorEnd: run.CursorEnd,
		PagesFetched: run.PagesFetched, Attempts: run.Attempts, RecordsSeen: run.RecordsSeen,
		RecordsInserted: run.RecordsInserted, RecordsUpdated: run.RecordsUpdated,
		ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage, StartedAt: run.StartedAt,
		FinishedAt: cloneTimePointer(run.FinishedAt),
	}
}

func domainSyncRun(row SyncRunRow) billing.SyncRun {
	return billing.SyncRun{
		ID: row.ID, ConnectorID: row.ConnectorID, Trigger: row.Trigger, Status: row.Status,
		RangeStart: row.RangeStart, RangeEnd: row.RangeEnd, CursorStart: row.CursorStart, CursorEnd: row.CursorEnd,
		PagesFetched: row.PagesFetched, Attempts: row.Attempts, RecordsSeen: row.RecordsSeen,
		RecordsInserted: row.RecordsInserted, RecordsUpdated: row.RecordsUpdated,
		ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, StartedAt: row.StartedAt,
		FinishedAt: cloneTimePointer(row.FinishedAt),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
