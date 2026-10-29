package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGatewayModelAccessKeyProjectRebindPreservesSecretUsageAndQuota(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_scope_project_02", "project.created", "project", "project_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "project_02", "name": "Second Project", "ownerExternalId": "user_01",
	}))
	created := createGatewayModelAccessKeyForTest(t, app, "rebind_request", "user", "user_01")

	requestTime := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	if err := store.db.Create(&UsageRecord{ID: "usage_rebind_old", RequestID: "request_rebind_old", ProjectID: created.Data.ProjectID, APIKeyID: created.Data.ID, ModelName: "gpt-4.1", TotalTokens: 42, CostUSD: 0.42, CreatedAt: requestTime}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{ID: "log_rebind_old", RequestID: "request_rebind_old", ProjectID: created.Data.ProjectID, APIKeyID: created.Data.ID, ModelName: "gpt-4.1", StatusCode: http.StatusOK, CreatedAt: requestTime}).Error; err != nil {
		t.Fatal(err)
	}
	oldProjectID := created.Data.ProjectID
	var before APIKey
	if err := store.db.First(&before, "id = ?", created.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	beforeQuota := before.Limits

	rebound := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", map[string]interface{}{
		"tenant_id": "tenant_01", "principal_type": "user", "principal_id": "user_01",
		"project_id": "project_02", "expected_project_id": "project_01", "requested_by": "user_01",
	}, "integration_token")
	if rebound.Code != http.StatusOK {
		t.Fatalf("expected rebind to succeed, got %d: %s", rebound.Code, rebound.Body)
	}
	if strings.Contains(rebound.Body, created.APIKey) {
		t.Fatal("project response disclosed the credential secret")
	}
	var payload struct {
		Data APIKey `json:"data"`
	}
	if err := json.Unmarshal([]byte(rebound.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.ID != created.Data.ID || payload.Data.ProjectExternalID != "project_02" {
		t.Fatalf("expected stable key and new project, got %+v", payload.Data)
	}
	if _, _, err := store.ValidateAPIKey(created.APIKey, ""); err != nil {
		t.Fatalf("expected original secret to remain valid after rebind, got %v", err)
	}

	var after APIKey
	if err := store.db.First(&after, "id = ?", created.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.ProjectExternalID != "project_02" || after.ProjectID == oldProjectID || after.Limits != beforeQuota || after.KeyHash != before.KeyHash || after.KeyCiphertext != before.KeyCiphertext {
		t.Fatalf("rebind changed immutable key or quota state: before=%+v after=%+v", before, after)
	}
	var usage UsageRecord
	if err := store.db.First(&usage, "id = ?", "usage_rebind_old").Error; err != nil {
		t.Fatal(err)
	}
	if usage.ProjectID != oldProjectID || usage.TotalTokens != 42 || usage.CostUSD != 0.42 {
		t.Fatalf("historical usage changed after rebind: %+v", usage)
	}
	var audit AuditEvent
	if err := store.db.First(&audit, "action = ? AND resource_id = ?", "gateway_model_access_key_project_rebind", created.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if audit.ActorUserID != "user_01" {
		t.Fatalf("unexpected rebind audit actor: %+v", audit)
	}
	if strings.Contains(audit.BeforeSnapshot+audit.AfterSnapshot, created.APIKey) {
		t.Fatal("project audit disclosed the credential secret")
	}
}

func TestGatewayModelAccessKeyProjectRebindRejectsScopeMismatchAndStaleExpectation(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_scope_project_02_rebind", "project.created", "project", "project_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "project_02", "name": "Second Project", "ownerExternalId": "user_01",
	}))
	created := createGatewayModelAccessKeyForTest(t, app, "rebind_scope_request", "user", "user_01")

	wrongTenant := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", map[string]interface{}{
		"tenant_id": "tenant_02", "principal_type": "user", "principal_id": "user_01", "project_id": "project_02", "expected_project_id": "project_01", "requested_by": "user_01",
	}, "integration_token")
	if wrongTenant.Code != http.StatusNotFound || !jsonBodyHasCode(wrongTenant.Body, "model_access_key_not_found") {
		t.Fatalf("expected cross-tenant rebind to be hidden, got %d: %s", wrongTenant.Code, wrongTenant.Body)
	}
	stale := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", map[string]interface{}{
		"tenant_id": "tenant_01", "principal_type": "user", "principal_id": "user_01", "project_id": "project_02", "expected_project_id": "stale_project", "requested_by": "user_01",
	}, "integration_token")
	if stale.Code != http.StatusConflict || !jsonBodyHasCode(stale.Body, "model_access_key_project_conflict") {
		t.Fatalf("expected stale project expectation conflict, got %d: %s", stale.Code, stale.Body)
	}
}

