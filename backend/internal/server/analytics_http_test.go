package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestProjectAnalyticsCredentialQueriesOnlyScopedTokenCosts(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Agent Analytics Project", Status: StatusActive})
	otherProject := store.CreateProject(Project{Name: "Hidden Analytics Project", Status: StatusActive})
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/analytics/credentials", map[string]any{
		"name":       "local-cost-agent",
		"scope_type": "project",
		"project_id": project.ID,
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create analytics credential status = %d, want 201: %s", created.Code, created.Body)
	}
	var credentialPayload struct {
		Credential AnalyticsCredential `json:"credential"`
		Token      string              `json:"token"`
	}
	if err := json.Unmarshal([]byte(created.Body), &credentialPayload); err != nil {
		t.Fatalf("decode analytics credential: %v", err)
	}
	if !strings.HasPrefix(credentialPayload.Token, "tha_") {
		t.Fatalf("analytics token = %q, want tha_ prefix", credentialPayload.Token)
	}
	if credentialPayload.Credential.ScopeType != AnalyticsScopeProject || credentialPayload.Credential.ProjectID != project.ID {
		t.Fatalf("analytics credential scope = %#v, want project %q", credentialPayload.Credential, project.ID)
	}
	if strings.Contains(created.Body, "key_hash") {
		t.Fatalf("credential response exposed key hash: %s", created.Body)
	}

	now := time.Now().UTC().Truncate(time.Second)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_agent_success", RequestID: "req_agent_success", ProjectID: project.ID,
		APIKeyID: "key_agent", ModelName: "gpt-agent", ProviderID: "provider-agent",
		StatusCode: http.StatusOK, CreatedAt: now.Add(-2 * time.Minute),
	}, &UsageRecord{
		ID: "use_agent_success", RequestID: "req_agent_success", ProjectID: project.ID,
		APIKeyID: "key_agent", AttributedUserID: "user_agent", ModelName: "gpt-agent",
		ProviderID: "provider-agent", InputTokens: 100, CachedInputTokens: 40,
		OutputTokens: 20, TotalTokens: 120, CostUSD: 0.25, CreatedAt: now.Add(-2 * time.Minute),
	})
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_agent_error", RequestID: "req_agent_error", ProjectID: project.ID,
		APIKeyID: "key_agent", ModelName: "gpt-agent", ProviderID: "provider-agent",
		StatusCode: http.StatusBadGateway, ErrorCode: "upstream_error", CreatedAt: now.Add(-time.Minute),
	}, nil)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_hidden", RequestID: "req_hidden", ProjectID: otherProject.ID,
		APIKeyID: "key_hidden", ModelName: "gpt-hidden", ProviderID: "provider-hidden",
		StatusCode: http.StatusOK, CreatedAt: now.Add(-time.Minute),
	}, &UsageRecord{
		ID: "use_hidden", RequestID: "req_hidden", ProjectID: otherProject.ID,
		APIKeyID: "key_hidden", AttributedUserID: "user_hidden", ModelName: "gpt-hidden",
		ProviderID: "provider-hidden", InputTokens: 999, OutputTokens: 999,
		TotalTokens: 1998, CostUSD: 99, CreatedAt: now.Add(-time.Minute),
	})

	endpoint := "/api/v1/analytics/token-costs?from=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)) +
		"&to=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))
	response := doJSON(t, app, http.MethodGet, endpoint, nil, credentialPayload.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("query token costs status = %d, want 200: %s", response.Code, response.Body)
	}
	var payload TokenCostResponse
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("decode token cost response: %v", err)
	}
	if payload.SchemaVersion != TokenCostSchemaVersion {
		t.Fatalf("schema version = %q, want %q", payload.SchemaVersion, TokenCostSchemaVersion)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("token cost row count = %d, want 2: %s", len(payload.Data), response.Body)
	}
	if payload.Data[0].ProjectID != project.ID || payload.Data[0].RequestID != "req_agent_success" {
		t.Fatalf("first token cost row = %#v", payload.Data[0])
	}
	if payload.Data[0].Metrics.InputTokens != 100 || payload.Data[0].Metrics.CachedInputTokens != 40 ||
		payload.Data[0].Metrics.OutputTokens != 20 || payload.Data[0].Metrics.RequestCount != 1 ||
		payload.Data[0].Metrics.ErrorCount != 0 || payload.Data[0].Metrics.EstimatedCostUSD != 0.25 {
		t.Fatalf("success metrics = %#v", payload.Data[0].Metrics)
	}
	if payload.Data[1].RequestID != "req_agent_error" || payload.Data[1].Status != TokenCostStatusError ||
		payload.Data[1].Metrics.RequestCount != 1 || payload.Data[1].Metrics.ErrorCount != 1 {
		t.Fatalf("error token cost row = %#v", payload.Data[1])
	}
	if strings.Contains(response.Body, otherProject.ID) || strings.Contains(response.Body, "req_hidden") ||
		strings.Contains(response.Body, "provider_cost_usd") || strings.Contains(response.Body, credentialPayload.Token) {
		t.Fatalf("token cost response exposed out-of-scope or sensitive data: %s", response.Body)
	}
	if payload.Watermark == "" {
		t.Fatalf("token cost response omitted watermark: %s", response.Body)
	}

	var queryAudit *AuditEvent
	for _, event := range store.ListAuditEvents() {
		if event.Action == "query" && event.ResourceType == "token_cost_analytics" {
			copy := event
			queryAudit = &copy
		}
	}
	if queryAudit == nil || queryAudit.ActorUserID != credentialPayload.Credential.ID || queryAudit.Status != "success" {
		t.Fatalf("analytics query audit = %#v", queryAudit)
	}
	if strings.Contains(queryAudit.AfterSnapshot, credentialPayload.Token) {
		t.Fatalf("analytics audit exposed credential token: %#v", queryAudit)
	}
}

