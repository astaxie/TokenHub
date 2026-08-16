package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type adminCredentialMethodRoute struct {
	name          string
	path          string
	allowed       []string
	allow         string
	wrongMethod   string
	authenticated bool
}

func TestAdminCredentialMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, _, app, adminToken, ordinaryToken, teamLeaderToken, securityToken, credentialID, _ := newAdminCredentialMethodRoutingServer(t)
	credentialsBefore := len(store.ListAnalyticsCredentials())
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminCredentialMethodRoutes(credentialID)[:2] {
		for _, credential := range []struct {
			name       string
			token      string
			wantStatus int
			wantCode   string
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized, wantCode: "invalid_admin_token"},
			{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: route.allow},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
			{name: "team_leader", token: teamLeaderToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
			{name: "security_admin", token: securityToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		} {
			t.Run(route.name+"/"+credential.name, func(t *testing.T) {
				response := methodRoutingRequest(app, route.wrongMethod, route.path, credential.token)
				assertJSONError(t, response, credential.wantStatus, credential.wantCode)
				assertAllowHeader(t, response, credential.wantAllow)
			})
		}
	}

	assertAdminCredentialMethodRouteCounts(t, store, credentialsBefore, auditsBefore)
}

func TestAdminAnalyticsCredentialMethodRoutesPreserveAllowedMethodAuthorization(t *testing.T) {
	store, _, app, _, ordinaryToken, teamLeaderToken, securityToken, credentialID, _ := newAdminCredentialMethodRoutingServer(t)
	credentialsBefore := len(store.ListAnalyticsCredentials())
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "list", method: http.MethodGet, path: "/api/admin/analytics/credentials"},
		{name: "create", method: http.MethodPost, path: "/api/admin/analytics/credentials"},
		{name: "revoke", method: http.MethodDelete, path: "/api/admin/analytics/credentials/" + credentialID},
	} {
		for _, credential := range []struct {
			name   string
			token  string
			code   string
			status int
		}{
			{name: "no_token", status: http.StatusUnauthorized, code: "invalid_admin_token"},
			{name: "ordinary_user", token: ordinaryToken, status: http.StatusForbidden, code: "admin_forbidden"},
			{name: "team_leader", token: teamLeaderToken, status: http.StatusForbidden, code: "admin_forbidden"},
			{name: "security_admin", token: securityToken, status: http.StatusForbidden, code: "admin_forbidden"},
		} {
			t.Run(route.name+"/"+credential.name, func(t *testing.T) {
				response := methodRoutingRequest(app, route.method, route.path, credential.token)
				assertJSONError(t, response, credential.status, credential.code)
				assertAllowHeader(t, response, "")
			})
		}
	}

	assertAdminCredentialMethodRouteCounts(t, store, credentialsBefore, auditsBefore)
}

func TestAdminCredentialMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	store, _, app, adminToken, _, _, _, credentialID, _ := newAdminCredentialMethodRoutingServer(t)
	credentialsBefore := len(store.ListAnalyticsCredentials())
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminCredentialMethodRoutes(credentialID) {
		for _, method := range unsupportedAdminCredentialMethods(route.allowed) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				token := ""
				if route.authenticated {
					token = adminToken
				}
				response := methodRoutingRequest(app, method, route.path, token)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}

	assertAdminCredentialMethodRouteCounts(t, store, credentialsBefore, auditsBefore)
}

