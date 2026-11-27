package server

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOverviewBoundsAlertsAndOmitsModelDetails(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	alerts := make([]AlertEvent, 301)
	for i := range alerts {
		alerts[i] = AlertEvent{ID: fmt.Sprintf("alert_%04d", i), Code: "quota_exceeded", ScopeID: "p", Severity: "warning", CreatedAt: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)}
	}
	if err := store.db.CreateInBatches(alerts, 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&Model{ID: "m", Name: "m", Status: StatusActive, Metadata: map[string]string{"description": strings.Repeat("private-model-details", 1000)}}).Error; err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, New(store).Handler(), http.MethodGet, "/api/admin/overview", nil, "")
	if response.Code != 200 {
		t.Fatalf("response=%d %s", response.Code, response.Body)
	}
	var payload struct {
		Alerts  []AlertEvent
		Models  json.RawMessage
		Summary map[string]float64    `json:"summary"`
		Status  OverviewSummaryStatus `json:"summary_status"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Alerts) != 20 || payload.Alerts[0].ID != "alert_0300" {
		t.Fatalf("unbounded or unsorted alerts: %d", len(payload.Alerts))
	}
	if len(payload.Models) > 0 || strings.Contains(response.Body, "private-model-details") {
		t.Fatal("overview loaded model details")
	}
	if payload.Summary["model_count"] != 1 || payload.Status.State != "ready" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestAlertPagesEnforceBoundsAndFilters(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 160; i++ {
		alert := AlertEvent{ID: fmt.Sprintf("alert_%04d", i), ScopeID: "p", Severity: "warning", CreatedAt: now}
		if i%2 == 0 {
			alert.ScopeID = "other"
		}
		if err := store.db.Create(&alert).Error; err != nil {
			t.Fatal(err)
		}
	}
	app := New(store).Handler()
	response := doJSON(t, app, http.MethodGet, "/api/admin/alerts?page_size=10000", nil, "")
	var page AlertPage
	if err := json.Unmarshal([]byte(response.Body), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(page.Data) != 100 || page.PageSize != 100 || page.Total != 160 || page.Data[0].ID != "alert_0159" {
		t.Fatalf("page=%+v", page)
	}
	response = doJSON(t, app, http.MethodGet, "/api/admin/alerts?page=2&page_size=50&project_id=p&severity=warning&from=2026-01-01T00%3A00%3A00Z&before=2026-01-02T00%3A00%3A00Z", nil, "")
	if err := json.Unmarshal([]byte(response.Body), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || page.Total != 80 || len(page.Data) != 30 || page.Data[0].ID != "alert_0059" {
		t.Fatalf("filtered=%+v body=%s", page, response.Body)
	}
	for _, query := range []string{"page=-1", "page_size=invalid", "from=invalid", "from=2026-01-02T00:00:00Z&before=2026-01-01T00:00:00Z"} {
		if response := doJSON(t, app, http.MethodGet, "/api/admin/alerts?"+query, nil, ""); response.Code != 400 {
			t.Fatalf("query=%s status=%d", query, response.Code)
		}
	}
	if _, err := store.QueryAlertPage(context.Background(), AlertPageQuery{PageSize: 1000}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectQuotaEventsIncludeFullHistory(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.db.Create(&AlertEvent{ID: "a", ScopeID: "p", Code: "quota_exceeded", CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{ID: "r", ProjectID: "p", StatusCode: 429, CreatedAt: at.Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	values, err := store.QueryProjectQuotaEvents(context.Background(), []string{"p"}, UsageSummaryScope{Global: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if values["p"].Count != 2 || values["p"].Latest != at.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("events=%+v", values)
	}
	hidden, err := store.QueryProjectQuotaEvents(context.Background(), []string{"other"}, UsageSummaryScope{Global: true}, true)
	if err != nil || len(hidden) != 0 {
		t.Fatalf("hidden=%+v err=%v", hidden, err)
	}
}

func TestProjectQuotaEventScopesHideOtherKeysAndAlerts(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Create(&AlertEvent{ID: "hidden_alert", ScopeID: "p", Code: "quota_exceeded"}).Error; err != nil {
		t.Fatal(err)
	}
	logs := []RequestLog{{ID: "mine", ProjectID: "p", APIKeyID: "mine", StatusCode: 429}, {ID: "other", ProjectID: "p", APIKeyID: "other", StatusCode: 429}}
	if err := store.db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	result, err := store.QueryProjectQuotaEvents(t.Context(), []string{"p"}, UsageSummaryScope{APIKeyIDs: []string{"mine"}}, false)
	if err != nil || result["p"].Count != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestModelSearchPreservesCapabilitiesAndModality(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	model := Model{ID: "search_m", Name: "search-m", Status: StatusActive, Modality: "embedding", Capabilities: []string{"chat", "tools"}}
	if err := store.db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"tools", "embedding", "model", "chat tools"} {
		models, err := store.SearchAdminModels(t.Context(), true, query)
		if err != nil || len(models) != 1 || len(models[0].Capabilities) != 2 {
			t.Fatalf("query=%s models=%+v err=%v", query, models, err)
		}
	}
	models, err := store.SearchAdminModels(t.Context(), false, "tools")
	if err != nil || len(models) != 0 {
		t.Fatalf("unrouted models leaked: %+v err=%v", models, err)
	}
	literals := []Model{{ID: "literal", Name: "model100%_x", Status: StatusActive}, {ID: "broad", Name: "model100plainax", Status: StatusActive}}
	if err := store.db.Create(&literals).Error; err != nil {
		t.Fatal(err)
	}
	literal, err := store.SearchAdminModels(t.Context(), true, "100%_x")
	if err != nil || len(literal) != 1 || literal[0].ID != "literal" {
		t.Fatalf("literal=%+v err=%v", literal, err)
	}

}

func TestOverviewModelConsumersPreserveRoleVisibility(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Create(&[]Model{
		{ID: "routed", Name: "test-routed", Status: StatusActive},
		{ID: "hidden", Name: "test-unrouted", Status: StatusActive},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&ModelRoute{ID: "test-route", ModelName: "test-routed", Status: StatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, role := range []string{"team_leader", "security_admin", "user"} {
		t.Run(role, func(t *testing.T) {
			token := createAdminOperationMethodRoutingSession(t, store, role+"-model-reader", role)
			for _, path := range []string{"/api/admin/models", "/api/admin/model-search?q=test-"} {
				resp := methodRoutingJSONRequest(t, app, http.MethodGet, path, nil, token)
				var result struct{ Data []Model }
				if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &result) != nil {
					t.Fatalf("%s: %d %s", path, resp.Code, resp.Body)
				}
				names := map[string]bool{}
				for _, item := range result.Data {
					names[item.Name] = true
				}
				if !names["test-routed"] || names["test-unrouted"] != (role == "security_admin") {
					t.Fatalf("%s: models=%v", path, names)
				}
			}
			resp := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/models", nil, token)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("model writes: status=%d", resp.Code)
			}
		})
	}
}

func TestAlertHistoryFindsProjectQuotaEventsAndSearchesRetainedRows(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	assertAlertHistoryProjectFilters(t, store)
}

func assertAlertHistoryProjectFilters(t *testing.T, store *GormStore) {
	t.Helper()
	project := "history-project"
	counter := &QuotaCounter{TotalTokens: 20}
	if err := raiseQuotaAlerts(store.db, APIKey{ID: "history-key", ProjectID: project, Limits: QuotaLimits{DailyTokens: 10}}, counter, counter); err != nil {
		t.Fatal(err)
	}
	if err := raiseUserQuotaAlerts(store.db, project, counter, counter, QuotaLimits{DailyTokens: 10}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"team", "project"} {
		budget := AdminResource{ID: "budget-" + scope, Kind: "budgets", Fields: map[string]any{"scope": scope, "scope_id": project}}
		if err := store.db.Create(&budget).Error; err != nil {
			t.Fatal(err)
		}
	}
	alerts := []AlertEvent{
		{ID: "literal", ScopeType: "project", ScopeID: project, Code: "cost_80%", Message: "historical warning", CreatedAt: time.Unix(1, 0).UTC()},
		{ID: "collision", ScopeType: "provider", ScopeID: project, ResourceID: project, Code: "costA80X"},
		{ID: "another-project", ScopeType: "api_key", ScopeID: "other-key", ResourceID: "other-project", Code: "costA80X"},
		{ID: "team-cost", ScopeType: "budget", ScopeID: "budget-team", ResourceID: project, Code: "budget_warn_threshold"},
		{ID: "project-cost", ScopeType: "budget", ScopeID: "budget-project", ResourceID: project, Code: "budget_warn_threshold"},
	}
	if err := store.db.Create(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, test := range []struct {
		query string
		total int64
	}{
		{"project_id=history-project", 4},
		{"project_id=history-project&q=budget_warn", 1},
		{"project_id=history-project&q=DAILY_TOKENS", 2},
		{"q=cost_80%25", 1},
		{"q=HISTORICAL+WARNING", 1},
		{"q=another-project", 1},
	} {
		resp := doJSON(t, app, http.MethodGet, "/api/admin/alerts?page_size=1&"+test.query, nil, "")
		var page AlertPage
		if err := json.Unmarshal([]byte(resp.Body), &page); err != nil || resp.Code != 200 || page.Total != test.total || len(page.Data) != 1 {
			t.Errorf("%s: want total %d, got %s", test.query, test.total, resp.Body)
		}
	}
}

func TestOverviewDoesNotCountAlertHistory(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	app := New(store).Handler()
	var countQueries int
	const callback = "test_overview_alert_count"
	if err := store.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "alert_events" && strings.Contains(strings.ToUpper(tx.Statement.SQL.String()), "COUNT(") {
			countQueries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.db.Callback().Query().Remove(callback); err != nil {
			t.Error(err)
		}
	})
	response := doJSON(t, app, http.MethodGet, "/api/admin/overview", nil, "")
	if response.Code != 200 || countQueries != 0 {
		t.Fatalf("status=%d alert_count_queries=%d", response.Code, countQueries)
	}
}

func TestOverviewKeepsMetadataWhenSummaryDeadlineExpires(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Create(&Project{ID: "deadline_project", Name: "Deadline project", Status: StatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(blockedOverviewSummaryStore{Store: store}).Handler()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil).WithContext(ctx)
	request.Header.Set("authorization", "Bearer dev_admin_token")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"error"`) || !strings.Contains(response.Body.String(), "deadline_project") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type blockedOverviewSummaryStore struct{ Store }

func (blockedOverviewSummaryStore) QueryOverviewSummary(ctx context.Context, _ UsageSummaryQuery) (UsageSummary, OverviewProgress, error) {
	<-ctx.Done()
	return UsageSummary{}, OverviewProgress{}, ctx.Err()
}
