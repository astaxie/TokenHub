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

func TestMethodRoutingContracts(t *testing.T) {
	app := newTestServer()

	valid := methodRoutingRequest(app, http.MethodGet, "/livez", "")
	if valid.Code != http.StatusOK {
		t.Fatalf("GET /livez: expected 200, got %d: %s", valid.Code, valid.Body.String())
	}

	tests := []struct {
		name      string
		method    string
		path      string
		wantCode  string
		wantAllow string
	}{
		{name: "gateway wrong method", method: http.MethodGet, path: "/v1/chat/completions", wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "models rejects head", method: http.MethodHead, path: "/v1/models", wantCode: "method_not_allowed", wantAllow: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "")
			assertJSONError(t, response, http.StatusMethodNotAllowed, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestSimpleMethodRoutesReachHandlers(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		method     string
		path       string
		wantStatus int
	}{
		{method: http.MethodGet, path: "/livez", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/readyz", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/api/admin/auth/identity-providers", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/api/admin/auth/oauth/start", wantStatus: http.StatusNotFound},
		{method: http.MethodGet, path: "/api/admin/auth/oauth/callback", wantStatus: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/admin/auth/oauth/exchange", wantStatus: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/admin/auth/login", wantStatus: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/admin/auth/logout", wantStatus: http.StatusNoContent},
		{method: http.MethodPost, path: "/api/admin/auth/reset-password", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "")
			if response.Code != test.wantStatus {
				t.Fatalf("expected %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestSimpleMethodRoutesRejectWrongMethodsAsJSON(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		allowed string
		method  string
		path    string
	}{
		{allowed: http.MethodGet, method: http.MethodPost, path: "/livez"},
		{allowed: http.MethodGet, method: http.MethodPost, path: "/readyz"},
		{allowed: http.MethodGet, method: http.MethodPost, path: "/healthz"},
		{allowed: http.MethodGet, method: http.MethodPost, path: "/api/admin/auth/identity-providers"},
		{allowed: http.MethodGet, method: http.MethodPost, path: "/api/admin/auth/oauth/start"},
		{allowed: http.MethodGet, method: http.MethodPost, path: "/api/admin/auth/oauth/callback"},
		{allowed: http.MethodPost, method: http.MethodGet, path: "/api/admin/auth/oauth/exchange"},
		{allowed: http.MethodPost, method: http.MethodGet, path: "/api/admin/auth/login"},
		{allowed: http.MethodPost, method: http.MethodGet, path: "/api/admin/auth/logout"},
		{allowed: http.MethodPost, method: http.MethodGet, path: "/api/admin/auth/reset-password"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "")
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, test.allowed)
		})
	}
}

func TestSimpleGETMethodRoutesRejectHEAD(t *testing.T) {
	server := httptest.NewServer(newTestServer())
	defer server.Close()

	paths := []string{
		"/livez",
		"/readyz",
		"/healthz",
		"/api/admin/auth/identity-providers",
		"/api/admin/auth/oauth/start",
		"/api/admin/auth/oauth/callback",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405, got %d", response.StatusCode)
			}
			if got := response.Header.Get("Allow"); got != http.MethodGet {
				t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
			}
			if requestID := response.Header.Get("x-request-id"); requestID == "" {
				t.Fatal("x-request-id is empty")
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != 0 {
				t.Fatalf("HEAD response body = %q, want empty", body)
			}
		})
	}
}

func TestSingleMethodRouteLeavesFallbackHeadersToFallback(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	server.registerSingleMethodRoute(http.MethodGet, "/protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, NewHTTPError(http.StatusUnauthorized, "invalid_admin_token", "Invalid admin token"))
	})

	response := methodRoutingRequest(server.mux, http.MethodPost, "/protected", "")
	assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, response, "")
}

func TestSimpleMethodRouteLogoutRevokesSession(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "method-routing-old-password")
	token, _ := loginMethodRoutingAdmin(t, app, "method-routing-old-password")

	response := methodRoutingRequest(app, http.MethodPost, "/api/admin/auth/logout", token)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout: expected 204, got %d: %s", response.Code, response.Body.String())
	}

	response = methodRoutingRequest(app, http.MethodGet, "/api/admin/auth/me", token)
	assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
}