func TestOrganizationAnalyticsCredentialFiltersAndAggregatesTokenCosts(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Aggregate Analytics Project", Status: StatusActive})
	otherProject := store.CreateProject(Project{Name: "Other Aggregate Project", Status: StatusActive})
	app := New(store).Handler()
	token := createAnalyticsCredentialToken(t, app, map[string]any{
		"name": "organization-cost-agent", "scope_type": AnalyticsScopeOrganization,
	})

	day := time.Date(2026, time.July, 4, 0, 0, 0, 0, time.UTC)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_aggregate_a", RequestID: "req_aggregate_a", ProjectID: project.ID,
		APIKeyID: "key_aggregate", ModelName: "gpt-aggregate", ProviderID: "provider-aggregate",
		StatusCode: http.StatusOK, CreatedAt: day.Add(10 * time.Minute),
	}, &UsageRecord{
		ID: "use_aggregate_a", RequestID: "req_aggregate_a", ProjectID: project.ID,
		APIKeyID: "key_aggregate", AttributedUserID: "user_aggregate", ModelName: "gpt-aggregate",
		ProviderID: "provider-aggregate", InputTokens: 10, CachedInputTokens: 4,
		CacheWriteTokens: 2, OutputTokens: 5, ReasoningTokens: 1, TotalTokens: 15,
		CostUSD: 0.1, CreatedAt: day.Add(10 * time.Minute),
	})
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_aggregate_b", RequestID: "req_aggregate_b", ProjectID: project.ID,
		APIKeyID: "key_aggregate", ModelName: "gpt-aggregate", ProviderID: "provider-aggregate",
		StatusCode: http.StatusOK, CreatedAt: day.Add(20 * time.Minute),
	}, &UsageRecord{
		ID: "use_aggregate_b", RequestID: "req_aggregate_b", ProjectID: project.ID,
		APIKeyID: "key_aggregate", AttributedUserID: "user_aggregate", ModelName: "gpt-aggregate",
		ProviderID: "provider-aggregate", InputTokens: 20, CachedInputTokens: 6,
		CacheWriteTokens: 3, OutputTokens: 7, ReasoningTokens: 2, TotalTokens: 27,
		CostUSD: 0.2, CreatedAt: day.Add(20 * time.Minute),
	})
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_aggregate_error", RequestID: "req_aggregate_error", ProjectID: project.ID,
		APIKeyID: "key_aggregate", ModelName: "gpt-aggregate", ProviderID: "provider-aggregate",
		StatusCode: http.StatusBadGateway, CreatedAt: day.Add(30 * time.Minute),
	}, nil)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_aggregate_other", RequestID: "req_aggregate_other", ProjectID: otherProject.ID,
		APIKeyID: "key_other", ModelName: "gpt-other", ProviderID: "provider-other",
		StatusCode: http.StatusOK, CreatedAt: day.Add(40 * time.Minute),
	}, &UsageRecord{
		ID: "use_aggregate_other", RequestID: "req_aggregate_other", ProjectID: otherProject.ID,
		APIKeyID: "key_other", AttributedUserID: "user_other", ModelName: "gpt-other",
		ProviderID: "provider-other", InputTokens: 1000, OutputTokens: 1000, TotalTokens: 2000,
		CostUSD: 10, CreatedAt: day.Add(40 * time.Minute),
	})

	query := url.Values{
		"from":        {day.Format(time.RFC3339)},
		"to":          {day.Add(24 * time.Hour).Format(time.RFC3339)},
		"project_id":  {project.ID},
		"user_id":     {"user_aggregate"},
		"api_key_id":  {"key_aggregate"},
		"provider_id": {"provider-aggregate"},
		"model":       {"gpt-aggregate"},
		"status":      {TokenCostStatusSuccess},
		"granularity": {"day"},
		"group_by":    {"project,provider,model,status"},
	}
	response := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?"+query.Encode(), nil, token)
	if response.Code != http.StatusOK {
		t.Fatalf("aggregate token costs status = %d, want 200: %s", response.Code, response.Body)
	}
	var payload TokenCostResponse
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("decode aggregate token costs: %v", err)
	}
	if payload.Query.Granularity != "day" || strings.Join(payload.Query.GroupBy, ",") != "project,provider,model,status" {
		t.Fatalf("aggregate query metadata = %#v", payload.Query)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("aggregate row count = %d, want 1: %s", len(payload.Data), response.Body)
	}
	row := payload.Data[0]
	if row.Bucket != "2026-07-04" || row.ProjectID != project.ID || row.ProviderID != "provider-aggregate" ||
		row.Model != "gpt-aggregate" || row.Status != TokenCostStatusSuccess {
		t.Fatalf("aggregate dimensions = %#v", row)
	}
	if row.Metrics.RequestCount != 2 || row.Metrics.ErrorCount != 0 || row.Metrics.InputTokens != 30 ||
		row.Metrics.CachedInputTokens != 10 || row.Metrics.CacheWriteTokens != 5 ||
		row.Metrics.OutputTokens != 12 || row.Metrics.ReasoningTokens != 3 || row.Metrics.TotalTokens != 42 ||
		math.Abs(row.Metrics.EstimatedCostUSD-0.3) > 1e-12 {
		t.Fatalf("aggregate metrics = %#v", row.Metrics)
	}
	if strings.Contains(response.Body, "req_aggregate_error") || strings.Contains(response.Body, otherProject.ID) {
		t.Fatalf("aggregate response ignored filters: %s", response.Body)
	}
}

