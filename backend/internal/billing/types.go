// Package billing owns provider-neutral billing synchronization and pricing-source
// contracts. Persistence and HTTP adapters implement the ports declared here.
package billing

import (
	"context"
	"errors"
	"time"
)

const (
	ConnectorAliyun = "aliyun"
	ConnectorNewAPI = "newapi"
	ConnectorOneAPI = "oneapi"

	SyncRunning   = "running"
	SyncSucceeded = "succeeded"
	SyncFailed    = "failed"

	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Connector is the persisted configuration for one external billing source.
// Credentials are accepted through Credentials and must never be serialized by
// an adapter in a connector summary.
type Connector struct {
	ID                      string
	Name                    string
	Type                    string
	BaseURL                 string
	Status                  string
	ScheduleIntervalMinutes int
	Config                  map[string]string
	CredentialCiphertext    string
	Credentials             map[string]string
	CredentialsConfigured   bool
	CredentialFields        []string
	Checkpoint              string
	LastSyncedThrough       *time.Time
	LastSyncStatus          string
	LastSyncMessage         string
	LastSyncAt              *time.Time
	NextSyncAt              *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// Record is the provider-neutral billing model consumed by reconciliation and
// analytics. Monetary values remain canonical decimal strings.
type Record struct {
	ID                string
	ConnectorID       string
	ExternalID        string
	SourceType        string
	AccountID         string
	Service           string
	Product           string
	Model             string
	Currency          string
	GrossAmount       string
	DiscountAmount    string
	TaxAmount         string
	RefundAmount      string
	NetAmount         string
	UsageQuantity     int64
	UsageUnit         string
	UsageStartAt      time.Time
	UsageEndAt        time.Time
	SourceTimezone    string
	BillingPeriod     string
	ExternalRequestID string
	RawSnapshotID     string
	Metadata          map[string]string
	RawPayload        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type RawSnapshot struct {
	ID                string
	ConnectorID       string
	ExternalID        string
	PayloadHash       string
	PayloadCiphertext string
	CapturedAt        time.Time
}

type SyncRun struct {
	ID              string
	ConnectorID     string
	Trigger         string
	Status          string
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
	StartedAt       time.Time
	FinishedAt      *time.Time
}

type SyncRequest struct {
	From time.Time
	To   time.Time
}

type FetchRequest struct {
	From     time.Time
	To       time.Time
	Cursor   string
	PageSize int
}

type FetchPage struct {
	Records    []Record
	NextCursor string
}

// Adapter is the only capability the synchronizer needs from an upstream
// billing source. Concrete HTTP adapters may live outside this package.
type Adapter interface {
	Fetch(context.Context, Connector, FetchRequest) (FetchPage, error)
}

// Store is the persistence port used by the synchronization service.
type Store interface {
	GetBillingConnector(string, bool) (Connector, error)
	StartBillingSyncRun(SyncRun) (SyncRun, error)
	SaveBillingPage(string, string, []Record) (inserted int, updated int, err error)
	FinishBillingSyncRun(SyncRun) (SyncRun, error)
	ListDueBillingConnectors(time.Time, int) []Connector
	RecordScheduledBillingAudit(SyncRun)
}

// ManagementStore is the persistence seam used by the connector management
// application. It is separate from Store because synchronization does not need
// connector mutation or administrative list queries.
type ManagementStore interface {
	CreateBillingConnector(Connector) (Connector, error)
	ListBillingConnectors() []Connector
	GetBillingConnector(string, bool) (Connector, error)
	UpdateBillingConnector(string, Connector) (Connector, error)
	DeleteBillingConnector(string) error
	ListBillingRecords(string, int) []Record
	ListBillingSyncRuns(string, int) []SyncRun
}

type Repository interface {
	Store
	ManagementStore
}

type ErrorKind string

const (
	ErrorInvalidInput ErrorKind = "invalid_input"
	ErrorConflict     ErrorKind = "conflict"
	ErrorNotFound     ErrorKind = "not_found"
	ErrorRateLimited  ErrorKind = "rate_limited"
	ErrorUpstream     ErrorKind = "upstream"
	ErrorTimeout      ErrorKind = "timeout"
)

// Error carries a domain failure without prescribing a transport status.
type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

func NewError(kind ErrorKind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// WrapError preserves an adapter or persistence cause while exposing its
// domain classification to callers that cannot depend on the concrete error.
func WrapError(cause error, kind ErrorKind, code, message string) error {
	err := NewError(kind, code, message)
	err.Cause = cause
	return err
}

type errorInfo interface {
	ErrorKind() ErrorKind
	ErrorCode() string
	ErrorMessage() string
}

func (e *Error) ErrorKind() ErrorKind { return e.Kind }
func (e *Error) ErrorCode() string    { return e.Code }
func (e *Error) ErrorMessage() string { return e.Message }

func ErrorInfo(err error) (kind ErrorKind, code, message string, ok bool) {
	var info errorInfo
	if err == nil || !errors.As(err, &info) {
		return "", "", "", false
	}
	return info.ErrorKind(), info.ErrorCode(), info.ErrorMessage(), true
}

// AdapterFailure lets an adapter preserve its original error while exposing
// domain-level retry and diagnostic information to the synchronizer.
type AdapterFailure struct {
	Err                error
	Kind               ErrorKind
	Code               string
	Message            string
	Retry              bool
	RetryAfterDuration time.Duration
}

func (e *AdapterFailure) Error() string             { return e.Message }
func (e *AdapterFailure) Unwrap() error             { return e.Err }
func (e *AdapterFailure) ErrorKind() ErrorKind      { return e.Kind }
func (e *AdapterFailure) ErrorCode() string         { return e.Code }
func (e *AdapterFailure) ErrorMessage() string      { return e.Message }
func (e *AdapterFailure) Retryable() bool           { return e.Retry }
func (e *AdapterFailure) RetryAfter() time.Duration { return e.RetryAfterDuration }

func NewAdapterFailure(err error, kind ErrorKind, code, message string, retry bool, retryAfter time.Duration) *AdapterFailure {
	return &AdapterFailure{Err: err, Kind: kind, Code: code, Message: message, Retry: retry, RetryAfterDuration: retryAfter}
}
