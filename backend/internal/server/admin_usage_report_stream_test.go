package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestUsageReportStreamsLargeHistoryWithOneDatabaseConnection(t *testing.T) {
	store := NewMemoryStore()
	database, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	const recordCount = 5000
	for offset := 0; offset < recordCount; offset += 100 {
		batch := make([]UsageRecord, 0, 100)
		logs := make([]RequestLog, 0, 100)
		for index := offset; index < offset+100; index++ {
			id := fmt.Sprintf("report_stream_%05d", index)
			timestamp := now.Add(-time.Duration(index%48+1) * time.Hour)
			batch = append(batch, UsageRecord{
				ID: id, RequestID: id, ProjectID: "project_stream", APIKeyID: "key_stream", ModelName: "model_stream",
				InputTokens: 3, CachedInputTokens: 1, CacheWriteTokens: 1, OutputTokens: 2, ReasoningTokens: 1,
				TotalTokens: 5, CostUSD: 0.25, CreatedAt: timestamp,
			})
			logs = append(logs, usageSummaryLog(id, "project_stream", "key_stream", "", http.StatusOK, timestamp))
		}
		if err := store.db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&logs).Error; err != nil {
			t.Fatal(err)
		}
	}
	oldFailure := usageSummaryLog("old_stream_failure", "project_stream", "key_stream", "", http.StatusBadGateway, now.AddDate(-1, 0, 0))
	if err := store.db.Create(&oldFailure).Error; err != nil {
		t.Fatal(err)
	}
	// The request cursor arrives after usage aggregation. Its old failure must
	// convert recent daily totals to monthly buckets without rereading rows.
	server := New(store)
	aggregate := server.newUsageReportAggregate(AdminUser{Role: "admin"}, "all", now, time.UTC)
	query := UsageSummaryQuery{UsageRecords: UsageSummaryScope{Global: true, CreatedAtBefore: now}, RequestLogs: UsageSummaryScope{Global: true, CreatedAtBefore: now}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := store.StreamUsageReportRecords(ctx, query, aggregate.addUsage, aggregate.addRequest); err != nil {
		t.Fatal(err)
	}
	if aggregate.summary.RequestCount != recordCount+1 || aggregate.summary.UsageRecordCount != recordCount || aggregate.summary.TotalTokens != recordCount*5 || aggregate.summary.EstimatedCostUSD != recordCount*0.25 || aggregate.summary.Errors != 1 {
		t.Fatalf("large-history summary = %#v", aggregate.summary)
	}
	if aggregate.granularity != "month" || len(aggregate.buckets) != 2 {
		t.Fatalf("retained time aggregates = %d (%s), want two months", len(aggregate.buckets), aggregate.granularity)
	}
	for dimension, buckets := range aggregate.dimensions {
		if len(buckets) != 1 {
			t.Fatalf("retained %s aggregates = %d, want one dimension value", dimension, len(buckets))
		}
	}
	assertUsageReportSeriesMatchesSummary(t, map[string]any{
		"summary":    usageSummaryPayload(aggregate.summary),
		"timeseries": usageReportTimeseries(aggregate.buckets, aggregate.earliest, now, time.UTC, aggregate.granularity),
	})
}

func TestUsageReportStreamingStopsWhenCanceled(t *testing.T) {
	store := NewMemoryStore()
	for index := 0; index < 20; index++ {
		record := usageSummaryRecord(fmt.Sprintf("cancel_stream_%d", index), "project_stream", "key_stream", "", 1, time.Now().UTC())
		if err := store.db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	count := 0
	err := store.StreamUsageReportRecords(ctx, UsageSummaryQuery{UsageRecords: UsageSummaryScope{Global: true}, RequestLogs: UsageSummaryScope{Global: true}}, func(UsageRecord) {
		count++
		cancel()
	}, func(UsageReportRequest) {
		t.Fatal("request cursor must not start after cancellation")
	})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatalf("stream stopped with count=%d error=%v, want one row and cancellation", count, err)
	}
}

func assertUsageReportBreakdownMatchesLegacy(t *testing.T, server *Server, user AdminUser, report map[string]any) {
	t.Helper()
	expected := server.usageBreakdownForUser(user)
	records := server.filterUsageRecordsForUser(user, server.store.ListUsageRecords())
	expected["api_keys"] = aggregateUsage(records, func(record UsageRecord) string { return record.APIKeyID })
	if !isPlatformAdminRole(user.Role) {
		delete(expected, "providers")
		delete(expected, "provider_resources")
	}
	actual := report["breakdown"].(map[string]any)
	if len(actual) != len(expected) {
		t.Fatalf("breakdown dimensions = %d, want %d", len(actual), len(expected))
	}
	for dimension, expectedRows := range expected {
		byID := func(rows []map[string]any) map[string]map[string]any {
			result := map[string]map[string]any{}
			for _, row := range rows {
				result[row["id"].(string)] = row
			}
			return result
		}
		if !reflect.DeepEqual(byID(actual[dimension].([]map[string]any)), byID(expectedRows.([]map[string]any))) {
			t.Fatalf("%s streaming breakdown = %#v, want legacy %#v", dimension, actual[dimension], expectedRows)
		}
	}
}
