package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestGatewayModelAccessKeyProjectRebindPreservesInFlightScopeAndCumulativeQuota(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	var privateKey APIKey
	if err := store.db.First(&privateKey, "id = ?", created.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	privateKey.Limits = QuotaLimits{DailyRequests: 2, MonthlyRequests: 2, MaxConcurrency: 2, TokenLimitTPM: 100}
	tokens := int64(100)
	privateKey.TokenLimitTPM = &tokens
	if err := store.db.Save(&privateKey).Error; err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "rebind-model", Status: StatusActive})
	project, key, err := store.ValidateAPIKey(created.APIKey, "")
	if err != nil {
		t.Fatal(err)
	}
	oldCall, err := store.StartCall(context.Background(), project, key, "rebind-model", 5)
	if err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+key.ID+"/project", gatewayModelAccessKeyRebindPayload(), "integration_token")
	if response.Code != http.StatusOK {
		t.Fatalf("expected rebind, got %d: %s", response.Code, response.Body)
	}
	var activeLeases int64
	if err := store.db.Model(&InFlightLease{}).Where("scope_type = ? AND scope_id = ?", "api_key", key.ID).Count(&activeLeases).Error; err != nil {
		t.Fatal(err)
	}
	if activeLeases != 1 {
		t.Fatalf("rebind changed the admitted concurrency lease count: %d", activeLeases)
	}
	newProject, newKey, err := store.ValidateAPIKey(created.APIKey, "")
	if err != nil {
		t.Fatal(err)
	}
	if newProject.ID == project.ID || newKey.ProjectExternalID != "project_02" {
		t.Fatalf("authentication returned stale scope: %+v", newKey)
	}
	newCall, err := store.StartCall(context.Background(), newProject, newKey, "rebind-model", 7)
	if err != nil {
		t.Fatal(err)
	}
	if oldCall.Project.ID != project.ID || newCall.Project.ID != newProject.ID {
		t.Fatal("admission did not retain each request project")
	}
	store.FinishCall(oldCall, RouteSelection{}, Usage{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5}, 200, "", "", "")
	store.FinishCall(newCall, RouteSelection{}, Usage{PromptTokens: 6, CompletionTokens: 1, TotalTokens: 7}, 200, "", "", "")
	for _, call := range []CallContext{oldCall, newCall} {
		var usage UsageRecord
		if err := store.db.First(&usage, "request_id = ?", call.RequestID).Error; err != nil {
			t.Fatal(err)
		}
		var log RequestLog
		if err := store.db.First(&log, "request_id = ?", call.RequestID).Error; err != nil {
			t.Fatal(err)
		}
		if usage.ProjectID != call.Project.ID || log.ProjectID != call.Project.ID {
			t.Fatalf("settlement changed request project: usage=%+v log=%+v", usage, log)
		}
	}
	_, err = store.StartCall(context.Background(), newProject, newKey, "rebind-model", 1)
	if err == nil || AsHTTPError(err).Code != ErrQuotaExceeded.Code {
		t.Fatalf("expected cumulative key quota to remain exhausted, got %v", err)
	}
	var daily QuotaBucket
	if err := store.db.First(&daily, "key_id = ? AND scope = ? AND attributed_user_id = ?", key.ID, "day", unattributedQuotaUserID).Error; err != nil {
		t.Fatal(err)
	}
	if daily.Requests != 2 || daily.TotalTokens != 12 {
		t.Fatalf("rebind reset or split key quota: %+v", daily)
	}
}

