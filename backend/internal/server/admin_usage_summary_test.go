package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestAdminUsageSummaryIncludesCacheWriteAndReasoningTokens(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()

	call := CallContext{
		RequestID: "req_summary_tokens",
		Project:   Project{ID: "project_summary_tokens"},
		Key:       APIKey{ID: "key_summary_tokens", OwnerUserID: "user_summary_tokens"},
		Model:     Model{Name: "summary-chat", Modality: "chat"},
		StartedAt: time.Now(),
	}
	route := RouteSelection{Provider: Provider{ID: "provider_summary_tokens"}}
	store.FinishCall(call, route, Usage{
		PromptTokens:          1000,
		CachedInputTokens:     400,
		CacheWriteInputTokens: 25,
		CompletionTokens:      100,
		ReasoningOutputTokens: 30,
	}, http.StatusOK, "", "127.0.0.1", "summary-test")

	response := doJSON(t, app, http.MethodGet, "/api/admin/usage/summary", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("usage summary status = %d, want 200: %s", response.Code, response.Body)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(response.Body), &summary); err != nil {
		t.Fatalf("decode usage summary: %v", err)
	}

	want := map[string]float64{
		"input_tokens":             1000,
		"cached_input_tokens":      400,
		"cache_write_input_tokens": 25,
		"output_tokens":            100,
		"reasoning_output_tokens":  30,
	}
	for key, expected := range want {
		value, ok := summary[key].(float64)
		if !ok {
			t.Fatalf("usage summary %q = %#v, want number %v", key, summary[key], expected)
		}
		if value != expected {
			t.Fatalf("usage summary %q = %v, want %v", key, value, expected)
		}
	}
}

