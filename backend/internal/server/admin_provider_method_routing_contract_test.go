package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type adminProviderMethodRoute struct {
	name        string
	path        string
	wrongMethod string
	allow       string
}

func TestAdminProviderMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-routing-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "provider-routing-user",
		Name:     "Provider Routing User",
		Email:    "provider-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "provider-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	providersBefore := len(store.ListProviders())
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminProviderMethodRoutes() {
		t.Run(route.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/ordinary_user", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, userSession.Token)
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})
	}
	if got := len(store.ListProviders()); got != providersBefore {
		t.Fatalf("wrong-method requests changed provider count: got %d, want %d", got, providersBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong-method requests wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminProviderMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-head-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-head-routing-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "provider-head-routing-user",
		Name:     "Provider Head Routing User",
		Email:    "provider-head-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "provider-head-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminProviderMethodRoutes() {
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "ordinary_user", token: userSession.Token, wantStatus: http.StatusForbidden},
			{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantAllow: route.allow},
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
				defer response.Body.Close()
				assertRealHEADResponse(t, response, auth.wantStatus, auth.wantAllow, "application/json", true)
			})
		}
	}
}

func TestAdminProviderMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newTestServer()
	for _, route := range adminProviderMethodRoutes() {
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

func TestAdminProviderMethodRoutesPreservePathBoundaries(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "provider-path-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-path-routing-password")
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "empty catalog id", method: http.MethodGet, path: "/api/admin/provider-catalog/", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "catalog trailing slash", method: http.MethodDelete, path: "/api/admin/provider-catalog/openai/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
		{name: "special catalog trailing slash", method: http.MethodDelete, path: "/api/admin/provider-catalog/custom/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "GET, POST"},
		{name: "encoded catalog id wrong method", method: http.MethodPost, path: "/api/admin/provider-catalog/vendor%2Fmodel", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
		{name: "empty provider id", method: http.MethodGet, path: "/api/admin/providers/", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "provider trailing slash", method: http.MethodGet, path: "/api/admin/providers/prv_mock/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "test connection trailing slash", method: http.MethodGet, path: "/api/admin/providers/test-connection/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "monitoring wrong method", method: http.MethodPatch, path: "/api/admin/providers/monitoring", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
		{name: "unknown action", method: http.MethodGet, path: "/api/admin/providers/prv_mock/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "extra action segment", method: http.MethodPost, path: "/api/admin/providers/prv_mock/health/extra", wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, adminToken)
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestAdminProviderMethodRoutesReachCatalogHandlers(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "provider-success-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-success-routing-password")
	for _, path := range []string{
		"/api/admin/provider-catalog",
		"/api/admin/provider-catalog/openai",
		"/api/admin/provider-catalog/custom",
		"/api/admin/provider-catalog/kronk",
		"/api/admin/providers",
	} {
		response := methodRoutingRequest(app, http.MethodGet, path, adminToken)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, response.Code, response.Body.String())
		}
	}

	codexGET := methodRoutingRequest(app, http.MethodGet, "/api/admin/provider-catalog/"+codexProviderCatalogID, adminToken)
	assertJSONError(t, codexGET, http.StatusConflict, "codex_account_required")
	codexPOST := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/provider-catalog/"+codexProviderCatalogID, map[string]any{}, adminToken)
	assertJSONError(t, codexPOST, http.StatusBadRequest, "openai_account_token_missing")
	unknown := methodRoutingRequest(app, http.MethodGet, "/api/admin/provider-catalog/not-configured", adminToken)
	assertJSONError(t, unknown, http.StatusNotFound, "provider_catalog_not_found")
}

func TestAdminProviderCatalogRoutesPreserveCodexQueriesCredentialsAndHeaders(t *testing.T) {
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "provider-codex-catalog-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_codex_catalog_route",
		Name:    "Codex Catalog Route",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_catalog_route",
		ProviderID:   provider.ID,
		Name:         "Codex Catalog Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "stored-codex-token",
			AccountID:   "stored-codex-account",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	type observedCredentials struct {
		token     string
		accountID string
	}
	observed := make([]observedCredentials, 0, 2)
	server := NewWithConfig(store, config)
	server.codexSubscription.ModelsURL = "https://codex-models.example/models"
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://codex-models.example/models?client_version="+openAICodexVersion {
			t.Fatalf("unexpected Codex models request: %s %s", request.Method, request.URL.String())
		}
		observed = append(observed, observedCredentials{
			token:     request.Header.Get("authorization"),
			accountID: request.Header.Get("chatgpt-account-id"),
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"models":[{"slug":"gpt-route-codex","display_name":"GPT Route Codex","visibility":"list","supported_in_api":true,"priority":1}]}`,
			)),
			Request: request,
		}, nil
	})}
	app := server.Handler()
	adminToken, _ := loginMethodRoutingAdmin(t, app, config.BootstrapAdminPassword)

	getResponse := methodRoutingRequest(app, http.MethodGet, "/api/admin/provider-catalog/"+codexProviderCatalogID+"?resource_id="+resource.ID, adminToken)
	assertCodexCatalogRoutingResponse(t, getResponse)
	postResponse := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/provider-catalog/"+codexProviderCatalogID, ProviderResourceCredentials{
		AccessToken: "submitted-codex-token",
		AccountID:   "submitted-codex-account",
	}, adminToken)
	assertCodexCatalogRoutingResponse(t, postResponse)

	want := []observedCredentials{
		{token: "Bearer stored-codex-token", accountID: "stored-codex-account"},
		{token: "Bearer submitted-codex-token", accountID: "submitted-codex-account"},
	}
	if len(observed) != len(want) {
		t.Fatalf("Codex upstream request count = %d, want %d", len(observed), len(want))
	}
	for index := range want {
		if observed[index] != want[index] {
			t.Fatalf("Codex upstream request %d credentials = %+v, want %+v", index, observed[index], want[index])
		}
	}
}

func assertCodexCatalogRoutingResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("Codex catalog: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data   ProviderCatalogEntry `json:"data"`
		Source string               `json:"source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source != "openai-codex-live" || payload.Data.ID != codexProviderCatalogID ||
		len(payload.Data.Models) != 1 || payload.Data.Models[0].ID != "gpt-route-codex" {
		t.Fatalf("unexpected Codex catalog response: %+v", payload)
	}
}

func TestAdminProviderCatalogRoutesPreserveRefreshQuery(t *testing.T) {
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "provider-catalog-refresh-password"
	config.ProviderCatalogFile = catalogFile
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(upstream.Close)
	server := NewWithConfig(store, config)
	server.providerCatalog.upstreamURL = upstream.URL
	server.providerCatalog.upstreamClient = upstream.Client()
	app := server.Handler()
	adminToken, _ := loginMethodRoutingAdmin(t, app, config.BootstrapAdminPassword)

	readBaseURL := func(path string) string {
		t.Helper()
		response := methodRoutingRequest(app, http.MethodGet, path, adminToken)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, response.Code, response.Body.String())
		}
		var payload struct {
			Data ProviderCatalogEntry `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return payload.Data.BaseURL
	}
	readCollectionBaseURL := func(path string) string {
		t.Helper()
		response := methodRoutingRequest(app, http.MethodGet, path, adminToken)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, response.Code, response.Body.String())
		}
		var payload struct {
			Data []ProviderCatalogEntry `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		for _, entry := range payload.Data {
			if entry.ID == "fresh-provider" {
				return entry.BaseURL
			}
		}
		t.Fatal("collection response has no fresh-provider entry")
		return ""
	}

	const originalURL = "https://fresh-provider.example/v1"
	const refreshedURL = "https://refreshed-provider.example/v1"
	const collectionRefreshedURL = "https://collection-refreshed-provider.example/v1"
	if got := readBaseURL("/api/admin/provider-catalog/fresh-provider?refresh=true"); got != originalURL {
		t.Fatalf("initial refreshed base URL = %q, want %q", got, originalURL)
	}
	replaceProviderCatalogFixtureURL(t, catalogFile, "fresh-provider", refreshedURL)
	if got := readBaseURL("/api/admin/provider-catalog/fresh-provider"); got != originalURL {
		t.Fatalf("cached base URL = %q, want %q", got, originalURL)
	}
	if got := readBaseURL("/api/admin/provider-catalog/fresh-provider?refresh=true"); got != refreshedURL {
		t.Fatalf("refreshed base URL = %q, want %q", got, refreshedURL)
	}
	replaceProviderCatalogFixtureURL(t, catalogFile, "fresh-provider", collectionRefreshedURL)
	if got := readCollectionBaseURL("/api/admin/provider-catalog"); got != refreshedURL {
		t.Fatalf("cached collection base URL = %q, want %q", got, refreshedURL)
	}
	if got := readCollectionBaseURL("/api/admin/provider-catalog?refresh=true"); got != collectionRefreshedURL {
		t.Fatalf("refreshed collection base URL = %q, want %q", got, collectionRefreshedURL)
	}
}

func TestAdminProviderMethodRoutesPreserveEscapedProviderID(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-escaped-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-escaped-routing-password")
	provider := store.AddProvider(Provider{
		ID:      "tenant/provider",
		Name:    "Escaped Provider",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})
	health := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/tenant%2Fprovider/health", map[string]any{"healthy": false}, adminToken)
	if health.Code != http.StatusOK {
		t.Fatalf("POST health for escaped provider: expected 200, got %d: %s", health.Code, health.Body.String())
	}
	updated, ok := store.GetProvider(provider.ID)
	if !ok || updated.Healthy {
		t.Fatalf("escaped provider health was not updated: %+v", updated)
	}
	refresh := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/tenant%2Fprovider/refresh-token", map[string]any{"healthy": true}, adminToken)
	if refresh.Code != http.StatusOK {
		t.Fatalf("POST refresh-token for escaped provider: expected 200, got %d: %s", refresh.Code, refresh.Body.String())
	}
	updated, ok = store.GetProvider(provider.ID)
	if !ok || !updated.Healthy {
		t.Fatalf("escaped provider refresh-token was not routed to health update: %+v", updated)
	}

	response := methodRoutingRequest(app, http.MethodDelete, "/api/admin/providers/tenant%2Fprovider", adminToken)
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE escaped provider: expected 204, got %d: %s", response.Code, response.Body.String())
	}
	if _, ok := store.GetProvider(provider.ID); ok {
		t.Fatal("escaped provider ID was not deleted")
	}
	wantActions := map[string]bool{"health": false, "delete": false}
	for _, event := range store.ListAuditEvents() {
		if _, ok := wantActions[event.Action]; ok && event.ResourceType == "provider" && event.ResourceID == provider.ID {
			wantActions[event.Action] = true
		}
	}
	for action, found := range wantActions {
		if !found {
			t.Fatalf("missing %s audit for escaped provider ID", action)
		}
	}
}

