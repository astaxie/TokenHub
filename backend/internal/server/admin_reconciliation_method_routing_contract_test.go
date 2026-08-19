package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type adminReconciliationMethodRoute struct {
	name           string
	path           string
	allowed        []string
	allow          string
	wantAdminCode  string
	wantAdminState int
}

func TestAdminReconciliationMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app, _ := newMethodRoutingReconciliationServer(t, 0)
	ordinaryToken := createAdminOperationMethodRoutingSession(t, store, "reconciliation-routing-user", "user")
	securityToken := createAdminOperationMethodRoutingSession(t, store, "reconciliation-routing-security", "security_admin")
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminReconciliationMethodRoutes() {
		wrongMethod := unsupportedAdminReconciliationMethods(route)[0]
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantCode   string
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized, wantCode: "invalid_admin_token"},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
			{name: "security_admin", token: securityToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
			{name: "admin", token: "dev_admin_token", wantStatus: route.wantAdminState, wantCode: route.wantAdminCode, wantAllow: route.allow},
		} {
			t.Run(route.name+"/"+auth.name, func(t *testing.T) {
				response := methodRoutingRequest(app, wrongMethod, route.path, auth.token)
				assertJSONError(t, response, auth.wantStatus, auth.wantCode)
				assertAllowHeader(t, response, auth.wantAllow)
			})
		}
	}

	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong methods wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminReconciliationMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	_, app, _ := newMethodRoutingReconciliationServer(t, 0)
	for _, route := range adminReconciliationMethodRoutes() {
		for _, method := range unsupportedAdminReconciliationMethods(route) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, "dev_admin_token")
				assertJSONError(t, response, route.wantAdminState, route.wantAdminCode)
				assertAllowHeader(t, response, route.allow)
			})
		}
	}
}

func TestAdminReconciliationMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app, _ := newMethodRoutingReconciliationServer(t, 0)
	ordinaryToken := createAdminOperationMethodRoutingSession(t, store, "reconciliation-head-user", "user")
	securityToken := createAdminOperationMethodRoutingSession(t, store, "reconciliation-head-security", "security_admin")
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminReconciliationMethodRoutes() {
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden},
			{name: "security_admin", token: securityToken, wantStatus: http.StatusForbidden},
			{name: "admin", token: "dev_admin_token", wantStatus: route.wantAdminState, wantAllow: route.allow},
		} {
			t.Run(route.name+"/"+auth.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, httpServer.URL+route.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if auth.token != "" {
					request.Header.Set("authorization", "Bearer "+auth.token)
				}
				response, err := httpServer.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				assertRealHEADResponse(t, response, auth.wantStatus, auth.wantAllow, "application/json", true)
				_ = response.Body.Close()
			})
		}
	}
}

func TestAdminReconciliationMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, app, _ := newMethodRoutingReconciliationServer(t, 0)
	for _, route := range adminReconciliationMethodRoutes() {
		t.Run(route.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.allowed[0])
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
				t.Fatalf("access-control-allow-methods = %q", got)
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestAdminReconciliationMethodRoutesPreservePathBoundaries(t *testing.T) {
	_, app, _ := newMethodRoutingReconciliationServer(t, 0)

	unauthorizedRules := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliation-rules/", "")
	assertJSONError(t, unauthorizedRules, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, unauthorizedRules, "")
	rules := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliation-rules/", "dev_admin_token")
	assertJSONError(t, rules, http.StatusNotFound, "reconciliation_rule_not_found")
	assertAllowHeader(t, rules, "")

	unknownRuleAction := methodRoutingRequest(app, http.MethodPost, "/api/admin/billing/reconciliation-rules/missing/unknown", "dev_admin_token")
	assertJSONError(t, unknownRuleAction, http.StatusNotFound, "reconciliation_action_not_found")
	assertAllowHeader(t, unknownRuleAction, "")
	runWrongMethod := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliation-rules/missing/run", "dev_admin_token")
	assertJSONError(t, runWrongMethod, http.StatusNotFound, "reconciliation_action_not_found")
	assertAllowHeader(t, runWrongMethod, "")
	extraRuleSegment := methodRoutingRequest(app, http.MethodPost, "/api/admin/billing/reconciliation-rules/missing/run/extra", "dev_admin_token")
	assertJSONError(t, extraRuleSegment, http.StatusNotFound, "reconciliation_rule_not_found")
	assertAllowHeader(t, extraRuleSegment, "")
	trailingRuleItem := methodRoutingRequest(app, http.MethodPost, "/api/admin/billing/reconciliation-rules/missing/", "dev_admin_token")
	assertJSONError(t, trailingRuleItem, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, trailingRuleItem, "GET, PATCH")

	unauthorizedRuns := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliations/", "")
	assertJSONError(t, unauthorizedRuns, http.StatusUnauthorized, "invalid_admin_token")
	runs := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliations/", "dev_admin_token")
	assertJSONError(t, runs, http.StatusNotFound, "reconciliation_run_not_found")
	unknownRunAction := methodRoutingRequest(app, http.MethodPost, "/api/admin/billing/reconciliations/missing/unknown", "dev_admin_token")
	assertJSONError(t, unknownRunAction, http.StatusNotFound, "reconciliation_action_not_found")
	wrongLockMethod := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliations/missing/lock", "dev_admin_token")
	assertJSONError(t, wrongLockMethod, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, wrongLockMethod, http.MethodPost)
	extraRunSegment := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliations/missing/lock/extra", "dev_admin_token")
	assertJSONError(t, extraRunSegment, http.StatusNotFound, "reconciliation_run_not_found")
}

func TestAdminReconciliationMethodRoutesPreservePayloadValidationAndFailureAudits(t *testing.T) {
	configMax := int64(1 << 10)
	store, app, connector := newMethodRoutingReconciliationServer(t, configMax)
	rule := createMethodRoutingReconciliationRule(t, app, connector.ID)
	rulePath := "/api/admin/billing/reconciliation-rules/" + rule.ID

	malformedPatch := reconciliationMethodRoutingBodyRequest(app, http.MethodPatch, rulePath, "{", "dev_admin_token")
	assertJSONError(t, malformedPatch, http.StatusBadRequest, "invalid_reconciliation_rule")
	largePatch := reconciliationMethodRoutingBodyRequest(app, http.MethodPatch, rulePath, `{"name":"`+strings.Repeat("x", 4096)+`"}`, "dev_admin_token")
	assertJSONError(t, largePatch, http.StatusRequestEntityTooLarge, "payload_too_large")

	malformedRun := reconciliationMethodRoutingBodyRequest(app, http.MethodPost, rulePath+"/run", "{", "dev_admin_token")
	assertJSONError(t, malformedRun, http.StatusBadRequest, "invalid_reconciliation_run")
	largeRun := reconciliationMethodRoutingBodyRequest(app, http.MethodPost, rulePath+"/run", `{"period_start":"`+strings.Repeat("x", 4096)+`"}`, "dev_admin_token")
	assertJSONError(t, largeRun, http.StatusRequestEntityTooLarge, "payload_too_large")

	missingLock := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/reconciliations/missing/lock", map[string]any{}, "dev_admin_token")
	assertJSONError(t, missingLock, http.StatusNotFound, "reconciliation_run_not_found")
	missingRecalculate := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/reconciliations/missing/recalculate", map[string]any{}, "dev_admin_token")
	assertJSONError(t, missingRecalculate, http.StatusNotFound, "reconciliation_run_not_found")

	assertReconciliationMethodRoutingAudit(t, store, "reconcile", "failed", "invalid_reconciliation_run")
	assertReconciliationMethodRoutingAudit(t, store, "lock", "failed", "reconciliation_run_not_found")
	assertReconciliationMethodRoutingAudit(t, store, "recalculate", "failed", "reconciliation_run_not_found")
}

func TestAdminReconciliationMethodRoutesPreserveCreatePayloadValidation(t *testing.T) {
	store, app, _ := newMethodRoutingReconciliationServer(t, 1<<10)
	rulesBefore := len(store.ListReconciliationRules())
	auditsBefore := len(store.ListAuditEvents())

	malformed := reconciliationMethodRoutingBodyRequest(app, http.MethodPost, "/api/admin/billing/reconciliation-rules", "{", "dev_admin_token")
	assertJSONError(t, malformed, http.StatusBadRequest, "invalid_reconciliation_rule")
	large := reconciliationMethodRoutingBodyRequest(app, http.MethodPost, "/api/admin/billing/reconciliation-rules", `{"name":"`+strings.Repeat("x", 4096)+`"}`, "dev_admin_token")
	assertJSONError(t, large, http.StatusRequestEntityTooLarge, "payload_too_large")

	if got := len(store.ListReconciliationRules()); got != rulesBefore {
		t.Fatalf("invalid create requests wrote reconciliation rules: got %d, want %d", got, rulesBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("invalid create requests wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminReconciliationMethodRoutesPreserveStateAndCSV(t *testing.T) {
	store, app, connector := newMethodRoutingReconciliationServer(t, 0)
	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	createReconciliationBillingRecords(t, store, []BillingRecord{{
		ID: "billing-routing-reconciliation-record", ConnectorID: connector.ID, ExternalID: "external-routing-record",
		SourceType: BillingConnectorOneAPI, Model: "routing-model", Currency: "USD", NetAmount: "1",
		UsageStartAt: periodStart.Add(time.Minute), UsageEndAt: periodStart.Add(2 * time.Minute), ExternalRequestID: "routing-request",
	}})
	createReconciliationUsageRecords(t, store, []UsageRecord{{
		ID: "usage-routing-reconciliation-record", RequestID: "routing-request", ProviderID: BillingConnectorOneAPI,
		ModelName: "routing-model", ProviderCostUSD: 1, CreatedAt: periodStart.Add(time.Minute),
	}})
	rule := createMethodRoutingReconciliationRule(t, app, connector.ID)

	list := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliation-rules", "dev_admin_token")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), rule.ID) {
		t.Fatalf("list rules: status=%d body=%s", list.Code, list.Body.String())
	}
	item := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliation-rules/"+rule.ID, "dev_admin_token")
	if item.Code != http.StatusOK || !strings.Contains(item.Body.String(), rule.ID) {
		t.Fatalf("get rule: status=%d body=%s", item.Code, item.Body.String())
	}
	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/billing/reconciliation-rules/"+rule.ID, map[string]any{"name": "Routing Rule Updated"}, "dev_admin_token")
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), "Routing Rule Updated") {
		t.Fatalf("patch rule: status=%d body=%s", patched.Code, patched.Body.String())
	}

	runResponse := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules/"+rule.ID+"/run", map[string]any{
		"period_start": periodStart.Format(time.RFC3339), "period_end": periodStart.Add(time.Hour).Format(time.RFC3339),
	}, "dev_admin_token")
	if runResponse.Code != http.StatusCreated {
		t.Fatalf("run rule: status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	var run ReconciliationRun
	if err := json.Unmarshal(runResponse.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != ReconciliationRunSucceeded || run.RuleID != rule.ID {
		t.Fatalf("unexpected run: %#v", run)
	}

	runList := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliations?rule_id="+rule.ID+"&limit=1", "dev_admin_token")
	if runList.Code != http.StatusOK || !strings.Contains(runList.Body.String(), run.ID) {
		t.Fatalf("list runs: status=%d body=%s", runList.Code, runList.Body.String())
	}
	detail := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliations/"+run.ID+"?status=matched&limit=1&offset=0", "dev_admin_token")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), run.ID) || !strings.Contains(detail.Body.String(), `"limit":1`) {
		t.Fatalf("get run detail: status=%d body=%s", detail.Code, detail.Body.String())
	}

	recalculated := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/reconciliations/"+run.ID+"/recalculate", map[string]any{}, "dev_admin_token")
	if recalculated.Code != http.StatusOK || !strings.Contains(recalculated.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("recalculate run: status=%d body=%s", recalculated.Code, recalculated.Body.String())
	}
	export := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/reconciliations/"+run.ID+"/export?status=matched", "dev_admin_token")
	if export.Code != http.StatusOK || !strings.HasPrefix(export.Header().Get("content-type"), "text/csv") || !strings.Contains(export.Header().Get("content-disposition"), run.ID) || !strings.HasPrefix(export.Body.String(), "\ufeffstatus,") {
		t.Fatalf("export run: status=%d headers=%v body-prefix=%q", export.Code, export.Header(), export.Body.String()[:minRoutingTestInt(20, len(export.Body.String()))])
	}

	locked := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/reconciliations/"+run.ID+"/lock", map[string]any{}, "dev_admin_token")
	if locked.Code != http.StatusOK || !strings.Contains(locked.Body.String(), `"locked_at"`) {
		t.Fatalf("lock run: status=%d body=%s", locked.Code, locked.Body.String())
	}
	blocked := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/reconciliations/"+run.ID+"/recalculate", map[string]any{}, "dev_admin_token")
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "reconciliation_run_locked") {
		t.Fatalf("recalculate locked run: status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	if !hasMethodRoutingAudit(store.ListAuditEvents(), "create", rule.ID) {
		t.Fatalf("missing create audit for reconciliation rule %s", rule.ID)
	}
	assertReconciliationMethodRoutingAudit(t, store, "reconcile", "success", "")
	assertReconciliationMethodRoutingAudit(t, store, "export", "success", "")
	assertReconciliationMethodRoutingAudit(t, store, "lock", "success", "")
}

func adminReconciliationMethodRoutes() []adminReconciliationMethodRoute {
	return []adminReconciliationMethodRoute{
		{name: "rule_collection", path: "/api/admin/billing/reconciliation-rules", allowed: []string{http.MethodGet, http.MethodPost}, allow: "GET, POST", wantAdminCode: "method_not_allowed", wantAdminState: http.StatusMethodNotAllowed},
		{name: "rule_item", path: "/api/admin/billing/reconciliation-rules/missing", allowed: []string{http.MethodGet, http.MethodPatch}, allow: "GET, PATCH", wantAdminCode: "method_not_allowed", wantAdminState: http.StatusMethodNotAllowed},
		{name: "rule_run", path: "/api/admin/billing/reconciliation-rules/missing/run", allowed: []string{http.MethodPost}, wantAdminCode: "reconciliation_action_not_found", wantAdminState: http.StatusNotFound},
		{name: "runs_collection", path: "/api/admin/billing/reconciliations", allowed: []string{http.MethodGet}, allow: http.MethodGet, wantAdminCode: "method_not_allowed", wantAdminState: http.StatusMethodNotAllowed},
		{name: "run_item", path: "/api/admin/billing/reconciliations/missing", allowed: []string{http.MethodGet}, allow: http.MethodGet, wantAdminCode: "method_not_allowed", wantAdminState: http.StatusMethodNotAllowed},
		{name: "run_lock", path: "/api/admin/billing/reconciliations/missing/lock", allowed: []string{http.MethodPost}, allow: http.MethodPost, wantAdminCode: "method_not_allowed", wantAdminState: http.StatusMethodNotAllowed},
		{name: "run_recalculate", path: "/api/admin/billing/reconciliations/missing/recalculate", allowed: []string{http.MethodPost}, allow: http.MethodPost, wantAdminCode: "method_not_allowed", wantAdminState: http.StatusMethodNotAllowed},
		{name: "run_export", path: "/api/admin/billing/reconciliations/missing/export", allowed: []string{http.MethodGet}, allow: http.MethodGet, wantAdminCode: "method_not_allowed", wantAdminState: http.StatusMethodNotAllowed},
	}
}

func unsupportedAdminReconciliationMethods(route adminReconciliationMethodRoute) []string {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodTrace, http.MethodConnect}
	unsupported := make([]string, 0, len(methods))
	for _, method := range methods {
		allowed := false
		for _, candidate := range route.allowed {
			if candidate == method {
				allowed = true
				break
			}
		}
		if !allowed {
			unsupported = append(unsupported, method)
		}
	}
	return unsupported
}