func TestUsageSummarySQLMatchesLegacyPermissions(t *testing.T) {
	store := NewMemoryStore()
	for _, teamID := range []string{"team_summary_a", "team_summary_b"} {
		store.CreateResource("teams", AdminResource{ID: teamID, Name: teamID, Status: StatusActive})
	}
	admin := createUsageSummaryUser(t, store, AdminUser{ID: "usr_summary_admin", Role: "admin"})
	security := createUsageSummaryUser(t, store, AdminUser{ID: "usr_summary_security", Role: "security_admin"})
	leader := createUsageSummaryUser(t, store, AdminUser{ID: "usr_summary_leader", Role: "team_leader", TeamID: "team_summary_a"})
	member := createUsageSummaryUser(t, store, AdminUser{ID: "usr_summary_member", Role: "user", TeamID: "team_summary_a"})
	additionalTeamMember := createUsageSummaryUser(t, store, AdminUser{
		ID: "usr_summary_additional_member", Role: "user", TeamID: "team_summary_b", TeamIDs: []string{"team_summary_a"},
	})
	user := createUsageSummaryUser(t, store, AdminUser{ID: "usr_summary_user", Role: "user"})
	other := createUsageSummaryUser(t, store, AdminUser{ID: "usr_summary_other", Role: "user", TeamID: "team_summary_b"})

	teamProject := store.CreateProject(Project{ID: "prj_summary_team", Name: "Team Summary", TeamID: leader.TeamID})
	sharedProject := store.CreateProject(Project{ID: "prj_summary_shared", Name: "Shared Summary", OwnerUserID: other.ID})
	otherProject := store.CreateProject(Project{ID: "prj_summary_other", Name: "Other Summary", TeamID: other.TeamID})
	store.CreateResource("project-members", AdminResource{
		ID: "member_summary_user_viewer", Name: "Summary User Viewer", Status: StatusActive,
		Fields: map[string]any{"project_id": sharedProject.ID, "user_id": user.ID, "role": "viewer"},
	})

	userKey := createUsageSummaryKey(t, store, APIKey{ID: "key_summary_user", ProjectID: sharedProject.ID, OwnerUserID: user.ID})
	sharedOtherKey := createUsageSummaryKey(t, store, APIKey{ID: "key_summary_shared_other", ProjectID: sharedProject.ID, OwnerUserID: other.ID})
	teamKey := createUsageSummaryKey(t, store, APIKey{ID: "key_summary_team", ProjectID: teamProject.ID, OwnerUserID: member.ID})
	otherKey := createUsageSummaryKey(t, store, APIKey{ID: "key_summary_other", ProjectID: otherProject.ID, OwnerUserID: other.ID})

	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	records := []UsageRecord{
		usageSummaryRecord("usage_summary_user_attr", otherProject.ID, otherKey.ID, user.ID, 10, now),
		usageSummaryRecord("usage_summary_user_key", sharedProject.ID, userKey.ID, "", 20, now),
		usageSummaryRecord("usage_summary_shared_other", sharedProject.ID, sharedOtherKey.ID, "", 30, now),
		usageSummaryRecord("usage_summary_team_missing_key", teamProject.ID, "key_summary_deleted", "", 40, now),
		usageSummaryRecord("usage_summary_team_member_attr", otherProject.ID, otherKey.ID, member.ID, 50, now),
		usageSummaryRecord("usage_summary_team_overlap", teamProject.ID, teamKey.ID, additionalTeamMember.ID, 60, now),
		usageSummaryRecord("usage_summary_other", otherProject.ID, otherKey.ID, other.ID, 70, now),
		usageSummaryRecord("usage_summary_empty_scope", "", "", "", 80, now),
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	logs := []RequestLog{
		usageSummaryLog("log_summary_user_key", sharedProject.ID, userKey.ID, "", http.StatusOK, now),
		usageSummaryLog("log_summary_user_attr", otherProject.ID, otherKey.ID, user.ID, http.StatusBadGateway, now),
		usageSummaryLog("log_summary_shared_other", sharedProject.ID, sharedOtherKey.ID, "", http.StatusBadGateway, now),
		usageSummaryLog("log_summary_team_missing_key", teamProject.ID, "key_summary_deleted", "", http.StatusBadGateway, now),
		usageSummaryLog("log_summary_team_member_attr", otherProject.ID, otherKey.ID, member.ID, http.StatusBadGateway, now),
		usageSummaryLog("log_summary_team_overlap", teamProject.ID, teamKey.ID, additionalTeamMember.ID, http.StatusOK, now),
		usageSummaryLog("log_summary_other", otherProject.ID, otherKey.ID, other.ID, http.StatusBadGateway, now),
		usageSummaryLog("log_summary_empty_scope", "", "", "", http.StatusBadGateway, now),
		usageSummaryLog("log_summary_playground", "admin_playground", userKey.ID, user.ID, http.StatusBadGateway, now),
	}
	if err := store.db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}

	server := New(store)
	tests := []struct {
		name              string
		user              AdminUser
		wantUsageRecords  int64
		wantRequestCount  int64
		wantRequestErrors int64
	}{
		{name: "admin", user: admin, wantUsageRecords: 8, wantRequestCount: 8, wantRequestErrors: 6},
		{name: "security admin", user: security, wantUsageRecords: 8, wantRequestCount: 8, wantRequestErrors: 6},
		{name: "team leader", user: leader, wantUsageRecords: 3, wantRequestCount: 2, wantRequestErrors: 1},
		{name: "user", user: user, wantUsageRecords: 2, wantRequestCount: 1, wantRequestErrors: 0},
		{name: "no visible records", user: AdminUser{ID: "usr_summary_nobody", Role: "user"}, wantUsageRecords: 0, wantRequestCount: 0, wantRequestErrors: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			legacy := summarizeUsage(
				server.filterUsageRecordsForUser(test.user, store.ListUsageRecords()),
				server.filterRequestLogsForUser(test.user, store.ListRequestLogs()),
			)
			actual, err := server.usageSummaryForUser(t.Context(), test.user)
			if err != nil {
				t.Fatal(err)
			}
			assertUsageSummaryEqual(t, actual, legacy)
			if got := int64(usageSummaryNumber(t, actual["usage_record_count"])); got != test.wantUsageRecords {
				t.Fatalf("usage_record_count = %d, want %d", got, test.wantUsageRecords)
			}
			if got := int64(usageSummaryNumber(t, actual["request_count"])); got != test.wantRequestCount {
				t.Fatalf("request_count = %d, want %d", got, test.wantRequestCount)
			}
			if got := int64(usageSummaryNumber(t, actual["errors"])); got != test.wantRequestErrors {
				t.Fatalf("errors = %d, want %d", got, test.wantRequestErrors)
			}
		})
	}
}