func TestAdminProviderMethodRoutesPreserveMonitoringProviderIDWithTrailingSlash(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-monitoring-id-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-monitoring-id-password")
	store.AddProvider(Provider{
		ID:      "monitoring",
		Name:    "Monitoring Provider",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})

	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/providers/monitoring/", map[string]any{
		"name": "Updated Monitoring Provider",
	}, adminToken)
	if patched.Code != http.StatusOK {
		t.Fatalf("PATCH monitoring provider: expected 200, got %d: %s", patched.Code, patched.Body.String())
	}
	provider, ok := store.GetProvider("monitoring")
	if !ok || provider.Name != "Updated Monitoring Provider" {
		t.Fatalf("monitoring provider was not updated: %+v", provider)
	}
	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/providers/monitoring/", adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE monitoring provider: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	if _, ok := store.GetProvider("monitoring"); ok {
		t.Fatal("monitoring provider was not deleted")
	}
}

func TestAdminProviderMethodRoutesKeepReservedStaticPathsAheadOfProviderIDs(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-reserved-path-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-reserved-path-password")
	for _, id := range []string{"monitoring", "test-connection"} {
		store.AddProvider(Provider{
			ID:      id,
			Name:    "Reserved " + id,
			Type:    ProviderMock,
			Status:  StatusActive,
			Healthy: true,
		})
	}

	for _, test := range []struct {
		path  string
		allow string
	}{
		{path: "/api/admin/providers/monitoring", allow: http.MethodGet},
		{path: "/api/admin/providers/test-connection", allow: http.MethodPost},
	} {
		for _, method := range []string{http.MethodPatch, http.MethodDelete} {
			t.Run(method+" "+test.path, func(t *testing.T) {
				response := methodRoutingJSONRequest(t, app, method, test.path, map[string]any{"name": "must not change"}, adminToken)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, test.allow)
			})
		}
	}

	for _, id := range []string{"monitoring", "test-connection"} {
		provider, ok := store.GetProvider(id)
		if !ok || provider.Name != "Reserved "+id {
			t.Fatalf("reserved provider %q was changed: %+v", id, provider)
		}
	}
}

