package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type adminMethodRouteContract struct {
	name           string
	allowedMethod  string
	wrongMethod    string
	path           string
	userWantStatus int
	userWantCode   string
}

func TestAdminMethodRoutesPreserveAuthenticationAndAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "admin-method-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "admin-method-routing-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "method-routing-user",
		Name:     "Method Routing User",
		Email:    "method-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "method-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range adminMethodRouteContracts() {
		t.Run(test.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, test.wrongMethod, test.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})

		t.Run(test.name+"/admin", func(t *testing.T) {
			response := methodRoutingRequest(app, test.wrongMethod, test.path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, test.allowedMethod)
		})

		t.Run(test.name+"/ordinary_user", func(t *testing.T) {
			response := methodRoutingRequest(app, test.wrongMethod, test.path, userSession.Token)
			assertJSONError(t, response, test.userWantStatus, test.userWantCode)
			if test.userWantStatus == http.StatusMethodNotAllowed {
				assertAllowHeader(t, response, test.allowedMethod)
			} else {
				assertAllowHeader(t, response, "")
			}
		})
	}
}

func TestAdminGETMethodRoutesRejectHEADAfterAuthorization(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "admin-head-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "admin-head-routing-password")
	server := httptest.NewServer(app)
	defer server.Close()

	paths := []string{
		"/api/admin/auth/me",
		"/api/admin/overview",
		"/api/admin/provider-adapters",
		"/api/admin/providers/monitoring",
		"/api/admin/provider-models",
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
				defer response.Body.Close()

				if response.StatusCode != credential.wantStatus {
					t.Fatalf("HEAD %s: expected %d, got %d", path, credential.wantStatus, response.StatusCode)
				}
				if contentType := response.Header.Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
					t.Fatalf("HEAD %s: content type = %q, want application/json", path, contentType)
				}
				if requestID := response.Header.Get("x-request-id"); requestID == "" {
					t.Fatalf("HEAD %s: x-request-id is empty", path)
				}
				_, allowPresent := response.Header[http.CanonicalHeaderKey("Allow")]
				if credential.wantAllow == "" && allowPresent {
					t.Fatalf("HEAD %s: Allow header is present with value %q, want it absent", path, response.Header.Get("Allow"))
				}
				if credential.wantAllow != "" && (!allowPresent || response.Header.Get("Allow") != credential.wantAllow) {
					t.Fatalf("HEAD %s: Allow = %q, want %q", path, response.Header.Get("Allow"), credential.wantAllow)
				}
				body, err := io.ReadAll(response.Body)
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

func TestAdminMethodRouteProviderAdaptersReachesHandler(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "admin-adapter-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "admin-adapter-routing-password")

	response := methodRoutingRequest(app, http.MethodGet, "/api/admin/provider-adapters", adminToken)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/provider-adapters: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("GET /api/admin/provider-adapters: content type = %q, want application/json", contentType)
	}
	if !strings.Contains(response.Body.String(), `"data"`) {
		t.Fatalf("GET /api/admin/provider-adapters: response has no data field: %s", response.Body.String())
	}
}

func TestAdminModelPOSTRoutesPreserveRBACForOtherWriteMethods(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "admin-model-write-routing-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "model-write-routing-user",
		Name:     "Model Write Routing User",
		Email:    "model-write-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "model-write-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/admin/provider-models/import",
		"/api/admin/models/restore-defaults",
	} {
		for _, method := range []string{http.MethodDelete, http.MethodPatch} {
			t.Run(method+" "+path, func(t *testing.T) {
				response := methodRoutingRequest(app, method, path, userSession.Token)
				assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
				assertAllowHeader(t, response, "")
			})
		}
	}
}

func TestAdminMethodRoutePreservesCORSPreflight(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/admin/overview", nil)
	request.Header.Set("origin", "https://console.example.com")
	request.Header.Set("access-control-request-method", http.MethodGet)
	request.Header.Set("access-control-request-headers", "authorization,content-type")
	response := httptest.NewRecorder()

	newTestServer().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /api/admin/overview: expected 204, got %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
		t.Fatalf("access-control-allow-methods = %q", got)
	}
	if got := response.Header().Get("access-control-allow-headers"); got != "authorization,content-type" {
		t.Fatalf("access-control-allow-headers = %q", got)
	}
}

func adminMethodRouteContracts() []adminMethodRouteContract {
	return []adminMethodRouteContract{
		{name: "auth_me", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/auth/me", userWantStatus: http.StatusMethodNotAllowed, userWantCode: "method_not_allowed"},
		{name: "overview", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/overview", userWantStatus: http.StatusForbidden, userWantCode: "admin_forbidden"},
		{name: "playground_chat", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/playground/chat", userWantStatus: http.StatusMethodNotAllowed, userWantCode: "method_not_allowed"},
		{name: "playground_chat_stream", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/playground/chat/stream", userWantStatus: http.StatusMethodNotAllowed, userWantCode: "method_not_allowed"},
		{name: "provider_adapters", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/provider-adapters", userWantStatus: http.StatusForbidden, userWantCode: "admin_forbidden"},
		{name: "openai_oauth_generate", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/provider-account-oauth/openai/generate-auth-url", userWantStatus: http.StatusForbidden, userWantCode: "admin_forbidden"},
		{name: "openai_oauth_exchange", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/provider-account-oauth/openai/exchange-code", userWantStatus: http.StatusForbidden, userWantCode: "admin_forbidden"},
		{name: "provider_monitoring", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/providers/monitoring", userWantStatus: http.StatusForbidden, userWantCode: "admin_forbidden"},
		{name: "provider_models", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/provider-models", userWantStatus: http.StatusForbidden, userWantCode: "admin_forbidden"},
		{name: "provider_models_import", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/provider-models/import", userWantStatus: http.StatusMethodNotAllowed, userWantCode: "method_not_allowed"},
		{name: "models_restore_defaults", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/models/restore-defaults", userWantStatus: http.StatusMethodNotAllowed, userWantCode: "method_not_allowed"},
		{name: "routing_policy_simulation", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/routing-policies/simulate", userWantStatus: http.StatusForbidden, userWantCode: "admin_forbidden"},
	}
}
