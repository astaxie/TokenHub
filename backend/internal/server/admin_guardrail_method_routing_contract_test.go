package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/guardrails"
)

type adminGuardrailMethodRoute struct {
	name        string
	path        string
	allowed     []string
	allow       string
	wrongMethod string
}

func TestAdminGuardrailMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, _, app, adminToken, securityToken, ordinaryToken, teamLeaderToken, policyID, _ := newAdminGuardrailMethodRoutingServer(t)
	policiesBefore := adminGuardrailPolicyCount(t, store)
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminGuardrailMethodRoutes(policyID) {
		for _, credential := range []struct {
			name       string
			token      string
			wantStatus int
			wantCode   string
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized, wantCode: "invalid_admin_token"},
			{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: route.allow},
			{name: "security_admin", token: securityToken, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: route.allow},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
			{name: "team_leader", token: teamLeaderToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		} {
			t.Run(route.name+"/"+credential.name, func(t *testing.T) {
				response := methodRoutingRequest(app, route.wrongMethod, route.path, credential.token)
				assertJSONError(t, response, credential.wantStatus, credential.wantCode)
				assertAllowHeader(t, response, credential.wantAllow)
			})
		}
	}

	assertAdminGuardrailMethodRouteCounts(t, store, policiesBefore, auditsBefore)
}

func TestAdminGuardrailMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	store, _, app, adminToken, _, _, _, policyID, _ := newAdminGuardrailMethodRoutingServer(t)
	policiesBefore := adminGuardrailPolicyCount(t, store)
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminGuardrailMethodRoutes(policyID) {
		for _, method := range unsupportedAdminGuardrailMethods(route.allowed) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, adminToken)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}

	assertAdminGuardrailMethodRouteCounts(t, store, policiesBefore, auditsBefore)
}

func TestAdminGuardrailMethodRoutesRejectRealHEAD(t *testing.T) {
	_, server, _, adminToken, securityToken, ordinaryToken, teamLeaderToken, policyID, _ := newAdminGuardrailMethodRoutingServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	for _, test := range []struct {
		name  string
		path  string
		allow string
	}{
		{name: "collection", path: "/api/admin/guardrail-policies", allow: "GET, POST"},
		{name: "test action", path: adminGuardrailPolicyTestPath, allow: http.MethodPost},
		{name: "policy item", path: "/api/admin/guardrail-policies/" + policyID, allow: "GET, PUT, DELETE"},
	} {
		for _, credential := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantAllow: test.allow},
			{name: "security_admin", token: securityToken, wantStatus: http.StatusMethodNotAllowed, wantAllow: test.allow},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden},
			{name: "team_leader", token: teamLeaderToken, wantStatus: http.StatusForbidden},
		} {
			t.Run(test.name+"/"+credential.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, httpServer.URL+test.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if credential.token != "" {
					request.Header.Set("authorization", "Bearer "+credential.token)
				}
				response, err := httpServer.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				assertRealHEADResponse(t, response, credential.wantStatus, credential.wantAllow, "application/json", true)
			})
		}
	}
}

func TestAdminGuardrailMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, _, app, _, _, _, _, policyID, _ := newAdminGuardrailMethodRoutingServer(t)
	for _, route := range adminGuardrailMethodRoutes(policyID) {
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

func TestAdminGuardrailMethodRoutesPreserveStaticAndDynamicPathBoundaries(t *testing.T) {
	store, _, app, adminToken, _, _, _, policyID, encodedPolicyID := newAdminGuardrailMethodRoutingServer(t)
	policiesBefore := adminGuardrailPolicyCount(t, store)
	auditsBefore := len(store.ListAuditEvents())

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
		wantText   string
	}{
		{name: "test GET stays action", method: http.MethodGet, path: adminGuardrailPolicyTestPath, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "test trailing slash stays policy item", method: http.MethodGet, path: adminGuardrailPolicyTestPath + "/", wantStatus: http.StatusNotFound, wantCode: "guardrail_policy_not_found"},
		{name: "policy POST rejects before lookup", method: http.MethodPost, path: "/api/admin/guardrail-policies/missing", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "GET, PUT, DELETE"},
		{name: "policy trailing slash wrong method", method: http.MethodPost, path: "/api/admin/guardrail-policies/" + policyID + "/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "GET, PUT, DELETE"},
		{name: "unknown child", method: http.MethodGet, path: "/api/admin/guardrail-policies/" + policyID + "/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "unknown child wrong method", method: http.MethodPatch, path: "/api/admin/guardrail-policies/" + policyID + "/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "blank policy id", method: http.MethodGet, path: "/api/admin/guardrail-policies/%20", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "blank policy id wrong method", method: http.MethodPost, path: "/api/admin/guardrail-policies/%20", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "encoded policy id", method: http.MethodGet, path: "/api/admin/guardrail-policies/" + strings.ReplaceAll(encodedPolicyID, "/", "%2F"), wantStatus: http.StatusOK, wantText: "Encoded guardrail policy"},
		{name: "encoded policy wrong method", method: http.MethodPost, path: "/api/admin/guardrail-policies/" + strings.ReplaceAll(encodedPolicyID, "/", "%2F"), wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "GET, PUT, DELETE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, adminToken)
			if test.wantCode != "" {
				assertJSONError(t, response, test.wantStatus, test.wantCode)
				assertAllowHeader(t, response, test.wantAllow)
				return
			}
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantText) {
				t.Fatalf("expected %d containing %q, got %d: %s", test.wantStatus, test.wantText, response.Code, response.Body.String())
			}
			assertAllowHeader(t, response, "")
		})
	}

	assertAdminGuardrailMethodRouteCounts(t, store, policiesBefore, auditsBefore)
}

func TestAdminGuardrailMethodRoutesAuthorizeBeforeMalformedPathValidation(t *testing.T) {
	_, _, app, adminToken, securityToken, ordinaryToken, teamLeaderToken, policyID, _ := newAdminGuardrailMethodRoutingServer(t)
	path := "/api/admin/guardrail-policies/" + policyID + "/unknown"

	for _, credential := range []struct {
		name       string
		token      string
		wantStatus int
		wantCode   string
	}{
		{name: "no_token", wantStatus: http.StatusUnauthorized, wantCode: "invalid_admin_token"},
		{name: "admin", token: adminToken, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "security_admin", token: securityToken, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "team_leader", token: teamLeaderToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
	} {
		t.Run(credential.name, func(t *testing.T) {
			response := methodRoutingRequest(app, http.MethodPatch, path, credential.token)
			assertJSONError(t, response, credential.wantStatus, credential.wantCode)
			assertAllowHeader(t, response, "")
		})
	}
}

func TestAdminGuardrailMethodRoutesPreserveTrailingSlashMutations(t *testing.T) {
	store, _, app, adminToken, _, _, _, _, _ := newAdminGuardrailMethodRoutingServer(t)
	policy, err := store.CreateGuardrailPolicy(testGuardrailPolicy("Trailing guardrail policy", defaultProjectID))
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/guardrail-policies/" + policy.ID + "/"

	update := testGuardrailPolicy("Trailing guardrail policy updated", defaultProjectID)
	updated := methodRoutingJSONRequest(t, app, http.MethodPut, path, update, adminToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "Trailing guardrail policy updated") {
		t.Fatalf("trailing slash PUT: expected 200, got %d: %s", updated.Code, updated.Body.String())
	}
	deleted := methodRoutingRequest(app, http.MethodDelete, path, adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("trailing slash DELETE: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	if _, err := store.GetGuardrailPolicy(policy.ID); AsHTTPError(err).Code != "guardrail_policy_not_found" {
		t.Fatalf("trailing slash DELETE left policy behind: %v", err)
	}

	wantAudit := map[string]bool{"update": false, "delete": false}
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType == "guardrail_policy" && event.ResourceID == policy.ID {
			wantAudit[event.Action] = true
		}
	}
	for action, found := range wantAudit {
		if !found {
			t.Fatalf("missing %s audit event for trailing slash mutation", action)
		}
	}
}

func TestAdminGuardrailMethodRoutesPreserveEncodedPolicyIDMutations(t *testing.T) {
	store, _, app, adminToken, _, _, _, _, encodedPolicyID := newAdminGuardrailMethodRoutingServer(t)
	path := "/api/admin/guardrail-policies/" + strings.ReplaceAll(encodedPolicyID, "/", "%2F")

	update := testGuardrailPolicy("Encoded guardrail policy updated", defaultProjectID)
	updated := methodRoutingJSONRequest(t, app, http.MethodPut, path, update, adminToken)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "Encoded guardrail policy updated") {
		t.Fatalf("encoded policy PUT: expected 200, got %d: %s", updated.Code, updated.Body.String())
	}
	deleted := methodRoutingRequest(app, http.MethodDelete, path, adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("encoded policy DELETE: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	if _, err := store.GetGuardrailPolicy(encodedPolicyID); AsHTTPError(err).Code != "guardrail_policy_not_found" {
		t.Fatalf("encoded policy DELETE left policy behind: %v", err)
	}

	wantAudit := map[string]bool{"update": false, "delete": false}
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType == "guardrail_policy" && event.ResourceID == encodedPolicyID {
			wantAudit[event.Action] = true
		}
	}
	for action, found := range wantAudit {
		if !found {
			t.Fatalf("missing %s audit event for encoded policy ID %q", action, encodedPolicyID)
		}
	}
}

