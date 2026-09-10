package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	billingstore "tokenhub/backend/internal/billing/persistence"
)

func TestExternalStatementBoundaryRowsDoNotConsumeLimit(t *testing.T) {
	store := NewMemoryStore()
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]billingstore.RecordRow, statementRowLimit+1)
	for index := range rows {
		rows[index] = billingstore.RecordRow{ID: fmt.Sprintf("boundary-%d", index), ExternalID: fmt.Sprint(index), UsageStartAt: from.Add(-time.Hour), UsageEndAt: from, CreatedAt: from, NetAmount: "1", Currency: "USD"}
	}
	if err := store.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	query := statementTestQuery("provider")
	query.ProjectIDs = nil
	result, err := store.BillingStatement(context.Background(), query)
	if err != nil || len(result.Rows) != 0 {
		t.Fatalf("out-of-period rows consumed limit: rows=%d err=%v", len(result.Rows), err)
	}
	instant := billingstore.RecordRow{ID: "instant", ExternalID: "instant", UsageStartAt: from, UsageEndAt: from, CreatedAt: from, NetAmount: "0", Currency: "USD"}
	if err := store.db.Create(&instant).Error; err != nil {
		t.Fatal(err)
	}
	result, err = store.BillingStatement(context.Background(), query)
	if err != nil || len(result.Rows) != 1 || result.Rows[0].ID != "instant" {
		t.Fatalf("instant at from was lost: %+v %v", result.Rows, err)
	}
}