func TestAdminCredentialMethodRoutesRejectRealHEAD(t *testing.T) {
	_, server, _, adminToken, ordinaryToken, teamLeaderToken, securityToken, credentialID, _ := newAdminCredentialMethodRoutingServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	for _, route := range adminCredentialMethodRoutes(credentialID) {
		credentials := []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "public", wantStatus: http.StatusMethodNotAllowed, wantAllow: route.allow},
		}
		if route.authenticated {
			credentials = []struct {
				name       string
				token      string
				wantStatus int
				wantAllow  string
			}{
				{name: "no_token", wantStatus: http.StatusUnauthorized},
				{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantAllow: route.allow},
				{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden},
				{name: "team_leader", token: teamLeaderToken, wantStatus: http.StatusForbidden},
				{name: "security_admin", token: securityToken, wantStatus: http.StatusForbidden},
			}
		}
		for _, credential := range credentials {
			t.Run(route.name+"/"+credential.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, httpServer.URL+route.path, nil)
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

func TestAdminCredentialMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, _, app, _, _, _, _, credentialID, _ := newAdminCredentialMethodRoutingServer(t)
	for _, route := range adminCredentialMethodRoutes(credentialID) {
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

func TestAdminAnalyticsCredentialMethodRoutesPreserveMalformedPathOrder(t *testing.T) {
	store, _, app, adminToken, ordinaryToken, _, _, credentialID, _ := newAdminCredentialMethodRoutingServer(t)
	credentialsBefore := len(store.ListAnalyticsCredentials())
	auditsBefore := len(store.ListAuditEvents())
	paths := []string{
		"/api/admin/analytics/credentials/",
		"/api/admin/analytics/credentials/" + credentialID + "/",
		"/api/admin/analytics/credentials/" + credentialID + "/unknown",
		"/api/admin/analytics/credentials/encoded%2Fcredential",
	}

	for _, path := range paths {
		t.Run(path+"/delete", func(t *testing.T) {
			response := methodRoutingRequest(app, http.MethodDelete, path, adminToken)
			assertJSONError(t, response, http.StatusNotFound, "analytics_credential_not_found")
			assertAllowHeader(t, response, "")
		})

		t.Run(path+"/wrong_method", func(t *testing.T) {
			response := methodRoutingRequest(app, http.MethodPatch, path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, http.MethodDelete)
		})
	}

	path := "/api/admin/analytics/credentials/" + credentialID + "/unknown"
	for _, method := range []string{http.MethodDelete, http.MethodPatch} {
		for _, credential := range []struct {
			name       string
			token      string
			wantStatus int
			wantCode   string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized, wantCode: "invalid_admin_token"},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		} {
			t.Run("authorization/"+method+"/"+credential.name, func(t *testing.T) {
				response := methodRoutingRequest(app, method, path, credential.token)
				assertJSONError(t, response, credential.wantStatus, credential.wantCode)
				assertAllowHeader(t, response, "")
			})
		}
	}

	assertAdminCredentialMethodRouteCounts(t, store, credentialsBefore, auditsBefore)
}

func TestAdminAnalyticsCredentialMethodRoutesPreserveLifecycleAndHeaders(t *testing.T) {
	store, _, app, adminToken, _, _, _, _, _ := newAdminCredentialMethodRoutingServer(t)
	created := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/analytics/credentials", map[string]any{
		"name":       "method-routing-analytics",
		"scope_type": AnalyticsScopeProject,
		"project_id": defaultProjectID,
	}, adminToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	if got := created.Header().Get("cache-control"); got != "no-store" {
		t.Fatalf("create cache-control = %q, want no-store", got)
	}
	createdBody := created.Body.String()
	var payload struct {
		Credential AnalyticsCredential `json:"credential"`
		Token      string              `json:"token"`
	}
	if err := json.Unmarshal([]byte(createdBody), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Credential.ID == "" || !strings.HasPrefix(payload.Token, "tha_") {
		t.Fatalf("incomplete credential response: %#v", payload)
	}
	if strings.Contains(createdBody, "key_hash") {
		t.Fatalf("create response exposed key hash: %s", createdBody)
	}

	listed := methodRoutingRequest(app, http.MethodGet, "/api/admin/analytics/credentials", adminToken)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), payload.Credential.ID) {
		t.Fatalf("list: expected created credential, got %d: %s", listed.Code, listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), payload.Token) || strings.Contains(listed.Body.String(), "key_hash") {
		t.Fatalf("list response exposed credential secret: %s", listed.Body.String())
	}

	revoked := methodRoutingRequest(app, http.MethodDelete, "/api/admin/analytics/credentials/"+payload.Credential.ID, adminToken)
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), payload.Credential.ID) {
		t.Fatalf("revoke: expected 200, got %d: %s", revoked.Code, revoked.Body.String())
	}
	if _, err := store.ValidateAnalyticsCredential(payload.Token); AsHTTPError(err).Code != "invalid_analytics_credential" {
		t.Fatalf("revoked credential still validates: %v", err)
	}

	wantAudits := map[string]bool{"create": false, "revoke": false}
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType == "analytics_credential" && event.ResourceID == payload.Credential.ID {
			wantAudits[event.Action] = true
		}
	}
	for action, found := range wantAudits {
		if !found {
			t.Fatalf("missing %s audit event for analytics credential", action)
		}
	}
}

