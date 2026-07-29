package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func gatewayIntegrationEvent(eventID string, eventType string, aggregateType string, aggregateID string, tenantID string, version int64, payload map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion": 1,
		"eventId":       eventID,
		"eventType":     eventType,
		"aggregateType": aggregateType,
		"aggregateId":   aggregateID,
		"tenantId":      tenantID,
		"version":       version,
		"occurredAt":    "2026-07-24T08:00:00.000Z",
		"traceId":       "trace_01",
		"sourceService": "system-service",
		"payload":       payload,
	}
}

func TestGatewayIntegrationEndpointRequiresDedicatedToken(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken:       "admin_token",
		IntegrationToken: "integration_token",
		SecretKey:        "test_secret",
	}).Handler()
	event := gatewayIntegrationEvent("evt_auth", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "tenant_01",
		"name":       "企业一",
	})

	unauthorized := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "admin_token")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected integration endpoint to reject admin token, got %d: %s", unauthorized.Code, unauthorized.Body)
	}
	authorized := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
	if authorized.Code != http.StatusOK {
		t.Fatalf("expected integration token to be accepted, got %d: %s", authorized.Code, authorized.Body)
	}
}

func TestGatewayIntegrationAcceptsGenericSourcesAndRejectsTenantScopeMismatch(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	event := gatewayIntegrationEvent("evt_generic_source", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "tenant_01", "name": "Tenant one",
	})
	event["sourceService"] = "external-orchestrator"
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("expected generic event source to be accepted, got %d: %s", response.Code, response.Body)
	}

	mismatch := gatewayIntegrationEvent("evt_tenant_mismatch", "tenant.created", "tenant", "tenant_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "tenant_02", "name": "Tenant two",
	})
	response = doJSON(t, app, http.MethodPost, "/api/internal/integration/events", mismatch, "integration_token")
	if response.Code != http.StatusBadRequest || !jsonBodyHasCode(response.Body, "invalid_integration_event") {
		t.Fatalf("expected tenant scope mismatch rejection, got %d: %s", response.Code, response.Body)
	}

	incomplete := gatewayIntegrationEvent("evt_incomplete_project", "project.created", "project", "project_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "project_01",
	})
	response = doJSON(t, app, http.MethodPost, "/api/internal/integration/events", incomplete, "integration_token")
	if response.Code != http.StatusBadRequest || !jsonBodyHasCode(response.Body, "invalid_integration_event") {
		t.Fatalf("expected incomplete payload rejection, got %d: %s", response.Code, response.Body)
	}
}

func TestGatewayIntegrationReconciliationReturnsTenantEventWatermark(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	events := []map[string]interface{}{
		gatewayIntegrationEvent("evt_tenant_watermark", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "tenant_01", "name": "企业一",
		}),
		gatewayIntegrationEvent("evt_org_watermark", "organization.created", "organization", "org_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "org_01", "name": "研发中心", "parentExternalId": nil,
		}),
	}
	for _, event := range events {
		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("expected integration event to succeed, got %d: %s", response.Code, response.Body)
		}
	}

	unauthorized := doJSON(t, app, http.MethodGet, "/api/internal/integration/reconciliation?tenant_id=tenant_01", nil, "admin_token")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected reconciliation to reject admin token, got %d: %s", unauthorized.Code, unauthorized.Body)
	}
	response := doJSON(t, app, http.MethodGet, "/api/internal/integration/reconciliation?tenant_id=tenant_01", nil, "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("expected reconciliation summary, got %d: %s", response.Code, response.Body)
	}
	var summary GatewayIntegrationReconciliationSummary
	if err := json.Unmarshal([]byte(response.Body), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.TenantID != "tenant_01" || summary.ReceivedEvents != 2 || summary.AggregateCounts["tenant"] != 1 || summary.AggregateCounts["organization"] != 1 {
		t.Fatalf("unexpected reconciliation summary: %+v", summary)
	}
	if summary.LatestReceivedAt == nil || summary.LatestReceivedAt.IsZero() || summary.LatestProcessedAt == nil || summary.LatestProcessedAt.IsZero() {
		t.Fatalf("expected reconciliation timestamps: %+v", summary)
	}
}