func TestTokenCostCursorPaginatesSnapshotAndWatermarkSupportsIncrementalPull(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Cursor Analytics Project", Status: StatusActive})
	app := New(store).Handler()
	token := createAnalyticsCredentialToken(t, app, map[string]any{
		"name": "cursor-cost-agent", "scope_type": AnalyticsScopeProject, "project_id": project.ID,
	})

	now := time.Now().UTC().Truncate(time.Second)
	for index, requestID := range []string{"req_cursor_a", "req_cursor_b", "req_cursor_c"} {
		occurredAt := now.Add(time.Duration(index-10) * time.Minute)
		createAnalyticsRequest(t, store, RequestLog{
			ID: "log_" + requestID, RequestID: requestID, ProjectID: project.ID,
			APIKeyID: "key_cursor", ModelName: "gpt-cursor", ProviderID: "provider-cursor",
			StatusCode: http.StatusOK, CreatedAt: occurredAt,
		}, &UsageRecord{
			ID: "use_" + requestID, RequestID: requestID, ProjectID: project.ID,
			APIKeyID: "key_cursor", ModelName: "gpt-cursor", ProviderID: "provider-cursor",
			InputTokens: int64(index + 1), TotalTokens: int64(index + 1), CreatedAt: occurredAt,
		})
	}
	from := now.Add(-time.Hour).Format(time.RFC3339)
	to := now.Add(-time.Minute).Format(time.RFC3339)
	firstQuery := url.Values{"from": {from}, "to": {to}, "limit": {"1"}}
	firstResponse := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?"+firstQuery.Encode(), nil, token)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first cursor page status = %d, want 200: %s", firstResponse.Code, firstResponse.Body)
	}
	var first TokenCostResponse
	if err := json.Unmarshal([]byte(firstResponse.Body), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.Data[0].RequestID != "req_cursor_a" || !first.HasMore || first.NextCursor == "" || first.Watermark == "" {
		t.Fatalf("first cursor page = %#v", first)
	}

	secondQuery := url.Values{"cursor": {first.NextCursor}, "limit": {"1"}}
	secondResponse := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?"+secondQuery.Encode(), nil, token)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("second cursor page status = %d, want 200: %s", secondResponse.Code, secondResponse.Body)
	}
	var second TokenCostResponse
	if err := json.Unmarshal([]byte(secondResponse.Body), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].RequestID != "req_cursor_b" || !second.HasMore || second.NextCursor == first.NextCursor {
		t.Fatalf("second cursor page = %#v", second)
	}

	newTime := now.Add(time.Minute)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_req_cursor_new", RequestID: "req_cursor_new", ProjectID: project.ID,
		APIKeyID: "key_cursor", ModelName: "gpt-cursor", ProviderID: "provider-cursor",
		StatusCode: http.StatusOK, CreatedAt: newTime,
	}, &UsageRecord{
		ID: "use_req_cursor_new", RequestID: "req_cursor_new", ProjectID: project.ID,
		APIKeyID: "key_cursor", ModelName: "gpt-cursor", ProviderID: "provider-cursor",
		InputTokens: 4, TotalTokens: 4, CreatedAt: newTime,
	})
	incrementalQuery := url.Values{
		"after": {first.Watermark}, "to": {now.Add(2 * time.Minute).Format(time.RFC3339)},
	}
	incrementalResponse := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?"+incrementalQuery.Encode(), nil, token)
	if incrementalResponse.Code != http.StatusOK {
		t.Fatalf("incremental token cost pull status = %d, want 200: %s", incrementalResponse.Code, incrementalResponse.Body)
	}
	var incremental TokenCostResponse
	if err := json.Unmarshal([]byte(incrementalResponse.Body), &incremental); err != nil {
		t.Fatal(err)
	}
	foundNew := false
	for _, row := range incremental.Data {
		foundNew = foundNew || row.RequestID == "req_cursor_new"
	}
	if incremental.Query.IncrementalMode != TokenCostIncrementalChanges || !foundNew || incremental.Watermark == first.Watermark {
		t.Fatalf("incremental token cost pull = %#v", incremental)
	}
}

