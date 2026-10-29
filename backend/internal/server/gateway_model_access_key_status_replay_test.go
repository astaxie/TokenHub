package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGatewayModelAccessKeyArchivedProjectExposesConfiguredStatus(t *testing.T) {
	for _, status := range []string{StatusActive, StatusDisabled} {
		t.Run(status, func(t *testing.T) {
			store, app, created := newGatewayModelAccessKeyRebindFixture(t)
			if err := store.db.Model(&APIKey{}).Where("id = ?", created.Data.ID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_key_old_archived", "project.archived", "project", "project_01", "tenant_01", 2, map[string]interface{}{"externalId": "project_01"}))
			response := doJSON(t, app, http.MethodGet, "/api/internal/model-access-keys?tenant_id=tenant_01&status=disabled", nil, "integration_token")
			var page struct {
				Data []map[string]any `json:"data"`
			}
			if err := json.Unmarshal([]byte(response.Body), &page); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || len(page.Data) != 1 || page.Data[0]["status"] != StatusDisabled || page.Data[0]["configured_status"] != status {
				t.Fatalf("raw/effective status missing: %d %s", response.Code, response.Body)
			}
			response = doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", gatewayModelAccessKeyRebindPayload(), "integration_token")
			if status == StatusActive {
				if response.Code != http.StatusOK {
					t.Fatalf("raw-active key could not leave archived project: %d %s", response.Code, response.Body)
				}
				assertGatewayModelAccessKeyActive(t, store, created.APIKey)
			} else if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "model_access_key_unavailable") {
				t.Fatalf("raw-disabled key could rebind: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestGatewayModelAccessKeyCreateReplayRejectsReboundProject(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	rebindJobKey(t, store, created.Data.ID)
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys", map[string]interface{}{
		"request_id": "rebind_fixture_request", "tenant_id": "tenant_01", "project_id": "project_01",
		"principal_type": "user", "principal_id": "user_01", "name": "rebind_fixture_request",
	}, "integration_token")
	if response.Code != http.StatusConflict || !jsonBodyHasCode(response.Body, "model_access_key_request_project_changed") {
		t.Fatalf("stale create replay returned different project: %d %s", response.Code, response.Body)
	}
	key, ok := store.GetAPIKey(created.Data.ID)
	if !ok || key.ProjectExternalID != "project_02" {
		t.Fatalf("create replay changed the current credential: %+v", key)
	}
	assertGatewayModelAccessKeyActive(t, store, created.APIKey)
}