func TestAdminProviderMethodRoutesPreserveLegacyRefreshToken(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-refresh-token-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-refresh-token-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "provider-refresh-token-user",
		Name:     "Provider Refresh Token User",
		Email:    "provider-refresh-token-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "provider-refresh-token-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_refresh_token",
		Name:    "Refresh Token Provider",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})
	auditsBefore := len(store.ListAuditEvents())

	nonexistentPost := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/prv_missing/refresh-token", map[string]any{
		"healthy": false,
	}, adminToken)
	assertJSONError(t, nonexistentPost, http.StatusNotFound, "provider_not_found")
	assertAllowHeader(t, nonexistentPost, "")
	if _, ok := store.GetProvider("prv_missing"); ok {
		t.Fatal("nonexistent refresh-token target was unexpectedly created")
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("nonexistent refresh-token request wrote audit events: got %d, want %d", got, auditsBefore)
	}

	ordinaryPost := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/"+provider.ID+"/refresh-token", map[string]any{
		"healthy": false,
	}, userSession.Token)
	assertJSONError(t, ordinaryPost, http.StatusForbidden, "admin_forbidden")
	assertAllowHeader(t, ordinaryPost, "")
	if updated, ok := store.GetProvider(provider.ID); !ok || !updated.Healthy {
		t.Fatalf("ordinary-user refresh-token changed provider health: %+v", updated)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("ordinary-user refresh-token request wrote audit events: got %d, want %d", got, auditsBefore)
	}

	unauthorizedPost := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/"+provider.ID+"/refresh-token", map[string]any{
		"healthy": false,
	}, "")
	assertJSONError(t, unauthorizedPost, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, unauthorizedPost, "")
	if updated, ok := store.GetProvider(provider.ID); !ok || !updated.Healthy {
		t.Fatalf("unauthorized refresh-token changed provider health: %+v", updated)
	}

	unauthorized := methodRoutingRequest(app, http.MethodGet, "/api/admin/providers/"+provider.ID+"/refresh-token", "")
	assertJSONError(t, unauthorized, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, unauthorized, "")
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		t.Run("wrong method "+method, func(t *testing.T) {
			wrongMethod := methodRoutingRequest(app, method, "/api/admin/providers/"+provider.ID+"/refresh-token", adminToken)
			assertJSONError(t, wrongMethod, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, wrongMethod, http.MethodPost)
		})
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("unauthorized and wrong-method requests wrote audit events: got %d, want %d", got, auditsBefore)
	}
	malformedRequest := httptest.NewRequest(http.MethodPost, "/api/admin/providers/"+provider.ID+"/refresh-token", strings.NewReader(`{"healthy":`))
	malformedRequest.Header.Set("authorization", "Bearer "+adminToken)
	malformedRequest.Header.Set("content-type", "application/json")
	malformedResponse := httptest.NewRecorder()
	app.ServeHTTP(malformedResponse, malformedRequest)
	assertJSONError(t, malformedResponse, http.StatusBadRequest, "invalid_request")
	assertAllowHeader(t, malformedResponse, "")
	if updated, ok := store.GetProvider(provider.ID); !ok || !updated.Healthy {
		t.Fatalf("malformed refresh-token changed provider health: %+v", updated)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("malformed refresh-token wrote audit events: got %d, want %d", got, auditsBefore)
	}
	refreshed := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/"+provider.ID+"/refresh-token", map[string]any{
		"healthy": false,
	}, adminToken)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("POST legacy refresh-token: expected 200, got %d: %s", refreshed.Code, refreshed.Body.String())
	}
	var response Provider
	if err := json.NewDecoder(refreshed.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ID != provider.ID || response.Healthy {
		t.Fatalf("legacy refresh-token response = %+v", response)
	}
	updated, ok := store.GetProvider(provider.ID)
	if !ok || updated.Healthy {
		t.Fatalf("legacy refresh-token did not preserve health behavior: %+v", updated)
	}
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "health", "provider", provider.ID)
}