func TestGatewayModelAccessKeyHistoryUsesRecordedProject(t *testing.T) {
	store, _, created := newGatewayModelAccessKeyRebindFixture(t)
	now := time.Now().UTC()
	oldID := created.Data.ProjectID
	var target GatewayProject
	if err := store.db.First(&target, "external_project_id = ?", "project_02").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&APIKey{}).Where("id = ?", created.Data.ID).Updates(map[string]any{"project_id": target.ID, "project_external_id": target.ExternalProjectID}).Error; err != nil {
		t.Fatal(err)
	}
	for i, projectID := range []string{oldID, target.ID} {
		id := []string{"old_history", "new_history"}[i]
		if err := store.db.Create(&UsageRecord{ID: id, RequestID: id, ProjectID: projectID, APIKeyID: created.Data.ID, ModelName: "history-model", TotalTokens: int64(i+1) * 10, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&RequestLog{ID: id, RequestID: id, ProjectID: projectID, APIKeyID: created.Data.ID, ModelName: "history-model", StatusCode: 200, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, externalID := range []string{"project_01", "project_02"} {
		report, err := store.GetGatewayUsage(GatewayUsageFilter{TenantExternalID: "tenant_01", ProjectExternalID: externalID, DateFrom: now.Add(-time.Hour), DateTo: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if report.Summary.TotalTokens != int64(i+1)*10 || report.Summary.RequestCount != 1 || len(report.Breakdown.Projects) != 1 || report.Breakdown.Projects[0].ID != externalID {
			t.Fatalf("history attributed to current key project: %+v", report)
		}
		logs, err := store.ListGatewayRequestLogs(GatewayRequestLogFilter{TenantExternalID: "tenant_01", ProjectExternalID: externalID})
		if err != nil {
			t.Fatal(err)
		}
		if logs.Total != 1 || len(logs.Items) != 1 || logs.Items[0].ProjectExternalID != externalID {
			t.Fatalf("log history attributed to current key project: %+v", logs)
		}
	}
}

func TestGatewayModelAccessKeyProjectRebindRollsBackWithoutAudit(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	const callback = "test:reject_rebind_audit"
	if err := store.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_events" {
			_ = tx.AddError(errors.New("audit unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.db.Callback().Create().Remove(callback); err != nil {
			t.Error(err)
		}
	})
	response := doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", gatewayModelAccessKeyRebindPayload(), "integration_token")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected audit failure to reject rebind, got %d: %s", response.Code, response.Body)
	}
	var key APIKey
	if err := store.db.First(&key, "id = ?", created.Data.ID).Error; err != nil {
		t.Fatal(err)
	}
	if key.ProjectExternalID != "project_01" {
		t.Fatalf("audit failure committed key scope: %+v", key)
	}
}

func TestGatewayModelAccessKeyProjectRebindSerializesConflictingRequests(t *testing.T) {
	store, app, created := newGatewayModelAccessKeyRebindFixture(t)
	applyGatewayIntegrationEventForTest(t, app, gatewayIntegrationEvent("evt_rebind_third_project", "project.created", "project", "project_03", "tenant_01", 1, map[string]interface{}{"externalId": "project_03", "name": "Third Project", "ownerExternalId": "user_01"}))
	start := make(chan struct{})
	results := make(chan responseBody, 2)
	for _, projectID := range []string{"project_02", "project_03"} {
		go func(target string) {
			<-start
			input := gatewayModelAccessKeyRebindPayload()
			input["project_id"] = target
			results <- doJSON(t, app, http.MethodPost, "/api/internal/model-access-keys/"+created.Data.ID+"/project", input, "integration_token")
		}(projectID)
	}
	close(start)
	counts := map[int]int{}
	for range 2 {
		response := <-results
		counts[response.Code]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("expected one winner and one conflict: %+v", counts)
	}
	var audits []AuditEvent
	if err := store.db.Where("action = ? AND resource_id = ?", "gateway_model_access_key_project_rebind", created.Data.ID).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected one atomic audit, got %d", len(audits))
	}
	var before, after map[string]string
	if err := json.Unmarshal([]byte(audits[0].BeforeSnapshot), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(audits[0].AfterSnapshot), &after); err != nil {
		t.Fatal(err)
	}
	if before["project_id"] != "project_01" || (after["project_id"] != "project_02" && after["project_id"] != "project_03") {
		t.Fatalf("audit lacks project transition: %+v", audits[0])
	}
}