func TestGatewayModelAccessKeyProjectRebindIsIdempotentAndRejectsRevokedKey(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_scope_project_02_idempotent", "project.created", "project", "project_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "project_02", "name": "Second Project", "ownerExternalId": "user_01",
	}))
	created := createGatewayModelAccessKeyForTest(t, app, "rebind_idempotent_request", "user", "user_01")
	input := map[string]interface{}{"tenant_id": "tenant_01", "principal_type": "user", "principal_id": "user_01", "project_id": "project_02", "expected_project_id": "project_01", "requested_by": "user_01"}
	first := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", input, "integration_token")
	second := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", input, "integration_token")
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body != second.Body {
		t.Fatalf("expected idempotent rebind response, first=%d second=%d", first.Code, second.Code)
	}
	var auditCount int64
	if err := store.db.Model(&AuditEvent{}).Where("action = ? AND resource_id = ?", gatewayModelAccessKeyProjectRebindAction, created.Data.ID).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("retry duplicated the transition audit: %d", auditCount)
	}
	stale := gatewayModelAccessKeyRebindPayload()
	stale["expected_project_id"] = "unknown_project"
	conflict := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", stale, "integration_token")
	if conflict.Code != http.StatusConflict || !jsonBodyHasCode(conflict.Body, "model_access_key_project_conflict") {
		t.Fatalf("unexpected stale no-op response: %d %s", conflict.Code, conflict.Body)
	}
	if _, err := store.RevokeGatewayModelAccessKey(created.Data.ID, "tenant_01", "user", "user_01", "test", "user_01"); err != nil {
		t.Fatal(err)
	}
	revoked := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", map[string]interface{}{"tenant_id": "tenant_01", "principal_type": "user", "principal_id": "user_01", "project_id": "project_01", "expected_project_id": "project_02", "requested_by": "user_01"}, "integration_token")
	if revoked.Code != http.StatusConflict || !jsonBodyHasCode(revoked.Body, "model_access_key_unavailable") {
		t.Fatalf("expected revoked rebind rejection, got %d: %s", revoked.Code, revoked.Body)
	}
}

