package server

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	billingstore "tokenhub/backend/internal/billing/persistence"
	"tokenhub/backend/internal/metering"
)

const statementRowLimit = 10000

type statementQuery struct {
	Side       string   `json:"side"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Timezone   string   `json:"timezone"`
	Customer   string   `json:"customer"`
	ProjectIDs []string `json:"project_ids"`
	ProviderID string   `json:"provider_id"`
	ResourceID string   `json:"resource_id"`
	Model      string   `json:"model"`
}

type statementRow struct {
	ExternalID        string                 `json:"external_id,omitempty"`
	ExternalRequestID string                 `json:"external_request_id,omitempty"`
	UsageQuantity     int64                  `json:"usage_quantity,omitempty"`
	UsageUnit         string                 `json:"usage_unit,omitempty"`
	ID                string                 `json:"id"`
	RequestID         string                 `json:"request_id,omitempty"`
	Source            string                 `json:"source"`
	At                time.Time              `json:"at"`
	EndAt             *time.Time             `json:"end_at,omitempty"`
	Timezone          string                 `json:"timezone"`
	ProjectID         string                 `json:"project_id,omitempty"`
	TeamID            string                 `json:"team_id,omitempty"`
	APIKeyID          string                 `json:"api_key_id,omitempty"`
	Model             string                 `json:"model"`
	ProviderID        string                 `json:"provider_id,omitempty"`
	ResourceID        string                 `json:"resource_id,omitempty"`
	Currency          string                 `json:"currency"`
	Amount            *string                `json:"amount"`
	USD               *string                `json:"usd"`
	Status            string                 `json:"status"`
	Reason            string                 `json:"reason,omitempty"`
	Units             metering.Units         `json:"units"`
	Lines             []metering.Line        `json:"lines,omitempty"`
	Price             *meteringPriceSnapshot `json:"price,omitempty"`
}

type statementResult struct {
	Query              statementQuery    `json:"query"`
	GeneratedAt        time.Time         `json:"generated_at"`
	From               time.Time         `json:"from"`
	To                 time.Time         `json:"to"`
	TimeBasis          string            `json:"time_basis"`
	Rows               []statementRow    `json:"rows"`
	Totals             map[string]string `json:"totals"`
	UnknownCount       int               `json:"unknown_count"`
	IncompleteCount    int               `json:"incomplete_count"`
	EstimatedMarginUSD *string           `json:"estimated_margin_usd"`
}

func (q *statementQuery) window() (time.Time, time.Time, error) {
	bad := func() (time.Time, time.Time, error) {
		return time.Time{}, time.Time{}, NewHTTPError(400, "invalid_statement_query", "Select a valid side, time zone, date range (up to 93 days), and customer projects")
	}
	if q.Side != "tenant" && q.Side != "provider" && q.Side != "margin" {
		return bad()
	}
	q.Customer = strings.TrimSpace(q.Customer)
	if q.Side == "provider" {
		q.Customer = ""
	}
	if len(q.Customer) > 200 || len(q.ProjectIDs) > 100 || len(q.Model) > 256 || len(q.ProviderID) > 256 || len(q.ResourceID) > 256 {
		return bad()
	}
	if q.Side == "tenant" && (q.Customer == "" || len(q.ProjectIDs) == 0 || q.ProviderID != "" || q.ResourceID != "") {
		return bad()
	}
	if q.Side == "margin" && (q.ProviderID != "" || q.ResourceID != "") {
		return bad()
	}
	for _, id := range q.ProjectIDs {
		if strings.TrimSpace(id) == "" || len(id) > 256 {
			return bad()
		}
	}
	if q.Timezone == "" {
		q.Timezone = "UTC"
	}
	loc, err := time.LoadLocation(q.Timezone)
	if err != nil {
		return bad()
	}
	from, err := time.ParseInLocation("2006-01-02", q.From, loc)
	if err != nil {
		return bad()
	}
	to, err := time.ParseInLocation("2006-01-02", q.To, loc)
	if err != nil {
		return bad()
	}
	if !to.After(from) || to.After(from.AddDate(0, 0, 93)) {
		return bad()
	}
	return from.UTC(), to.UTC(), nil
}

func (s *Server) handleBillingStatement(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	if normalizeAdminRole(user.Role) != "admin" {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Only platform administrators can generate statements"))
		return
	}
	var q statementQuery
	if err := s.decodeJSON(w, r, &q); err != nil {
		writeError(w, r, err)
		return
	}
	if _, _, err := q.window(); err != nil {
		writeError(w, r, err)
		return
	}
	store, ok := s.store.(interface {
		BillingStatement(context.Context, statementQuery) (statementResult, error)
	})
	if !ok {
		writeError(w, r, NewHTTPError(503, "statements_unavailable", "Statement persistence is unavailable"))
		return
	}
	result, err := store.BillingStatement(r.Context(), q)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

func (s *GormStore) BillingStatement(ctx context.Context, q statementQuery) (statementResult, error) {
	from, to, err := q.window()
	if err != nil {
		return statementResult{}, err
	}
	out := statementResult{Query: q, GeneratedAt: time.Now().UTC(), From: from, To: to, TimeBasis: "request_admission", Rows: []statementRow{}, Totals: map[string]string{}}
	if q.Side == "provider" {
		out.TimeBasis = "upstream_attempt_start"
	}
	if q.Side == "provider" {
		if err := billingstore.BackfillRecordAttribution(s.db.WithContext(ctx), ""); err != nil {
			return statementResult{}, err
		}
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := buildStatement(tx, &out); err != nil {
			return err
		}
		if q.Side == "provider" {
			if err := appendExternalStatementRows(tx, &out); err != nil {
				return err
			}
		}
		return summarizeStatement(&out)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return out, err
}

func statementMatches(q statementQuery, project, model string) bool {
	if q.Model != "" && q.Model != model {
		return false
	}
	if len(q.ProjectIDs) == 0 {
		return true
	}
	for _, id := range q.ProjectIDs {
		if id == project {
			return true
		}
	}
	return false
}
func statementInWindow(at time.Time, out *statementResult) bool {
	return !at.Before(out.From) && at.Before(out.To)
}
func statementLimit(n int) error {
	if n > statementRowLimit {
		return NewHTTPError(422, "statement_too_large", "Statement exceeds 10000 records; narrow the date range or filters")
	}
	return nil
}
func statementString(s string) *string { return &s }

func summarizeStatement(out *statementResult) error {
	sums := map[string]*big.Rat{}
	for _, row := range out.Rows {
		if row.Amount == nil || row.USD == nil {
			out.UnknownCount++
		}
		if row.Status != "estimated" && row.Status != "provider_billed" {
			out.IncompleteCount++
		}
		for _, item := range []struct {
			key   string
			value *string
		}{{row.Source + ":" + row.Currency, row.Amount}, {row.Source + ":USD_equivalent", row.USD}} {
			if item.value == nil {
				continue
			}
			value, ok := new(big.Rat).SetString(*item.value)
			if !ok {
				return fmt.Errorf("invalid stored statement amount")
			}
			if sums[item.key] == nil {
				sums[item.key] = new(big.Rat)
			}
			sums[item.key].Add(sums[item.key], value)
		}
	}
	for key, value := range sums {
		out.Totals[key] = value.FloatString(12)
	}
	if out.Query.Side == "margin" && out.UnknownCount == 0 && out.IncompleteCount == 0 {
		margin := new(big.Rat)
		if v := sums["tenant:USD_equivalent"]; v != nil {
			margin.Add(margin, v)
		}
		if v := sums["provider_estimate:USD_equivalent"]; v != nil {
			margin.Sub(margin, v)
		}
		out.EstimatedMarginUSD = statementString(margin.FloatString(12))
	}
	return statementLimit(len(out.Rows))
}
