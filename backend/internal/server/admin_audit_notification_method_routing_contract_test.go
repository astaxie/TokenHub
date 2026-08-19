package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type adminAuditNotificationMethodRoute struct {
	name        string
	path        string
	wrongMethod string
	allow       string
}

func TestAdminAuditNotificationMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "audit-notification-routing-password")
	ordinaryToken := createAdminOperationMethodRoutingSession(t, store, "audit-notification-ordinary", "user")

	for _, route := range adminAuditNotificationMethodRoutes() {
		t.Run(route.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/ordinary_user", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, ordinaryToken)
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "dev_admin_token")
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})
	}
}

func TestAdminAuditNotificationMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "audit-notification-methods-password")
	for _, route := range adminAuditNotificationMethodRoutes() {
		for _, method := range unsupportedAdminAuditNotificationMethods(route.allow) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, "dev_admin_token")
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}
}

func TestAdminAuditNotificationMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "audit-notification-head-password")
	ordinaryToken := createAdminOperationMethodRoutingSession(t, store, "audit-notification-head-user", "user")
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminAuditNotificationMethodRoutes() {
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden},
			{name: "admin", token: "dev_admin_token", wantStatus: http.StatusMethodNotAllowed, wantAllow: route.allow},
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

func TestAdminAuditNotificationMethodRoutesRejectTrailingSlashRealHEAD(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "audit-notification-trailing-head-password")
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range []adminAuditNotificationMethodRoute{
		{name: "request_detail", path: "/api/admin/audit/requests/request-missing/", allow: http.MethodGet},
		{name: "alert_deliver", path: "/api/admin/alerts/alert-missing/deliver/", allow: http.MethodPost},
		{name: "approval_approve", path: "/api/admin/approvals/approval-missing/approve/", allow: http.MethodPost},
		{name: "approval_reject", path: "/api/admin/approvals/approval-missing/reject/", allow: http.MethodPost},
	} {
		t.Run(route.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodHead, httpServer.URL+route.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("authorization", "Bearer dev_admin_token")
			response, err := httpServer.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			assertRealHEADResponse(t, response, http.StatusMethodNotAllowed, route.allow, "application/json", true)
			_ = response.Body.Close()
		})
	}
}

func TestAdminAuditNotificationMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "audit-notification-cors-password")
	for _, route := range adminAuditNotificationMethodRoutes() {
		t.Run(route.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.wrongMethod)
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

func TestAdminAuditNotificationMethodRoutesPreserveSuccessAndAudits(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "audit-notification-success-password")
	now := time.Now().UTC()
	if err := store.db.Create(&RequestLog{
		ID: "log_method_route", RequestID: "req_method_route", ProjectID: defaultProjectID,
		ModelName: "gemini-3.6", StatusCode: http.StatusOK, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	detail := methodRoutingRequest(app, http.MethodGet, "/api/admin/audit/requests/req_method_route", "dev_admin_token")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "req_method_route") {
		t.Fatalf("request detail: status=%d body=%s", detail.Code, detail.Body.String())
	}

	var received bytes.Buffer
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("webhook method = %s, want POST", r.Method)
		}
		if _, err := io.Copy(&received, r.Body); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(webhook.Close)
	store.CreateResource("notification-channels", AdminResource{
		Name: "Method Routing Webhook", Status: StatusActive,
		Fields: map[string]any{"type": "webhook", "webhook_url": webhook.URL},
	})
	alert := AlertEvent{ID: "alert_method_route", ScopeType: "project", ScopeID: defaultProjectID, Severity: "warning", Code: "method_route", Message: "Method routing alert", CreatedAt: now}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}

	auditsBefore := len(store.ListAuditEvents())
	delivered := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "dev_admin_token")
	if delivered.Code != http.StatusOK || !strings.Contains(delivered.Body.String(), `"status":"success"`) {
		t.Fatalf("alert delivery: status=%d body=%s", delivered.Code, delivered.Body.String())
	}
	if !strings.Contains(received.String(), alert.Code) {
		t.Fatalf("webhook payload does not contain %q: %s", alert.Code, received.String())
	}
	if deliveries := store.ListAlertDeliveries(); len(deliveries) != 1 || deliveries[0].AlertID != alert.ID {
		t.Fatalf("alert deliveries = %+v", deliveries)
	}

	approved := store.CreateApprovalRequest(ApprovalRequest{ID: "approval_method_approve", Trigger: "method_route", ResourceType: "method_route", ResourceID: "resource-approve"})
	approveResponse := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/approvals/"+approved.ID+"/approve", map[string]any{}, "dev_admin_token")
	if approveResponse.Code != http.StatusOK || !strings.Contains(approveResponse.Body.String(), `"status":"approved"`) || !strings.Contains(approveResponse.Body.String(), `"applied":false`) {
		t.Fatalf("approve: status=%d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	updatedApproved, err := store.GetApprovalRequest(approved.ID)
	if err != nil || updatedApproved.Status != "approved" {
		t.Fatalf("approved request = %+v, err=%v", updatedApproved, err)
	}

	rejected := store.CreateApprovalRequest(ApprovalRequest{ID: "approval_method_reject", Trigger: "method_route", ResourceType: "method_route", ResourceID: "resource-reject"})
	rejectResponse := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/approvals/"+rejected.ID+"/reject", map[string]any{"reason": "not ready"}, "dev_admin_token")
	if rejectResponse.Code != http.StatusOK || !strings.Contains(rejectResponse.Body.String(), `"status":"rejected"`) {
		t.Fatalf("reject: status=%d body=%s", rejectResponse.Code, rejectResponse.Body.String())
	}
	updatedRejected, err := store.GetApprovalRequest(rejected.ID)
	if err != nil || updatedRejected.Status != "rejected" || updatedRejected.Reason != "not ready" {
		t.Fatalf("rejected request = %+v, err=%v", updatedRejected, err)
	}

	audits := store.ListAuditEvents()
	if len(audits) != auditsBefore+4 || !hasMethodRoutingAudit(audits, "deliver", alert.ID) || !hasMethodRoutingAudit(audits, "apply_approval", approved.ResourceID) || !hasMethodRoutingAudit(audits, "approved", approved.ID) || !hasMethodRoutingAudit(audits, "rejected", rejected.ID) {
		t.Fatalf("unexpected audit events: %+v", audits)
	}
}

func TestAdminAuditNotificationMethodRoutesPreserveTrailingSlashSuccessfulOperations(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "audit-notification-trailing-success-password")
	now := time.Now().UTC()
	if err := store.db.Create(&RequestLog{
		ID: "log_trailing_method_route", RequestID: "req_trailing_method_route", ProjectID: defaultProjectID,
		ModelName: "gemini-3.6", StatusCode: http.StatusOK, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	detail := methodRoutingRequest(app, http.MethodGet, "/api/admin/audit/requests/req_trailing_method_route/", "dev_admin_token")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "req_trailing_method_route") {
		t.Fatalf("trailing request detail: status=%d body=%s", detail.Code, detail.Body.String())
	}

	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("webhook method = %s, want POST", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(webhook.Close)
	store.CreateResource("notification-channels", AdminResource{
		Name: "Trailing Method Routing Webhook", Status: StatusActive,
		Fields: map[string]any{"type": "webhook", "webhook_url": webhook.URL},
	})
	alert := AlertEvent{ID: "alert_trailing_method_route", ScopeType: "project", ScopeID: defaultProjectID, Severity: "warning", Code: "trailing_method_route", Message: "Trailing method routing alert", CreatedAt: now}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}

	auditsBefore := len(store.ListAuditEvents())
	delivered := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver/", map[string]any{}, "dev_admin_token")
	if delivered.Code != http.StatusOK || !strings.Contains(delivered.Body.String(), `"status":"success"`) {
		t.Fatalf("trailing alert delivery: status=%d body=%s", delivered.Code, delivered.Body.String())
	}
	if deliveries := store.ListAlertDeliveries(); len(deliveries) != 1 || deliveries[0].AlertID != alert.ID {
		t.Fatalf("trailing alert deliveries = %+v", deliveries)
	}

	approved := store.CreateApprovalRequest(ApprovalRequest{
		ID:           "approval_trailing_approve",
		Trigger:      "method_route",
		ResourceType: "budgets",
		Payload:      `{"name":"Trailing Method Routing Budget","status":"active","fields":{"scope":"global"}}`,
	})
	approveResponse := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/approvals/"+approved.ID+"/approve/", map[string]any{}, "dev_admin_token")
	if approveResponse.Code != http.StatusOK || !strings.Contains(approveResponse.Body.String(), `"status":"approved"`) {
		t.Fatalf("trailing approve: status=%d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	updatedApproved, err := store.GetApprovalRequest(approved.ID)
	if err != nil || updatedApproved.Status != "approved" {
		t.Fatalf("trailing approved request = %+v, err=%v", updatedApproved, err)
	}
	foundBudget := false
	for _, budget := range store.ListResources("budgets") {
		if budget.Name == "Trailing Method Routing Budget" {
			foundBudget = true
			break
		}
	}
	if !foundBudget {
		t.Fatal("trailing approve did not apply the pending budget")
	}

	rejected := store.CreateApprovalRequest(ApprovalRequest{ID: "approval_trailing_reject", Trigger: "method_route", ResourceType: "method_route", ResourceID: "resource-trailing-reject"})
	rejectResponse := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/approvals/"+rejected.ID+"/reject/", map[string]any{"reason": "not ready"}, "dev_admin_token")
	if rejectResponse.Code != http.StatusOK || !strings.Contains(rejectResponse.Body.String(), `"status":"rejected"`) {
		t.Fatalf("trailing reject: status=%d body=%s", rejectResponse.Code, rejectResponse.Body.String())
	}
	updatedRejected, err := store.GetApprovalRequest(rejected.ID)
	if err != nil || updatedRejected.Status != "rejected" || updatedRejected.Reason != "not ready" {
		t.Fatalf("trailing rejected request = %+v, err=%v", updatedRejected, err)
	}

	audits := store.ListAuditEvents()
	if len(audits) != auditsBefore+4 || !hasMethodRoutingAudit(audits, "deliver", alert.ID) || !hasMethodRoutingAudit(audits, "apply_approval", approved.ResourceID) || !hasMethodRoutingAudit(audits, "approved", approved.ID) || !hasMethodRoutingAudit(audits, "rejected", rejected.ID) {
		t.Fatalf("unexpected trailing audit events: %+v", audits)
	}
}

func TestAdminAuditNotificationMethodRoutesPreservePathBoundaries(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "audit-notification-boundaries-password")
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/admin/audit/requests/"},
		{method: http.MethodGet, path: "/api/admin/audit/requests/request-missing/extra"},
		{method: http.MethodGet, path: "/api/admin/audit/requests/request%2Fmissing"},
		{method: http.MethodPost, path: "/api/admin/alerts/alert-missing/unknown"},
		{method: http.MethodPost, path: "/api/admin/alerts/alert-missing/deliver/extra"},
		{method: http.MethodPost, path: "/api/admin/alerts/alert%2Fmissing/deliver"},
		{method: http.MethodPost, path: "/api/admin/approvals/approval-missing/unknown"},
		{method: http.MethodPost, path: "/api/admin/approvals/approval-missing/approve/extra"},
		{method: http.MethodPost, path: "/api/admin/approvals/approval%2Fmissing/approve"},
	} {
		response := methodRoutingRequest(app, test.method, test.path, "dev_admin_token")
		assertJSONError(t, response, http.StatusNotFound, "not_found")
		assertAllowHeader(t, response, "")
	}
}

func adminAuditNotificationMethodRoutes() []adminAuditNotificationMethodRoute {
	return []adminAuditNotificationMethodRoute{
		{name: "request_detail", path: "/api/admin/audit/requests/request-missing", wrongMethod: http.MethodPost, allow: http.MethodGet},
		{name: "alert_deliver", path: "/api/admin/alerts/alert-missing/deliver", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "approval_approve", path: "/api/admin/approvals/approval-missing/approve", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "approval_reject", path: "/api/admin/approvals/approval-missing/reject", wrongMethod: http.MethodGet, allow: http.MethodPost},
	}
}

func unsupportedAdminAuditNotificationMethods(allow string) []string {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodTrace, http.MethodConnect}
	unsupported := make([]string, 0, len(methods))
	for _, method := range methods {
		if !strings.Contains(","+strings.ReplaceAll(allow, " ", "")+",", ","+method+",") {
			unsupported = append(unsupported, method)
		}
	}
	return unsupported
}

func hasMethodRoutingAudit(events []AuditEvent, action string, resourceID string) bool {
	for _, event := range events {
		if event.Action == action && event.ResourceID == resourceID {
			return true
		}
	}
	return false
}