func TestAdminProviderMethodRoutesPreserveTrailingSlashHandlers(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-trailing-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-trailing-routing-password")
	provider := store.AddProvider(Provider{
		ID:      "prv_trailing_route",
		Name:    "Trailing Route Provider",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})

	catalog := methodRoutingRequest(app, http.MethodGet, "/api/admin/provider-catalog/custom/", adminToken)
	if catalog.Code != http.StatusOK {
		t.Fatalf("catalog trailing slash: expected 200, got %d: %s", catalog.Code, catalog.Body.String())
	}
	connection := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/test-connection/", map[string]any{}, adminToken)
	assertJSONError(t, connection, http.StatusBadRequest, "provider_base_url_required")
	health := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/"+provider.ID+"/health/", map[string]any{"healthy": false}, adminToken)
	if health.Code != http.StatusOK {
		t.Fatalf("health trailing slash: expected 200, got %d: %s", health.Code, health.Body.String())
	}
	updated, ok := store.GetProvider(provider.ID)
	if !ok || updated.Healthy {
		t.Fatalf("health trailing slash did not update provider: %+v", updated)
	}
	refresh := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/providers/"+provider.ID+"/refresh-token/", map[string]any{"healthy": true}, adminToken)
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh-token trailing slash: expected 200, got %d: %s", refresh.Code, refresh.Body.String())
	}
	updated, ok = store.GetProvider(provider.ID)
	if !ok || !updated.Healthy {
		t.Fatalf("refresh-token trailing slash did not preserve health behavior: %+v", updated)
	}
	wrongRefresh := methodRoutingRequest(app, http.MethodGet, "/api/admin/providers/"+provider.ID+"/refresh-token/", adminToken)
	assertJSONError(t, wrongRefresh, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, wrongRefresh, http.MethodPost)
	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/providers/"+provider.ID+"/", adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE trailing slash: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
}