func TestGatewayModelCatalogRequiresIntegrationTokenAndFiltersOnServer(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{ID: "model_gateway_active", Name: "gateway-chat", Category: "chat", Family: "gateway", Modality: "text", ContextWindow: 128000, Capabilities: []string{"tools"}, Status: StatusActive})
	store.AddModel(Model{ID: "model_gateway_disabled", Name: "gateway-embedding", Category: "embedding", Family: "gateway", Modality: "embedding", ContextWindow: 8192, Status: StatusDisabled})
	app := NewWithConfig(store, Config{AdminToken: "admin_token", IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()

	unauthorized := doJSON(t, app, http.MethodGet, "/api/internal/models", nil, "admin_token")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected admin token to be rejected, got %d: %s", unauthorized.Code, unauthorized.Body)
	}
	filtered := doJSON(t, app, http.MethodGet, "/api/internal/models?name=GATEWAY&category=chat&status=active&page=1&page_size=1", nil, "integration_token")
	if filtered.Code != http.StatusOK {
		t.Fatalf("expected model list, got %d: %s", filtered.Code, filtered.Body)
	}
	var payload struct {
		Data     []gatewayModelItem `json:"data"`
		Page     int                `json:"page"`
		PageSize int                `json:"page_size"`
		Total    int                `json:"total"`
	}
	if err := json.Unmarshal([]byte(filtered.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 1 || payload.Page != 1 || payload.PageSize != 1 || len(payload.Data) != 1 || payload.Data[0].Name != "gateway-chat" {
		t.Fatalf("unexpected filtered models: %+v", payload)
	}
	invalid := doJSON(t, app, http.MethodGet, "/api/internal/models?page_size=101", nil, "integration_token")
	if invalid.Code != http.StatusBadRequest || !jsonBodyHasCode(invalid.Body, "invalid_model_query") {
		t.Fatalf("expected invalid pagination rejection, got %d: %s", invalid.Code, invalid.Body)
	}
}

func TestGatewayIntegrationEventIsIdempotentAndRejectsEventIDReuse(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	event := gatewayIntegrationEvent("evt_tenant_01", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "tenant_01",
		"name":       "企业一",
	})

	first := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
	duplicate := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
	if first.Code != http.StatusOK || duplicate.Code != http.StatusOK {
		t.Fatalf("expected idempotent requests to succeed: first=%d duplicate=%d", first.Code, duplicate.Code)
	}
	var duplicateResult GatewayIntegrationApplyResult
	if err := json.Unmarshal([]byte(duplicate.Body), &duplicateResult); err != nil {
		t.Fatal(err)
	}
	if duplicateResult.Outcome != "duplicate" || duplicateResult.TokenHubEntityID == "" {
		t.Fatalf("unexpected duplicate result: %+v", duplicateResult)
	}

	changed := gatewayIntegrationEvent("evt_tenant_01", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "tenant_01",
		"name":       "被篡改名称",
	})
	conflict := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", changed, "integration_token")
	if conflict.Code != http.StatusConflict || !jsonBodyHasCode(conflict.Body, "integration_event_conflict") {
		t.Fatalf("expected reused event ID conflict, got %d: %s", conflict.Code, conflict.Body)
	}
}

func TestGatewayIntegrationIgnoresStaleVersionsAndSoftDeletes(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	create := gatewayIntegrationEvent("evt_tenant_create", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "tenant_01",
		"name":       "初始名称",
	})
	update := gatewayIntegrationEvent("evt_tenant_update", "tenant.updated", "tenant", "tenant_01", "tenant_01", 3, map[string]interface{}{
		"externalId": "tenant_01",
		"name":       "最新名称",
		"status":     "active",
	})
	stale := gatewayIntegrationEvent("evt_tenant_stale", "tenant.updated", "tenant", "tenant_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "tenant_01",
		"name":       "过期名称",
	})
	for _, event := range []map[string]interface{}{create, update} {
		resp := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
		if resp.Code != http.StatusOK {
			t.Fatalf("expected projection event to succeed, got %d: %s", resp.Code, resp.Body)
		}
	}
	staleResponse := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", stale, "integration_token")
	if staleResponse.Code != http.StatusOK || !jsonBodyHasField(staleResponse.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected stale version to be ignored, got %d: %s", staleResponse.Code, staleResponse.Body)
	}

	deleted := gatewayIntegrationEvent("evt_tenant_delete", "tenant.deleted", "tenant", "tenant_01", "tenant_01", 4, map[string]interface{}{
		"externalId": "tenant_01",
		"name":       "最新名称",
	})
	deleteResponse := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", deleted, "integration_token")
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("expected delete event to succeed, got %d: %s", deleteResponse.Code, deleteResponse.Body)
	}
	var tenant GatewayTenant
	if err := store.db.First(&tenant, "external_tenant_id = ?", "tenant_01").Error; err != nil {
		t.Fatal(err)
	}
	if tenant.Name != "最新名称" || tenant.Status != integrationStatusDeleted || tenant.DeletedAt == nil || tenant.Version != 4 {
		t.Fatalf("expected retained soft-deleted projection, got %+v", tenant)
	}
}

func TestGatewayIntegrationReportsMissingProjectionDependency(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	event := gatewayIntegrationEvent("evt_org_create", "organization.created", "organization", "org_01", "tenant_missing", 1, map[string]interface{}{
		"externalId":       "org_01",
		"name":             "平台工程",
		"parentExternalId": nil,
	})

	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "integration_dependency_missing") {
		t.Fatalf("expected missing dependency conflict, got %d: %s", response.Code, response.Body)
	}
	var count int64
	if err := store.db.Model(&IntegrationInbox{}).Where("event_id = ?", "evt_org_create").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed event must roll back its inbox row, got %d", count)
	}
}

func TestGatewayProjectProjectionCreatesServingProject(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)

	var projection GatewayProject
	if err := store.db.First(&projection, "external_project_id = ?", "project_01").Error; err != nil {
		t.Fatal(err)
	}
	project, found := store.GetProject(projection.ID)
	if !found || project.Name != "智能客服" || project.Status != StatusActive {
		t.Fatalf("expected active serving project, got found=%v project=%+v", found, project)
	}

	archived := gatewayIntegrationEvent("evt_project_archived", "project.archived", "project", "project_01", "tenant_01", 2, map[string]interface{}{
		"externalId":      "project_01",
		"name":            "智能客服",
		"ownerExternalId": "user_01",
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", archived, "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("expected project archive event to succeed, got %d: %s", response.Code, response.Body)
	}
	project, found = store.GetProject(projection.ID)
	if !found || project.Status != StatusDisabled {
		t.Fatalf("expected archived serving project to be disabled, got found=%v project=%+v", found, project)
	}
}

