package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGatewayModelAccessKeyAsyncIdentityChecksRawSecretStatusAndIP(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	rebindJobKey(t, store, created.Data.ID)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_identity_project_archived", "project.archived", "project", "project_02", "tenant_01", 2, map[string]interface{}{"externalId": "project_02"}))
	if err := store.db.Exec("UPDATE api_keys SET ip_allowlist = ? WHERE id = ?", `["203.0.113.99"]`, created.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	key, err := store.AuthenticateAsyncJobAPIKey(created.APIKey, "203.0.113.99")
	if err != nil || key.ID != created.Data.ID || len(key.IPAllowlist) != 1 {
		t.Fatalf("valid raw identity was blocked by current project state: %+v %v", key, err)
	}
	if _, err := store.AuthenticateAsyncJobAPIKey(created.APIKey, "192.0.2.1"); err == nil || AsHTTPError(err).Code != ErrAPIKeyDisabled.Code {
		t.Fatalf("raw identity ignored IP restrictions: %v", err)
	}
	if _, err := store.AuthenticateAsyncJobAPIKey("wrong-secret", "203.0.113.99"); err == nil || AsHTTPError(err).Code != ErrInvalidAPIKey.Code {
		t.Fatalf("raw identity ignored secret hash: %v", err)
	}
	for _, status := range []string{StatusDisabled, StatusRevoked} {
		if err := store.db.Model(&APIKey{}).Where("id = ?", key.ID).Update("status", status).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := store.AuthenticateAsyncJobAPIKey(created.APIKey, "203.0.113.99"); err == nil || AsHTTPError(err).Code != ErrAPIKeyDisabled.Code {
			t.Fatalf("raw identity ignored %s status: %v", status, err)
		}
	}
	if err := store.db.Model(&APIKey{}).Where("id = ?", key.ID).Updates(map[string]any{"status": StatusActive, "expires_at": time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateAsyncJobAPIKey(created.APIKey, "203.0.113.99"); err == nil || AsHTTPError(err).Code != ErrAPIKeyExpired.Code {
		t.Fatalf("raw identity ignored expiry: %v", err)
	}
}

func TestGatewayModelAccessKeyRebindAsyncJobsRejectLostOriginalAuthorization(t *testing.T) {
	for _, change := range []string{"key_disabled", "key_revoked", "key_expired", "key_ip", "key_owner", "old_project", "old_organization", "old_membership", "old_tenant", "old_model"} {
		t.Run(change, func(t *testing.T) {
			store, app, created := newGatewayModelAccessKeyRebindFixture(t)
			addRebindJobModel(t, store, "rebind-model", ProviderMock)
			for _, event := range []map[string]interface{}{
				gatewayIntegrationEvent("evt_async_org", "organization.created", "organization", "org_01", "tenant_01", 1, map[string]interface{}{"externalId": "org_01", "name": "Original Organization"}),
				gatewayIntegrationEvent("evt_async_org_member", "organization_member.added", "organization_member", "org_membership_01", "tenant_01", 1, map[string]interface{}{"externalId": "org_membership_01", "principalExternalId": "user_01", "organizationExternalId": "org_01"}),
				gatewayIntegrationEvent("evt_async_project_org", "project.updated", "project", "project_01", "tenant_01", 2, map[string]interface{}{"externalId": "project_01", "organizationExternalId": "org_01"}),
			} {
				applyGatewayIntegrationEventForTest(t, app, event)
			}
			job := createRebindResponseJob(t, store, created)
			imageJob, err := store.CreateImageJob(ImageJob{ProjectID: job.ProjectID, APIKeyID: job.APIKeyID, AttributedUserID: job.AttributedUserID, Model: job.Model}, "draw")
			if err != nil {
				t.Fatal(err)
			}
			rebindJobKey(t, store, created.Data.ID)
			var mutationErr error
			switch change {
			case "key_disabled":
				mutationErr = store.db.Model(&APIKey{}).Where("id = ?", job.APIKeyID).Update("status", StatusDisabled).Error
			case "key_revoked":
				mutationErr = store.db.Model(&APIKey{}).Where("id = ?", job.APIKeyID).Update("status", StatusRevoked).Error
			case "key_expired":
				mutationErr = store.db.Model(&APIKey{}).Where("id = ?", job.APIKeyID).Update("expires_at", time.Now().Add(-time.Hour)).Error
			case "key_ip":
				mutationErr = store.db.Exec("UPDATE api_keys SET ip_allowlist = ? WHERE id = ?", `["203.0.113.99"]`, job.APIKeyID).Error
			case "key_owner":
				mutationErr = store.db.Model(&APIKey{}).Where("id = ?", job.APIKeyID).Update("owner_user_id", "changed-owner").Error
			case "old_project":
				applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_async_project_archived", "project.archived", "project", "project_01", "tenant_01", 3, map[string]interface{}{"externalId": "project_01"}))
			case "old_organization":
				applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_async_org_disabled", "organization.disabled", "organization", "org_01", "tenant_01", 2, map[string]interface{}{"externalId": "org_01", "status": StatusDisabled}))
			case "old_membership":
				applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_async_member_removed", "organization_member.removed", "organization_member", "org_membership_01", "tenant_01", 2, map[string]interface{}{"externalId": "org_membership_01", "principalExternalId": "user_01", "organizationExternalId": "org_01"}))
			case "old_tenant":
				mutationErr = store.db.Model(&GatewayProject{}).Where("id = ?", job.ProjectID).Update("tenant_id", "other-tenant").Error
			case "old_model":
				mutationErr = store.db.Model(&Project{}).Where("id = ?", job.ProjectID).Updates(map[string]any{"model_access_mode": ModelAccessModeRestricted, "allowed_models": "[]"}).Error
			}
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			if change == "key_ip" {
				key, ok := store.GetAPIKey(job.APIKeyID)
				if !ok || len(key.IPAllowlist) != 1 || key.IPAllowlist[0] != "203.0.113.99" {
					t.Fatalf("IP allowlist fixture was not stored: %+v", key.IPAllowlist)
				}
				request := httptest.NewRequest(http.MethodGet, "/v1/responses/"+job.ID, nil)
				request.RemoteAddr = "203.0.113.99:1234"
				request.Header.Set("Authorization", "Bearer "+created.APIKey)
				response := httptest.NewRecorder()
				app.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("matching IP lost access after rebind: %d %s", response.Code, response.Body)
				}
			}
			for _, path := range []string{"/v1/responses/" + job.ID, "/v1/image-jobs/" + imageJob.ID} {
				response := doJSON(t, app, http.MethodGet, path, nil, created.APIKey)
				if response.Code == http.StatusOK {
					t.Fatalf("lost authorization allowed read: %s %s", path, response.Body)
				}
			}
			config := responseJobTestConfig()
			config.SecretKey = "test_secret"
			server := NewWithConfig(store, config)
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			claimed, ok, err := store.ClaimResponseJob("authorization-worker", time.Second, time.Minute)
			if err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			server.processResponseJob(claimed, "authorization-worker", time.Second, time.Minute)
			final, _, err := store.GetResponseJob(job.ID)
			if err != nil || final.Status != responseJobStatusFailed || final.ErrorCode != "response_authorization_lost" || final.ProjectID != job.ProjectID {
				t.Fatalf("lost authorization executed response: %+v %v", final, err)
			}
		})
	}
}

func TestGatewayModelAccessKeyRebindResponseReadPreservesModelVisibility(t *testing.T) {
	for _, change := range []string{"model_disabled", "route_disabled", "original_project_route_scope", "original_project_policy"} {
		t.Run(change, func(t *testing.T) {
			store, app, created := newGatewayModelAccessKeyRebindFixture(t)
			addRebindJobModel(t, store, "rebind-model", ProviderMock)
			job := createRebindResponseJob(t, store, created)
			rebindJobKey(t, store, created.Data.ID)
			var err error
			switch change {
			case "model_disabled":
				err = store.db.Model(&Model{}).Where("name = ?", job.Model).Update("status", StatusDisabled).Error
			case "route_disabled":
				err = store.db.Model(&ModelRoute{}).Where("model_name = ?", job.Model).Update("status", StatusDisabled).Error
			case "original_project_route_scope":
				route := store.ListRoutes()[0]
				route.ProjectScope = RouteProjectScopeExclude
				route.ProjectIDs = []string{job.ProjectID}
				err = store.db.Save(&route).Error
			case "original_project_policy":
				_, err = store.CreateRoutingPolicy(scopedPolicyResource("pol_original_job", RoutingPolicyScopeProject, job.ProjectID, "unavailable-provider"))
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, request := range []struct{ method, path string }{{http.MethodGet, "/v1/responses/" + job.ID}, {http.MethodPost, "/v1/responses/" + job.ID + "/cancel"}} {
				response := doJSON(t, app, request.method, request.path, nil, created.APIKey)
				if response.Code != http.StatusNotFound {
					t.Fatalf("invisible original model allowed task access: %s %d %s", request.path, response.Code, response.Body)
				}
			}
		})
	}
}

func TestGatewayModelAccessKeyRebindQueuedResponseHonorsCumulativeQuota(t *testing.T) {
	store, _, created := newGatewayModelAccessKeyRebindFixture(t)
	addRebindJobModel(t, store, "rebind-model", ProviderMock)
	job := createRebindResponseJob(t, store, created)
	rebindJobKey(t, store, created.Data.ID)
	if err := store.db.Model(&APIKey{}).Where("id = ?", job.APIKeyID).Update("limit_daily_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	project, key, err := store.ValidateAPIKey(created.APIKey, "")
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, key, "rebind-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusOK, "", "", "")
	config := responseJobTestConfig()
	config.SecretKey = "test_secret"
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	claimed, ok, err := store.ClaimResponseJob("quota-worker", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	server.processResponseJob(claimed, "quota-worker", time.Second, time.Minute)
	final, _, err := store.GetResponseJob(job.ID)
	if err != nil || final.Status != responseJobStatusFailed || final.ErrorCode != "quota_exceeded" || final.ProjectID != job.ProjectID {
		t.Fatalf("queued job bypassed cumulative key quota: %+v %v", final, err)
	}
}