func TestTokenCostWatermarkPreservesOriginalFiltersAndAggregation(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Watermark Query Project", Status: StatusActive})
	app := New(store).Handler()
	token := createAnalyticsCredentialToken(t, app, map[string]any{
		"name": "watermark-query-agent", "scope_type": AnalyticsScopeOrganization,
	})
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_watermark_initial", RequestID: "req_watermark_initial", ProjectID: project.ID,
		ProviderID: "provider-watermark-a", ModelName: "gpt-watermark", StatusCode: http.StatusOK, CreatedAt: base,
	}, &UsageRecord{
		ID: "use_watermark_initial", RequestID: "req_watermark_initial", ProjectID: project.ID,
		ProviderID: "provider-watermark-a", ModelName: "gpt-watermark", InputTokens: 1, TotalTokens: 1, CreatedAt: base,
	})
	initialQuery := url.Values{
		"from":        {base.Add(-time.Minute).Format(time.RFC3339)},
		"to":          {base.Add(time.Minute).Format(time.RFC3339)},
		"provider_id": {"provider-watermark-a"},
		"granularity": {"day"},
		"group_by":    {"provider"},
	}
	initialResponse := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?"+initialQuery.Encode(), nil, token)
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("initial watermark query: %d %s", initialResponse.Code, initialResponse.Body)
	}
	var initial TokenCostResponse
	if err := json.Unmarshal([]byte(initialResponse.Body), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Watermark == "" {
		t.Fatalf("initial query omitted watermark: %s", initialResponse.Body)
	}

	for index, providerID := range []string{"provider-watermark-a", "provider-watermark-b"} {
		occurredAt := base.Add(time.Duration(index+2) * time.Minute)
		requestID := "req_watermark_new_" + strconv.Itoa(index)
		createAnalyticsRequest(t, store, RequestLog{
			ID: "log_" + requestID, RequestID: requestID, ProjectID: project.ID,
			ProviderID: providerID, ModelName: "gpt-watermark", StatusCode: http.StatusOK, CreatedAt: occurredAt,
		}, &UsageRecord{
			ID: "use_" + requestID, RequestID: requestID, ProjectID: project.ID,
			ProviderID: providerID, ModelName: "gpt-watermark", InputTokens: 2, TotalTokens: 2, CreatedAt: occurredAt,
		})
	}
	incremental := url.Values{
		"after": {initial.Watermark},
		"to":    {base.Add(10 * time.Minute).Format(time.RFC3339)},
	}
	incrementalResponse := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?"+incremental.Encode(), nil, token)
	if incrementalResponse.Code != http.StatusOK {
		t.Fatalf("incremental watermark query: %d %s", incrementalResponse.Code, incrementalResponse.Body)
	}
	var pulled TokenCostResponse
	if err := json.Unmarshal([]byte(incrementalResponse.Body), &pulled); err != nil {
		t.Fatal(err)
	}
	if pulled.Query.Granularity != "day" || strings.Join(pulled.Query.GroupBy, ",") != "provider" ||
		pulled.Query.Filters["provider_id"] != "provider-watermark-a" {
		t.Fatalf("incremental query lost original shape: %#v", pulled.Query)
	}
	if len(pulled.Data) != 1 || pulled.Data[0].ProviderID != "provider-watermark-a" ||
		pulled.Data[0].Metrics.RequestCount != 1 || pulled.Data[0].DedupeKey == initial.Data[0].DedupeKey ||
		pulled.Query.IncrementalMode != TokenCostIncrementalChanges {
		t.Fatalf("incremental query mixed unrelated records: %#v", pulled.Data)
	}
}

