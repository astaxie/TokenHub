package persistence

import (
	"time"

	"tokenhub/backend/internal/reconciliation"
)

// RuleRow, RunRow and ItemRow are the database representations of the
// reconciliation domain objects. Their tags intentionally preserve the
// existing table and serializer layout.
type RuleRow struct {
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

func (RuleRow) TableName() string { return "reconciliation_rules" }

type RunRow struct {
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

func (RunRow) TableName() string { return "reconciliation_runs" }

type ItemRow struct {
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

func (ItemRow) TableName() string { return "reconciliation_items" }

func ruleFromRow(row RuleRow) reconciliation.Rule { return reconciliation.Rule(row) }
func ruleToRow(value reconciliation.Rule) RuleRow { return RuleRow(value) }
func runFromRow(row RunRow) reconciliation.Run    { return reconciliation.Run(row) }
func runToRow(value reconciliation.Run) RunRow    { return RunRow(value) }
func itemFromRow(row ItemRow) reconciliation.Item {
	return reconciliation.Item{ID: row.ID, RunID: row.RunID, MatchKey: row.MatchKey, Status: row.Status, BucketStart: row.BucketStart, BucketEnd: row.BucketEnd, RequestID: row.RequestID, Provider: row.Provider, ResourceAccount: row.ResourceAccount, ResourceAccountMasked: row.ResourceAccountMasked, Model: row.Model, Project: row.Project, Currency: row.Currency, ProviderAmount: row.ProviderAmount, TokenHubAmount: row.TokenHubAmount, DifferenceAmount: row.DifferenceAmount, DifferenceRatio: row.DifferenceRatio, PossibleReason: row.PossibleReason, ProviderRecordIDs: row.ProviderRecordIDs, TokenHubRecordIDs: row.TokenHubRecordIDs, CreatedAt: row.CreatedAt}
}
func itemToRow(value reconciliation.Item) ItemRow {
	return ItemRow{ID: value.ID, RunID: value.RunID, MatchKey: value.MatchKey, Status: value.Status, BucketStart: value.BucketStart, BucketEnd: value.BucketEnd, RequestID: value.RequestID, Provider: value.Provider, ResourceAccount: value.ResourceAccount, ResourceAccountMasked: value.ResourceAccountMasked, Model: value.Model, Project: value.Project, Currency: value.Currency, ProviderAmount: value.ProviderAmount, TokenHubAmount: value.TokenHubAmount, DifferenceAmount: value.DifferenceAmount, DifferenceRatio: value.DifferenceRatio, PossibleReason: value.PossibleReason, ProviderRecordIDs: value.ProviderRecordIDs, TokenHubRecordIDs: value.TokenHubRecordIDs, CreatedAt: value.CreatedAt}
}

func usageFromRecord(id, requestID, projectID, modelName, providerID, resourceID string, cost, providerCost float64, createdAt time.Time) reconciliation.Usage {
	return reconciliation.Usage{ID: id, RequestID: requestID, ProjectID: projectID, ModelName: modelName, ProviderID: providerID, ProviderResourceID: resourceID, CostUSD: cost, ProviderCostUSD: providerCost, CreatedAt: createdAt}
}