func adminProviderMethodRoutes() []adminProviderMethodRoute {
	return []adminProviderMethodRoute{
		{name: "catalog_collection", path: "/api/admin/provider-catalog", wrongMethod: http.MethodPost, allow: http.MethodGet},
		{name: "catalog_item", path: "/api/admin/provider-catalog/openai", wrongMethod: http.MethodDelete, allow: http.MethodGet},
		{name: "codex_catalog", path: "/api/admin/provider-catalog/" + codexProviderCatalogID, wrongMethod: http.MethodDelete, allow: "GET, POST"},
		{name: "custom_catalog", path: "/api/admin/provider-catalog/custom", wrongMethod: http.MethodDelete, allow: "GET, POST"},
		{name: "kronk_catalog", path: "/api/admin/provider-catalog/kronk", wrongMethod: http.MethodDelete, allow: "GET, POST"},
		{name: "provider_collection", path: "/api/admin/providers", wrongMethod: http.MethodPut, allow: "GET, POST"},
		{name: "provider_monitoring", path: "/api/admin/providers/monitoring", wrongMethod: http.MethodPost, allow: http.MethodGet},
		{name: "test_connection", path: "/api/admin/providers/test-connection", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_item", path: "/api/admin/providers/prv_mock", wrongMethod: http.MethodGet, allow: "PATCH, DELETE"},
		{name: "provider_health", path: "/api/admin/providers/prv_mock/health", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_test", path: "/api/admin/providers/prv_mock/test", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_refresh_token", path: "/api/admin/providers/prv_mock/refresh-token", wrongMethod: http.MethodGet, allow: http.MethodPost},
	}
}