func TestGatewayModelAccessKeyLifecycleIsTenantScopedAndIdempotent(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken:       "admin_token",
		IntegrationToken: "integration_token",
		SecretKey:        "test_secret",
	}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	request := map[string]interface{}{
		"request_id":     "request_key_01",
		"tenant_id":      "tenant_01",
		"project_id":     "project_01",
		"principal_type": "user",
		"principal_id":   "user_01",
		"name":           "Local Development",
		"environment":    "development",
		"allowed_models": []string{"gpt-4.1", "deepseek-v3", "gpt-4.1"},
		"requested_by":   "user_01",
	}

	unauthorized := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", request, "admin_token")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected admin token to be rejected, got %d: %s", unauthorized.Code, unauthorized.Body)
	}
	created := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", request, "integration_token")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected model access key creation, got %d: %s", created.Code, created.Body)
	}
	var createdPayload gatewayModelAccessKeyCreateResponse
	if err := json.Unmarshal([]byte(created.Body), &createdPayload); err != nil {
		t.Fatal(err)
	}
	if createdPayload.APIKey == "" || !createdPayload.PlainTextVisibleOnce || createdPayload.Data.TenantExternalID != "tenant_01" || createdPayload.Data.PrincipalExternalID != "user_01" {
		t.Fatalf("unexpected create response: %+v", createdPayload)
	}
	if len(createdPayload.Data.Allowed) != 2 || createdPayload.Data.KeyHash != "" {
		t.Fatalf("expected normalized public key response, got %+v", createdPayload.Data)
	}
	if _, validated, err := store.ValidateAPIKey(createdPayload.APIKey, ""); err != nil || validated.ID != createdPayload.Data.ID {
		t.Fatalf("expected created secret to authenticate, key=%+v err=%v", validated, err)
	}
	var persistedKey APIKey
	if err := store.db.First(&persistedKey, "id = ?", createdPayload.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedKey.KeyCiphertext == "" || persistedKey.KeyCiphertext == createdPayload.APIKey || !strings.HasPrefix(persistedKey.KeyCiphertext, "enc:v1:") {
		t.Fatalf("expected encrypted model access key secret, got %q", persistedKey.KeyCiphertext)
	}

	wrongPrincipalReveal := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+createdPayload.Data.ID+"/reveal", map[string]interface{}{
		"tenant_id": "tenant_01", "principal_type": "user", "principal_id": "user_02", "requested_by": "user_02",
	}, "integration_token")
	if wrongPrincipalReveal.Code != http.StatusNotFound {
		t.Fatalf("expected principal-scoped reveal to hide the key, got %d: %s", wrongPrincipalReveal.Code, wrongPrincipalReveal.Body)
	}
	revealed := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+createdPayload.Data.ID+"/reveal", map[string]interface{}{
		"tenant_id": "tenant_01", "principal_type": "user", "principal_id": "user_01", "requested_by": "user_01",
	}, "integration_token")
	if revealed.Code != http.StatusOK {
		t.Fatalf("expected model access key reveal, got %d: %s", revealed.Code, revealed.Body)
	}
	var revealPayload gatewayModelAccessKeyRevealResponse
	if err := json.Unmarshal([]byte(revealed.Body), &revealPayload); err != nil {
		t.Fatal(err)
	}
	if revealPayload.APIKey != createdPayload.APIKey {
		t.Fatalf("expected revealed secret to match created secret")
	}
	var revealAudit AuditEvent
	if err := store.db.First(&revealAudit, "action = ? AND resource_id = ?", "gateway_model_access_key_reveal", createdPayload.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if revealAudit.ActorUserID != "user_01" {
		t.Fatalf("expected reveal audit actor user_01, got %+v", revealAudit)
	}

	replayed := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", request, "integration_token")
	if replayed.Code != http.StatusOK {
		t.Fatalf("expected idempotent replay, got %d: %s", replayed.Code, replayed.Body)
	}
	var replayPayload gatewayModelAccessKeyCreateResponse
	if err := json.Unmarshal([]byte(replayed.Body), &replayPayload); err != nil {
		t.Fatal(err)
	}
	if replayPayload.Data.ID != createdPayload.Data.ID || replayPayload.APIKey != "" || replayPayload.PlainTextVisibleOnce || !replayPayload.IdempotentReplay {
		t.Fatalf("unexpected idempotent replay response: %+v", replayPayload)
	}

	changedRequest := map[string]interface{}{}
	for key, value := range request {
		changedRequest[key] = value
	}
	changedRequest["name"] = "复用请求号的不同名称"
	conflict := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", changedRequest, "integration_token")
	if conflict.Code != http.StatusConflict || !jsonBodyHasCode(conflict.Body, "model_access_key_request_conflict") {
		t.Fatalf("expected request ID conflict, got %d: %s", conflict.Code, conflict.Body)
	}

	listed := doJSON(t, app, http.MethodGet, "/api/internal/model-access-keys?tenant_id=tenant_01&project_id=project_01&principal_type=user&principal_id=user_01&name=LOCAL&status=active", nil, "integration_token")
	if listed.Code != http.StatusOK {
		t.Fatalf("expected model access key list, got %d: %s", listed.Code, listed.Body)
	}
	var listPayload struct {
		Data []APIKey `json:"data"`
	}
	if err := json.Unmarshal([]byte(listed.Body), &listPayload); err != nil {
		t.Fatal(err)
	}
	if len(listPayload.Data) != 1 || listPayload.Data[0].ID != createdPayload.Data.ID {
		t.Fatalf("unexpected tenant-scoped key list: %+v", listPayload.Data)
	}
	notListed := doJSON(t, app, http.MethodGet, "/api/internal/model-access-keys?tenant_id=tenant_01&principal_type=application", nil, "integration_token")
	if notListed.Code != http.StatusOK {
		t.Fatalf("expected filtered model access key list, got %d: %s", notListed.Code, notListed.Body)
	}
	if err := json.Unmarshal([]byte(notListed.Body), &listPayload); err != nil {
		t.Fatal(err)
	}
	if len(listPayload.Data) != 0 {
		t.Fatalf("expected principal type filter to exclude the key, got %+v", listPayload.Data)
	}

	requestTime := time.Date(2026, time.July, 26, 17, 30, 0, 0, time.UTC)
	if err := store.db.Create(&RequestLog{
		ID: "log_integration_01", RequestID: "req_integration_01", ProjectID: createdPayload.Data.ProjectID,
		APIKeyID: createdPayload.Data.ID, ModelName: "gpt-4.1", ProviderID: "provider_01",
		StatusCode: http.StatusOK, LatencyMS: 125, CreatedAt: requestTime,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&UsageRecord{
		ID: "usage_integration_01", RequestID: "req_integration_01", ProjectID: createdPayload.Data.ProjectID,
		APIKeyID: createdPayload.Data.ID, ModelName: "gpt-4.1", ProviderID: "provider_01",
		InputTokens: 100, OutputTokens: 25, TotalTokens: 125, CostUSD: 0.00125, CreatedAt: requestTime,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{
		ID: "log_integration_error", RequestID: "req_integration_error", ProjectID: createdPayload.Data.ProjectID,
		APIKeyID: createdPayload.Data.ID, ModelName: "gpt-4.1", ProviderID: "provider_01",
		StatusCode: http.StatusBadGateway, ErrorCode: "provider_error", LatencyMS: 80, CreatedAt: requestTime,
	}).Error; err != nil {
		t.Fatal(err)
	}
	requestLogs := doJSON(t, app, http.MethodGet, "/api/internal/request-logs?tenant_id=tenant_01&request_id=integration_01&principal=user_01&project_id=project_01&model=gpt&outcome=success&date_from=2026-07-27T00:00:00%2B08:00&date_to=2026-07-27T23:59:59%2B08:00&page=1&page_size=10", nil, "integration_token")
	if requestLogs.Code != http.StatusOK {
		t.Fatalf("expected tenant request logs, got %d: %s", requestLogs.Code, requestLogs.Body)
	}
	var requestLogPayload struct {
		Data GatewayRequestLogPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(requestLogs.Body), &requestLogPayload); err != nil {
		t.Fatal(err)
	}
	if requestLogPayload.Data.Total != 1 || len(requestLogPayload.Data.Items) != 1 {
		t.Fatalf("unexpected request log page: %+v", requestLogPayload.Data)
	}
	item := requestLogPayload.Data.Items[0]
	if item.RequestID != "req_integration_01" || item.PrincipalExternalID != "user_01" || item.ProjectExternalID != "project_01" || item.TotalTokens != 125 {
		t.Fatalf("unexpected request log item: %+v", item)
	}
	crossTenantLogs := doJSON(t, app, http.MethodGet, "/api/internal/request-logs?tenant_id=tenant_02", nil, "integration_token")
	if crossTenantLogs.Code != http.StatusOK || !strings.Contains(crossTenantLogs.Body, `"total":0`) {
		t.Fatalf("expected empty cross-tenant request logs, got %d: %s", crossTenantLogs.Code, crossTenantLogs.Body)
	}
	adminRequestLogs := doJSON(t, app, http.MethodGet, "/api/internal/request-logs?tenant_id=tenant_01", nil, "admin_token")
	if adminRequestLogs.Code != http.StatusUnauthorized {
		t.Fatalf("expected admin token to be rejected for request logs, got %d: %s", adminRequestLogs.Code, adminRequestLogs.Body)
	}
	usage := doJSON(t, app, http.MethodGet, "/api/internal/usage?tenant_id=tenant_01&project_id=project_01&principal=user_01&model=gpt&date_from=2026-07-27T00:00:00%2B08:00&date_to=2026-07-27T23:59:59%2B08:00", nil, "integration_token")
	if usage.Code != http.StatusOK {
		t.Fatalf("expected tenant usage, got %d: %s", usage.Code, usage.Body)
	}
	var usagePayload struct {
		Data GatewayUsageReport `json:"data"`
	}
	if err := json.Unmarshal([]byte(usage.Body), &usagePayload); err != nil {
		t.Fatal(err)
	}
	if usagePayload.Data.Summary.RequestCount != 2 || usagePayload.Data.Summary.ErrorCount != 1 || usagePayload.Data.Summary.TotalTokens != 125 || len(usagePayload.Data.Timeseries) != 1 {
		t.Fatalf("unexpected usage report: %+v", usagePayload.Data)
	}
	if usagePayload.Data.Timeseries[0].Date != "2026-07-27" || usagePayload.Data.Timeseries[0].TotalTokens != 125 {
		t.Fatalf("unexpected usage daily total: %+v", usagePayload.Data.Timeseries[0])
	}
	if len(usagePayload.Data.Breakdown.Projects) != 1 || usagePayload.Data.Breakdown.Projects[0].ID != "project_01" || usagePayload.Data.Breakdown.Projects[0].RequestCount != 2 {
		t.Fatalf("unexpected usage project breakdown: %+v", usagePayload.Data.Breakdown.Projects)
	}
	westernUsage := doJSON(t, app, http.MethodGet, "/api/internal/usage?tenant_id=tenant_01&date_from=2026-07-26T00:00:00-05:00&date_to=2026-07-26T23:59:59-05:00", nil, "integration_token")
	if westernUsage.Code != http.StatusOK {
		t.Fatalf("expected usage in requested timezone, got %d: %s", westernUsage.Code, westernUsage.Body)
	}
	if err := json.Unmarshal([]byte(westernUsage.Body), &usagePayload); err != nil {
		t.Fatal(err)
	}
	if len(usagePayload.Data.Timeseries) != 1 || usagePayload.Data.Timeseries[0].Date != "2026-07-26" || usagePayload.Data.Timeseries[0].TotalTokens != 125 {
		t.Fatalf("unexpected requested-timezone usage: %+v", usagePayload.Data.Timeseries)
	}
	crossTenantUsage := doJSON(t, app, http.MethodGet, "/api/internal/usage?tenant_id=tenant_02&date_from=2026-07-27T00:00:00Z&date_to=2026-07-27T23:59:59Z", nil, "integration_token")
	if crossTenantUsage.Code != http.StatusOK || !strings.Contains(crossTenantUsage.Body, `"request_count":0`) {
		t.Fatalf("expected empty cross-tenant usage, got %d: %s", crossTenantUsage.Code, crossTenantUsage.Body)
	}
	adminUsage := doJSON(t, app, http.MethodGet, "/api/internal/usage?tenant_id=tenant_01&date_from=2026-07-27T00:00:00Z&date_to=2026-07-27T23:59:59Z", nil, "admin_token")
	if adminUsage.Code != http.StatusUnauthorized {
		t.Fatalf("expected admin token to be rejected for usage, got %d: %s", adminUsage.Code, adminUsage.Body)
	}

	wrongTenant := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+createdPayload.Data.ID+"/revoke", map[string]interface{}{
		"tenant_id": "tenant_02",
		"reason":    "cross_tenant_attempt",
	}, "integration_token")
	if wrongTenant.Code != http.StatusNotFound {
		t.Fatalf("expected cross-tenant revoke to hide the key, got %d: %s", wrongTenant.Code, wrongTenant.Body)
	}
	revoked := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+createdPayload.Data.ID+"/revoke", map[string]interface{}{
		"tenant_id":    "tenant_01",
		"reason":       "no_longer_needed",
		"requested_by": "user_01",
	}, "integration_token")
	if revoked.Code != http.StatusOK || !jsonBodyHasNestedField(revoked.Body, "data", "status", StatusRevoked) {
		t.Fatalf("expected key revocation, got %d: %s", revoked.Code, revoked.Body)
	}
	if _, _, err := store.ValidateAPIKey(createdPayload.APIKey, ""); err == nil {
		t.Fatal("expected revoked model access key secret to be rejected")
	}
	var revokeAudit AuditEvent
	if err := store.db.First(&revokeAudit, "action = ? AND resource_id = ?", "gateway_model_access_key_revoke", createdPayload.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if revokeAudit.ActorUserID != "user_01" {
		t.Fatalf("expected revoke audit actor user_01, got %+v", revokeAudit)
	}
}

func TestGatewayModelAccessKeyRequestIDIsTenantScoped(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	first := createGatewayModelAccessKeyForTest(t, app, "shared_request", "user", "user_01")

	for _, event := range []map[string]interface{}{
		gatewayIntegrationEvent("evt_scope_tenant_02", "tenant.created", "tenant", "tenant_02", "tenant_02", 1, map[string]interface{}{
			"externalId": "tenant_02", "name": "Tenant two",
		}),
		gatewayIntegrationEvent("evt_scope_member_02", "tenant_member.added", "tenant_member", "membership_02", "tenant_02", 1, map[string]interface{}{
			"externalId": "membership_02", "principalExternalId": "user_02", "name": "User two",
		}),
		gatewayIntegrationEvent("evt_scope_project_02", "project.created", "project", "project_02", "tenant_02", 1, map[string]interface{}{
			"externalId": "project_02", "name": "Project two", "ownerExternalId": "user_02",
		}),
	} {
		applyGatewayIntegrationEventForTest(t, app, event)
	}
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", map[string]interface{}{
		"request_id": "shared_request", "tenant_id": "tenant_02", "project_id": "project_02",
		"principal_type": "user", "principal_id": "user_02", "name": "Tenant two key",
	}, "integration_token")
	if response.Code != http.StatusCreated {
		t.Fatalf("expected request ID reuse in another tenant, got %d: %s", response.Code, response.Body)
	}
	var second gatewayModelAccessKeyCreateResponse
	if err := json.Unmarshal([]byte(response.Body), &second); err != nil {
		t.Fatal(err)
	}
	if second.Data.ID == first.Data.ID {
		t.Fatal("expected tenant-scoped request IDs to create distinct keys")
	}
}

