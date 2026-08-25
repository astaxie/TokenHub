// Package reconciliation owns billing-to-usage comparison rules, execution,
// matching, and scheduling. Persistence and transport adapters live outside
// this package and implement the ports declared here.
package reconciliation

import (
	"errors"
	"time"
)

const (
	GranularityDetail = "detail"
	GranularityHour   = "hour"
	GranularityDay    = "day"
	GranularityMonth  = "month"

	Matched        = "matched"
	ProviderOnly   = "provider_only"
	TokenHubOnly   = "tokenhub_only"
	AmountMismatch = "amount_mismatch"

	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"

	StatusActive   = "active"
	StatusDisabled = "disabled"
)

var dimensions = map[string]struct{}{
	"request_id": {}, "provider": {}, "resource_account": {},
	"model": {}, "project": {}, "currency": {},
}

type Rule struct {
	ID                      string
	Name                    string
	ConnectorID             string
	ConnectorType           string
	ProviderID              string
	ProviderResourceID      string
	Status                  string
	Granularity             string
	MatchDimensions         []string
	DimensionMappings       map[string]map[string]string
	AmountTolerance         string
	RatioTolerance          string
	USDExchangeRate         string
	TimeWindowMinutes       int
	BillingDelayMinutes     int
	ScheduleIntervalMinutes int
	Timezone                string
	Currency                string
	Version                 int
	RuleHash                string
	CreatedBy               string
	UpdatedBy               string
	LastRunAt               *time.Time
	NextRunAt               *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type RuleInput struct {
	Name                    string
	ConnectorID             string
	Status                  string
	Granularity             string
	MatchDimensions         []string
	DimensionMappings       map[string]map[string]string
	AmountTolerance         string
	RatioTolerance          string
	USDExchangeRate         string
	TimeWindowMinutes       int
	BillingDelayMinutes     int
	ScheduleIntervalMinutes int
	Timezone                string
	Currency                string
}

type RulePatch struct {
	Name                    *string
	Status                  *string
	Granularity             *string
	MatchDimensions         *[]string
	DimensionMappings       *map[string]map[string]string
	AmountTolerance         *string
	RatioTolerance          *string
	USDExchangeRate         *string
	TimeWindowMinutes       *int
	BillingDelayMinutes     *int
	ScheduleIntervalMinutes *int
	Timezone                *string
	Currency                *string
}

type Run struct {
	ID                  string
	RuleID              string
	ConnectorID         string
	ConnectorType       string
	ProviderID          string
	ProviderResourceID  string
	Trigger             string
	Status              string
	PeriodStart         time.Time
	PeriodEnd           time.Time
	Granularity         string
	MatchDimensions     []string
	DimensionMappings   map[string]map[string]string
	AmountTolerance     string
	RatioTolerance      string
	USDExchangeRate     string
	TimeWindowMinutes   int
	BillingDelayMinutes int
	Timezone            string
	Currency            string
	RuleVersion         int
	RuleHash            string
	InputHash           string
	ProviderRecordCount int
	TokenHubRecordCount int
	MatchedCount        int
	ProviderOnlyCount   int
	TokenHubOnlyCount   int
	AmountMismatchCount int
	ProviderAmount      string
	TokenHubAmount      string
	DifferenceAmount    string
	CreatedBy           string
	StartedAt           time.Time
	FinishedAt          *time.Time
	LockedAt            *time.Time
	LockedBy            string
	ErrorCode           string
	ErrorMessage        string
}

type RunInput struct {
	PeriodStart time.Time
	PeriodEnd   time.Time
}

type Item struct {
	ID                    string
	RunID                 string
	MatchKey              string
	Status                string
	BucketStart           time.Time
	BucketEnd             time.Time
	RequestID             string
	Provider              string
	ResourceAccount       string
	ResourceAccountMasked string
	Model                 string
	Project               string
	Currency              string
	ProviderAmount        string
	TokenHubAmount        string
	DifferenceAmount      string
	DifferenceRatio       string
	PossibleReason        string
	ProviderRecordIDs     []string
	TokenHubRecordIDs     []string
	CreatedAt             time.Time
}

// Usage is the projection reconciliation needs from the usage domain.
type Usage struct {
	ID                 string
	RequestID          string
	ProjectID          string
	ModelName          string
	ProviderID         string
	ProviderResourceID string
	CostUSD            float64
	ProviderCostUSD    float64
	CreatedAt          time.Time
}

// ConnectorSnapshot is the immutable billing scope reconciliation needs from
// a connector. Credential and synchronization fields deliberately do not cross
// this boundary.
type ConnectorSnapshot struct {
	Type               string
	ProviderID         string
	ProviderResourceID string
}

// BillingRecord is the projection reconciliation needs from a provider bill.
type BillingRecord struct {
	ID                 string
	ExternalID         string
	SourceType         string
	AccountID          string
	ProviderID         string
	ProviderResourceID string
	ResourceID         string
	ProjectID          string
	Model              string
	Currency           string
	NetAmount          string
	UsageStartAt       time.Time
	ExternalRequestID  string
}

// Store is the persistence port consumed by reconciliation application
// services. It includes both execution and administrative projections so the
// HTTP adapter never coordinates persistence operations itself.
type Store interface {
	CreateRule(Rule) (Rule, error)
	ListRules() []Rule
	GetRule(string) (Rule, error)
	UpdateRule(Rule) (Rule, error)
	BackfillRuleConnectorSnapshot(Rule) (Rule, error)
	ListDueRules(time.Time, int) []Rule
	ListUsages(time.Time, time.Time, time.Duration) ([]Usage, error)
	SaveRun(Run, []Item) (Run, error)
	ReplaceRun(Run, []Item) (Run, error)
	ListRuns(string, int) []Run
	GetRun(string) (Run, error)
	ListItems(string, string, int, int) ([]Item, int64)
	ListItemBatch(string, string, string, bool, int) []Item
	SaveRunLock(Run) (Run, error)
	RecordScheduledAudit(Run)
}

// BillingReader exposes only the billing projections required to snapshot a
// connector and calculate a reconciliation run.
type BillingReader interface {
	GetConnectorSnapshot(string) (ConnectorSnapshot, error)
	ListRecordsInRange(string, time.Time, time.Time) ([]BillingRecord, error)
}

type ErrorKind string

const (
	ErrorInvalidInput ErrorKind = "invalid_input"
	ErrorConflict     ErrorKind = "conflict"
	ErrorNotFound     ErrorKind = "not_found"
	ErrorUnavailable  ErrorKind = "unavailable"
)

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

func WrapError(cause error, kind ErrorKind, code, message string) error {
	err := NewError(kind, code, message)
	err.Cause = cause
	return err
}

func ErrorInfo(err error) (ErrorKind, string, string, bool) {
	var domainErr *Error
	if err == nil || !errors.As(err, &domainErr) {
		return "", "", "", false
	}
	return domainErr.Kind, domainErr.Code, domainErr.Message, true
}