func TestTokenCostAnalyticsExportsCSVWithStableSchemaHeaders(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "CSV Analytics Project", Status: StatusActive})
	app := New(store).Handler()
	token := createAnalyticsCredentialToken(t, app, map[string]any{
		"name": "csv-cost-agent", "scope_type": AnalyticsScopeProject, "project_id": project.ID,
	})
	now := time.Now().UTC().Truncate(time.Second)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_csv", RequestID: "req_csv", ProjectID: project.ID, APIKeyID: "key_csv",
		ModelName: "gpt-csv", ProviderID: "provider-csv", StatusCode: http.StatusOK, CreatedAt: now,
	}, &UsageRecord{
		ID: "use_csv", RequestID: "req_csv", ProjectID: project.ID, APIKeyID: "key_csv",
		AttributedUserID: "user_csv", ModelName: "gpt-csv", ProviderID: "provider-csv",
		InputTokens: 11, CachedInputTokens: 3, CacheWriteTokens: 2, OutputTokens: 7,
		ReasoningTokens: 1, TotalTokens: 18, CostUSD: 0.125, CreatedAt: now,
	})

	query := url.Values{
		"from":   {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":     {now.Add(time.Hour).Format(time.RFC3339)},
		"format": {"csv"},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/token-costs?"+query.Encode(), nil)
	request.Header.Set("authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("CSV token costs status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("content-type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("CSV content type = %q", contentType)
	}
	if response.Header().Get("x-tokenhub-schema-version") != TokenCostSchemaVersion ||
		response.Header().Get("x-tokenhub-watermark") == "" ||
		response.Header().Get("x-tokenhub-checkpoint-by") != "commit_sequence" ||
		response.Header().Get("x-tokenhub-incremental-mode") != TokenCostIncrementalSnapshot {
		t.Fatalf("CSV schema headers = %#v", response.Header())
	}
	header := "dedupe_key,bucket,request_id,occurred_at,project_id,user_id,api_key_id,provider_id,model,status,status_code,request_count,error_count,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_output_tokens,total_tokens,estimated_cost_usd"
	if !strings.HasPrefix(response.Body.String(), header+"\n") || !strings.Contains(response.Body.String(), "req_csv") ||
		!strings.Contains(response.Body.String(), "0.125") || strings.Contains(response.Body.String(), "provider_cost_usd") {
		t.Fatalf("unexpected CSV token costs: %s", response.Body.String())
	}
}

func TestAnalyticsCredentialsAreReadOnlyScopedRevocableAndBounded(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Security Analytics Project", Status: StatusActive})
	otherProject := store.CreateProject(Project{Name: "Forbidden Analytics Project", Status: StatusActive})
	if _, err := store.CreateAdminUser(AdminUser{
		ID: "usr_analytics_admin", Username: "analytics-admin", Name: "Analytics Admin",
		Email: "analytics-admin@tokenhub.local", Role: "admin", Status: StatusActive,
	}, "analytics-admin-password"); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{
		Username: "analytics-user", Name: "Analytics User", Email: "analytics-user@tokenhub.local",
		Role: "user", Status: StatusActive,
	}, "analytics-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	forbiddenCreate := doJSON(t, app, http.MethodPost, "/api/admin/analytics/credentials", map[string]any{
		"name": "forbidden-agent", "scope_type": AnalyticsScopeProject, "project_id": project.ID,
	}, userSession.Token)
	if forbiddenCreate.Code != http.StatusForbidden {
		t.Fatalf("ordinary user created analytics credential: %d %s", forbiddenCreate.Code, forbiddenCreate.Body)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/analytics/credentials", map[string]any{
		"name": "security-cost-agent", "scope_type": AnalyticsScopeProject, "project_id": project.ID,
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create security analytics credential: %d %s", created.Code, created.Body)
	}
	var payload struct {
		Credential AnalyticsCredential `json:"credential"`
		Token      string              `json:"token"`
	}
	if err := json.Unmarshal([]byte(created.Body), &payload); err != nil {
		t.Fatal(err)
	}

	wrongProject := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?project_id="+url.QueryEscape(otherProject.ID), nil, payload.Token)
	if wrongProject.Code != http.StatusForbidden || !strings.Contains(wrongProject.Body, "analytics_scope_forbidden") {
		t.Fatalf("project credential escaped scope: %d %s", wrongProject.Code, wrongProject.Body)
	}
	gateway := doJSON(t, app, http.MethodGet, "/v1/models", nil, payload.Token)
	if gateway.Code != http.StatusUnauthorized {
		t.Fatalf("analytics credential authenticated to model gateway: %d %s", gateway.Code, gateway.Body)
	}
	tooLarge := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z", nil, payload.Token)
	if tooLarge.Code != http.StatusBadRequest || !strings.Contains(tooLarge.Body, "analytics_time_range_too_large") {
		t.Fatalf("unbounded request-level query was accepted: %d %s", tooLarge.Code, tooLarge.Body)
	}
	tooManyRows := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs?limit=1001", nil, payload.Token)
	if tooManyRows.Code != http.StatusBadRequest || !strings.Contains(tooManyRows.Body, "invalid_analytics_limit") {
		t.Fatalf("oversized analytics page was accepted: %d %s", tooManyRows.Code, tooManyRows.Body)
	}

	revoked := doJSON(t, app, http.MethodDelete, "/api/admin/analytics/credentials/"+payload.Credential.ID, nil, "")
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke analytics credential: %d %s", revoked.Code, revoked.Body)
	}
	afterRevoke := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs", nil, payload.Token)
	if afterRevoke.Code != http.StatusUnauthorized || !strings.Contains(afterRevoke.Body, "invalid_analytics_credential") {
		t.Fatalf("revoked analytics credential still worked: %d %s", afterRevoke.Code, afterRevoke.Body)
	}
	future := time.Now().UTC().Add(time.Hour)
	expiredCredential, expiredToken, err := store.CreateAnalyticsCredential(AnalyticsCredential{
		Name: "expired-cost-agent", ScopeType: AnalyticsScopeProject, ProjectID: project.ID,
		ExpiresAt: &future, CreatedBy: "usr_analytics_admin",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := store.db.Model(&AnalyticsCredential{}).Where("id = ?", expiredCredential.ID).Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	afterExpiry := doJSON(t, app, http.MethodGet, "/api/v1/analytics/token-costs", nil, expiredToken)
	if afterExpiry.Code != http.StatusUnauthorized || !strings.Contains(afterExpiry.Body, "analytics_credential_expired") {
		t.Fatalf("expired analytics credential still worked: %d %s", afterExpiry.Code, afterExpiry.Body)
	}
}

func TestTokenCostAnalyticsKeepsLargeQueriesIndexedAndPageBounded(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Large Analytics Project", Status: StatusActive})
	now := time.Now().UTC().Truncate(time.Second)
	logs := make([]RequestLog, 1200)
	for index := range logs {
		requestID := "req_large_" + strconv.Itoa(index)
		logs[index] = RequestLog{
			ID: "log_large_" + strconv.Itoa(index), RequestID: requestID, ProjectID: project.ID,
			APIKeyID: "key_large", ModelName: "gpt-large", ProviderID: "provider-large",
			StatusCode: http.StatusOK, CreatedAt: now.Add(time.Duration(index) * time.Millisecond),
		}
	}
	if err := store.db.CreateInBatches(logs, 200).Error; err != nil {
		t.Fatalf("seed large analytics dataset: %v", err)
	}

	rows, hasMore, err := store.QueryTokenCosts(t.Context(), TokenCostQuery{
		From: now.Add(-time.Minute), To: now.Add(time.Minute), ProjectID: project.ID,
		Granularity: "request", Limit: 37,
	})
	if err != nil {
		t.Fatalf("query large analytics dataset: %v", err)
	}
	if len(rows) != 37 || !hasMore {
		t.Fatalf("large analytics page returned %d rows, has_more=%v", len(rows), hasMore)
	}

	for table, expected := range map[string][]string{
		"request_logs": {"idx_request_logs_created_at", "idx_request_logs_project_created",
			"idx_request_logs_commit_sequence_v2", "idx_request_logs_project_commit_sequence"},
		"usage_records": {"idx_usage_records_created_at", "idx_usage_records_project_created"},
	} {
		var indexes []struct {
			Name string `gorm:"column:name"`
		}
		if err := store.db.Raw("PRAGMA index_list(" + table + ")").Scan(&indexes).Error; err != nil {
			t.Fatalf("list %s indexes: %v", table, err)
		}
		found := map[string]bool{}
		for _, index := range indexes {
			found[index.Name] = true
		}
		for _, name := range expected {
			if !found[name] {
				t.Fatalf("%s is missing analytics index %s; indexes=%v", table, name, found)
			}
		}
	}
	checkpoint, err := store.tokenCostGlobalCheckpoint(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	query := TokenCostQuery{
		From: now.Add(-365 * 24 * time.Hour), To: now.Add(time.Hour), ProjectID: project.ID,
		Granularity: "request", Limit: 37, AfterSequence: checkpoint,
		ThroughSequence: checkpoint, ThroughSequenceSet: true, Incremental: true,
	}
	var planRows []tokenCostDatabaseRow
	dryRun := store.tokenCostRequestQuery(t.Context(), query).
		Select("rl.request_id, rl.created_at AS occurred_at").
		Order("rl.created_at ASC, rl.request_id ASC").
		Limit(query.Limit + 1).
		Session(&gorm.Session{DryRun: true}).
		Find(&planRows)
	if dryRun.Error != nil {
		t.Fatal(dryRun.Error)
	}
	var plan []struct {
		Detail string `gorm:"column:detail"`
	}
	if err := store.db.Raw("EXPLAIN QUERY PLAN "+dryRun.Statement.SQL.String(), dryRun.Statement.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	usesProjectSequence := false
	for _, step := range plan {
		usesProjectSequence = usesProjectSequence || strings.Contains(step.Detail, "idx_request_logs_project_commit_sequence")
	}
	if !usesProjectSequence {
		t.Fatalf("empty project delta did not use project/sequence index: %#v", plan)
	}
}

func TestTokenCostAnalyticsUsesAnIsolatedReadPool(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Isolated Analytics Project", Status: StatusActive})
	analyticsSQL, err := store.analyticsDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := analyticsSQL.Conn(t.Context())
	if err != nil {
		t.Fatalf("reserve analytics connection: %v", err)
	}
	defer connection.Close()

	coreReadDone := make(chan bool, 1)
	go func() {
		_, ok := store.GetProject(project.ID)
		coreReadDone <- ok
	}()
	select {
	case ok := <-coreReadDone:
		if !ok {
			t.Fatal("core project read failed while analytics pool was busy")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("busy analytics pool blocked a core gateway database read")
	}
}

func TestTokenCostAggregateMatchesAdminUsageMetrics(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Parity Analytics Project", Status: StatusActive})
	now := time.Now().UTC().Truncate(time.Second)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_parity_success", RequestID: "req_parity_success", ProjectID: project.ID,
		APIKeyID: "key_parity", ModelName: "gpt-parity", ProviderID: "provider-parity",
		StatusCode: http.StatusOK, CreatedAt: now,
	}, &UsageRecord{
		ID: "use_parity_success", RequestID: "req_parity_success", ProjectID: project.ID,
		APIKeyID: "key_parity", ModelName: "gpt-parity", ProviderID: "provider-parity",
		InputTokens: 80, CachedInputTokens: 30, CacheWriteTokens: 5, OutputTokens: 20,
		ReasoningTokens: 4, TotalTokens: 100, CostUSD: 0.42, CreatedAt: now,
	})
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_parity_error", RequestID: "req_parity_error", ProjectID: project.ID,
		APIKeyID: "key_parity", ModelName: "gpt-parity", ProviderID: "provider-parity",
		StatusCode: http.StatusBadGateway, CreatedAt: now.Add(time.Second),
	}, nil)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_parity_playground", RequestID: "req_parity_playground", ProjectID: "admin_playground",
		ModelName: "gpt-parity", ProviderID: "provider-parity", StatusCode: http.StatusBadGateway,
		CreatedAt: now.Add(2 * time.Second),
	}, nil)

	rows, hasMore, err := store.QueryTokenCosts(t.Context(), TokenCostQuery{
		From: now.Add(-time.Minute), To: now.Add(time.Minute),
		Granularity: "none", Limit: 10,
	})
	if err != nil || hasMore || len(rows) != 1 {
		t.Fatalf("aggregate parity query rows=%#v has_more=%v err=%v", rows, hasMore, err)
	}
	summary := summarizeUsage(store.ListUsageRecords(), store.ListRequestLogs())
	metrics := rows[0].Metrics
	checks := map[string]float64{
		"request_count":            float64(metrics.RequestCount),
		"errors":                   float64(metrics.ErrorCount),
		"input_tokens":             float64(metrics.InputTokens),
		"cached_input_tokens":      float64(metrics.CachedInputTokens),
		"cache_write_input_tokens": float64(metrics.CacheWriteTokens),
		"output_tokens":            float64(metrics.OutputTokens),
		"reasoning_output_tokens":  float64(metrics.ReasoningTokens),
		"total_tokens":             float64(metrics.TotalTokens),
		"estimated_cost_usd":       metrics.EstimatedCostUSD,
	}
	for key, actual := range checks {
		expected := analyticsNumberForTest(summary[key])
		if math.Abs(actual-expected) > 1e-12 {
			t.Fatalf("analytics metric %s = %v, admin usage = %v", key, actual, expected)
		}
	}
}

