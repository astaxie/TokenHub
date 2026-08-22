package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAdminReconciliationMethodRoutesPreserveTrailingSuccess(t *testing.T) {
	store, app, connector := newMethodRoutingReconciliationServer(t, 0)
	rule := createMethodRoutingReconciliationRule(t, app, connector.ID)
	rulePath := "/api/admin/billing/reconciliation-rules/" + rule.ID

	item := methodRoutingRequest(app, http.MethodGet, rulePath+"/", "dev_admin_token")
	if item.Code != http.StatusOK || !strings.Contains(item.Body.String(), rule.ID) {
		t.Fatalf("get trailing rule: status=%d body=%s", item.Code, item.Body.String())
	}
	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, rulePath+"/", map[string]any{"name": "Trailing Reconciliation Rule"}, "dev_admin_token")
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), "Trailing Reconciliation Rule") {
		t.Fatalf("patch trailing rule: status=%d body=%s", patched.Code, patched.Body.String())
	}

	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	runResponse := methodRoutingJSONRequest(t, app, http.MethodPost, rulePath+"/run/", map[string]any{
		"period_start": periodStart.Format(time.RFC3339),
		"period_end":   periodStart.Add(time.Hour).Format(time.RFC3339),
	}, "dev_admin_token")
	if runResponse.Code != http.StatusCreated {
		t.Fatalf("run trailing rule: status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	var run ReconciliationRun
	if err := json.NewDecoder(runResponse.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	runPath := "/api/admin/billing/reconciliations/" + run.ID

	detail := methodRoutingRequest(app, http.MethodGet, runPath+"/", "dev_admin_token")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), run.ID) {
		t.Fatalf("get trailing run: status=%d body=%s", detail.Code, detail.Body.String())
	}
	recalculated := methodRoutingJSONRequest(t, app, http.MethodPost, runPath+"/recalculate/", map[string]any{}, "dev_admin_token")
	if recalculated.Code != http.StatusOK || !strings.Contains(recalculated.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("recalculate trailing run: status=%d body=%s", recalculated.Code, recalculated.Body.String())
	}
	exported := methodRoutingRequest(app, http.MethodGet, runPath+"/export/", "dev_admin_token")
	if exported.Code != http.StatusOK || !strings.HasPrefix(exported.Header().Get("content-type"), "text/csv") {
		t.Fatalf("export trailing run: status=%d headers=%v body=%s", exported.Code, exported.Header(), exported.Body.String())
	}
	locked := methodRoutingJSONRequest(t, app, http.MethodPost, runPath+"/lock/", map[string]any{}, "dev_admin_token")
	if locked.Code != http.StatusOK || !strings.Contains(locked.Body.String(), `"locked_at"`) {
		t.Fatalf("lock trailing run: status=%d body=%s", locked.Code, locked.Body.String())
	}
	blocked := methodRoutingJSONRequest(t, app, http.MethodPost, runPath+"/recalculate/", map[string]any{}, "dev_admin_token")
	assertJSONError(t, blocked, http.StatusConflict, "reconciliation_run_locked")

	for _, action := range []string{"update", "reconcile", "recalculate", "export", "lock"} {
		if !hasReconciliationPathRoutingAudit(store.ListAuditEvents(), action) {
			t.Fatalf("missing %s audit for trailing reconciliation route", action)
		}
	}
}

func TestAdminReconciliationMethodRoutesPreserveEncodedPathFailures(t *testing.T) {
	store, app, connector := newMethodRoutingReconciliationServer(t, 0)
	rule := createMethodRoutingReconciliationRule(t, app, connector.ID)
	storedRule, err := store.GetReconciliationRule(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedRule.ID = "reconciliation/encoded-rule"
	storedRule.Name = "Encoded Reconciliation Rule"
	storedRule.CreatedAt = time.Time{}
	storedRule.UpdatedAt = time.Time{}
	storedRule.LastRunAt = nil
	storedRule.NextRunAt = nil
	if _, err := store.CreateReconciliationRule(storedRule); err != nil {
		t.Fatal(err)
	}

	encodedRulePath := "/api/admin/billing/reconciliation-rules/" + url.PathEscape(storedRule.ID)
	for _, test := range []struct {
		name       string
		method     string
		path       string
		wantCode   string
		wantStatus int
	}{
		{name: "allowed item method", method: http.MethodGet, path: encodedRulePath, wantCode: "reconciliation_action_not_found", wantStatus: http.StatusNotFound},
		{name: "head item method", method: http.MethodHead, path: encodedRulePath, wantCode: "reconciliation_action_not_found", wantStatus: http.StatusNotFound},
		{name: "wrong item method", method: http.MethodDelete, path: encodedRulePath, wantCode: "reconciliation_action_not_found", wantStatus: http.StatusNotFound},
		{name: "run method", method: http.MethodPost, path: encodedRulePath + "/run", wantCode: "reconciliation_rule_not_found", wantStatus: http.StatusNotFound},
	} {
		t.Run("rule/"+test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "dev_admin_token")
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, "")
		})
	}

	encodedRun := ReconciliationRun{ID: "reconciliation/encoded-run", RuleID: rule.ID, Status: ReconciliationRunSucceeded}
	if err := store.db.Create(&encodedRun).Error; err != nil {
		t.Fatal(err)
	}
	encodedRunPath := "/api/admin/billing/reconciliations/" + url.PathEscape(encodedRun.ID)
	for _, test := range []struct {
		name       string
		method     string
		path       string
		wantCode   string
		wantStatus int
	}{
		{name: "allowed item method", method: http.MethodGet, path: encodedRunPath, wantCode: "reconciliation_action_not_found", wantStatus: http.StatusNotFound},
		{name: "head item method", method: http.MethodHead, path: encodedRunPath, wantCode: "reconciliation_action_not_found", wantStatus: http.StatusNotFound},
		{name: "wrong item method", method: http.MethodDelete, path: encodedRunPath, wantCode: "reconciliation_action_not_found", wantStatus: http.StatusNotFound},
		{name: "lock method", method: http.MethodPost, path: encodedRunPath + "/lock", wantCode: "reconciliation_run_not_found", wantStatus: http.StatusNotFound},
		{name: "wrong lock method", method: http.MethodGet, path: encodedRunPath + "/lock", wantCode: "reconciliation_run_not_found", wantStatus: http.StatusNotFound},
	} {
		t.Run("run/"+test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "dev_admin_token")
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, "")
		})
	}

	unauthorized := methodRoutingRequest(app, http.MethodDelete, encodedRunPath, "")
	assertJSONError(t, unauthorized, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, unauthorized, "")
}

func hasReconciliationPathRoutingAudit(events []AuditEvent, action string) bool {
	for _, event := range events {
		if event.Action == action && (event.ResourceType == "reconciliation_rule" || event.ResourceType == "billing_reconciliation") {
			return true
		}
	}
	return false
}
