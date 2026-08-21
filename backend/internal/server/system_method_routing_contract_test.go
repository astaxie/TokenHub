package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type systemMethodRouteContract struct {
	name          string
	allowedMethod string
	wrongMethod   string
	path          string
}

func TestSystemMethodRoutesRejectWrongMethodsBeforeAuthentication(t *testing.T) {
	app := newSystemMethodRoutingServer("system-method-routing-token")

	for _, test := range systemMethodRouteContracts() {
		for _, credential := range []struct {
			name  string
			token string
		}{
			{name: "no_token"},
			{name: "admin", token: "system-method-routing-token"},
		} {
			t.Run(test.name+"/"+credential.name, func(t *testing.T) {
				response := methodRoutingRequest(app, test.wrongMethod, test.path, credential.token)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, test.allowedMethod)
			})
		}
	}
}

func TestSystemMethodRoutesAuthenticateAllowedMethods(t *testing.T) {
	app := newSystemMethodRoutingServer("system-method-routing-token")

	for _, test := range systemMethodRouteContracts() {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.allowedMethod, test.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})
	}
}

func TestSystemMethodRoutesPreserveRBACForAllowedMethods(t *testing.T) {
	config := Config{AdminToken: "system-method-routing-token"}
	store := NewMemoryStoreWithConfig(config)
	user, err := store.CreateAdminUser(AdminUser{
		Username: "system-method-routing-user",
		Name:     "System Method Routing User",
		Email:    "system-method-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "system-method-routing-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, config).Handler()

	for _, test := range systemMethodRouteContracts() {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.allowedMethod, test.path, session.Token)
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
	}
}

func TestSystemGETMethodRoutesRejectHEADBeforeAuthentication(t *testing.T) {
	server := httptest.NewServer(newSystemMethodRoutingServer("system-method-routing-token"))
	defer server.Close()

	for _, path := range []string{
		"/api/admin/system/version",
		"/api/admin/system/rollback-versions",
	} {
		t.Run(path, func(t *testing.T) {
			response := doSystemMethodRoutingHEAD(t, server, path, "")
			defer response.Body.Close()

			assertRealHEADResponse(t, response, http.StatusMethodNotAllowed, http.MethodGet, "application/json", true)
		})
	}
}

func TestDBStatusMethodRoutePreservesPlainTextRejection(t *testing.T) {
	app := newSystemMethodRoutingServer("db-status-method-routing-token")

	wrongMethod := methodRoutingRequest(app, http.MethodPost, "/api/admin/system/db-status", "")
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST db-status: expected 405, got %d: %s", wrongMethod.Code, wrongMethod.Body.String())
	}
	assertAllowHeader(t, wrongMethod, http.MethodGet)
	if got := wrongMethod.Header().Get("content-type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("POST db-status: content type = %q, want text/plain; charset=utf-8", got)
	}
	if got := wrongMethod.Header().Get("x-content-type-options"); got != "nosniff" {
		t.Fatalf("POST db-status: x-content-type-options = %q, want nosniff", got)
	}
	if got := wrongMethod.Body.String(); got != "Method not allowed\n" {
		t.Fatalf("POST db-status: body = %q, want %q", got, "Method not allowed\n")
	}
	if requestID := wrongMethod.Header().Get("x-request-id"); requestID != "" {
		t.Fatalf("POST db-status: x-request-id = %q, want empty", requestID)
	}

	unauthorized := methodRoutingRequest(app, http.MethodGet, "/api/admin/system/db-status", "")
	assertJSONError(t, unauthorized, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, unauthorized, "")
}

func TestDBStatusMethodRouteRejectsHEADAsPlainText(t *testing.T) {
	server := httptest.NewServer(newSystemMethodRoutingServer("db-status-method-routing-token"))
	defer server.Close()

	response := doSystemMethodRoutingHEAD(t, server, "/api/admin/system/db-status", "")
	defer response.Body.Close()

	assertRealHEADResponse(t, response, http.StatusMethodNotAllowed, http.MethodGet, "text/plain; charset=utf-8", false)
	if got := response.Header.Get("x-content-type-options"); got != "nosniff" {
		t.Fatalf("HEAD db-status: x-content-type-options = %q, want nosniff", got)
	}
}

func TestDBStatusMethodRouteReachesHandler(t *testing.T) {
	app := newSystemMethodRoutingServer("db-status-method-routing-token")
	response := methodRoutingRequest(app, http.MethodGet, "/api/admin/system/db-status", "db-status-method-routing-token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET db-status: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("content-type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("GET db-status: content type = %q, want application/json", got)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"database_type", "is_docker", "connection_ok"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("GET db-status: response is missing %q: %#v", field, payload)
		}
	}
}