func TestQueryUsageSummaryEmptyScopesReturnZero(t *testing.T) {
	store := NewMemoryStore()
	if err := store.db.Create(&UsageRecord{ID: "usage_summary_hidden", InputTokens: 10, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{ID: "log_summary_hidden", StatusCode: http.StatusBadGateway, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := store.QueryUsageSummary(t.Context(), UsageSummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if summary != (UsageSummary{}) {
		t.Fatalf("empty scopes returned %+v, want zero summary", summary)
	}
}

func TestQueryUsageSummaryAcceptsLargePermissionScopes(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	if err := store.db.Create(&UsageRecord{
		ID: "usage_summary_large_scope", AttributedUserID: "usr_large_match", ProjectID: "prj_large_match",
		APIKeyID: "key_large_match", InputTokens: 10, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{
		ID: "log_summary_large_scope", ProjectID: "prj_large_match", APIKeyID: "key_large_match",
		StatusCode: http.StatusBadGateway, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	const scopeSize = 33000
	ids := make([]string, scopeSize)
	for index := range ids {
		ids[index] = fmt.Sprintf("scope_%d", index)
	}
	ids[scopeSize-3] = "usr_large_match"
	ids[scopeSize-2] = "prj_large_match"
	ids[scopeSize-1] = "key_large_match"
	summary, err := store.QueryUsageSummary(t.Context(), UsageSummaryQuery{
		UsageRecords: UsageSummaryScope{AttributedUserIDs: ids, ProjectIDs: ids, APIKeyIDs: ids},
		RequestLogs:  UsageSummaryScope{ProjectIDs: ids, APIKeyIDs: ids},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsageRecordCount != 1 || summary.RequestCount != 1 || summary.Errors != 1 || summary.InputTokens != 10 {
		t.Fatalf("large permission scope summary = %+v", summary)
	}
}

func TestBackfillUsageRecordAttributionNormalization(t *testing.T) {
	store := NewMemoryStore()
	if err := store.db.Delete(&ClusterTaskState{}, "name = ?", usageAttributionNormalizationTask).Error; err != nil {
		t.Fatal(err)
	}
	rawAttribution := "\t\u00a0usr_backfill_summary\u3000\n"
	if err := store.db.Exec(
		"INSERT INTO usage_records (id, attributed_user_id, input_tokens, created_at) VALUES (?, ?, ?, ?)",
		"usage_summary_backfill", rawAttribution, 17, time.Now().UTC(),
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillUsageRecordAttributionNormalization(store.db); err != nil {
		t.Fatal(err)
	}
	var record UsageRecord
	if err := store.db.First(&record, "id = ?", "usage_summary_backfill").Error; err != nil {
		t.Fatal(err)
	}
	if record.AttributedUserID != "usr_backfill_summary" {
		t.Fatalf("normalized attribution = %q", record.AttributedUserID)
	}
	summary, err := store.QueryUsageSummary(t.Context(), UsageSummaryQuery{
		UsageRecords: UsageSummaryScope{AttributedUserIDs: []string{"usr_backfill_summary"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsageRecordCount != 1 || summary.InputTokens != 17 {
		t.Fatalf("backfilled attribution summary = %+v", summary)
	}
	queries := countStoreQueries(t, store, func() {
		if err := backfillUsageRecordAttributionNormalization(store.db); err != nil {
			t.Fatal(err)
		}
	})
	if queries != 1 {
		t.Fatalf("completed normalization issued %d queries, want only the task-state lookup", queries)
	}
}

func TestUsageSummarySQLiteQueryUsesScopeIndexes(t *testing.T) {
	store := NewMemoryStore()
	dryDB := store.db.Session(&gorm.Session{DryRun: true})
	scoped, err := applyUsageSummaryScope(dryDB.Model(&UsageRecord{}), store.dbDriver, UsageSummaryScope{
		AttributedUserIDs: []string{"usr_plan"},
		ProjectIDs:        []string{"prj_plan"},
		APIKeyIDs:         []string{"key_plan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var aggregate struct{ Count int64 }
	statement := scoped.Select("COUNT(*) AS count").Find(&aggregate).Statement
	if statement.SQL.Len() == 0 {
		t.Fatal("dry-run summary query did not produce SQL")
	}
	var plan []struct{ Detail string }
	if err := store.db.Raw("EXPLAIN QUERY PLAN "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	details := make([]string, 0, len(plan))
	for _, row := range plan {
		details = append(details, row.Detail)
	}
	joined := strings.Join(details, "\n")
	if strings.Contains(joined, "SCAN usage_records") {
		t.Fatalf("scoped summary query scans usage history instead of using indexes:\n%s", joined)
	}
	for _, indexName := range []string{
		"idx_usage_records_attributed_user_id",
		"idx_usage_records_project_id",
		"idx_usage_records_api_key_id",
	} {
		if !strings.Contains(joined, indexName) {
			t.Fatalf("query plan does not use %s:\n%s", indexName, joined)
		}
	}
}

func TestUsageSummaryHandlersReportQueryFailure(t *testing.T) {
	store := NewMemoryStore()
	app := New(failingUsageSummaryStore{Store: store}).Handler()
	for _, path := range []string{"/api/admin/usage/summary", "/api/admin/overview"} {
		t.Run(path, func(t *testing.T) {
			response := doJSON(t, app, http.MethodGet, path, nil, "")
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", response.Code, response.Body)
			}
		})
	}
}

func BenchmarkUsageSummaryAggregation(b *testing.B) {
	store := NewMemoryStore()
	sqlDB, err := store.db.DB()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = sqlDB.Close() })
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	const historySize = 5000
	records := make([]UsageRecord, historySize)
	logs := make([]RequestLog, historySize)
	for index := 0; index < historySize; index++ {
		records[index] = usageSummaryRecord(fmt.Sprintf("usage_bench_%d", index), "prj_bench", "key_bench", "usr_bench", int64(index%100), now)
		logs[index] = usageSummaryLog(fmt.Sprintf("log_bench_%d", index), "prj_bench", "key_bench", "usr_bench", http.StatusOK, now)
	}
	if err := store.db.CreateInBatches(&records, 100).Error; err != nil {
		b.Fatal(err)
	}
	if err := store.db.CreateInBatches(&logs, 100).Error; err != nil {
		b.Fatal(err)
	}
	query := UsageSummaryQuery{
		UsageRecords: UsageSummaryScope{Global: true},
		RequestLogs:  UsageSummaryScope{Global: true},
	}
	b.Run("sql_aggregate", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			if _, err := store.QueryUsageSummary(b.Context(), query); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("legacy_in_memory", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			_ = summarizeUsage(store.ListUsageRecords(), store.ListRequestLogs())
		}
	})
}

type failingUsageSummaryStore struct {
	Store
}

func (failingUsageSummaryStore) QueryUsageSummary(context.Context, UsageSummaryQuery) (UsageSummary, error) {
	return UsageSummary{}, errors.New("forced usage summary failure")
}

func createUsageSummaryUser(t *testing.T, store *GormStore, user AdminUser) AdminUser {
	t.Helper()
	user.Username = user.ID
	user.Email = user.ID + "@tokenhub.local"
	user.Status = StatusActive
	created, err := store.CreateAdminUser(user, "summary123456")
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func createUsageSummaryKey(t *testing.T, store *GormStore, key APIKey) APIKey {
	t.Helper()
	key.Name = key.ID
	key.Status = StatusActive
	created, _, err := store.CreateAPIKey(key.ProjectID, key, "thk_"+key.ID)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func usageSummaryRecord(id string, projectID string, apiKeyID string, attributedUserID string, base int64, createdAt time.Time) UsageRecord {
	return UsageRecord{
		ID: id, RequestID: "req_" + id, ProjectID: projectID, APIKeyID: apiKeyID, AttributedUserID: attributedUserID,
		InputTokens: base, CachedInputTokens: base + 1, CacheWriteTokens: base + 2, OutputTokens: base + 3,
		ReasoningTokens: base + 4, TotalTokens: base + 5, CostUSD: float64(base) / 100, CreatedAt: createdAt,
	}
}

func usageSummaryLog(id string, projectID string, apiKeyID string, attributedUserID string, statusCode int, createdAt time.Time) RequestLog {
	return RequestLog{
		ID: id, RequestID: "req_" + id, ProjectID: projectID, APIKeyID: apiKeyID,
		AttributedUserID: attributedUserID, StatusCode: statusCode, CreatedAt: createdAt,
	}
}

func assertUsageSummaryEqual(t *testing.T, actual map[string]any, expected map[string]any) {
	t.Helper()
	for _, key := range []string{
		"request_count", "usage_record_count", "input_tokens", "cached_input_tokens", "cache_write_input_tokens",
		"output_tokens", "reasoning_output_tokens", "total_tokens", "estimated_cost_usd", "errors",
	} {
		actualNumber := usageSummaryNumber(t, actual[key])
		expectedNumber := usageSummaryNumber(t, expected[key])
		if math.Abs(actualNumber-expectedNumber) > 1e-12 {
			t.Fatalf("%s = %v, want %v", key, actual[key], expected[key])
		}
	}
}

func usageSummaryNumber(t *testing.T, value any) float64 {
	t.Helper()
	switch number := value.(type) {
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case float64:
		return number
	default:
		t.Fatalf("summary value %#v has unsupported type %T", value, value)
		return 0
	}
}
