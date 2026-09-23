package server

import (
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/metering"
)

type statementEvidence struct {
	Admission  *meteringRequestSnapshot
	At         time.Time
	Attempts   []meteringAttemptSnapshot
	Settlement *struct {
		Tenant   meteringShadowCharge    `json:"tenant"`
		Attempts []meteringAttemptCharge `json:"attempts"`
	}
	Usage []UsageRecord
}

func buildStatement(tx *gorm.DB, out *statementResult) error {
	// Select request cohorts by admission for tenant/margin and attempt time for
	// procurement. Load their complete evidence so retries crossing midnight stay
	// attached to the original customer request in margin reports.
	kind := "admission"
	if out.Query.Side == "provider" {
		kind = "attempt_prepared"
	}
	var seeds []meteringEntry
	if err := statementSeedQuery(tx, out, kind).Limit(statementRowLimit + 1).Find(&seeds).Error; err != nil {
		return err
	}
	if err := statementLimit(len(seeds)); err != nil {
		return err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, row := range seeds {
		if !seen[row.Scope] {
			ids = append(ids, row.Scope)
			seen[row.Scope] = true
		}
	}
	// Legacy usage has no admission snapshot. Do not invent historical identity,
	// prices or retry completeness from today's configuration.
	var legacy []UsageRecord
	query := tx.Where("created_at >= ? AND created_at < ? AND created_at <= ?", out.From, out.To, out.GeneratedAt).Where("NOT EXISTS (SELECT 1 FROM metering_entries WHERE kind = ? AND scope = usage_records.request_id)", "admission")
	if out.Query.Side == "provider" {
		if out.Query.Model != "" {
			query = query.Where("EXISTS (SELECT 1 FROM request_logs WHERE request_id = usage_records.request_id AND provider_model = ?)", out.Query.Model)
		}
		if out.Query.ProviderID != "" {
			query = query.Where("provider_id = ?", out.Query.ProviderID)
		}
		if out.Query.ResourceID != "" {
			query = query.Where("provider_resource_id = ?", out.Query.ResourceID)
		}
	}
	if len(out.Query.ProjectIDs) > 0 {
		query = query.Where("project_id IN ?", out.Query.ProjectIDs)
	}
	if out.Query.Model != "" && out.Query.Side != "provider" {
		query = query.Where("model_name = ?", out.Query.Model)
	}
	if err := query.Limit(statementRowLimit + 1).Find(&legacy).Error; err != nil {
		return err
	}
	if err := statementLimit(len(legacy)); err != nil {
		return err
	}
	for _, row := range legacy {
		if row.RequestID != "" && !seen[row.RequestID] {
			ids = append(ids, row.RequestID)
			seen[row.RequestID] = true
		}
	}
	evidence := map[string]*statementEvidence{}
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		batch := ids[start:end]
		var entries []meteringEntry
		if err := tx.Where("scope IN ? AND kind IN ? AND created_at <= ?", batch, []string{"admission", "attempt_prepared", "shadow_settlement"}, out.GeneratedAt).Limit(statementRowLimit + 1).Find(&entries).Error; err != nil {
			return err
		}
		if err := statementLimit(len(entries)); err != nil {
			return err
		}
		for _, row := range entries {
			item := evidence[row.Scope]
			if item == nil {
				item = &statementEvidence{}
				evidence[row.Scope] = item
			}
			switch row.Kind {
			case "admission":
				item.Admission = &meteringRequestSnapshot{}
				item.At = row.CreatedAt
				if err := json.Unmarshal([]byte(row.Payload), item.Admission); err != nil {
					return err
				}
			case "attempt_prepared":
				var attempt meteringAttemptSnapshot
				if err := json.Unmarshal([]byte(row.Payload), &attempt); err != nil {
					return err
				}
				item.Attempts = append(item.Attempts, attempt)
			case "shadow_settlement":
				if err := json.Unmarshal([]byte(row.Payload), &item.Settlement); err != nil {
					return err
				}
			}
		}
		var usages []UsageRecord
		if err := tx.Where("request_id IN ? AND created_at <= ?", batch, out.GeneratedAt).Limit(statementRowLimit + 1).Find(&usages).Error; err != nil {
			return err
		}
		if err := statementLimit(len(usages)); err != nil {
			return err
		}
		for _, row := range usages {
			item := evidence[row.RequestID]
			if item == nil {
				item = &statementEvidence{}
				evidence[row.RequestID] = item
			}
			item.Usage = append(item.Usage, row)
		}
	}
	for _, id := range ids {
		item := evidence[id]
		if item == nil {
			continue
		}
		if item.Admission == nil {
			if out.Query.Side != "provider" || len(item.Attempts) == 0 {
				continue
			}
			item.Admission = &meteringRequestSnapshot{RequestID: id}
			item.At = item.Attempts[0].At
		}
		admission := item.Admission
		model := admission.ModelName
		if model == "" && len(item.Usage) > 0 {
			model = item.Usage[0].ModelName
		}
		matches := statementMatches(out.Query, admission.ProjectID, model)
		if out.Query.Side == "provider" {
			matches = statementMatches(statementQuery{ProjectIDs: out.Query.ProjectIDs}, admission.ProjectID, "")
		}
		if !matches {
			continue
		}
		inPeriod := statementInWindow(item.At, out)
		if out.Query.Side != "provider" && !inPeriod {
			continue
		}
		if out.Query.Side != "provider" {
			appendTenantStatement(out, item, model)
		}
		if out.Query.Side != "tenant" {
			appendAttemptStatements(out, item, model)
		}
		if err := statementLimit(len(out.Rows)); err != nil {
			return err
		}
	}
	legacyModels, err := statementLegacyModels(tx, legacy)
	if err != nil {
		return err
	}
	for _, row := range legacy {
		if item := evidence[row.RequestID]; item != nil && item.Admission != nil {
			continue
		}
		appendLegacyStatement(out, row, legacyModels[row.RequestID])
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].At.Equal(out.Rows[j].At) {
			return out.Rows[i].ID < out.Rows[j].ID
		}
		return out.Rows[i].At.Before(out.Rows[j].At)
	})
	return statementLimit(len(out.Rows))
}