func TestDBStatusMethodRoutePreservesRBAC(t *testing.T) {
	config := Config{AdminToken: "db-status-method-routing-token"}
	store := NewMemoryStoreWithConfig(config)
	user, err := store.CreateAdminUser(AdminUser{
		Username: "db-status-method-routing-user",
		Name:     "DB Status Method Routing User",
		Email:    "db-status-method-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "db-status-method-routing-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, config).Handler()

	response := methodRoutingRequest(app, http.MethodGet, "/api/admin/system/db-status", session.Token)
	assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
	assertAllowHeader(t, response, "")
}

func TestSystemRollbackMethodRoutePreservesRequestAndAudit(t *testing.T) {
	// The rollback target must carry a verified database compatibility record
	// (v0.4.0 for the bridge release); records for other legacy versions do
	// not exist, so the compatibility preflight would refuse them.
	root := prepareNativeInstallRoot(t, "0.4.1", map[string]string{"0.4.0": "previous"})
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected release lookup", http.StatusInternalServerError)
	}))
	defer releases.Close()

	config := Config{
		AdminToken:     "system-rollback-routing-token",
		AppVersion:     "0.4.1",
		BuildType:      releaseBuildType,
		DeploymentType: nativeDeploymentType,
		InstallRoot:    root,
	}
	store := NewMemoryStoreWithConfig(config)
	app := NewWithConfig(store, config)
	app.versions = nativeTestVersionService(root, "0.4.1", releases)

	response := methodRoutingJSONRequest(t, app.Handler(), http.MethodPost, "/api/admin/system/rollback", map[string]any{
		"version": "0.4.0",
	}, "system-rollback-routing-token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST rollback: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		NeedRestart   bool   `json:"need_restart"`
		TargetVersion string `json:"target_version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.NeedRestart || payload.TargetVersion != "0.4.0" {
		t.Fatalf("POST rollback: unexpected response: %#v", payload)
	}
	assertNativeCurrentVersion(t, root, "0.4.0")
	assertSystemVersionAudit(t, store, "rollback", "success", "0.4.0")
}

func TestMetricsMethodRoutePreservesDisabledState(t *testing.T) {
	config := Config{AdminToken: "metrics-method-routing-token", MetricsEnabled: false}
	app := NewWithConfig(NewMemoryStoreWithConfig(config), config).Handler()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			response := methodRoutingRequest(app, method, "/metrics", "metrics-method-routing-token")
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s disabled metrics: expected 404, got %d: %s", method, response.Code, response.Body.String())
			}
			assertAllowHeader(t, response, "")
		})
	}

	server := httptest.NewServer(app)
	defer server.Close()
	response := doSystemMethodRoutingHEAD(t, server, "/metrics", "metrics-method-routing-token")
	defer response.Body.Close()
	assertRealHEADResponse(t, response, http.StatusNotFound, "", "text/plain; charset=utf-8", false)
}

func TestMetricsMethodRouteRejectsWrongMethodsBeforeTokenValidation(t *testing.T) {
	config := Config{AdminToken: "metrics-method-routing-token", MetricsEnabled: true}
	app := NewWithConfig(NewMemoryStoreWithConfig(config), config).Handler()

	for _, token := range []string{"", "wrong-token", "metrics-method-routing-token"} {
		response := methodRoutingRequest(app, http.MethodPost, "/metrics", token)
		assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		assertAllowHeader(t, response, http.MethodGet)
	}

	server := httptest.NewServer(app)
	defer server.Close()
	response := doSystemMethodRoutingHEAD(t, server, "/metrics", "")
	defer response.Body.Close()
	assertRealHEADResponse(t, response, http.StatusMethodNotAllowed, http.MethodGet, "application/json", true)
}

func TestMetricsMethodRouteHidesEnabledEndpointWithoutUsableToken(t *testing.T) {
	config := Config{MetricsEnabled: true}
	app := NewWithConfig(NewMemoryStoreWithConfig(config), config).Handler()

	response := methodRoutingRequest(app, http.MethodGet, "/metrics", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET metrics without configured token: expected 404, got %d: %s", response.Code, response.Body.String())
	}
	assertAllowHeader(t, response, "")
}

func TestMetricsMethodRouteRejectsWrongMethodWithoutUsableToken(t *testing.T) {
	config := Config{MetricsEnabled: true}
	app := NewWithConfig(NewMemoryStoreWithConfig(config), config).Handler()

	response := methodRoutingRequest(app, http.MethodPost, "/metrics", "")
	assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, response, http.MethodGet)
}

func TestSystemMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newSystemMethodRoutingServer("system-method-routing-token")
	for _, path := range []string{
		"/api/admin/system/version",
		"/api/admin/system/db-status",
		"/metrics",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", http.MethodGet)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("OPTIONS %s: expected 204, got %d: %s", path, response.Code, response.Body.String())
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func systemMethodRouteContracts() []systemMethodRouteContract {
	return []systemMethodRouteContract{
		{name: "version", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/system/version"},
		{name: "rollback_versions", allowedMethod: http.MethodGet, wrongMethod: http.MethodPost, path: "/api/admin/system/rollback-versions"},
		{name: "update", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/system/update"},
		{name: "rollback", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/system/rollback"},
		{name: "restart", allowedMethod: http.MethodPost, wrongMethod: http.MethodGet, path: "/api/admin/system/restart"},
	}
}

func newSystemMethodRoutingServer(adminToken string) http.Handler {
	config := Config{AdminToken: adminToken}
	return NewWithConfig(NewMemoryStoreWithConfig(config), config).Handler()
}

func doSystemMethodRoutingHEAD(t *testing.T, server *httptest.Server, path string, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("authorization", "Bearer "+token)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertRealHEADResponse(t *testing.T, response *http.Response, wantStatus int, wantAllow string, wantContentType string, wantRequestID bool) {
	t.Helper()
	if response.StatusCode != wantStatus {
		t.Fatalf("HEAD status = %d, want %d", response.StatusCode, wantStatus)
	}
	allowValues, allowPresent := response.Header[http.CanonicalHeaderKey("Allow")]
	if wantAllow == "" {
		if allowPresent {
			t.Fatalf("HEAD Allow is present with value %q, want absent", allowValues)
		}
	} else if !allowPresent || response.Header.Get("Allow") != wantAllow {
		t.Fatalf("HEAD Allow = %q, want %q", response.Header.Get("Allow"), wantAllow)
	}
	if got := response.Header.Get("content-type"); !strings.HasPrefix(got, wantContentType) {
		t.Fatalf("HEAD content type = %q, want %q", got, wantContentType)
	}
	if requestID := response.Header.Get("x-request-id"); wantRequestID != (requestID != "") {
		t.Fatalf("HEAD x-request-id = %q, want presence %t", requestID, wantRequestID)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD body = %q, want empty", body)
	}
}