func newMethodRoutingReconciliationServer(t *testing.T, maxJSONBytes int64) (*GormStore, http.Handler, BillingConnector) {
	t.Helper()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "reconciliation-routing-password"
	config.SecretKey = "reconciliation-method-routing-secret"
	if maxJSONBytes > 0 {
		config.MaxJSONRequestBytes = maxJSONBytes
	}
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	connector := createReconciliationTestConnector(t, store, "reconciliation-routing-connector")
	return store, NewWithConfig(store, config).Handler(), connector
}

func createMethodRoutingReconciliationRule(t *testing.T, app http.Handler, connectorID string) ReconciliationRule {
	t.Helper()
	response := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules", map[string]any{
		"name": "Method Routing Reconciliation", "connector_id": connectorID, "granularity": ReconciliationGranularityDay,
		"match_dimensions": []string{"model", "currency"}, "amount_tolerance": "0", "ratio_tolerance": "0", "timezone": "UTC",
	}, "dev_admin_token")
	if response.Code != http.StatusCreated {
		t.Fatalf("create reconciliation rule: status=%d body=%s", response.Code, response.Body.String())
	}
	var rule ReconciliationRule
	if err := json.NewDecoder(response.Body).Decode(&rule); err != nil {
		t.Fatal(err)
	}
	return rule
}

func reconciliationMethodRoutingBodyRequest(handler http.Handler, method string, path string, body string, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("content-type", "application/json")
	if token != "" {
		request.Header.Set("authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertReconciliationMethodRoutingAudit(t *testing.T, store *GormStore, action string, status string, message string) {
	t.Helper()
	for _, event := range store.ListAuditEvents() {
		if event.Action != action || event.ResourceType != "billing_reconciliation" || event.Status != status {
			continue
		}
		if message == "" || event.Message == message {
			return
		}
	}
	t.Fatalf("missing reconciliation audit action=%s status=%s message=%s", action, status, message)
}

func minRoutingTestInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