func appendTenantStatement(out *statementResult, item *statementEvidence, model string) {
	a := item.Admission
	row := statementRow{ID: a.RequestID + ":tenant", RequestID: a.RequestID, Source: "tenant", At: item.At, Timezone: out.Query.Timezone, ProjectID: a.ProjectID, TeamID: a.TeamID, APIKeyID: a.APIKeyID, Model: model, Currency: "USD", Status: "pending", Reason: "settlement_missing"}
	if item.Settlement != nil {
		t := item.Settlement.Tenant
		row.Amount = statementString(t.LegacyUSD)
		row.USD = row.Amount
		row.Units = t.Units
		row.Status = "legacy_incomplete"
		row.Reason = "historical_price_incomplete"
		// Shadow rate cards do not change the actual charged amount. Only attach
		// the actual legacy rate snapshot when its calculated total agrees.
		price := a.LegacyPrice
		if price == nil && a.Price.Source == "legacy_float_configuration" {
			price = &a.Price
		}
		if price != nil {
			charge, err := metering.Price(price.Rates, t.Units, "USD", "")
			if err == nil && equalStatementMoney(charge.Amount, t.LegacyUSD) && t.Status == "estimated" {
				row.Status = "estimated"
				row.Reason = "recorded_tenant_charge"
				row.Price = price
				row.Lines = charge.Lines
			}
		}
		if t.Status == "pending" {
			row.Status = "pending"
			row.Reason = t.Reason
		}
	}
	out.Rows = append(out.Rows, row)
}

func appendAttemptStatements(out *statementResult, item *statementEvidence, model string) {
	a := item.Admission
	for _, attempt := range item.Attempts {
		q := out.Query
		if q.Side == "provider" && !statementInWindow(attempt.At, out) {
			continue
		}
		if q.ProviderID != "" && q.ProviderID != attempt.ProviderID || q.ResourceID != "" && q.ResourceID != attempt.ResourceID {
			continue
		}
		if q.Side == "provider" && q.Model != "" && q.Model != attempt.UpstreamModel {
			continue
		}
		row := statementRow{ID: attempt.ID, RequestID: a.RequestID, Source: "provider_estimate", At: attempt.At, Timezone: q.Timezone, ProjectID: a.ProjectID, TeamID: a.TeamID, APIKeyID: a.APIKeyID, Model: attempt.UpstreamModel, ProviderID: attempt.ProviderID, ResourceID: attempt.ResourceID, Currency: "USD", Status: "pending", Reason: "possibly_sent", Price: attempt.Price}
		if attempt.Price != nil {
			row.Currency = attempt.Price.Currency
		}
		if item.Settlement != nil {
			for _, completion := range item.Settlement.Attempts {
				if completion.ID == attempt.ID {
					priced := completion.Charge
					row.Status = priced.Status
					row.Reason = priced.Reason
					row.Units = priced.Units
					if priced.Charge != nil {
						row.Amount = statementString(priced.Charge.Amount)
						row.Currency = priced.Charge.Currency
						row.Lines = priced.Charge.Lines
						if priced.Charge.USD != "" {
							row.USD = statementString(priced.Charge.USD)
						}
					}
					break
				}
			}
		}
		out.Rows = append(out.Rows, row)
	}
	// A settled request with no attempt evidence cannot prove procurement was
	// free (legacy transports and gateway cache paths must not fabricate cost).
	if len(item.Attempts) == 0 && out.Query.Side == "margin" {
		out.Rows = append(out.Rows, statementRow{ID: a.RequestID + ":unknown-provider", Source: "provider_estimate", RequestID: a.RequestID, At: item.At, Timezone: out.Query.Timezone, ProjectID: a.ProjectID, Model: model, Currency: "USD", Status: "legacy_incomplete", Reason: "attempt_evidence_missing"})
	}
}

