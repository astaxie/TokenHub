package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type adminOperationMethodRouteContract struct {
	name               string
	allowedMethod      string
	wrongMethod        string
	path               string
	userWantStatus     int
	securityWantStatus int
}

func TestAdminOperationMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "admin-operation-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "admin-operation-routing-password")
	userToken := createAdminOperationMethodRoutingSession(t, store, "operation-routing-user", "user")
	securityToken := createAdminOperationMethodRoutingSession(t, store, "operation-routing-security", "security_admin")
	teamLeaderToken := createAdminOperationMethodRoutingSession(t, store, "operation-routing-team-leader", "team_leader")

	for _, test := range adminOperationMethodRouteContracts() {
		t.Run(test.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, test.wrongMethod, test.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})

		t.Run(test.name+"/admin", func(t *testing.T) {
			assertAdminOperationMethodRejection(t, app, test, adminToken, http.StatusMethodNotAllowed)
		})

		t.Run(test.name+"/ordinary_user", func(t *testing.T) {
			assertAdminOperationMethodRejection(t, app, test, userToken, test.userWantStatus)
		})

		t.Run(test.name+"/security_admin", func(t *testing.T) {
			assertAdminOperationMethodRejection(t, app, test, securityToken, test.securityWantStatus)
		})
	}

	t.Run("billing_generate/security_admin_wrong_write_method", func(t *testing.T) {
		response := methodRoutingRequest(app, http.MethodDelete, "/api/admin/billing/generate", securityToken)
		assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
		assertAllowHeader(t, response, "")
	})

	t.Run("approvals/team_leader", func(t *testing.T) {
		response := methodRoutingRequest(app, http.MethodPost, "/api/admin/approvals", teamLeaderToken)
		assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		assertAllowHeader(t, response, http.MethodGet)
	})
}

func TestAdminOperationGETMethodRoutesRejectHEADAfterAuthorization(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "admin-operation-head-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "admin-operation-head-password")
	server := httptest.NewServer(app)
	defer server.Close()

	paths := []string{
		"/api/admin/usage/summary",
		"/api/admin/usage/breakdown",
		"/api/admin/usage/timeseries",
		"/api/admin/audit/requests",
		"/api/admin/audit/image-jobs",
		"/api/admin/audit/events",
		"/api/admin/alerts",
		"/api/admin/alert-deliveries",
		"/api/admin/approvals",
	}
	credentials := []struct {
		name       string
		token      string
		wantStatus int
		wantAllow  string
	}{
		{name: "no_token", wantStatus: http.StatusUnauthorized},
		{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodGet},
	}

	for _, path := range paths {
		for _, credential := range credentials {
			t.Run(strings.TrimPrefix(path, "/api/admin/")+"/"+credential.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if credential.token != "" {
					request.Header.Set("authorization", "Bearer "+credential.token)
				}
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}

				if response.StatusCode != credential.wantStatus {
					response.Body.Close()
					t.Fatalf("HEAD %s: expected %d, got %d", path, credential.wantStatus, response.StatusCode)
				}
				if contentType := response.Header.Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
					response.Body.Close()
					t.Fatalf("HEAD %s: content type = %q, want application/json", path, contentType)
				}
				if requestID := response.Header.Get("x-request-id"); requestID == "" {
					response.Body.Close()
					t.Fatalf("HEAD %s: x-request-id is empty", path)
				}
				_, allowPresent := response.Header[http.CanonicalHeaderKey("Allow")]
				if credential.wantAllow == "" && allowPresent {
					response.Body.Close()
					t.Fatalf("HEAD %s: Allow header is present with value %q, want it absent", path, response.Header.Get("Allow"))
				}
				if credential.wantAllow != "" && (!allowPresent || response.Header.Get("Allow") != credential.wantAllow) {
					response.Body.Close()
					t.Fatalf("HEAD %s: Allow = %q, want %q", path, response.Header.Get("Allow"), credential.wantAllow)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if len(body) != 0 {
					t.Fatalf("HEAD %s: response body = %q, want empty", path, body)
				}
			})
		}
	}
}

