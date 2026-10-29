package server

import (
	"context"
	"net/http"
	"testing"
)

func TestGatewayModelAccessKeyImageAdmissionUsesCommittedProjectAfterRebind(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	addRebindJobModel(t, store, openAIImageModelName, ProviderOpenAI)
	server := NewWithConfig(store, Config{SecretKey: "test_secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	originalReader := createGatewayModelAccessKeyForTest(t, app, "image_original_project_reader", "user", "user_01")
	currentReader := createGatewayModelAccessKeyForTest(t, app, "image_current_project_reader", "user", "user_01")
	rebindJobKey(t, store, currentReader.Data.ID)
	// Keep the request's authenticated snapshot while the credential changes
	// project before the transaction that admits and persists its image job.
	originalProject, originalKey, err := store.ValidateAPIKey(created.APIKey, "")
	if err != nil {
		t.Fatal(err)
	}
	rebindJobKey(t, store, created.Data.ID)
	currentProject, currentKey, err := store.ValidateAPIKey(created.APIKey, "")
	if err != nil {
		t.Fatal(err)
	}
	job, call, err := store.CreateImageJobWithAdmission(context.Background(), originalProject, originalKey, openAIImageModelName, 1, ImageJob{
		ProjectID: originalProject.ID, APIKeyID: originalKey.ID, Model: openAIImageModelName,
		Status: imageJobStatusQueued, Action: "generate",
	}, "draw")
	if err != nil {
		t.Fatal(err)
	}
	if call.Project.ID != currentProject.ID || call.Key.ProjectID != currentProject.ID {
		t.Fatalf("image admission did not use the committed project: project=%s key_project=%s current=%s", call.Project.ID, call.Key.ProjectID, currentProject.ID)
	}
	if job.ProjectID != call.Project.ID || job.APIKeyID != call.Key.ID || job.AttributedUserID != call.AttributedUserID {
		t.Fatalf("image job scope differs from admission: job_project=%s call_project=%s job_key=%s call_key=%s job_user=%s call_user=%s", job.ProjectID, call.Project.ID, job.APIKeyID, call.Key.ID, job.AttributedUserID, call.AttributedUserID)
	}
	persisted, ok := store.GetImageJob(job.ID)
	if !ok || persisted.ProjectID != currentProject.ID || persisted.APIKeyID != currentKey.ID {
		t.Fatalf("persisted image job retained the authentication snapshot: %+v", persisted)
	}
	for _, reader := range []struct {
		secret string
		status int
	}{
		{originalReader.APIKey, http.StatusNotFound},
		{currentReader.APIKey, http.StatusOK},
		{created.APIKey, http.StatusOK},
	} {
		response := doJSON(t, app, http.MethodGet, "/v1/image-jobs/"+job.ID, nil, reader.secret)
		if response.Code != reader.status {
			t.Fatalf("image sharing used the wrong project: got %d, want %d: %s", response.Code, reader.status, response.Body)
		}
	}
	invoked := false
	server.imageRunner = func(_ context.Context, _ RouteSelection, actual ImageJob) ([]byte, string, Usage, error) {
		invoked = true
		if actual.ProjectID != currentProject.ID || actual.APIKeyID != currentKey.ID {
			t.Fatalf("image execution used the wrong identity: project=%s key=%s", actual.ProjectID, actual.APIKeyID)
		}
		return realPNGFixture(t), "", Usage{TotalTokens: 3}, nil
	}
	server.processImageJob(imageJobWork{job: job, call: call})
	final, ok := store.GetImageJob(job.ID)
	if !invoked || !ok || final.Status != imageJobStatusCompleted || final.ProjectID != currentProject.ID {
		t.Fatalf("image execution did not retain admission scope: invoked=%v job=%+v", invoked, final)
	}
	logs, records := store.ListRequestLogs(), store.ListUsageRecords()
	if len(logs) != 1 || len(records) != 1 || logs[0].ProjectID != currentProject.ID || records[0].ProjectID != currentProject.ID || logs[0].APIKeyID != currentKey.ID || records[0].APIKeyID != currentKey.ID {
		t.Fatalf("image settlement differs from admission: logs=%+v usage=%+v", logs, records)
	}
}