func TestGatewayManagedProjectsAndKeysAreReadOnlyToLocalManagement(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	created := createGatewayModelAccessKeyForTest(t, app, "request_read_only", "user", "user_01")

	if _, err := store.UpdateProject(created.Data.ProjectID, Project{Name: "Local override"}); AsHTTPError(err).Code != "integration_managed_project_read_only" {
		t.Fatalf("expected managed project update rejection, got %v", err)
	}
	if err := store.DeleteProject(created.Data.ProjectID); AsHTTPError(err).Code != "integration_managed_project_read_only" {
		t.Fatalf("expected managed project delete rejection, got %v", err)
	}
	if _, _, err := store.CreateAPIKey(created.Data.ProjectID, APIKey{Name: "Local key"}, ""); AsHTTPError(err).Code != "integration_managed_project_read_only" {
		t.Fatalf("expected local key creation rejection, got %v", err)
	}
	if _, err := store.UpdateAPIKey(created.Data.ID, APIKey{Name: "Local override"}); AsHTTPError(err).Code != "integration_managed_key_read_only" {
		t.Fatalf("expected managed key update rejection, got %v", err)
	}
	if _, _, err := store.RotateAPIKey(created.Data.ID, nil); AsHTTPError(err).Code != "integration_managed_key_read_only" {
		t.Fatalf("expected managed key rotation rejection, got %v", err)
	}
	if err := store.DeleteAPIKey(created.Data.ID); AsHTTPError(err).Code != "integration_managed_key_read_only" {
		t.Fatalf("expected managed key delete rejection, got %v", err)
	}
}

