package server

import (
	"fmt"
	"testing"
	"time"
)

func TestCancellationAggregation(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Cancellation aggregation", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Observed key", Status: StatusActive}, "thk_cancellation_aggregation")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	var logs []RequestLog
	var usage []UsageRecord
	for index, outcome := range []struct {
		status int
		code   string
	}{
		{200, ""}, {499, "request_cancelled"}, {499, "other"}, {500, "internal_error"},
		{504, "provider_upstream_timeout"}, {400, "invalid_request"}, {499, "response_cancelled_worker_lost"},
		{499, ""}, {500, ""}, {500, "request_cancelled"},
	} {
		id := fmt.Sprintf("cancellation_%02d", index)
		logs = append(logs, RequestLog{ID: id, RequestID: id, ProjectID: project.ID, APIKeyID: key.ID, StatusCode: outcome.status, ErrorCode: outcome.code, CreatedAt: now})
		usage = append(usage, UsageRecord{ID: id, RequestID: id, ProjectID: project.ID, APIKeyID: key.ID, InputTokens: 3, OutputTokens: 2, TotalTokens: 5, CostUSD: 0.25, CreatedAt: now})
	}
	if err := store.db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&usage).Error; err != nil {
		t.Fatal(err)
	}
	// NULL must not disappear through SQL three-valued logic.
	if err := store.db.Model(&RequestLog{}).Where("id = ?", "cancellation_07").Update("error_code", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{ID: "hidden", RequestID: "hidden", ProjectID: "hidden", APIKeyID: "hidden", StatusCode: 500, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&UsageRecord{ID: "hidden", RequestID: "hidden", ProjectID: "hidden", APIKeyID: "hidden", TotalTokens: 999, CostUSD: 99, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	query := RequestLogQuery{Page: 1, PageSize: 3, APIKeyIDs: []string{key.ID}, Since: now.Add(-time.Hour), Until: now.Add(time.Second)}
	page, err := store.QueryRequestLogs(query)
	if err != nil {
		t.Fatal(err)
	}
	if page.Summary.All != 10 || page.Summary.OK != 1 || page.Summary.Error != 8 || page.Pagination.Total != 10 {
		t.Fatalf("all summary=%+v pagination=%+v", page.Summary, page.Pagination)
	}
	query.Status = "error"
	for number := 1; number <= 3; number++ {
		query.Page = number
		page, err = store.QueryRequestLogs(query)
		if err != nil {
			t.Fatal(err)
		}
		if page.Pagination.Total != 8 || page.Pagination.TotalPages != 3 {
			t.Fatalf("error pagination=%+v", page.Pagination)
		}
		want := 3
		if number == 3 {
			want = 2
		}
		if len(page.Data) != want {
			t.Fatalf("page %d has %d rows, want %d", number, len(page.Data), want)
		}
		for _, log := range page.Data {
			if log.RequestID == "cancellation_01" || log.StatusCode < 400 || log.APIKeyID != key.ID {
				t.Fatalf("unexpected error row=%+v", log)
			}
		}
	}
	query.Status, query.Page, query.Keyword = "all", 1, "request_cancelled"
	page, err = store.QueryRequestLogs(query)
	if err != nil || page.Pagination.Total != 2 {
		t.Fatalf("code search=%+v error=%v", page, err)
	}
	query.Keyword = "499"
	page, err = store.QueryRequestLogs(query)
	if err != nil || page.Pagination.Total != 4 {
		t.Fatalf("status search=%+v error=%v", page, err)
	}

	memory := summarizeUsage(usage, logs)
	if memory["request_count"] != 10 || memory["errors"] != 8 || memory["total_tokens"] != int64(50) || memory["estimated_cost_usd"] != 2.5 {
		t.Fatalf("memory summary=%+v", memory)
	}
	scope := UsageSummaryScope{APIKeyIDs: []string{key.ID}, CreatedAtFrom: now.Add(-time.Hour), CreatedAtBefore: now.Add(time.Second)}
	summaryQuery := UsageSummaryQuery{UsageRecords: scope, RequestLogs: scope}
	summary, err := store.QueryUsageSummary(t.Context(), summaryQuery)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequestCount != 10 || summary.Errors != 8 || summary.TotalTokens != 50 || summary.EstimatedCostUSD != 2.5 {
		t.Fatalf("SQL summary=%+v", summary)
	}
	keyUsage, err := store.QueryAPIKeyUsage(t.Context(), APIKeyUsageQuery{Key: key, Project: project, From: scope.CreatedAtFrom, To: scope.CreatedAtBefore})
	if err != nil {
		t.Fatal(err)
	}
	if keyUsage.Summary.RequestCount != 10 || keyUsage.Summary.ErrorCount != 8 || keyUsage.Summary.TotalTokens != 50 || keyUsage.Summary.EstimatedCostUSD != 2.5 {
		t.Fatalf("key summary=%+v", keyUsage.Summary)
	}
	var errorCount, requests int64
	for _, row := range keyUsage.Errors {
		if row.StatusCode == 499 && row.ErrorCode == "request_cancelled" {
			t.Fatalf("cancellation in error breakdown=%+v", row)
		}
		errorCount += row.RequestCount
	}
	if errorCount != 8 {
		t.Fatalf("error breakdown count=%d, want 8", errorCount)
	}
	errorCount = 0
	for _, point := range keyUsage.Timeseries {
		requests += point.RequestCount
		errorCount += point.ErrorCount
	}
	if requests != 10 || errorCount != 8 {
		t.Fatalf("key timeseries requests=%d errors=%d", requests, errorCount)
	}

	aggregate := New(store).newUsageReportAggregate(AdminUser{Role: "admin"}, "today", scope.CreatedAtBefore, time.UTC)
	if err := store.StreamUsageReportRecords(t.Context(), summaryQuery, aggregate.addUsage, aggregate.addRequest); err != nil {
		t.Fatal(err)
	}
	if aggregate.summary != summary {
		t.Fatalf("report summary=%+v, want %+v", aggregate.summary, summary)
	}
	if len(aggregate.buckets) != 1 {
		t.Fatalf("report buckets=%+v", aggregate.buckets)
	}
	for _, bucket := range aggregate.buckets {
		if *bucket != summary {
			t.Fatalf("report bucket=%+v, want %+v", *bucket, summary)
		}
	}
}
