package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAdminAPIKeyUsageAndRequestFiltersPreserveKeyScope(t *testing.T) {
	store, _, app, adminToken, userToken, _, securityToken, _, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	key, ok := store.GetAPIKey(keyID)
	if !ok {
		t.Fatalf("test key %s not found", keyID)
	}
	otherProject := store.CreateProject(Project{Name: "Hidden Usage Project", Status: StatusActive})
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{Name: "Hidden Usage Key", Status: StatusActive}, "thk_hidden_usage")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.db.Create(&[]RequestLog{
		{ID: "log_visible_usage", RequestID: "req_visible_usage", ProjectID: key.ProjectID, APIKeyID: key.ID, ModelName: "gpt-visible", StatusCode: http.StatusOK, CreatedAt: now},
		{ID: "log_hidden_usage", RequestID: "req_hidden_usage", ProjectID: otherProject.ID, APIKeyID: otherKey.ID, ModelName: "gpt-hidden", StatusCode: http.StatusOK, CreatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	usage := doJSON(t, app, http.MethodGet, "/api/admin/api-keys/"+key.ID+"/usage", nil, userToken)
	if usage.Code != http.StatusOK || !containsJSONField(usage.Body, "request_count", float64(1)) {
		t.Fatalf("accessible key usage: status=%d body=%s", usage.Code, usage.Body)
	}
	if strings.Contains(usage.Body, `"providers"`) {
		t.Fatalf("non-admin key usage exposed provider breakdown: %s", usage.Body)
	}
	adminUsage := doJSON(t, app, http.MethodGet, "/api/admin/api-keys/"+key.ID+"/usage", nil, adminToken)
	if adminUsage.Code != http.StatusOK || !strings.Contains(adminUsage.Body, `"providers"`) {
		t.Fatalf("admin key usage omitted provider breakdown: status=%d body=%s", adminUsage.Code, adminUsage.Body)
	}
	hidden := doJSON(t, app, http.MethodGet, "/api/admin/api-keys/"+otherKey.ID+"/usage", nil, userToken)
	if hidden.Code != http.StatusForbidden {
		t.Fatalf("hidden key usage status = %d, want 403: %s", hidden.Code, hidden.Body)
	}
	security := doJSON(t, app, http.MethodGet, "/api/admin/api-keys/"+key.ID+"/usage", nil, securityToken)
	if security.Code != http.StatusForbidden {
		t.Fatalf("security key usage status = %d, want 403: %s", security.Code, security.Body)
	}
	missing := doJSON(t, app, http.MethodGet, "/api/admin/api-keys/missing/usage", nil, adminToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing key usage status = %d, want 404: %s", missing.Code, missing.Body)
	}

	logs := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests?api_key_id="+key.ID, nil, adminToken)
	if logs.Code != http.StatusOK {
		t.Fatalf("filtered request logs: status=%d body=%s", logs.Code, logs.Body)
	}
	var page RequestLogPage
	if err := json.Unmarshal([]byte(logs.Body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].APIKeyID != key.ID {
		t.Fatalf("request logs escaped key scope: %+v", page.Data)
	}
}

func containsJSONField(body, field string, want any) bool {
	var value map[string]any
	if json.Unmarshal([]byte(body), &value) != nil {
		return false
	}
	summary, _ := value["summary"].(map[string]any)
	return summary[field] == want
}

func TestQueryAPIKeyUsageScopesMetricsAndReadsEffectiveKeyQuota(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "API Key Usage Project", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "Observed Key", Status: StatusActive, OwnerUserID: "usr_key_usage_owner",
		Limits: QuotaLimits{DailyRequests: 10, MonthlyTokens: 1000},
	}, "thk_api_key_usage")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Other Key", Status: StatusActive}, "thk_other_api_key_usage")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		ID: "quota_key_usage", Name: "Key Usage Quota", Status: StatusActive,
		Fields: map[string]any{"scope": "api_key", "scope_id": key.ID, "daily_requests": 5},
	})
	store.CreateResource("quota-policies", AdminResource{
		ID: "quota_user_usage", Name: "Aggregate User Quota", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": key.OwnerUserID, "daily_requests": 1},
	})

	now := time.Now().UTC().Truncate(time.Second)
	logs := []RequestLog{
		{ID: "log_usage_ok", RequestID: "req_usage_ok", ProjectID: project.ID, APIKeyID: key.ID, ModelName: "gpt-test", ProviderID: "provider-a", ProviderResourceID: "resource-a", StatusCode: http.StatusOK, LatencyMS: 100, CreatedAt: now.Add(-time.Hour)},
		{ID: "log_usage_error", RequestID: "req_usage_error", ProjectID: project.ID, APIKeyID: key.ID, ModelName: "gpt-test", StatusCode: http.StatusBadGateway, ErrorCode: "upstream_failed", LatencyMS: 300, CreatedAt: now.Add(-30 * time.Minute)},
		{ID: "log_usage_other", RequestID: "req_usage_other", ProjectID: project.ID, APIKeyID: otherKey.ID, ModelName: "gpt-other", StatusCode: http.StatusOK, LatencyMS: 1, CreatedAt: now.Add(-time.Minute)},
	}
	if err := store.db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	usage := []UsageRecord{
		{ID: "usage_key_first", RequestID: "req_usage_ok", ProjectID: project.ID, APIKeyID: key.ID, ModelName: "gpt-test", ProviderID: "provider-a", InputTokens: 10, CachedInputTokens: 2, OutputTokens: 4, TotalTokens: 14, CostUSD: 0.01, CreatedAt: now.Add(-time.Hour)},
		{ID: "usage_key_second", RequestID: "req_usage_ok", ProjectID: project.ID, APIKeyID: key.ID, ModelName: "gpt-test", ProviderID: "provider-a", InputTokens: 3, CacheWriteTokens: 1, ReasoningTokens: 2, OutputTokens: 5, TotalTokens: 8, CostUSD: 0.02, CreatedAt: now.Add(-time.Hour)},
		{ID: "usage_other", RequestID: "req_usage_other", ProjectID: project.ID, APIKeyID: otherKey.ID, ModelName: "gpt-other", InputTokens: 999, TotalTokens: 999, CostUSD: 99, CreatedAt: now.Add(-time.Minute)},
	}
	if err := store.db.Create(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&QuotaBucket{KeyID: key.ID, Scope: "day", Bucket: dayBucket(now), QuotaCounter: QuotaCounter{Requests: 2, TotalTokens: 22, CostUSD: 0.03}}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := store.QueryAPIKeyUsage(t.Context(), APIKeyUsageQuery{
		Key: key, Project: project, From: now.Add(-24 * time.Hour), To: now.Add(time.Second), IncludeProviders: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.RequestCount != 2 || result.Summary.ErrorCount != 1 || result.Summary.AverageLatencyMS != 200 {
		t.Fatalf("unexpected request summary: %+v", result.Summary)
	}
	if result.Summary.InputTokens != 13 || result.Summary.CachedInputTokens != 2 || result.Summary.CacheWriteTokens != 1 || result.Summary.TotalTokens != 22 {
		t.Fatalf("unexpected token summary: %+v", result.Summary)
	}
	if result.Summary.EstimatedCostUSD != 0.03 {
		t.Fatalf("estimated cost = %v, want 0.03", result.Summary.EstimatedCostUSD)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "gpt-test" || result.Models[0].RequestCount != 2 {
		t.Fatalf("unexpected model breakdown: %+v", result.Models)
	}
	if len(result.Errors) != 1 || result.Errors[0].ID != "upstream_failed" || result.Errors[0].StatusCode != http.StatusBadGateway || result.Errors[0].LastOccurredAt == nil {
		t.Fatalf("unexpected error breakdown: %+v", result.Errors)
	}
	if len(result.Providers) != 2 {
		t.Fatalf("provider breakdown = %+v, want routed and unrouted groups", result.Providers)
	}
	if result.Quota.EffectiveLimits.DailyRequests != 5 || result.Quota.EffectiveLimits.MonthlyTokens != 1000 {
		t.Fatalf("unexpected effective quota: %+v", result.Quota.EffectiveLimits)
	}
	if result.Quota.Day.Usage.Requests != 2 || result.Quota.Day.Usage.TotalTokens != 22 {
		t.Fatalf("unexpected day quota usage: %+v", result.Quota.Day)
	}
}

func TestQueryAPIKeyUsageDoesNotCreateMissingQuotaBuckets(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Empty Usage Project", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Empty Key", Status: StatusActive}, "thk_empty_key_usage")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.QueryAPIKeyUsage(t.Context(), APIKeyUsageQuery{Key: key, Project: project, From: now.Add(-time.Hour), To: now}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ?", key.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("read-only usage query created %d quota buckets", count)
	}
}

func TestParseAPIKeyUsageRange(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/api/admin/api-keys/key/usage?from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z", nil)
	if err != nil {
		t.Fatal(err)
	}
	usageRange, err := parseAPIKeyUsageRange(request)
	if err != nil {
		t.Fatal(err)
	}
	if usageRange.From.Format(time.RFC3339) != "2026-01-01T00:00:00Z" || usageRange.To.Format(time.RFC3339) != "2026-02-01T00:00:00Z" {
		t.Fatalf("unexpected range: %+v", usageRange)
	}

	invalid, err := http.NewRequest(http.MethodGet, "/api/admin/api-keys/key/usage?from=2026-02-01T00:00:00Z&to=2026-01-01T00:00:00Z", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseAPIKeyUsageRange(invalid); AsHTTPError(err).Code != "invalid_request" {
		t.Fatalf("reverse range error = %v", err)
	}
}
