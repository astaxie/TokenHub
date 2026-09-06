package server

import (
	"context"
	"strconv"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

// Reconciliation uses the same per-attempt cost projection as platform
// statements, never the downstream charge attached to the winning route.
func (s *GormStore) ListProviderReconciliationUsages(from, to time.Time, window time.Duration) ([]reconciliation.Usage, error) {
	q := billingStatementQuery{Kind: "provider", From: from.Add(-window), To: to.Add(window), GroupBy: "provider"}
	result := []reconciliation.Usage{}
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
		result = append(result, reconciliation.Usage{ID: row.ID, RequestID: requestID, ProjectID: row.ProjectID, ModelName: row.Model, ProviderID: row.ProviderID, ProviderResourceID: row.ResourceID, ProviderCostUSD: amount, ProviderCostKnown: known, CreatedAt: row.OccurredAt})
		return nil
	})
	return result, err
}
