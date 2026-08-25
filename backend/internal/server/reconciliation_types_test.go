package server

import (
	"time"

	"tokenhub/backend/internal/billing"
	"tokenhub/backend/internal/reconciliation"
	reconciliationpersistence "tokenhub/backend/internal/reconciliation/persistence"
)

// These wire representations keep the pre-extraction server characterization
// tests readable. Production code uses reconciliation's tag-free domain types
// and persistence rows directly.
type BillingConnector = billing.Connector
type BillingRecord = billing.Record

const (
	BillingConnectorAliyun = billing.ConnectorAliyun
	BillingConnectorNewAPI = billing.ConnectorNewAPI
	BillingConnectorOneAPI = billing.ConnectorOneAPI

	ReconciliationGranularityDetail = reconciliation.GranularityDetail
	ReconciliationGranularityHour   = reconciliation.GranularityHour
	ReconciliationGranularityDay    = reconciliation.GranularityDay
	ReconciliationGranularityMonth  = reconciliation.GranularityMonth

	ReconciliationMatched        = reconciliation.Matched
	ReconciliationProviderOnly   = reconciliation.ProviderOnly
	ReconciliationTokenHubOnly   = reconciliation.TokenHubOnly
	ReconciliationAmountMismatch = reconciliation.AmountMismatch

	ReconciliationRunRunning   = reconciliation.RunRunning
	ReconciliationRunSucceeded = reconciliation.RunSucceeded
	ReconciliationRunFailed    = reconciliation.RunFailed
)

type ReconciliationRule struct {
	ID                      string                       `json:"id" gorm:"primaryKey"`
	Name                    string                       `json:"name"`
	ConnectorID             string                       `json:"connector_id" gorm:"index"`
	ConnectorType           string                       `json:"connector_type"`
	ProviderID              string                       `json:"provider_id"`
	ProviderResourceID      string                       `json:"-"`
	Status                  string                       `json:"status" gorm:"index"`
	Granularity             string                       `json:"granularity"`
	MatchDimensions         []string                     `json:"match_dimensions" gorm:"serializer:json"`
	DimensionMappings       map[string]map[string]string `json:"dimension_mappings,omitempty" gorm:"serializer:json"`
	AmountTolerance         string                       `json:"amount_tolerance"`
	RatioTolerance          string                       `json:"ratio_tolerance"`
	USDExchangeRate         string                       `json:"usd_exchange_rate"`
	TimeWindowMinutes       int                          `json:"time_window_minutes"`
	BillingDelayMinutes     int                          `json:"billing_delay_minutes"`
	ScheduleIntervalMinutes int                          `json:"schedule_interval_minutes"`
	Timezone                string                       `json:"timezone"`
	Currency                string                       `json:"currency,omitempty"`
	Version                 int                          `json:"version"`
	RuleHash                string                       `json:"rule_hash" gorm:"index"`
	CreatedBy               string                       `json:"created_by"`
	UpdatedBy               string                       `json:"updated_by"`
	LastRunAt               *time.Time                   `json:"last_run_at,omitempty"`
	NextRunAt               *time.Time                   `json:"next_run_at,omitempty" gorm:"index"`
	CreatedAt               time.Time                    `json:"created_at"`
	UpdatedAt               time.Time                    `json:"updated_at"`
}

type ReconciliationRun struct {
	ID                  string                       `json:"id" gorm:"primaryKey"`
	RuleID              string                       `json:"rule_id" gorm:"index"`
	ConnectorID         string                       `json:"connector_id" gorm:"index"`
	ConnectorType       string                       `json:"connector_type"`
	ProviderID          string                       `json:"provider_id"`
	ProviderResourceID  string                       `json:"-"`
	Trigger             string                       `json:"trigger" gorm:"index"`
	Status              string                       `json:"status" gorm:"index"`
	PeriodStart         time.Time                    `json:"period_start" gorm:"index"`
	PeriodEnd           time.Time                    `json:"period_end" gorm:"index"`
	Granularity         string                       `json:"granularity"`
	MatchDimensions     []string                     `json:"match_dimensions" gorm:"serializer:json"`
	DimensionMappings   map[string]map[string]string `json:"dimension_mappings,omitempty" gorm:"serializer:json"`
	AmountTolerance     string                       `json:"amount_tolerance"`
	RatioTolerance      string                       `json:"ratio_tolerance"`
	USDExchangeRate     string                       `json:"usd_exchange_rate"`
	TimeWindowMinutes   int                          `json:"time_window_minutes"`
	BillingDelayMinutes int                          `json:"billing_delay_minutes"`
	Timezone            string                       `json:"timezone"`
	Currency            string                       `json:"currency,omitempty"`
	RuleVersion         int                          `json:"rule_version"`
	RuleHash            string                       `json:"rule_hash" gorm:"index"`
	InputHash           string                       `json:"input_hash" gorm:"index"`
	ProviderRecordCount int                          `json:"provider_record_count"`
	TokenHubRecordCount int                          `json:"tokenhub_record_count"`
	MatchedCount        int                          `json:"matched_count"`
	ProviderOnlyCount   int                          `json:"provider_only_count"`
	TokenHubOnlyCount   int                          `json:"tokenhub_only_count"`
	AmountMismatchCount int                          `json:"amount_mismatch_count"`
	ProviderAmount      string                       `json:"provider_amount"`
	TokenHubAmount      string                       `json:"tokenhub_amount"`
	DifferenceAmount    string                       `json:"difference_amount"`
	CreatedBy           string                       `json:"created_by"`
	StartedAt           time.Time                    `json:"started_at" gorm:"index"`
	FinishedAt          *time.Time                   `json:"finished_at,omitempty"`
	LockedAt            *time.Time                   `json:"locked_at,omitempty" gorm:"index"`
	LockedBy            string                       `json:"locked_by,omitempty"`
	ErrorCode           string                       `json:"error_code,omitempty"`
	ErrorMessage        string                       `json:"error_message,omitempty"`
}

type ReconciliationItem struct {
	ID                    string    `json:"id" gorm:"primaryKey"`
	RunID                 string    `json:"run_id" gorm:"index"`
	MatchKey              string    `json:"match_key" gorm:"index"`
	Status                string    `json:"status" gorm:"index"`
	BucketStart           time.Time `json:"bucket_start"`
	BucketEnd             time.Time `json:"bucket_end"`
	RequestID             string    `json:"request_id,omitempty"`
	Provider              string    `json:"provider,omitempty"`
	ResourceAccount       string    `json:"-"`
	ResourceAccountMasked string    `json:"resource_account,omitempty" gorm:"-"`
	Model                 string    `json:"model,omitempty"`
	Project               string    `json:"project,omitempty"`
	Currency              string    `json:"currency"`
	ProviderAmount        string    `json:"provider_amount"`
	TokenHubAmount        string    `json:"tokenhub_amount"`
	DifferenceAmount      string    `json:"difference_amount"`
	DifferenceRatio       string    `json:"difference_ratio"`
	PossibleReason        string    `json:"possible_reason"`
	ProviderRecordIDs     []string  `json:"provider_record_ids" gorm:"serializer:json"`
	TokenHubRecordIDs     []string  `json:"tokenhub_record_ids" gorm:"serializer:json"`
	CreatedAt             time.Time `json:"created_at"`
}

type ReconciliationDetail struct {
	Run    ReconciliationRun    `json:"run"`
	Items  []ReconciliationItem `json:"items"`
	Total  int64                `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

type ReconciliationBillingReader = reconciliationpersistence.BillingSource
