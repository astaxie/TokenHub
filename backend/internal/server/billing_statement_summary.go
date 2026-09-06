package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"sort"
	"time"

	"gorm.io/gorm"
)

type billingStatementQuery struct {
	Kind                              string
	From, To                          time.Time
	Provider, Model, Project, GroupBy string
	Offset, Limit                     int
}
type billingStatementRow struct {
	ID                string    `json:"id"`
	RequestID         string    `json:"request_id"`
	UpstreamRequestID string    `json:"upstream_request_id,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
	ProviderID        string    `json:"provider_id,omitempty"`
	ProviderName      string    `json:"provider_name,omitempty"`
	ResourceID        string    `json:"resource_id,omitempty"`
	ResourceName      string    `json:"resource_name,omitempty"`
	Model             string    `json:"model"`
	ProjectID         string    `json:"project_id,omitempty"`
	ProjectName       string    `json:"project_name,omitempty"`
	UserID            string    `json:"user_id,omitempty"`
	APIKeyID          string    `json:"api_key_id,omitempty"`
	Status            string    `json:"status"`
	Reason            string    `json:"reason,omitempty"`
	Source            string    `json:"source"`
	Currency          string    `json:"currency"`
	Amount            string    `json:"amount,omitempty"`
	AmountUSD         string    `json:"amount_usd,omitempty"`
}
type billingStatementGroup struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Records        int    `json:"records"`
	Pending        int    `json:"pending"`
	KnownAmountUSD string `json:"known_amount_usd"`
}
type billingStatement struct {
	Kind           string                  `json:"kind"`
	From           time.Time               `json:"from"`
	To             time.Time               `json:"to"`
	KnownAmountUSD string                  `json:"known_amount_usd"`
	Records        int                     `json:"records"`
	Pending        int                     `json:"pending"`
	Legacy         int                     `json:"legacy"`
	Complete       bool                    `json:"complete"`
	Offset         int                     `json:"offset"`
	Limit          int                     `json:"limit"`
	Groups         []billingStatementGroup `json:"groups"`
	Items          []billingStatementRow   `json:"items"`
}

type billingSettlement struct {
	Tenant   meteringShadowCharge    `json:"tenant"`
	Attempts []meteringAttemptCharge `json:"attempts"`
}

func (q billingStatementQuery) includes(row billingStatementRow) bool {
	return (q.Provider == "" || row.ProviderID == q.Provider) && (q.Model == "" || row.Model == q.Model) && (q.Project == "" || row.ProjectID == q.Project)
}
func statementAmount(value string) (*big.Rat, bool) {
	if value == "" {
		return nil, false
	}
	v, ok := new(big.Rat).SetString(value)
	return v, ok && v.Sign() >= 0
}

func (s *GormStore) scanBillingStatements(ctx context.Context, q billingStatementQuery, emit func(billingStatementRow) error) error {
	q.From = q.From.UTC()
	q.To = q.To.UTC()
	var options *sql.TxOptions
	if s.dbDriver == "postgres" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		kind := "admission"
		if q.Kind == "provider" {
			kind = "attempt_prepared"
		}
		var after time.Time
		afterID := ""
		for {
			var batch []meteringEntry
			query := tx.Where("kind = ? AND created_at >= ? AND created_at < ?", kind, q.From, q.To)
			if afterID != "" {
				query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", after, after, afterID)
			}
			if err := query.Order("created_at ASC, id ASC").Limit(250).Find(&batch).Error; err != nil {
				return err
			}
			if len(batch) == 0 {
				break
			}
			scopes := make([]string, 0, len(batch))
			for _, entry := range batch {
				scopes = append(scopes, entry.Scope)
			}
			var evidence []meteringEntry
			if err := tx.Where("scope IN ? AND kind IN ?", scopes, []string{"admission", "shadow_settlement"}).Find(&evidence).Error; err != nil {
				return err
			}
			var usageRecords []UsageRecord
			if err := tx.Where("request_id IN ?", scopes).Find(&usageRecords).Error; err != nil {
				return err
			}
			posted := map[string]UsageRecord{}
			for _, record := range usageRecords {
				posted[record.RequestID] = record
			}
			admissions := map[string]meteringRequestSnapshot{}
			settlements := map[string]billingSettlement{}
			for _, entry := range evidence {
				if entry.Kind == "admission" {
					var value meteringRequestSnapshot
					if err := json.Unmarshal([]byte(entry.Payload), &value); err != nil {
						return err
					}
					admissions[entry.Scope] = value
				} else {
					var value billingSettlement
					if err := json.Unmarshal([]byte(entry.Payload), &value); err != nil {
						return err
					}
					settlements[entry.Scope] = value
				}
			}
			for _, entry := range batch {
				admission := admissions[entry.Scope]
				settlement, settled := settlements[entry.Scope]
				row := billingStatementRow{ID: entry.ID, RequestID: entry.Scope, OccurredAt: entry.CreatedAt, ProjectID: admission.ProjectID, ProjectName: admission.ProjectName, UserID: admission.UserID, APIKeyID: admission.APIKeyID, Model: admission.ModelName, Status: "pending", Reason: "completion_pending", Source: "request_evidence", Currency: "USD"}
				if q.Kind == "tenant" {
					record, hasRecord := posted[entry.Scope]
					if row.Model == "" && hasRecord {
						row.Model = record.ModelName
					}
					if hasRecord {
						amount := billingAmount(record.CostUSD)
						if _, ok := statementAmount(amount); ok {
							row.Status = "charged"
							row.Amount = amount
							row.AmountUSD = amount
							row.Reason = ""
						}
					} else if settled {
						// The settlement commits with quota counters and the request
						// log. Even a zero charge is a posted amount, independently
						// of whether provider usage can be priced as an estimate.
						if _, ok := statementAmount(settlement.Tenant.LegacyUSD); ok {
							row.Status = "charged"
							row.Amount = settlement.Tenant.LegacyUSD
							row.AmountUSD = row.Amount
							row.Reason = settlement.Tenant.Reason
						}
					}
				} else {
					var attempt meteringAttemptSnapshot
					if err := json.Unmarshal([]byte(entry.Payload), &attempt); err != nil {
						return err
					}
					row.ProviderID = attempt.ProviderID
					row.ProviderName = attempt.ProviderName
					row.ResourceID = attempt.ResourceID
					row.ResourceName = attempt.ResourceName
					row.Model = attempt.UpstreamModel
					row.Source = "attempt_evidence"
					for _, item := range settlement.Attempts {
						if item.ID == attempt.ID {
							row.UpstreamRequestID = item.UpstreamRequestID
							row.Reason = item.Charge.Reason
							if charge := item.Charge.Charge; charge != nil {
								row.Currency = charge.Currency
								row.Amount = charge.Amount
								if _, ok := statementAmount(charge.USD); ok && item.Charge.Status != "pending" {
									row.Status = "estimated"
									row.AmountUSD = charge.USD
								} else {
									row.Reason = "exchange_rate_missing"
								}
							}
							break
						}
					}
				}
				if q.includes(row) {
					if err := emit(row); err != nil {
						return err
					}
				}
			}
			last := batch[len(batch)-1]
			after = last.CreatedAt
			afterID = last.ID
		}
		// Old rows without request/attempt evidence are displayed as incomplete legacy
		// coverage, using only persisted amounts. Current prices never rewrite history.
		return s.scanLegacyStatementRows(tx, q, kind, emit)
	}, options)
}
func (s *GormStore) scanLegacyStatementRows(tx *gorm.DB, q billingStatementQuery, kind string, emit func(billingStatementRow) error) error {
	var after time.Time
	afterID := ""
	for {
		var batch []UsageRecord
		query := tx.Where("created_at >= ? AND created_at < ?", q.From, q.To).
			Where("NOT EXISTS (SELECT 1 FROM metering_entries e WHERE e.kind = ? AND e.scope = usage_records.request_id)", kind)
		if afterID != "" {
			query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", after, after, afterID)
		}
		if err := query.Order("created_at ASC, id ASC").Limit(250).Find(&batch).Error; err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		requests := make([]string, 0, len(batch))
		for _, usage := range batch {
			requests = append(requests, usage.RequestID)
		}
		var logs []RequestLog
		if err := tx.Where("request_id IN ?", requests).Find(&logs).Error; err != nil {
			return err
		}
		identities := map[string]RequestLog{}
		for _, log := range logs {
			identities[log.RequestID] = log
		}
		for _, usage := range batch {
			row := billingStatementRow{ID: usage.ID, RequestID: usage.RequestID, OccurredAt: usage.CreatedAt, ProviderID: usage.ProviderID, ResourceID: usage.ProviderResourceID, ProjectID: usage.ProjectID, APIKeyID: usage.APIKeyID, UserID: usage.AttributedUserID, Model: usage.ModelName, Status: "pending", Reason: "legacy_cost_unknown", Source: "legacy_usage", Currency: "USD"}
			amount := usage.CostUSD
			if q.Kind == "provider" {
				amount = usage.ProviderCostUSD
				log := identities[usage.RequestID]
				if log.ProviderModel != "" {
					row.Model = log.ProviderModel
				}
				row.UpstreamRequestID = log.UpstreamRequestID
			}
			if amount > 0 || q.Kind == "tenant" && amount == 0 {
				row.Status = "estimated"
				if q.Kind == "tenant" {
					row.Status = "charged"
				}
				row.Amount = billingAmount(amount)
				row.AmountUSD = row.Amount
				row.Reason = "legacy_evidence_incomplete"
			}
			if q.includes(row) {
				if err := emit(row); err != nil {
					return err
				}
			}
		}
		last := batch[len(batch)-1]
		after = last.CreatedAt
		afterID = last.ID
	}
	return nil
}
func (s *GormStore) PlatformBillingStatement(ctx context.Context, q billingStatementQuery) (billingStatement, error) {
	result := billingStatement{Kind: q.Kind, From: q.From, To: q.To, Offset: q.Offset, Limit: q.Limit, Items: []billingStatementRow{}, Groups: []billingStatementGroup{}}
	total := new(big.Rat)
	groups := map[string]*billingStatementGroup{}
	sums := map[string]*big.Rat{}
	err := s.scanBillingStatements(ctx, q, func(row billingStatementRow) error {
		key, name := row.ProviderID, row.ProviderName
		switch q.GroupBy {
		case "model":
			key = row.Model
			name = key
		case "project":
			key = row.ProjectID
			name = row.ProjectName
		case "resource":
			key = row.ResourceID
			name = row.ResourceName
		}
		if name == "" {
			name = key
		}
		if key == "" {
			key = "unassigned"
		}
		if groups[key] == nil {
			groups[key] = &billingStatementGroup{ID: key, Name: name}
			sums[key] = new(big.Rat)
		}
		group := groups[key]
		group.Records++
		if amount, ok := statementAmount(row.AmountUSD); ok && row.Status != "pending" {
			total.Add(total, amount)
			sums[key].Add(sums[key], amount)
		} else {
			result.Pending++
			group.Pending++
		}
		if row.Source == "legacy_usage" {
			result.Legacy++
		}
		if result.Records >= q.Offset && len(result.Items) < q.Limit {
			result.Items = append(result.Items, row)
		}
		result.Records++
		return nil
	})
	if err != nil {
		return result, err
	}
	result.KnownAmountUSD = total.FloatString(12)
	result.Complete = result.Pending == 0 && result.Legacy == 0
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		groups[key].KnownAmountUSD = sums[key].FloatString(12)
		result.Groups = append(result.Groups, *groups[key])
	}
	return result, nil
}

func validateBillingStatementQuery(q billingStatementQuery) error {
	if q.Kind != "provider" && q.Kind != "tenant" {
		return NewHTTPError(400, "invalid_statement_kind", "Statement kind must be provider or tenant")
	}
	if q.From.IsZero() || q.To.IsZero() || !q.From.Before(q.To) || q.To.Sub(q.From) > 366*24*time.Hour {
		return NewHTTPError(400, "invalid_statement_period", "Choose a date range of at most 366 days")
	}
	if q.GroupBy != "provider" && q.GroupBy != "resource" && q.GroupBy != "model" && q.GroupBy != "project" {
		return NewHTTPError(400, "invalid_statement_group", "Unsupported grouping")
	}
	return nil
}