func adminGuardrailMethodRoutes(policyID string) []adminGuardrailMethodRoute {
	return []adminGuardrailMethodRoute{
		{name: "collection", path: "/api/admin/guardrail-policies", allowed: []string{http.MethodGet, http.MethodPost}, allow: "GET, POST", wrongMethod: http.MethodPut},
		{name: "test action", path: adminGuardrailPolicyTestPath, allowed: []string{http.MethodPost}, allow: http.MethodPost, wrongMethod: http.MethodGet},
		{name: "policy item", path: "/api/admin/guardrail-policies/" + policyID, allowed: []string{http.MethodGet, http.MethodPut, http.MethodDelete}, allow: "GET, PUT, DELETE", wrongMethod: http.MethodPost},
	}
}

func unsupportedAdminGuardrailMethods(allowed []string) []string {
	allowedSet := map[string]bool{}
	for _, method := range allowed {
		allowedSet[method] = true
	}
	methods := []string{}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !allowedSet[method] {
			methods = append(methods, method)
		}
	}
	return methods
}

func newAdminGuardrailMethodRoutingServer(t *testing.T) (*GormStore, *Server, http.Handler, string, string, string, string, string, string) {
	t.Helper()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "guardrail-method-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	policy, err := store.CreateGuardrailPolicy(testGuardrailPolicy("Method guardrail policy", defaultProjectID))
	if err != nil {
		t.Fatal(err)
	}
	encodedPolicy := testGuardrailPolicy("Encoded guardrail policy", defaultProjectID)
	encodedPolicy.ID = "grp/encoded"
	encodedPolicy, err = store.CreateGuardrailPolicy(encodedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	securityToken := createAdminGuardrailMethodSession(t, store, AdminUser{Username: "guardrail-security", Email: "guardrail-security@tokenhub.local", Role: "security_admin", Status: StatusActive})
	ordinaryToken := createAdminGuardrailMethodSession(t, store, AdminUser{Username: "guardrail-user", Email: "guardrail-user@tokenhub.local", Role: "user", Status: StatusActive})
	teamLeaderToken := createAdminGuardrailMethodSession(t, store, AdminUser{Username: "guardrail-leader", Email: "guardrail-leader@tokenhub.local", Role: "team_leader", TeamID: "team_platform", Status: StatusActive})
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	app := server.Handler()
	adminToken, _ := loginMethodRoutingAdmin(t, app, config.BootstrapAdminPassword)
	return store, server, app, adminToken, securityToken, ordinaryToken, teamLeaderToken, policy.ID, encodedPolicy.ID
}

func createAdminGuardrailMethodSession(t *testing.T, store *GormStore, user AdminUser) string {
	t.Helper()
	created, err := store.CreateAdminUser(user, "guardrail-method-role-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(created.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}

func adminGuardrailPolicyCount(t *testing.T, store *GormStore) int64 {
	t.Helper()
	var count int64
	if err := store.db.Model(&guardrails.Policy{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func assertAdminGuardrailMethodRouteCounts(t *testing.T, store *GormStore, wantPolicies int64, wantAudits int) {
	t.Helper()
	if got := adminGuardrailPolicyCount(t, store); got != wantPolicies {
		t.Fatalf("wrong methods changed policies: got %d, want %d", got, wantPolicies)
	}
	if got := len(store.ListAuditEvents()); got != wantAudits {
		t.Fatalf("wrong methods changed audit events: got %d, want %d", got, wantAudits)
	}
}