func TestGatewayModelAccessKeyProjectRebindValidatesIdentityAndTarget(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		value  string
		mutate func(*testing.T, *GormStore)
		status int
		code   string
	}{
		{name: "missing expectation", field: "expected_project_id", status: 400, code: "invalid_model_access_key_project"},
		{name: "missing principal", field: "principal_id", status: 400, code: "invalid_model_access_key_project"},
		{name: "workload principal", field: "principal_type", value: "application", status: 400, code: "invalid_model_access_key_project"},
		{name: "different actor", field: "requested_by", value: "user_02", status: 403, code: "model_access_key_actor_mismatch"},
		{name: "different owner", field: "principal_id", value: "user_02", status: 404, code: "model_access_key_not_found"},
		{name: "unknown target", field: "project_id", value: "missing", status: 409, code: "gateway_project_unavailable"},
		{name: "target in another tenant", field: "project_id", value: "foreign_project", mutate: func(t *testing.T, store *GormStore) {
			if err := store.db.Create(&GatewayTenant{ID: "foreign_tenant", ExternalTenantID: "tenant_02", Status: StatusActive}).Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Create(&GatewayProject{ID: "foreign_project", TenantID: "foreign_tenant", ExternalProjectID: "foreign_project", Status: StatusActive}).Error; err != nil {
				t.Fatal(err)
			}
		}, status: 409, code: "gateway_project_unavailable"},
		{name: "inactive tenant", mutate: func(t *testing.T, store *GormStore) {
			if err := store.db.Model(&GatewayTenant{}).Where("external_tenant_id = ?", "tenant_01").Update("status", StatusDisabled).Error; err != nil {
				t.Fatal(err)
			}
		}, status: 409, code: "gateway_tenant_unavailable"},
		{name: "expired key", mutate: func(t *testing.T, store *GormStore) {
			if err := store.db.Model(&APIKey{}).Where("managed_by = ?", gatewayModelAccessKeyManagedBy).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
		}, status: 409, code: "model_access_key_unavailable"},
		{name: "disabled target", mutate: func(t *testing.T, store *GormStore) {
			if err := store.db.Model(&GatewayProject{}).Where("external_project_id = ?", "project_02").Update("status", StatusDisabled).Error; err != nil {
				t.Fatal(err)
			}
		}, status: 409, code: "gateway_project_unavailable"},
		{name: "inactive principal", mutate: func(t *testing.T, store *GormStore) {
			if err := store.db.Model(&GatewayPrincipal{}).Where("external_principal_id = ?", "user_01").Update("status", StatusDisabled).Error; err != nil {
				t.Fatal(err)
			}
		}, status: 409, code: "gateway_principal_unavailable"},
		{name: "organization membership missing", mutate: func(t *testing.T, store *GormStore) {
			var tenant GatewayTenant
			if err := store.db.First(&tenant, "external_tenant_id = ?", "tenant_01").Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Create(&GatewayOrganization{ID: "org_target", TenantID: tenant.ID, ExternalOrganizationID: "org_target", Status: StatusActive}).Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Model(&GatewayProject{}).Where("external_project_id = ?", "project_02").Update("organization_id", "org_target").Error; err != nil {
				t.Fatal(err)
			}
		}, status: 409, code: "gateway_organization_membership_unavailable"},
		{name: "organization inactive", mutate: func(t *testing.T, store *GormStore) {
			var tenant GatewayTenant
			if err := store.db.First(&tenant, "external_tenant_id = ?", "tenant_01").Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Create(&GatewayOrganization{ID: "org_target", TenantID: tenant.ID, ExternalOrganizationID: "org_target", Status: StatusDisabled}).Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Model(&GatewayProject{}).Where("external_project_id = ?", "project_02").Update("organization_id", "org_target").Error; err != nil {
				t.Fatal(err)
			}
		}, status: 409, code: "gateway_organization_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, app, created := newGatewayModelAccessKeyRebindFixture(t)
			if test.mutate != nil {
				test.mutate(t, store)
			}
			input := gatewayModelAccessKeyRebindPayload()
			if test.field != "" {
				input[test.field] = test.value
			}
			if test.name == "different owner" {
				input["requested_by"] = "user_02"
			}
			response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", input, "integration_token")
			if response.Code != test.status || !jsonBodyHasCode(response.Body, test.code) {
				t.Fatalf("expected %d %s, got %d: %s", test.status, test.code, response.Code, response.Body)
			}
			var key APIKey
			if err := store.db.First(&key, "id = ?", created.Data.ID).Error; err != nil {
				t.Fatal(err)
			}
			if key.ProjectExternalID != "project_01" {
				t.Fatalf("failed rebind changed key project: %+v", key)
			}
		})
	}
}

func TestGatewayModelAccessKeyProjectRebindRequiresIntegrationTokenAndPost(t *testing.T) {
	_, app, created := newGatewayModelAccessKeyRebindFixture(t)
	path := "/api/internal/model-access-keys/" + created.Data.ID + "/project"
	for _, token := range []string{"", "admin_token", created.APIKey} {
		response := doJSON(t, app, http.MethodPost, path, gatewayModelAccessKeyRebindPayload(), token)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("expected dedicated integration token, got %d", response.Code)
		}
	}
	response := doJSON(t, app, http.MethodGet, path, nil, "integration_token")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected POST-only endpoint, got %d", response.Code)
	}
}

func newGatewayModelAccessKeyRebindFixture(t *testing.T) (*GormStore, http.Handler, gatewayModelAccessKeyCreateResponse) {
	t.Helper()
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{AdminToken: "admin_token", IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()
	seedGatewayModelAccessKeyScope(t, app)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_rebind_fixture_project", "project.created", "project", "project_02", "tenant_01", 1, map[string]interface{}{
		"externalId": "project_02", "name": "Second Project", "ownerExternalId": "user_01",
	}))
	return store, app, createGatewayModelAccessKeyForTest(t, app, "rebind_fixture_request", "user", "user_01")
}

func gatewayModelAccessKeyRebindPayload() map[string]interface{} {
	return map[string]interface{}{"tenant_id": "tenant_01", "principal_type": "user", "principal_id": "user_01", "project_id": "project_02", "expected_project_id": "project_01", "requested_by": "user_01"}
}
