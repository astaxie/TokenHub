package server

import (
	"context"
	"database/sql"
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
	TenantModel       string    `json:"tenant_model,omitempty"`
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

// Both operator summaries and customer/provider statements use buildStatement.
// This adapter adds pagination and known subtotals without a second ledger reader.
func (s *GormStore) scanBillingStatements(ctx context.Context, q billingStatementQuery, emit func(billingStatementRow) error) error {
	q.From = q.From.UTC()
	q.To = q.To.UTC()
	var options *sql.TxOptions
	if s.dbDriver == "postgres" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := statementQuery{Side: q.Kind, Timezone: "UTC", ProviderID: q.Provider, Model: q.Model}
		if q.Project != "" {
			query.ProjectIDs = []string{q.Project}
		}
		generated := time.Now().UTC()
		result := statementResult{Query: query, GeneratedAt: generated, From: q.From, To: q.To, Rows: []statementRow{}, Totals: map[string]string{}}
		if err := buildStatement(tx, &result); err != nil {
			return err
		}
		for _, entry := range result.Rows {
			if q.Kind == "provider" && entry.Source != "provider_estimate" || q.Kind == "tenant" && entry.Source != "tenant" {
				continue
			}
			row := billingStatementRow{ID: entry.ID, RequestID: entry.RequestID, UpstreamRequestID: entry.ExternalRequestID, OccurredAt: entry.At, ProviderID: entry.ProviderID, ProviderName: entry.ProviderName, ResourceID: entry.ResourceID, ResourceName: entry.ResourceName, Model: entry.Model, TenantModel: entry.TenantModel, ProjectID: entry.ProjectID, ProjectName: entry.ProjectName, UserID: entry.UserID, APIKeyID: entry.APIKeyID, Status: "pending", Reason: entry.Reason, Source: "request_evidence", Currency: entry.Currency}
			if q.Kind == "provider" {
				row.Source = "attempt_evidence"
			}
			if entry.Status == "legacy_incomplete" {
				row.Source = "legacy_usage"
			}
			if entry.Amount != nil {
				row.Amount = *entry.Amount
			}
			if entry.USD != nil {
				if _, ok := statementAmount(*entry.USD); ok {
					row.AmountUSD = *entry.USD
					if q.Kind == "tenant" {
						row.Status = "charged"
					} else if entry.Status != "pending" {
						row.Status = "estimated"
					} else {
						row.AmountUSD = ""
					}
				}
			}
			if q.includes(row) {
				if err := emit(row); err != nil {
					return err
				}
			}
		}
		return nil
	}, options)
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