func appendLegacyStatement(out *statementResult, u UsageRecord, upstreamModel string) {
	q := out.Query
	matchQuery := q
	if q.Side == "provider" {
		matchQuery.Model = ""
	}
	if !statementMatches(matchQuery, u.ProjectID, u.ModelName) {
		return
	}
	base := statementRow{ID: u.ID, RequestID: u.RequestID, At: u.CreatedAt, Timezone: q.Timezone, ProjectID: u.ProjectID, APIKeyID: u.APIKeyID, Model: u.ModelName, Currency: "USD", Status: "legacy_incomplete", Reason: "historical_evidence_incomplete", Units: metering.Units{Input: u.InputTokens - u.CachedInputTokens - u.CacheWriteTokens, CacheRead: u.CachedInputTokens, CacheWrite: u.CacheWriteTokens - u.CacheWrite5mTokens - u.CacheWrite1hTokens, CacheWrite5m: u.CacheWrite5mTokens, CacheWrite1h: u.CacheWrite1hTokens, Output: u.OutputTokens}}
	if q.Side != "provider" {
		row := base
		row.Source = "tenant"
		row.Amount = statementString(strconv.FormatFloat(u.CostUSD, 'f', 12, 64))
		row.USD = row.Amount
		out.Rows = append(out.Rows, row)
	}
	if q.Side != "tenant" {
		if q.ProviderID != "" && q.ProviderID != u.ProviderID || q.ResourceID != "" && q.ResourceID != u.ProviderResourceID {
			return
		}
		if q.Side == "provider" && q.Model != "" && q.Model != upstreamModel {
			return
		}
		row := base
		row.Model = upstreamModel
		if upstreamModel == "" {
			row.Reason = "historical_upstream_model_unknown"
		}
		row.ID += ":provider"
		row.Source = "provider_estimate"
		row.ProviderID = u.ProviderID
		row.ResourceID = u.ProviderResourceID
		if u.ProviderCostUSD > 0 {
			row.Amount = statementString(strconv.FormatFloat(u.ProviderCostUSD, 'f', 12, 64))
			row.USD = row.Amount
		}
		out.Rows = append(out.Rows, row)
	}
}

func equalStatementMoney(a, b string) bool {
	x, ok := new(big.Rat).SetString(a)
	if !ok {
		return false
	}
	y, ok := new(big.Rat).SetString(b)
	if !ok {
		return false
	}
	// Compare within half of the persisted decimal quantum, not a commercial
	// tolerance. Preserve the original charged value in the statement.
	delta := new(big.Rat).Abs(new(big.Rat).Sub(x, y))
	return delta.Cmp(big.NewRat(1, 2000000000000)) <= 0
}

func statementLegacyModels(tx *gorm.DB, usages []UsageRecord) (map[string]string, error) {
	result := map[string]string{}
	ids := []string{}
	for _, u := range usages {
		if u.RequestID != "" {
			ids = append(ids, u.RequestID)
		}
	}
	for start := 0; start < len(ids); start += 200 {
		var logs []RequestLog
		if err := tx.Select("request_id", "provider_model").Where("request_id IN ?", ids[start:min(start+200, len(ids))]).Find(&logs).Error; err != nil {
			return nil, err
		}
		for _, log := range logs {
			result[log.RequestID] = log.ProviderModel
		}
	}
	return result, nil
}