func TestOpenAIAccountOAuthCallbackMethodRoutePreservesRedirectBranches(t *testing.T) {
	store, _, app, _, _, _, _, _, _ := newAdminCredentialMethodRoutingServer(t)

	for _, test := range []struct {
		name      string
		session   providerAccountOAuthSession
		query     string
		wantQuery map[string]string
	}{
		{
			name: "success",
			session: providerAccountOAuthSession{
				ID: "oauth-method-success", State: "oauth-method-success-state", CodeVerifier: "oauth-method-success-verifier",
				ReturnURL: "http://localhost:3001/providers?source=settings", CreatedAt: time.Now().UTC(),
			},
			query: "code=oauth-code&state=oauth-method-success-state",
			wantQuery: map[string]string{
				"source": "settings", "provider_account_oauth": "1", "provider_account_oauth_session_id": "oauth-method-success",
				"provider_account_oauth_state": "oauth-method-success-state", "code": "oauth-code",
			},
		},
		{
			name: "provider error",
			session: providerAccountOAuthSession{
				ID: "oauth-method-provider-error", State: "oauth-method-provider-error-state", CodeVerifier: "oauth-method-provider-error-verifier",
				ReturnURL: "http://localhost:3001/providers", CreatedAt: time.Now().UTC(),
			},
			query:     "error=access_denied&state=oauth-method-provider-error-state",
			wantQuery: map[string]string{"provider_account_oauth_error": "provider_error"},
		},
		{
			name: "missing code",
			session: providerAccountOAuthSession{
				ID: "oauth-method-missing-code", State: "oauth-method-missing-code-state", CodeVerifier: "oauth-method-missing-code-verifier",
				ReturnURL: "http://localhost:3001/providers", CreatedAt: time.Now().UTC(),
			},
			query:     "state=oauth-method-missing-code-state",
			wantQuery: map[string]string{"provider_account_oauth_error": "missing_code"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := store.SaveProviderAccountOAuthSession(test.session); err != nil {
				t.Fatal(err)
			}
			response := methodRoutingRequest(app, http.MethodGet, "/api/admin/provider-account-oauth/openai/oauth/callback?"+test.query, "")
			if response.Code != http.StatusFound {
				t.Fatalf("expected 302, got %d: %s", response.Code, response.Body.String())
			}
			location, err := url.Parse(response.Header().Get("location"))
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range test.wantQuery {
				if got := location.Query().Get(key); got != want {
					t.Fatalf("redirect query %s = %q, want %q: %s", key, got, want, location.String())
				}
			}
		})
	}

	invalid := methodRoutingRequest(app, http.MethodGet, "/api/admin/provider-account-oauth/openai/oauth/callback?state=invalid", "")
	assertJSONError(t, invalid, http.StatusBadRequest, "invalid_oauth_state")
	assertAllowHeader(t, invalid, "")
}

func TestOpenAIAccountOAuthCallbackRejectsMethodBeforeSessionLookup(t *testing.T) {
	store, _, app, _, _, _, _, _, _ := newAdminCredentialMethodRoutingServer(t)
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	response := methodRoutingRequest(app, http.MethodPost, "/api/admin/provider-account-oauth/openai/oauth/callback?state=unreadable", "")
	assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, response, http.MethodGet)
	if location := response.Header().Get("location"); location != "" {
		t.Fatalf("wrong method returned Location %q", location)
	}
}

func adminCredentialMethodRoutes(credentialID string) []adminCredentialMethodRoute {
	return []adminCredentialMethodRoute{
		{name: "analytics collection", path: "/api/admin/analytics/credentials", allowed: []string{http.MethodGet, http.MethodPost}, allow: "GET, POST", wrongMethod: http.MethodDelete, authenticated: true},
		{name: "analytics credential", path: "/api/admin/analytics/credentials/" + credentialID, allowed: []string{http.MethodDelete}, allow: http.MethodDelete, wrongMethod: http.MethodGet, authenticated: true},
		{name: "OAuth callback", path: "/api/admin/provider-account-oauth/openai/oauth/callback?state=missing", allowed: []string{http.MethodGet}, allow: http.MethodGet, wrongMethod: http.MethodPost},
	}
}

func unsupportedAdminCredentialMethods(allowed []string) []string {
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

func newAdminCredentialMethodRoutingServer(t *testing.T) (*GormStore, *Server, http.Handler, string, string, string, string, string, string) {
	t.Helper()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "admin-credential-method-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	ordinaryToken := createAdminCredentialMethodSession(t, store, AdminUser{Username: "credential-method-user", Email: "credential-method-user@tokenhub.local", Role: "user", Status: StatusActive})
	teamLeaderToken := createAdminCredentialMethodSession(t, store, AdminUser{Username: "credential-method-leader", Email: "credential-method-leader@tokenhub.local", Role: "team_leader", TeamID: "team_platform", Status: StatusActive})
	securityToken := createAdminCredentialMethodSession(t, store, AdminUser{Username: "credential-method-security", Email: "credential-method-security@tokenhub.local", Role: "security_admin", Status: StatusActive})
	credential, credentialToken, err := store.CreateAnalyticsCredential(AnalyticsCredential{
		Name: "Method routing analytics credential", ScopeType: AnalyticsScopeProject, ProjectID: defaultProjectID, CreatedBy: "usr_admin",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	app := server.Handler()
	adminToken, _ := loginMethodRoutingAdmin(t, app, config.BootstrapAdminPassword)
	return store, server, app, adminToken, ordinaryToken, teamLeaderToken, securityToken, credential.ID, credentialToken
}

func createAdminCredentialMethodSession(t *testing.T, store *GormStore, user AdminUser) string {
	t.Helper()
	created, err := store.CreateAdminUser(user, "admin-credential-role-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(created.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}

func assertAdminCredentialMethodRouteCounts(t *testing.T, store *GormStore, wantCredentials int, wantAudits int) {
	t.Helper()
	if got := len(store.ListAnalyticsCredentials()); got != wantCredentials {
		t.Fatalf("wrong methods changed analytics credentials: got %d, want %d", got, wantCredentials)
	}
	if got := len(store.ListAuditEvents()); got != wantAudits {
		t.Fatalf("wrong methods changed audit events: got %d, want %d", got, wantAudits)
	}
}
