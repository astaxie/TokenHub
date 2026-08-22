package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestAdminRequestLogsDefaultsToTwentyItems(t *testing.T) {
	store := NewMemoryStore()
	createdAt := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	for index := 1; index <= 25; index++ {
		statusCode := http.StatusOK
		if index%5 == 0 {
			statusCode = http.StatusBadGateway
		}
		if err := store.db.Create(&RequestLog{
			ID:         fmt.Sprintf("log_%02d", index),
			RequestID:  fmt.Sprintf("req_%02d", index),
			ProjectID:  "project_audit_page",
			APIKeyID:   "key_audit_page",
			ModelName:  "gpt-audit",
			StatusCode: statusCode,
			LatencyMS:  int64(100 + index),
			CreatedAt:  createdAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.Create(&UsageRecord{
		ID:          "usage_audit_page",
		RequestID:   "req_25",
		ProjectID:   "project_audit_page",
		APIKeyID:    "key_audit_page",
		TotalTokens: 42,
		CostUSD:     0.0042,
		CreatedAt:   createdAt,
	}).Error; err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, New(store).Handler(), http.MethodGet, "/api/admin/audit/requests", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected request logs 200, got %d: %s", response.Code, response.Body)
	}
	var payload struct {
		Data       []RequestLog         `json:"data"`
		Pagination RequestLogPagination `json:"pagination"`
		Summary    RequestLogSummary    `json:"summary"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 20 {
		t.Fatalf("expected default page size 20, got %d", len(payload.Data))
	}
	if payload.Pagination.Page != 1 || payload.Pagination.PageSize != 20 || payload.Pagination.Total != 25 || payload.Pagination.TotalPages != 2 {
		t.Fatalf("unexpected pagination: %+v", payload.Pagination)
	}
	if payload.Summary.All != 25 || payload.Summary.OK != 20 || payload.Summary.Error != 5 || payload.Summary.AverageLatencyMS != 113 {
		t.Fatalf("unexpected summary: %+v", payload.Summary)
	}
	if payload.Data[0].ID != "log_25" || payload.Data[19].ID != "log_06" {
		t.Fatalf("expected stable created_at/id order, got first=%s last=%s", payload.Data[0].ID, payload.Data[19].ID)
	}
	if payload.Data[0].UsageRecordCount != 1 || payload.Data[0].TotalTokens != 42 || payload.Data[0].EstimatedCostUSD != 0.0042 {
		t.Fatalf("expected current-page usage enrichment, got %+v", payload.Data[0])
	}
	secondResponse := doJSON(t, New(store).Handler(), http.MethodGet, "/api/admin/audit/requests?page=2", nil, "")
	var secondPage RequestLogPage
	if err := json.Unmarshal([]byte(secondResponse.Body), &secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Data) != 5 || secondPage.Data[0].ID != "log_05" || secondPage.Data[4].ID != "log_01" {
		t.Fatalf("expected stable non-overlapping second page, got %+v", secondPage.Data)
	}
}

func TestAdminRequestLogsFiltersStatusBeforePagination(t *testing.T) {
	store := NewMemoryStore()
	createdAt := time.Date(2026, time.August, 5, 11, 0, 0, 0, time.UTC)
	for index := 1; index <= 31; index++ {
		statusCode := http.StatusOK
		if index > 25 {
			statusCode = http.StatusBadGateway
		}
		if err := store.db.Create(&RequestLog{
			ID:         fmt.Sprintf("status_log_%02d", index),
			RequestID:  fmt.Sprintf("status_req_%02d", index),
			ProjectID:  "project_status_page",
			APIKeyID:   "key_status_page",
			ModelName:  "gpt-status",
			StatusCode: statusCode,
			LatencyMS:  int64(index),
			CreatedAt:  createdAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	response := doJSON(t, New(store).Handler(), http.MethodGet, "/api/admin/audit/requests?status=error&page=1&page_size=5", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected filtered request logs 200, got %d: %s", response.Code, response.Body)
	}
	var payload struct {
		Data       []RequestLog         `json:"data"`
		Pagination RequestLogPagination `json:"pagination"`
		Summary    RequestLogSummary    `json:"summary"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 5 {
		t.Fatalf("expected five error logs on the first page, got %d", len(payload.Data))
	}
	for _, log := range payload.Data {
		if log.StatusCode < http.StatusBadRequest {
			t.Fatalf("status filter returned successful request: %+v", log)
		}
	}
	if payload.Pagination.Total != 6 || payload.Pagination.TotalPages != 2 {
		t.Fatalf("status filter must run before pagination: %+v", payload.Pagination)
	}
	if payload.Summary.All != 31 || payload.Summary.OK != 25 || payload.Summary.Error != 6 {
		t.Fatalf("summary should retain status-tab counts: %+v", payload.Summary)
	}
}

func TestAdminRequestLogsFiltersExactModelAndInclusiveTimeRange(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, time.August, 16, 1, 0, 0, 0, time.UTC)
	logs := []RequestLog{
		{ID: "log_before", RequestID: "req_before", ModelName: "gpt-4", StatusCode: http.StatusOK, LatencyMS: 100, CreatedAt: base.Add(-time.Second)},
		{ID: "log_since", RequestID: "req_since", ModelName: "gpt-4", StatusCode: http.StatusBadGateway, LatencyMS: 200, CreatedAt: base},
		{ID: "log_similar", RequestID: "req_similar", ModelName: "gpt-4o", StatusCode: http.StatusOK, LatencyMS: 300, CreatedAt: base.Add(30 * time.Minute)},
		{ID: "log_until", RequestID: "req_until", ModelName: "gpt-4", StatusCode: http.StatusOK, LatencyMS: 400, CreatedAt: base.Add(time.Hour)},
	}
	for index := range logs {
		if err := store.db.Create(&logs[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	path := "/api/admin/audit/requests?model=gpt-4&since=" + url.QueryEscape(base.Format(time.RFC3339Nano)) + "&until=" + url.QueryEscape(base.Add(time.Hour).Format(time.RFC3339Nano))
	response := doJSON(t, New(store).Handler(), http.MethodGet, path, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected filtered request logs 200, got %d: %s", response.Code, response.Body)
	}
	var payload RequestLogPage
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 2 || payload.Data[0].ID != "log_until" || payload.Data[1].ID != "log_since" {
		t.Fatalf("expected inclusive exact-model results, got %+v", payload.Data)
	}
	if payload.Pagination.Total != 2 || payload.Summary.All != 2 || payload.Summary.OK != 1 || payload.Summary.Error != 1 || payload.Summary.AverageLatencyMS != 300 {
		t.Fatalf("filters must apply to page, pagination, and summary: pagination=%+v summary=%+v", payload.Pagination, payload.Summary)
	}
	for _, invalidPath := range []string{
		"/api/admin/audit/requests?since=not-a-timestamp",
		"/api/admin/audit/requests?since=2026-08-16T02:00:00Z&until=2026-08-16T01:00:00Z",
	} {
		invalid := doJSON(t, New(store).Handler(), http.MethodGet, invalidPath, nil, "")
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid range %q to return 400, got %d: %s", invalidPath, invalid.Code, invalid.Body)
		}
	}
}

func TestAdminRequestLogsSearchesExistingAuditFields(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "project_search", Name: "Alpha Finance"})
	provider := store.AddProvider(Provider{ID: "provider_search", Name: "Jade Channel", Type: "openai_compatible"})
	createdAt := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	if err := store.db.Create(&RequestLog{
		ID:                 "log_search_match",
		RequestID:          "req-needle-id",
		ProjectID:          project.ID,
		APIKeyID:           "key-needle-id",
		ModelName:          "gpt-needle-model",
		ProviderID:         provider.ID,
		ProviderResourceID: "resource-needle-id",
		ProviderModel:      "upstream-needle-model",
		StatusCode:         http.StatusTooManyRequests,
		ErrorCode:          "rate-limit-needle",
		CreatedAt:          createdAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{
		ID:         "log_search_noise",
		RequestID:  "req-unrelated",
		ProjectID:  "project_unrelated",
		APIKeyID:   "key_unrelated",
		ModelName:  "gpt-unrelated",
		StatusCode: http.StatusOK,
		CreatedAt:  createdAt.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	keywords := []string{
		"REQ-NEEDLE-ID",
		"Alpha Finance",
		"jade channel",
		"key-needle-id",
		"gpt-needle-model",
		"provider_search",
		"resource-needle-id",
		"upstream-needle-model",
		"rate-limit-needle",
		"429",
	}
	for _, keyword := range keywords {
		t.Run(keyword, func(t *testing.T) {
			response := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests?q="+url.QueryEscape(keyword), nil, "")
			if response.Code != http.StatusOK {
				t.Fatalf("expected search 200, got %d: %s", response.Code, response.Body)
			}
			var payload struct {
				Data       []RequestLog         `json:"data"`
				Pagination RequestLogPagination `json:"pagination"`
				Summary    RequestLogSummary    `json:"summary"`
			}
			if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Data) != 1 || payload.Data[0].ID != "log_search_match" {
				t.Fatalf("keyword %q returned unexpected data: %+v", keyword, payload.Data)
			}
			if payload.Pagination.Total != 1 || payload.Summary.All != 1 || payload.Summary.Error != 1 {
				t.Fatalf("keyword %q returned unexpected metadata: pagination=%+v summary=%+v", keyword, payload.Pagination, payload.Summary)
			}
		})
	}
	for _, literal := range []string{"%", "!"} {
		wildcard := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests?q="+url.QueryEscape(literal), nil, "")
		var wildcardPayload RequestLogPage
		if err := json.Unmarshal([]byte(wildcard.Body), &wildcardPayload); err != nil {
			t.Fatal(err)
		}
		if len(wildcardPayload.Data) != 0 || wildcardPayload.Summary.All != 0 {
			t.Fatalf("LIKE wildcard %q must be searched literally: %+v", literal, wildcardPayload)
		}
	}
}

func TestAdminRequestLogsValidatesAndCapsPagination(t *testing.T) {
	app := New(NewMemoryStore()).Handler()
	for _, path := range []string{
		"/api/admin/audit/requests?page=0",
		"/api/admin/audit/requests?page_size=-1",
		"/api/admin/audit/requests?page=9223372036854775807&page_size=100",
		"/api/admin/audit/requests?status=pending",
	} {
		response := doJSON(t, app, http.MethodGet, path, nil, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected %s to return 400, got %d: %s", path, response.Code, response.Body)
		}
	}
	response := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests?page_size=1000", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected capped page size 200, got %d: %s", response.Code, response.Body)
	}
	var payload RequestLogPage
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Pagination.PageSize != maxRequestLogPageSize {
		t.Fatalf("page size = %d, want cap %d", payload.Pagination.PageSize, maxRequestLogPageSize)
	}
}

func TestRequestLogPaginationIndexesCoverVisibleScopes(t *testing.T) {
	store := NewMemoryStore()
	tests := []struct {
		name    string
		columns []string
	}{
		{name: "idx_request_logs_created_at", columns: []string{"created_at"}},
		{name: "idx_request_logs_project_created", columns: []string{"project_id", "created_at"}},
		{name: "idx_request_logs_api_key_created", columns: []string{"api_key_id", "created_at"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var owner struct {
				TableName string
			}
			if err := store.db.Raw(
				"SELECT tbl_name AS table_name FROM sqlite_master WHERE type = 'index' AND name = ?",
				test.name,
			).Scan(&owner).Error; err != nil {
				t.Fatal(err)
			}
			if owner.TableName != "request_logs" {
				t.Fatalf("index %s belongs to %q, want request_logs", test.name, owner.TableName)
			}
			var rows []struct {
				Seq  int
				Name string
			}
			if err := store.db.Raw("PRAGMA index_info('" + test.name + "')").Scan(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(test.columns) {
				t.Fatalf("index %s columns = %+v, want %+v", test.name, rows, test.columns)
			}
			for index, column := range test.columns {
				if rows[index].Name != column {
					t.Fatalf("index %s column %d = %s, want %s", test.name, index, rows[index].Name, column)
				}
			}
		})
	}
}

func TestUserRequestLogScopeRunsBeforePagination(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "request-scope-user",
		Email:    "request-scope-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := store.CreateAdminUser(AdminUser{
		Username: "request-scope-other",
		Email:    "request-scope-other@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "other123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Shared Request Scope", OwnerUserID: otherUser.ID})
	store.CreateResource("project-members", AdminResource{
		Name:   "Request Scope Viewer",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": project.ID,
			"user_id":    user.ID,
			"role":       "viewer",
		},
	})
	ownKey, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:     "request-scope-own-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": user.ID},
	}, "thk_request_scope_own")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:     "request-scope-other-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": otherUser.ID},
	}, "thk_request_scope_other")
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.August, 5, 13, 0, 0, 0, time.UTC)
	if err := store.db.Create(&RequestLog{
		ID: "log_scope_own", RequestID: "req_scope_own", ProjectID: project.ID, APIKeyID: ownKey.ID,
		StatusCode: http.StatusOK, CreatedAt: createdAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 25; index++ {
		if err := store.db.Create(&RequestLog{
			ID:         fmt.Sprintf("log_scope_other_%02d", index),
			RequestID:  fmt.Sprintf("req_scope_other_%02d", index),
			ProjectID:  project.ID,
			APIKeyID:   otherKey.ID,
			StatusCode: http.StatusOK,
			CreatedAt:  createdAt.Add(time.Duration(index) * time.Second),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []UsageRecord{
		{ID: "usage_scope_own", RequestID: "req_scope_own", ProjectID: project.ID, APIKeyID: ownKey.ID, TotalTokens: 42, CostUSD: 0.0042, CreatedAt: createdAt},
		{ID: "usage_scope_hidden_collision", RequestID: "req_scope_own", ProjectID: project.ID, APIKeyID: otherKey.ID, TotalTokens: 999, CostUSD: 9.99, CreatedAt: createdAt},
	} {
		if err := store.db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	app := New(store).Handler()
	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Email,
		"password": "user123456",
	}, "")
	var credentials struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &credentials); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests", nil, credentials.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("expected scoped request logs 200, got %d: %s", response.Code, response.Body)
	}
	var payload struct {
		Data       []RequestLog         `json:"data"`
		Pagination RequestLogPagination `json:"pagination"`
		Summary    RequestLogSummary    `json:"summary"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 || payload.Data[0].RequestID != "req_scope_own" {
		t.Fatalf("user scope leaked another key's log from the same project: %+v", payload.Data)
	}
	if payload.Data[0].TotalTokens != 42 || payload.Data[0].EstimatedCostUSD != 0.0042 || payload.Data[0].UsageRecordCount != 1 {
		t.Fatalf("user scope leaked colliding hidden usage into the visible log: %+v", payload.Data[0])
	}
	if payload.Pagination.Total != 1 || payload.Summary.All != 1 {
		t.Fatalf("hidden logs must not participate in metadata: pagination=%+v summary=%+v", payload.Pagination, payload.Summary)
	}
}