func TestSimpleMethodRouteResetPasswordInvalidatesOldCredentials(t *testing.T) {
	const (
		oldPassword = "method-routing-old-password"
		newPassword = "method-routing-new-password"
	)
	store, app := newMethodRoutingAdminServer(t, oldPassword)
	oldSession, user := loginMethodRoutingAdmin(t, app, oldPassword)
	resetToken, _, err := store.CreateAdminPasswordResetToken(user.ID, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	response := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/auth/reset-password", map[string]any{
		"token":    resetToken,
		"password": newPassword,
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("reset password: expected 200, got %d: %s", response.Code, response.Body.String())
	}

	response = methodRoutingRequest(app, http.MethodGet, "/api/admin/auth/me", oldSession)
	assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")

	response = methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Email,
		"password": oldPassword,
	}, "")
	assertJSONError(t, response, http.StatusUnauthorized, "invalid_credentials")

	loginMethodRoutingAdmin(t, app, newPassword)
}

func TestMethodRoutingPreservesAdminAuthenticationOrder(t *testing.T) {
	response := methodRoutingRequest(New(NewMemoryStore()).Handler(), http.MethodPost, "/api/admin/auth/me", "")
	assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, response, "")
}

func TestMethodRoutingPreservesAnthropicErrorShape(t *testing.T) {
	response := methodRoutingRequest(newTestServer(), http.MethodGet, "/v1/messages", "")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /v1/messages: expected 405, got %d: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("GET /v1/messages: content type = %q, want application/json", contentType)
	}
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode Anthropic method error: %v", err)
	}
	if payload.Type != "error" || payload.Error.Type != "invalid_request_error" || payload.Error.Code != "method_not_allowed" || payload.Error.Message == "" {
		t.Fatalf("unexpected Anthropic method error: %#v", payload)
	}
	assertRequestID(t, response, payload.RequestID)
	assertAllowHeader(t, response, http.MethodPost)
}

func TestMethodRoutingPreservesCORSPreflight(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/admin/auth/login", nil)
	request.Header.Set("origin", "https://console.example.com")
	request.Header.Set("access-control-request-method", http.MethodPost)
	request.Header.Set("access-control-request-headers", "authorization,content-type")
	response := httptest.NewRecorder()

	newTestServer().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /api/admin/auth/login: expected 204, got %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
		t.Fatalf("access-control-allow-methods = %q", got)
	}
	if got := response.Header().Get("access-control-allow-headers"); got != "authorization,content-type" {
		t.Fatalf("access-control-allow-headers = %q", got)
	}
}

func methodRoutingRequest(handler http.Handler, method string, path string, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func methodRoutingJSONRequest(t *testing.T, handler http.Handler, method string, path string, payload any, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	request.Header.Set("content-type", "application/json")
	if token != "" {
		request.Header.Set("authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newMethodRoutingAdminServer(t *testing.T, password string) (*GormStore, http.Handler) {
	t.Helper()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = password
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	return store, NewWithConfig(store, config).Handler()
}

func loginMethodRoutingAdmin(t *testing.T, handler http.Handler, password string) (string, AdminUser) {
	t.Helper()
	response := methodRoutingJSONRequest(t, handler, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "admin@tokenhub.local",
		"password": password,
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Token string    `json:"token"`
		User  AdminUser `json:"user"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" || payload.User.ID == "" {
		t.Fatalf("incomplete login response: %#v", payload)
	}
	return payload.Token, payload.User
}

func assertJSONError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("expected %d, got %d: %s", wantStatus, response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode method error: %v", err)
	}
	if payload.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, wantCode)
	}
	if payload.Error.Type != wantCode {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, wantCode)
	}
	if payload.Error.Message == "" {
		t.Fatal("error message is empty")
	}
	if wantCode == "method_not_allowed" && payload.Error.Message != "Method not allowed" {
		t.Fatalf("error message = %q, want %q", payload.Error.Message, "Method not allowed")
	}
	assertRequestID(t, response, payload.RequestID)
}

func assertRequestID(t *testing.T, response *httptest.ResponseRecorder, bodyRequestID string) {
	t.Helper()
	if bodyRequestID == "" {
		t.Fatal("error body has no request_id")
	}
	if headerRequestID := response.Header().Get("x-request-id"); headerRequestID != bodyRequestID {
		t.Fatalf("body request_id = %q, header x-request-id = %q", bodyRequestID, headerRequestID)
	}
}

func assertAllowHeader(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	_, present := response.Header()[http.CanonicalHeaderKey("Allow")]
	if want == "" {
		if present {
			t.Fatalf("Allow header is present with value %q, want it absent", response.Header().Get("Allow"))
		}
		return
	}
	if !present {
		t.Fatalf("Allow header is absent, want %q", want)
	}
	if got := response.Header().Get("Allow"); got != want {
		t.Fatalf("Allow = %q, want %q", got, want)
	}
}
