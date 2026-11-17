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