func TestAdminNotificationListMethodRoutesReachHandlers(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "admin-notification-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "admin-notification-routing-password")
	now := time.Now().UTC()

	alert := AlertEvent{
		ID: "alt_method_routing", ScopeType: "project", ScopeID: "prj_demo",
		Severity: "warning", Code: "method_routing", Message: "Method routing alert", CreatedAt: now,
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	delivery := store.RecordAlertDelivery(AlertDelivery{
		ID: "dlv_method_routing", AlertID: alert.ID, Channel: "webhook", Status: "success", CreatedAt: now,
	})
	approval := store.CreateApprovalRequest(ApprovalRequest{
		ID: "apr_method_routing", Trigger: "method_routing", ResourceType: "project", ResourceID: "prj_demo",
		Requester: "Method Routing", Status: "pending", CreatedAt: now,
	})

	for _, test := range []struct {
		name   string
		path   string
		marker string
	}{
		{name: "alerts", path: "/api/admin/alerts", marker: alert.ID},
		{name: "alert_deliveries", path: "/api/admin/alert-deliveries", marker: delivery.ID},
		{name: "approvals", path: "/api/admin/approvals", marker: approval.ID},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, http.MethodGet, test.path, adminToken)
			if response.Code != http.StatusOK {
				t.Fatalf("GET %s: expected 200, got %d: %s", test.path, response.Code, response.Body.String())
			}
			if contentType := response.Header().Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
				t.Fatalf("GET %s: content type = %q, want application/json", test.path, contentType)
			}
			if !strings.Contains(response.Body.String(), test.marker) {
				t.Fatalf("GET %s: response does not contain %q: %s", test.path, test.marker, response.Body.String())
			}
		})
	}
}

func assertAdminOperationMethodRejection(t *testing.T, app http.Handler, test adminOperationMethodRouteContract, token string, wantStatus int) {
	t.Helper()
	response := methodRoutingRequest(app, test.wrongMethod, test.path, token)
	wantCode := "admin_forbidden"
	wantAllow := ""
	if wantStatus == http.StatusMethodNotAllowed {
		wantCode = "method_not_allowed"
		wantAllow = test.allowedMethod
	}
	assertJSONError(t, response, wantStatus, wantCode)
	assertAllowHeader(t, response, wantAllow)
}

func createAdminOperationMethodRoutingSession(t *testing.T, store *GormStore, username string, role string) string {
	t.Helper()
	user, err := store.CreateAdminUser(AdminUser{
		Username: username,
		Name:     username,
		Email:    username + "@tokenhub.local",
		Role:     role,
		Status:   StatusActive,
	}, "method-routing-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}

func adminOperationMethodRouteContracts() []adminOperationMethodRouteContract {
	return []adminOperationMethodRouteContract{
		{name: "billing_generate", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/billing/generate", userWantStatus: http.StatusMethodNotAllowed, securityWantStatus: http.StatusMethodNotAllowed},
		{name: "usage_summary", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/usage/summary", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusForbidden},
		{name: "usage_breakdown", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/usage/breakdown", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusForbidden},
		{name: "usage_timeseries", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/usage/timeseries", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusForbidden},
		{name: "request_logs", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/audit/requests", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusMethodNotAllowed},
		{name: "image_jobs", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/audit/image-jobs", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusMethodNotAllowed},
		{name: "audit_events", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/audit/events", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusMethodNotAllowed},
		{name: "alerts", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/alerts", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusMethodNotAllowed},
		{name: "alert_deliveries", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/alert-deliveries", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusMethodNotAllowed},
		{name: "approvals", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/approvals", userWantStatus: http.StatusForbidden, securityWantStatus: http.StatusMethodNotAllowed},
	}
}
