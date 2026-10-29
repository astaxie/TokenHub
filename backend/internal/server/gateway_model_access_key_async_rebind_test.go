package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func addRebindJobModel(t *testing.T, store *GormStore, model, providerType string) {
	t.Helper()
	provider := store.AddProvider(Provider{ID: "prv_rebind_job", Name: "Rebind Job", Type: providerType, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_rebind_job", ProviderID: provider.ID, Name: "Rebind Job Resource", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_rebind_job", ModelName: model, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: model, Status: StatusActive, Priority: 1, Weight: 100})
}

func createRebindResponseJob(t *testing.T, store *GormStore, created gatewayModelAccessKeyCreateResponse) ResponseJob {
	t.Helper()
	project, key, err := store.ValidateAPIKey(created.APIKey, "")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"rebind-model","input":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateResponseJob(ResponseJob{ProjectID: project.ID, APIKeyID: key.ID, AttributedUserID: usageAttributionUserID(key, project), Model: "rebind-model"}, payload)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func rebindJobKey(t *testing.T, store *GormStore, id string) {
	t.Helper()
	_, err := store.RebindGatewayModelAccessKeyProject(id, GatewayModelAccessKeyProjectInput{TenantExternalID: "tenant_01", PrincipalType: "user", PrincipalExternalID: "user_01", ProjectExternalID: "project_02", ExpectedProjectExternalID: "project_01", RequestedBy: "user_01"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGatewayModelAccessKeyRebindKeepsResponseReadAndCancel(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	addRebindJobModel(t, store, "rebind-model", ProviderMock)
	job := createRebindResponseJob(t, store, created)
	for _, rebound := range []bool{false, true} {
		if rebound {
			rebindJobKey(t, store, created.Data.ID)
		}
		response := doJSON(t, app, http.MethodGet, "/v1/responses/"+job.ID, nil, created.APIKey)
		if response.Code != http.StatusOK {
			t.Fatalf("read after rebind=%v: %d %s", rebound, response.Code, response.Body)
		}
	}
	other := createGatewayModelAccessKeyForTest(t, app, "other_response_owner", "user", "user_01")
	for _, secret := range []string{other.APIKey, "invalid-key"} {
		response := doJSON(t, app, http.MethodPost, "/v1/responses/"+job.ID+"/cancel", nil, secret)
		if response.Code == http.StatusOK {
			t.Fatal("another credential cancelled the response")
		}
	}
	response := doJSON(t, app, http.MethodPost, "/v1/responses/"+job.ID+"/cancel", nil, created.APIKey)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel after rebind: %d %s", response.Code, response.Body)
	}
	persisted, _, err := store.GetResponseJob(job.ID)
	if err != nil || persisted.ProjectID != job.ProjectID || persisted.APIKeyID != job.APIKeyID || persisted.Status != responseJobStatusCancelled {
		t.Fatalf("cancel changed original identity: %+v %v", persisted, err)
	}
}

func TestGatewayModelAccessKeyRebindAsyncReadsUseOriginalProjectWhenCurrentArchived(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	addRebindJobModel(t, store, "rebind-model", ProviderMock)
	job := createRebindResponseJob(t, store, created)
	imageJob, err := store.CreateImageJob(ImageJob{ProjectID: job.ProjectID, APIKeyID: job.APIKeyID, AttributedUserID: job.AttributedUserID, Model: job.Model}, "draw")
	if err != nil {
		t.Fatal(err)
	}
	other := createGatewayModelAccessKeyForTest(t, app, "other_archived_project_key", "user", "user_01")
	rebindJobKey(t, store, created.Data.ID)
	rebindJobKey(t, store, other.Data.ID)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_async_new_archived", "project.archived", "project", "project_02", "tenant_01", 2, map[string]interface{}{"externalId": "project_02"}))
	for _, path := range []string{"/v1/responses/" + job.ID, "/v1/image-jobs/" + imageJob.ID} {
		response := doJSON(t, app, http.MethodGet, path, nil, created.APIKey)
		if response.Code != http.StatusOK {
			t.Fatalf("current project state blocked original job: %s %d %s", path, response.Code, response.Body)
		}
		response = doJSON(t, app, http.MethodGet, path, nil, other.APIKey)
		if response.Code == http.StatusOK {
			t.Fatalf("another key read original job: %s", path)
		}
	}
	response := doJSON(t, app, http.MethodPost, "/v1/responses/"+job.ID+"/cancel", nil, created.APIKey)
	if response.Code != http.StatusOK {
		t.Fatalf("current project state blocked original cancellation: %d %s", response.Code, response.Body)
	}
	response = doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{"model": "rebind-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}}, created.APIKey)
	if response.Code == http.StatusOK {
		t.Fatal("new call bypassed the current archived project")
	}
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_async_original_archived", "project.archived", "project", "project_01", "tenant_01", 2, map[string]interface{}{"externalId": "project_01"}))
	for _, path := range []string{"/v1/responses/" + job.ID, "/v1/image-jobs/" + imageJob.ID} {
		response := doJSON(t, app, http.MethodGet, path, nil, created.APIKey)
		if response.Code == http.StatusOK {
			t.Fatalf("original project state was bypassed: %s", path)
		}
	}
}

func TestGatewayModelAccessKeyRebindQueuedResponseUsesOriginalProject(t *testing.T) {
	store, _, created := newGatewayModelAccessKeyRebindFixture(t)
	addRebindJobModel(t, store, "rebind-model", ProviderMock)
	job := createRebindResponseJob(t, store, created)
	rebindJobKey(t, store, created.Data.ID)
	// A restriction on the new project must not be substituted for the job's
	// original project when the durable queue admits it.
	current, _ := store.GetAPIKey(created.Data.ID)
	if err := store.db.Model(&Project{}).Where("id = ?", current.ProjectID).Updates(map[string]any{"model_access_mode": ModelAccessModeRestricted, "allowed_models": "[]"}).Error; err != nil {
		t.Fatal(err)
	}
	config := responseJobTestConfig()
	config.SecretKey = "test_secret"
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	claimed, ok, err := store.ClaimResponseJob("rebind-worker", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	server.processResponseJob(claimed, "rebind-worker", time.Second, time.Minute)
	final, _, err := store.GetResponseJob(job.ID)
	if err != nil || final.Status != responseJobStatusSucceeded {
		t.Fatalf("queued response after rebind: %+v %v", final, err)
	}
	logs := store.ListRequestLogs()
	records := store.ListUsageRecords()
	if len(logs) != 1 || len(records) != 1 || logs[0].ProjectID != job.ProjectID || records[0].ProjectID != job.ProjectID || final.ProjectID != job.ProjectID {
		t.Fatalf("original scope lost: job=%+v logs=%+v usage=%+v", final, logs, records)
	}
}

func TestGatewayModelAccessKeyRebindKeepsImageReadAndProjectSharing(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	addRebindJobModel(t, store, openAIImageModelName, ProviderOpenAI)
	job, err := store.CreateImageJob(ImageJob{ProjectID: created.Data.ProjectID, APIKeyID: created.Data.ID, Status: imageJobStatusQueued, Model: openAIImageModelName}, "draw")
	if err != nil {
		t.Fatal(err)
	}
	other := createGatewayModelAccessKeyForTest(t, app, "other_image_owner", "user", "user_01")
	response := doJSON(t, app, http.MethodGet, "/v1/image-jobs/"+job.ID, nil, other.APIKey)
	if response.Code != http.StatusOK {
		t.Fatalf("same-project image sharing changed: %d %s", response.Code, response.Body)
	}
	rebindJobKey(t, store, created.Data.ID)
	rebindJobKey(t, store, other.Data.ID)
	response = doJSON(t, app, http.MethodGet, "/v1/image-jobs/"+job.ID, nil, other.APIKey)
	if response.Code != http.StatusNotFound {
		t.Fatalf("another key read cross-project image job: %d %s", response.Code, response.Body)
	}
	response = doJSON(t, app, http.MethodGet, "/v1/image-jobs/"+job.ID, nil, created.APIKey)
	if response.Code != http.StatusOK {
		t.Fatalf("image read after rebind: %d %s", response.Code, response.Body)
	}
}

type imageReadRebindStore struct {
	Store
	beforeValidate func()
}

func (s imageReadRebindStore) ValidateAPIKey(secret, clientIP string) (Project, APIKey, error) {
	s.beforeValidate()
	return s.Store.ValidateAPIKey(secret, clientIP)
}

func TestGatewayModelAccessKeyRebindImageSharingRejectsProjectChangeDuringAuthentication(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	job, err := store.CreateImageJob(ImageJob{ProjectID: created.Data.ProjectID, APIKeyID: created.Data.ID, Model: openAIImageModelName}, "draw")
	if err != nil {
		t.Fatal(err)
	}
	reader := createGatewayModelAccessKeyForTest(t, app, "image_reader_rebind_race", "user", "user_01")
	wrapped := imageReadRebindStore{Store: store, beforeValidate: func() { rebindJobKey(t, store, reader.Data.ID) }}
	server := NewWithConfig(wrapped, Config{SecretKey: "test_secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	response := doJSON(t, server.Handler(), http.MethodGet, "/v1/image-jobs/"+job.ID, nil, reader.APIKey)
	if response.Code != http.StatusNotFound {
		t.Fatalf("shared image used a different authenticated project: %d %s", response.Code, response.Body)
	}
}

func TestGatewayModelAccessKeyRebindImageExecutionKeepsScopeAndRechecksAuthorization(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "original_project_archived"}[disabled], func(t *testing.T) {
			store, app, created := newGatewayModelAccessKeyRebindFixture(t)
			addRebindJobModel(t, store, openAIImageModelName, ProviderOpenAI)
			server := NewWithConfig(store, Config{SecretKey: "test_secret", ImageStorageDir: t.TempDir()})
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			project, key, err := store.ValidateAPIKey(created.APIKey, "")
			if err != nil {
				t.Fatal(err)
			}
			job, call, err := store.CreateImageJobWithAdmission(context.Background(), project, key, openAIImageModelName, 1, ImageJob{ProjectID: project.ID, APIKeyID: key.ID, Model: openAIImageModelName, Status: imageJobStatusQueued, Action: "generate"}, "draw")
			if err != nil {
				t.Fatal(err)
			}
			rebindJobKey(t, store, key.ID)
			if disabled {
				applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_image_old_archived", "project.archived", "project", "project_01", "tenant_01", 2, map[string]interface{}{"externalId": "project_01"}))
			}
			invoked := false
			server.imageRunner = func(_ context.Context, _ RouteSelection, actual ImageJob) ([]byte, string, Usage, error) {
				invoked = true
				if actual.ProjectID != project.ID || actual.APIKeyID != key.ID {
					t.Fatal("image execution changed original identity")
				}
				return realPNGFixture(t), "", Usage{TotalTokens: 3}, nil
			}
			server.processImageJob(imageJobWork{job: job, call: call})
			final, _ := store.GetImageJob(job.ID)
			if disabled {
				if invoked || final.Status != imageJobStatusFailed || final.ErrorCode != "image_authorization_lost" {
					t.Fatalf("archived project image dispatched: invoked=%v job=%+v", invoked, final)
				}
			} else if !invoked || final.Status != imageJobStatusCompleted {
				t.Fatalf("image did not complete after rebind: invoked=%v job=%+v", invoked, final)
			}
			for _, log := range store.ListRequestLogs() {
				if log.ProjectID != project.ID || log.APIKeyID != key.ID {
					t.Fatalf("image settlement changed original identity: %+v", log)
				}
			}
		})
	}
}