func TestGatewayModelAccessKeyNamePatternEscapesLikeWildcards(t *testing.T) {
	if got, want := gatewayModelAccessKeyNamePattern(`LOCAL%_\`), `%local\%\_\\%`; got != want {
		t.Fatalf("expected escaped name pattern %q, got %q", want, got)
	}
}

func TestGatewayModelAccessKeyRequiresProjectedWorkloadForApplication(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", map[string]interface{}{
		"request_id":     "request_application_01",
		"tenant_id":      "tenant_01",
		"project_id":     "project_01",
		"principal_type": "application",
		"principal_id":   "application_01",
		"name":           "客服助手生产",
	}, "integration_token")
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "gateway_workload_unavailable") {
		t.Fatalf("expected missing workload projection conflict, got %d: %s", response.Code, response.Body)
	}
}

func TestGatewayModelAccessKeyRequiresMatchingWorkloadType(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_typed_workload", "workload.created", "workload", "application_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "application_01", "projectExternalId": "project_01", "ownerExternalId": "user_01",
		"name": "客服助手", "workloadType": "application", "environment": "production", "status": "active",
	}))
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", map[string]interface{}{
		"request_id": "request_wrong_workload_type", "tenant_id": "tenant_01", "project_id": "project_01",
		"principal_type": "agent", "principal_id": "application_01", "name": "错误类型",
	}, "integration_token")
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "gateway_workload_unavailable") {
		t.Fatalf("expected mismatched workload type conflict, got %d: %s", response.Code, response.Body)
	}
}

func TestGatewayModelAccessKeyListReportsExpiredStatus(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	created := createGatewayModelAccessKeyForTest(t, app, "request_expiring", "user", "user_01")
	past := time.Now().UTC().Add(-time.Minute)
	if err := store.db.Model(&APIKey{}).Where("id = ?", created.Data.ID).Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, app, http.MethodGet, "/api/internal/model-access-keys?tenant_id=tenant_01&status=expired&page=1&page_size=10", nil, "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("expected expired key list, got %d: %s", response.Code, response.Body)
	}
	var page GatewayModelAccessKeyPage
	if err := json.Unmarshal([]byte(response.Body), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.Data.ID || page.Items[0].Status != gatewayModelAccessKeyStatusExpired {
		t.Fatalf("unexpected expired key page: %+v", page)
	}
	if _, _, err := store.ValidateAPIKey(created.APIKey, ""); err == nil || AsHTTPError(err).Code != ErrAPIKeyExpired.Code {
		t.Fatalf("expected expired key authentication error, got %v", err)
	}
}

func TestGatewayModelAccessKeyListIsPaginated(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	createGatewayModelAccessKeyForTest(t, app, "request_page_one", "user", "user_01")
	createGatewayModelAccessKeyForTest(t, app, "request_page_two", "user", "user_01")

	loadPage := func(pageNumber int) GatewayModelAccessKeyPage {
		t.Helper()
		response := doJSON(t, app, http.MethodGet, "/api/internal/model-access-keys?tenant_id=tenant_01&page="+strconv.Itoa(pageNumber)+"&page_size=1", nil, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("expected model access key page, got %d: %s", response.Code, response.Body)
		}
		var page GatewayModelAccessKeyPage
		if err := json.Unmarshal([]byte(response.Body), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	first, second := loadPage(1), loadPage(2)
	if first.Total != 2 || second.Total != 2 || len(first.Items) != 1 || len(second.Items) != 1 || first.Items[0].ID == second.Items[0].ID {
		t.Fatalf("unexpected paginated key results: first=%+v second=%+v", first, second)
	}
}

func TestGatewayPrincipalIgnoresOlderMembershipGeneration(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)

	readded := gatewayIntegrationEvent("evt_member_generation_two", "tenant_member.added", "tenant_member", "membership_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "membership_02", "principalExternalId": "user_01", "name": "张晨", "status": "active",
	})
	readded["occurredAt"] = "2026-07-24T08:01:00.000Z"
	applyGatewayIntegrationEventForTest(t, app, readded)
	created := createGatewayModelAccessKeyForTest(t, app, "request_after_readd", "user", "user_01")

	lateRemoval := gatewayIntegrationEvent("evt_old_membership_late_remove", "tenant_member.removed", "tenant_member", "membership_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "membership_01", "principalExternalId": "user_01", "name": "张晨", "status": "removed",
	})
	lateRemoval["occurredAt"] = "2026-07-24T08:01:00.000Z"
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", lateRemoval, "integration_token")
	if response.Code != http.StatusOK || !jsonBodyHasField(response.Body, "outcome", "ignored_stale") {
		t.Fatalf("expected older membership generation to be ignored, got %d: %s", response.Code, response.Body)
	}
	assertGatewayModelAccessKeyActive(t, store, created.APIKey)
}

func TestGatewayManagedUserKeyFollowsTenantAndPrincipalStatus(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	created := createGatewayModelAccessKeyForTest(t, app, "request_user_status", "user", "user_01")
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_owned_workload_created", "workload.created", "workload", "owned_application_01", "tenant_01", 1, map[string]interface{}{
		"externalId": "owned_application_01", "projectExternalId": "project_01", "ownerExternalId": "user_01",
		"name": "Owned application", "workloadType": "application", "environment": "production", "status": "active",
	}))
	workloadKey := createGatewayModelAccessKeyForTest(t, app, "request_owned_workload_status", "application", "owned_application_01")

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_disabled", "tenant.disabled", "tenant", "tenant_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "tenant_01", "name": "企业一", "status": "inactive",
	}))
	assertGatewayModelAccessKeyDisabled(t, store, created.APIKey)
	assertGatewayModelAccessKeyDisabled(t, store, workloadKey.APIKey)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusDisabled)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_reenabled", "tenant.updated", "tenant", "tenant_01", "tenant_01", 3, map[string]interface{}{
		"externalId": "tenant_01", "name": "企业一", "status": "active",
	}))
	assertGatewayModelAccessKeyActive(t, store, created.APIKey)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusActive)

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_member_disabled", "tenant_member.removed", "tenant_member", "membership_01", "tenant_01", 2, map[string]interface{}{
		"externalId": "membership_01", "principalExternalId": "user_01", "name": "张晨", "status": "inactive",
	}))
	assertGatewayModelAccessKeyDisabled(t, store, created.APIKey)
	assertGatewayModelAccessKeyActive(t, store, workloadKey.APIKey)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusDisabled)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_member_reenabled", "tenant_member.updated", "tenant_member", "membership_01", "tenant_01", 3, map[string]interface{}{
		"externalId": "membership_01", "principalExternalId": "user_01", "name": "张晨", "status": "active",
	}))
	assertGatewayModelAccessKeyActive(t, store, created.APIKey)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusActive)

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_member_removed", "tenant_member.removed", "tenant_member", "membership_01", "tenant_01", 4, map[string]interface{}{
		"externalId": "membership_01", "principalExternalId": "user_01", "name": "张晨", "status": "removed",
	}))
	assertGatewayModelAccessKeyRevoked(t, store, created)
	assertGatewayModelAccessKeyActive(t, store, workloadKey.APIKey)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusRevoked)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_member_readded", "tenant_member.added", "tenant_member", "membership_01", "tenant_01", 5, map[string]interface{}{
		"externalId": "membership_01", "principalExternalId": "user_01", "name": "张晨", "status": "active",
	}))
	assertGatewayModelAccessKeyDisabled(t, store, created.APIKey)

	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_deleted", "tenant.deleted", "tenant", "tenant_01", "tenant_01", 4, map[string]interface{}{
		"externalId": "tenant_01", "name": "企业一", "status": "deleted",
	}))
	assertGatewayModelAccessKeyRevoked(t, store, workloadKey)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_tenant_recreated", "tenant.updated", "tenant", "tenant_01", "tenant_01", 5, map[string]interface{}{
		"externalId": "tenant_01", "name": "企业一", "status": "active",
	}))
	assertGatewayModelAccessKeyDisabled(t, store, workloadKey.APIKey)
}

func TestGatewayManagedWorkloadKeyFollowsWorkloadStatus(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	workloadPayload := map[string]interface{}{
		"externalId": "application_01", "projectExternalId": "project_01", "ownerExternalId": "user_01",
		"name": "客服助手", "workloadType": "application", "environment": "production", "status": "active",
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_created", "workload.created", "workload", "application_01", "tenant_01", 1, workloadPayload))
	created := createGatewayModelAccessKeyForTest(t, app, "request_workload_status", "application", "application_01")

	workloadPayload["status"] = "inactive"
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_disabled", "workload.disabled", "workload", "application_01", "tenant_01", 2, workloadPayload))
	assertGatewayModelAccessKeyDisabled(t, store, created.APIKey)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusDisabled)
	workloadPayload["status"] = "active"
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_reenabled", "workload.updated", "workload", "application_01", "tenant_01", 3, workloadPayload))
	assertGatewayModelAccessKeyActive(t, store, created.APIKey)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusActive)

	workloadPayload["status"] = "deleted"
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_deleted", "workload.deleted", "workload", "application_01", "tenant_01", 4, workloadPayload))
	assertGatewayModelAccessKeyRevoked(t, store, created)
	assertGatewayModelAccessKeyListStatus(t, store, created.Data.ID, StatusRevoked)
	workloadPayload["status"] = "active"
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_workload_recreated", "workload.updated", "workload", "application_01", "tenant_01", 5, workloadPayload))
	assertGatewayModelAccessKeyDisabled(t, store, created.APIKey)
}

func createGatewayModelAccessKeyForTest(t *testing.T, app http.Handler, requestID string, principalType string, principalID string) gatewayModelAccessKeyCreateResponse {
	t.Helper()
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", map[string]interface{}{
		"request_id": requestID, "tenant_id": "tenant_01", "project_id": "project_01",
		"principal_type": principalType, "principal_id": principalID, "name": requestID,
	}, "integration_token")
	if response.Code != http.StatusCreated {
		t.Fatalf("expected model access key creation, got %d: %s", response.Code, response.Body)
	}
	var result gatewayModelAccessKeyCreateResponse
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func applyGatewayIntegrationEventForTest(t *testing.T, app http.Handler, event map[string]interface{}) {
	t.Helper()
	response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("expected integration event to succeed, got %d: %s", response.Code, response.Body)
	}
}

func assertGatewayModelAccessKeyActive(t *testing.T, store *GormStore, secret string) {
	t.Helper()
	if _, _, err := store.ValidateAPIKey(secret, ""); err != nil {
		t.Fatalf("expected model access key to authenticate, got %v", err)
	}
}

func assertGatewayModelAccessKeyDisabled(t *testing.T, store *GormStore, secret string) {
	t.Helper()
	if _, _, err := store.ValidateAPIKey(secret, ""); err == nil || AsHTTPError(err).Code != ErrAPIKeyDisabled.Code {
		t.Fatalf("expected disabled model access key, got %v", err)
	}
}

func assertGatewayModelAccessKeyRevoked(t *testing.T, store *GormStore, created gatewayModelAccessKeyCreateResponse) {
	t.Helper()
	var key APIKey
	if err := store.db.First(&key, "id = ?", created.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if key.Status != StatusRevoked {
		t.Fatalf("expected model access key to be permanently revoked, got %s", key.Status)
	}
	assertGatewayModelAccessKeyDisabled(t, store, created.APIKey)
}

func assertGatewayModelAccessKeyListStatus(t *testing.T, store *GormStore, keyID string, expectedStatus string) {
	t.Helper()
	page, err := store.ListGatewayModelAccessKeys(GatewayModelAccessKeyFilter{
		TenantExternalID: "tenant_01",
		Status:           expectedStatus,
		Page:             1,
		PageSize:         100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == keyID && item.Status == expectedStatus {
			return
		}
	}
	t.Fatalf("expected key %s with effective status %s, got %+v", keyID, expectedStatus, page.Items)
}

func seedGatewayModelAccessKeyScope(t *testing.T, app http.Handler) {
	t.Helper()
	events := []map[string]interface{}{
		gatewayIntegrationEvent("evt_scope_tenant", "tenant.created", "tenant", "tenant_01", "tenant_01", 1, map[string]interface{}{
			"externalId": "tenant_01",
			"name":       "企业一",
		}),
		gatewayIntegrationEvent("evt_scope_member", "tenant_member.added", "tenant_member", "membership_01", "tenant_01", 1, map[string]interface{}{
			"externalId":          "membership_01",
			"principalExternalId": "user_01",
			"name":                "张晨",
		}),
		gatewayIntegrationEvent("evt_scope_project", "project.created", "project", "project_01", "tenant_01", 1, map[string]interface{}{
			"externalId":      "project_01",
			"name":            "智能客服",
			"ownerExternalId": "user_01",
		}),
	}
	for _, event := range events {
		response := doJSON(t, app, http.MethodPost, "/api/internal/integration/events", event, "integration_token")
		if response.Code != http.StatusOK {
			t.Fatalf("failed to seed gateway projection: %d %s", response.Code, response.Body)
		}
	}
}

func jsonBodyHasCode(body string, expected string) bool {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal([]byte(body), &payload) == nil && payload.Error.Code == expected
}

func jsonBodyHasField(body string, field string, expected string) bool {
	var payload map[string]interface{}
	if json.Unmarshal([]byte(body), &payload) != nil {
		return false
	}
	value, _ := payload[field].(string)
	return value == expected
}

func jsonBodyHasNestedField(body string, parent string, field string, expected string) bool {
	var payload map[string]interface{}
	if json.Unmarshal([]byte(body), &payload) != nil {
		return false
	}
	nested, _ := payload[parent].(map[string]interface{})
	value, _ := nested[field].(string)
	return value == expected
}