func TestTokenCostAnalyticsSupportsHourDayAndMonthBuckets(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Bucket Analytics Project", Status: StatusActive})
	occurredAt := time.Date(2026, time.July, 4, 13, 25, 0, 0, time.UTC)
	createAnalyticsRequest(t, store, RequestLog{
		ID: "log_buckets", RequestID: "req_buckets", ProjectID: project.ID,
		StatusCode: http.StatusOK, CreatedAt: occurredAt,
	}, nil)
	for granularity, expected := range map[string]string{
		"hour":  "2026-07-04T13:00:00Z",
		"day":   "2026-07-04",
		"month": "2026-07",
	} {
		t.Run(granularity, func(t *testing.T) {
			rows, hasMore, err := store.QueryTokenCosts(t.Context(), TokenCostQuery{
				From: occurredAt.Add(-time.Hour), To: occurredAt.Add(time.Hour), ProjectID: project.ID,
				Granularity: granularity, Limit: 10,
			})
			if err != nil || hasMore || len(rows) != 1 || rows[0].Bucket != expected {
				t.Fatalf("%s bucket rows=%#v has_more=%v err=%v", granularity, rows, hasMore, err)
			}
		})
	}
}

func analyticsNumberForTest(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	default:
		return 0
	}
}

func createAnalyticsRequest(t *testing.T, store *GormStore, log RequestLog, usage *UsageRecord) {
	t.Helper()
	if err := store.db.Create(&log).Error; err != nil {
		t.Fatalf("create analytics request log: %v", err)
	}
	if usage != nil {
		if err := store.db.Create(usage).Error; err != nil {
			t.Fatalf("create analytics usage record: %v", err)
		}
	}
}

func createAnalyticsCredentialToken(t *testing.T, app http.Handler, request map[string]any) string {
	t.Helper()
	response := doJSON(t, app, http.MethodPost, "/api/admin/analytics/credentials", request, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create analytics credential status = %d, want 201: %s", response.Code, response.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("decode analytics credential token: %v", err)
	}
	return payload.Token
}
