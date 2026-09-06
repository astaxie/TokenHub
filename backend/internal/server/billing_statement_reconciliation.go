package server

import (
	"context"
	"strconv"
	"strings"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

// Reconciliation uses the same per-attempt cost projection as platform
// statements, never the downstream charge attached to the winning route.
func (s *GormStore) ListProviderReconciliationUsages(from, to time.Time, window time.Duration) ([]reconciliation.Usage, error) {
	return s.ListScopedProviderReconciliationUsages(from, to, window, "", "")
}

func (s *GormStore) ListScopedProviderReconciliationUsages(from, to time.Time, window time.Duration, provider, resource string) ([]reconciliation.Usage, error) {
	base := billingStatementQuery{Kind: "provider", From: from.Add(-window), To: to.Add(window), GroupBy: "provider"}
	queries := []billingStatementQuery{}
	values := func(raw string) []string {
		seen := map[string]bool{}
		items := []string{}
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value != "" && !seen[value] {
				seen[value] = true
				items = append(items, value)
			}
		}
		return items
	}
	// Connector resource scopes take precedence over provider scopes.
	if resources := values(resource); len(resources) > 0 {
		for _, id := range resources {
			q := base
			q.Resource = id
			queries = append(queries, q)
		}
	} else {
		for _, id := range values(provider) {
			q := base
			q.Provider = id
			queries = append(queries, q)
		}
	}
	if len(queries) == 0 {
		queries = append(queries, base)
	}
	result := []reconciliation.Usage{}
	for _, q := range queries {
		err := s.scanBillingStatements(context.Background(), q, func(row billingStatementRow) error {
			amount, err := strconv.ParseFloat(row.AmountUSD, 64)
			known := err == nil && row.Status != "pending"
			if !known {
				amount = 0
			}
			requestID := row.UpstreamRequestID
			if requestID == "" {
				requestID = row.RequestID
			}
			if row.Source == "legacy_usage" {
				row.ID = strings.TrimSuffix(row.ID, ":provider")
			}
			model := row.TenantModel
			if model == "" {
				model = row.Model
			}
			result = append(result, reconciliation.Usage{ID: row.ID, RequestID: requestID, ProjectID: row.ProjectID, ModelName: model, ProviderID: row.ProviderID, ProviderResourceID: row.ResourceID, ProviderCostUSD: amount, ProviderCostKnown: known, CreatedAt: row.OccurredAt})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
