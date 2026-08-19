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

type adminProviderResourceMethodRoute struct {
	name        string
	path        string
	wrongMethod string
	allow       string
}

func TestAdminProviderResourceMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-resource-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-resource-routing-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "provider-resource-routing-user",
		Name:     "Provider Resource Routing User",
		Email:    "provider-resource-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "provider-resource-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	resourcesBefore := len(store.ListProviderResources())
	modelsBefore := len(store.ListProviderModels())
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminProviderResourceMethodRoutes() {
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
	if got := len(store.ListProviderResources()); got != resourcesBefore {
		t.Fatalf("wrong-method requests changed provider resource count: got %d, want %d", got, resourcesBefore)
	}
	if got := len(store.ListProviderModels()); got != modelsBefore {
		t.Fatalf("wrong-method requests changed provider model count: got %d, want %d", got, modelsBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong-method requests wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminProviderResourceMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-resource-head-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-resource-head-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "provider-resource-head-user",
		Name:     "Provider Resource Head User",
		Email:    "provider-resource-head-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "provider-resource-head-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminProviderResourceMethodRoutes() {
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
				assertRealHEADResponse(t, response, auth.wantStatus, auth.wantAllow, "application/json", true)
				_ = response.Body.Close()
			})
		}
	}
}

func TestAdminProviderResourceMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newTestServer()
	for _, route := range adminProviderResourceMethodRoutes() {
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

func TestAdminProviderResourceQuotaRoutePreservesRefreshAndUpstreamHeaders(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_quota_route", Name: "Quota Route Provider", Type: ProviderOpenAICodex,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_quota_route", ProviderID: provider.ID, Name: "Quota Route Resource",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "quota-route-secret", AccountID: "quota-route-account",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	upstreamCalls := 0
	var authorization, accountID, beta string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		authorization = r.Header.Get("authorization")
		accountID = r.Header.Get("chatgpt-account-id")
		beta = r.Header.Get("openai-beta")
		w.Header().Set("content-type", "application/json")
		plan := "cached-plan"
		if upstreamCalls > 1 {
			plan = "refreshed-plan"
		}
		_, _ = io.WriteString(w, `{"plan_type":"`+plan+`","rate_limit":{"allowed":true,"limit_reached":false}}`)
	}))
	t.Cleanup(upstream.Close)
	server := New(store)
	server.codexSubscription.QuotaURL = upstream.URL + "/backend-api/wham/usage"
	server.codexSubscription.Client = upstream.Client()
	app := server.Handler()

	readQuota := func(path string) OpenAIAccountQuota {
		t.Helper()
		response := methodRoutingRequest(app, http.MethodGet, path, "dev_admin_token")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "quota-route-secret") {
			t.Fatalf("quota response leaked access token: %s", response.Body.String())
		}
		var quota OpenAIAccountQuota
		if err := json.Unmarshal(response.Body.Bytes(), &quota); err != nil {
			t.Fatal(err)
		}
		return quota
	}
	if quota := readQuota("/api/admin/provider-resources/" + resource.ID + "/quota"); quota.PlanType != "cached-plan" {
		t.Fatalf("initial quota = %+v", quota)
	}
	if quota := readQuota("/api/admin/provider-resources/" + resource.ID + "/quota"); quota.PlanType != "cached-plan" {
		t.Fatalf("cached quota = %+v", quota)
	}
	if upstreamCalls != 1 {
		t.Fatalf("cached quota made %d upstream calls, want 1", upstreamCalls)
	}
	if quota := readQuota("/api/admin/provider-resources/" + resource.ID + "/quota?refresh=true"); quota.PlanType != "refreshed-plan" {
		t.Fatalf("refreshed quota = %+v", quota)
	}
	if upstreamCalls != 2 {
		t.Fatalf("forced quota refresh made %d upstream calls, want 2", upstreamCalls)
	}
	if authorization != "Bearer quota-route-secret" || accountID != "quota-route-account" || beta != openAIAccountQuotaBeta {
		t.Fatalf("quota upstream headers: authorization=%q account=%q beta=%q", authorization, accountID, beta)
	}
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "query_quota", "provider_resource", resource.ID)
}

func TestAdminProviderResourceQuotaResetCreditsRoutePreservesHeadersAndAudit(t *testing.T) {
	upstream := &quotaResetUpstream{availableCount: 2, creditID: "credit_route"}
	server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
	response := methodRoutingRequest(server.Handler(), http.MethodGet, "/api/admin/provider-resources/"+quotaResetTestResourceID+"/quota/reset-credits", "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET reset credits: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var details openAIAccountQuotaResetCredits
	if err := json.Unmarshal(response.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if details.AvailableCount != 2 || len(details.Credits) != 1 || details.Credits[0].ID != "credit_route" {
		t.Fatalf("reset credits response = %+v", details)
	}
	if upstream.getCalls != 1 || len(upstream.getAuth) != 1 || upstream.getAuth[0] != "Bearer access_initial" ||
		len(upstream.getAccountIDs) != 1 || upstream.getAccountIDs[0] != "account_reset_test" {
		t.Fatalf("reset credits upstream calls=%d auth=%v accounts=%v", upstream.getCalls, upstream.getAuth, upstream.getAccountIDs)
	}
	if strings.Contains(response.Body.String(), "access_initial") {
		t.Fatalf("reset credits response leaked access token: %s", response.Body.String())
	}
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "query_quota_reset_credits", "provider_resource", quotaResetTestResourceID)
}

func TestAdminProviderResourceRefreshTokenRoutePreservesRefreshMaskingAndAudit(t *testing.T) {
	tokenCalls := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-route-secret" {
			t.Fatalf("unexpected refresh form: %v", r.Form)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"fresh-route-secret","refresh_token":"fresh-refresh-secret","token_type":"bearer","expires_in":3600}`)
	}))
	t.Cleanup(tokenServer.Close)
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	t.Cleanup(func() { openAIAccountOAuthTokenEndpoint = previousEndpoint })

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_refresh_route", Name: "Refresh Route Provider", Type: ProviderOpenAICodex,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_refresh_route", ProviderID: provider.ID, Name: "Refresh Route Resource",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "old-route-secret", RefreshToken: "refresh-route-secret",
			AccountID: "refresh-route-account", Email: "refresh-route@example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := methodRoutingJSONRequest(t, New(store).Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/refresh-token", map[string]any{}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST refresh token: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if tokenCalls != 1 {
		t.Fatalf("refresh token upstream calls = %d, want 1", tokenCalls)
	}
	var payload struct {
		CredentialSummary map[string]string `json:"credential_summary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CredentialSummary["account_id"] != "refresh-route-account" || payload.CredentialSummary["has_refresh_token"] != "true" {
		t.Fatalf("refresh credential summary = %+v", payload.CredentialSummary)
	}
	for _, secret := range []string{"old-route-secret", "refresh-route-secret", "fresh-route-secret", "fresh-refresh-secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("refresh response leaked %q: %s", secret, response.Body.String())
		}
	}
	stored, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("refreshed provider resource not found")
	}
	creds := store.providerResourceCredentialsForRuntime(stored)
	if creds.AccessToken != "fresh-route-secret" || creds.RefreshToken != "fresh-refresh-secret" || creds.AccountID != "refresh-route-account" {
		t.Fatalf("refreshed credentials = %+v", providerAccountCredentialSummary(creds))
	}
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "refresh_token", "provider_resource", resource.ID)
	for _, event := range store.ListAuditEvents() {
		for _, secret := range []string{"old-route-secret", "refresh-route-secret", "fresh-route-secret", "fresh-refresh-secret"} {
			if strings.Contains(event.BeforeSnapshot+event.AfterSnapshot, secret) {
				t.Fatalf("refresh audit leaked %q: %+v", secret, event)
			}
		}
	}
}

func TestAdminProviderResourceRoutesKeepStaticPathsAheadOfResourceIDs(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-resource-static-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-resource-static-password")
	provider := store.AddProvider(Provider{
		ID: "prv_resource_static", Name: "Resource Static Provider", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	for _, id := range []string{"bulk", "import"} {
		if _, err := store.AddProviderResource(ProviderResource{
			ID: id, ProviderID: provider.ID, Name: "Reserved " + id,
			ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, id := range []string{"bulk", "import"} {
		for _, method := range []string{http.MethodPatch, http.MethodDelete} {
			t.Run(method+"_"+id, func(t *testing.T) {
				response := methodRoutingJSONRequest(t, app, method, "/api/admin/provider-resources/"+id, map[string]any{"name": "must not change"}, adminToken)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, http.MethodPost)
			})
		}
	}
	for _, id := range []string{"bulk", "import"} {
		resource, ok := store.GetProviderResource(id)
		if !ok || resource.Name != "Reserved "+id {
			t.Fatalf("reserved provider resource %q was changed: %+v", id, resource)
		}
	}
}

func TestAdminProviderResourceRoutesPreserveEscapedSlashedAndTrailingIDs(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-resource-path-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-resource-path-password")
	provider := store.AddProvider(Provider{
		ID: "prv_resource_path", Name: "Resource Path Provider", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	resources := []struct {
		id   string
		path string
		name string
	}{
		{id: "tenant/encoded", path: "tenant%2Fencoded", name: "Encoded Resource"},
		{id: "tenant/raw", path: "tenant/raw", name: "Raw Slashed Resource"},
		{id: "tenant/health", path: "tenant%2Fhealth", name: "Encoded Action Resource"},
		{id: "trailing", path: "trailing/", name: "Trailing Resource"},
	}
	for _, test := range resources {
		if _, err := store.AddProviderResource(ProviderResource{
			ID: test.id, ProviderID: provider.ID, Name: test.name,
			ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range resources {
		t.Run(test.id, func(t *testing.T) {
			wantName := test.name + " Updated"
			response := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/provider-resources/"+test.path, map[string]any{"name": wantName}, adminToken)
			if response.Code != http.StatusOK {
				t.Fatalf("PATCH %s: expected 200, got %d: %s", test.path, response.Code, response.Body.String())
			}
			resource, ok := store.GetProviderResource(test.id)
			if !ok || resource.Name != wantName {
				t.Fatalf("provider resource %q was not updated: %+v", test.id, resource)
			}
		})
	}

	for _, test := range []struct {
		id   string
		path string
	}{
		{id: "tenant/raw", path: "tenant/raw/health"},
		{id: "tenant/health", path: "tenant%2Fhealth/health"},
		{id: "trailing", path: "trailing/health/"},
	} {
		t.Run("health_"+test.id, func(t *testing.T) {
			response := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/provider-resources/"+test.path, map[string]any{"healthy": false}, adminToken)
			if response.Code != http.StatusOK {
				t.Fatalf("POST %s: expected 200, got %d: %s", test.path, response.Code, response.Body.String())
			}
			resource, ok := store.GetProviderResource(test.id)
			if !ok || resource.Healthy {
				t.Fatalf("provider resource %q health was not updated: %+v", test.id, resource)
			}
		})
	}
	for _, test := range resources {
		assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "update", "provider_resource", test.id)
	}
	for _, id := range []string{"tenant/raw", "tenant/health", "trailing"} {
		assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "health", "provider_resource", id)
	}
	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/provider-resources/tenant%2Fencoded", adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE encoded provider resource: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	if _, ok := store.GetProviderResource("tenant/encoded"); ok {
		t.Fatal("encoded provider resource was not deleted")
	}
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "delete", "provider_resource", "tenant/encoded")
}

func TestAdminProviderModelRoutesPreserveItemsAndImportBoundary(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "provider-model-item-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "provider-model-item-password")
	patchModel := store.AddProviderModel(ProviderModel{
		ID: "pmdl_patch_route", ProviderID: "prv_mock", UpstreamModel: "patch-route-model",
		DisplayName: "Patch Route Model", Status: StatusActive,
		InputPriceUSDPer1M: 1, CacheReadPriceUSDPer1M: 0.5, OutputPriceUSDPer1M: 2,
	})
	deleteModel := store.AddProviderModel(ProviderModel{
		ID: "pmdl_delete_route", ProviderID: "prv_mock", UpstreamModel: "delete-route-model",
		DisplayName: "Delete Route Model", Status: StatusActive,
	})
	importModel := store.AddProviderModel(ProviderModel{
		ID: "import", ProviderID: "prv_mock", UpstreamModel: "import-route-model",
		DisplayName: "Import ID Model", Status: StatusActive,
	})
	slashedModel := store.AddProviderModel(ProviderModel{
		ID: "tenant/model", ProviderID: "prv_mock", UpstreamModel: "slashed-route-model",
		DisplayName: "Slashed ID Model", Status: StatusActive,
	})

	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/provider-models/"+patchModel.ID, map[string]any{
		"display_name": "Patched Route Model",
	}, adminToken)
	if patched.Code != http.StatusOK {
		t.Fatalf("PATCH provider model: expected 200, got %d: %s", patched.Code, patched.Body.String())
	}
	updated, ok := providerModelByIDForRoutingTest(store.ListProviderModels(), patchModel.ID)
	if !ok || updated.DisplayName != "Patched Route Model" || updated.InputPriceUSDPer1M != 1 || updated.CacheReadPriceUSDPer1M != 0.5 || updated.OutputPriceUSDPer1M != 2 {
		t.Fatalf("provider model patch changed unexpected fields: %+v", updated)
	}
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "encoded leading slash", path: "%2F" + patchModel.ID},
		{name: "encoded trailing slash", path: patchModel.ID + "%2F"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/provider-models/"+test.path, map[string]any{
				"display_name": test.name,
			}, adminToken)
			if response.Code != http.StatusOK {
				t.Fatalf("PATCH %s: expected 200, got %d: %s", test.path, response.Code, response.Body.String())
			}
			model, ok := providerModelByIDForRoutingTest(store.ListProviderModels(), patchModel.ID)
			if !ok || model.DisplayName != test.name {
				t.Fatalf("encoded edge slash did not preserve provider model ID: %+v", model)
			}
		})
	}

	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/provider-models/"+deleteModel.ID, adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE provider model: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	if _, ok := providerModelByIDForRoutingTest(store.ListProviderModels(), deleteModel.ID); ok {
		t.Fatal("provider model was not deleted")
	}

	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		response := methodRoutingJSONRequest(t, app, method, "/api/admin/provider-models/import", map[string]any{"display_name": "must not change"}, adminToken)
		assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		assertAllowHeader(t, response, http.MethodPost)
	}
	unchanged, ok := providerModelByIDForRoutingTest(store.ListProviderModels(), importModel.ID)
	if !ok || unchanged.DisplayName != importModel.DisplayName {
		t.Fatalf("provider model with reserved import ID was changed: %+v", unchanged)
	}
	encodedImport := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/provider-models/%2Fimport", map[string]any{
		"display_name": "Import ID Model Via Encoded Slash",
	}, adminToken)
	if encodedImport.Code != http.StatusOK {
		t.Fatalf("PATCH encoded import provider model: expected 200, got %d: %s", encodedImport.Code, encodedImport.Body.String())
	}
	importTrailing := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/provider-models/import/", map[string]any{
		"display_name": "Import ID Model Via Trailing Slash",
	}, adminToken)
	if importTrailing.Code != http.StatusOK {
		t.Fatalf("PATCH trailing import provider model: expected 200, got %d: %s", importTrailing.Code, importTrailing.Body.String())
	}
	updatedImport, ok := providerModelByIDForRoutingTest(store.ListProviderModels(), importModel.ID)
	if !ok || updatedImport.DisplayName != "Import ID Model Via Trailing Slash" {
		t.Fatalf("trailing import provider model path did not preserve item behavior: %+v", updatedImport)
	}

	encodedSlash := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/provider-models/tenant%2Fmodel", map[string]any{"display_name": "must not change"}, adminToken)
	assertJSONError(t, encodedSlash, http.StatusNotFound, "not_found")
	unchanged, ok = providerModelByIDForRoutingTest(store.ListProviderModels(), slashedModel.ID)
	if !ok || unchanged.DisplayName != slashedModel.DisplayName {
		t.Fatalf("slashed provider model ID was changed: %+v", unchanged)
	}
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "update", "provider_model", patchModel.ID)
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "delete", "provider_model", deleteModel.ID)
	assertProviderRoutingAuditEvent(t, store.ListAuditEvents(), "update", "provider_model", importModel.ID)
}

func assertProviderRoutingAuditEvent(t *testing.T, events []AuditEvent, action string, resourceType string, resourceID string) {
	t.Helper()
	for _, event := range events {
		if event.Action == action && event.ResourceType == resourceType && event.ResourceID == resourceID && event.Status == "success" {
			return
		}
	}
	t.Fatalf("missing successful %s audit for %s %q", action, resourceType, resourceID)
}

func providerModelByIDForRoutingTest(models []ProviderModel, id string) (ProviderModel, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return ProviderModel{}, false
}

func adminProviderResourceMethodRoutes() []adminProviderResourceMethodRoute {
	return []adminProviderResourceMethodRoute{
		{name: "provider_resource_collection", path: "/api/admin/provider-resources", wrongMethod: http.MethodPut, allow: "GET, POST"},
		{name: "provider_resource_bulk", path: "/api/admin/provider-resources/bulk", wrongMethod: http.MethodDelete, allow: http.MethodPost},
		{name: "provider_resource_import", path: "/api/admin/provider-resources/import", wrongMethod: http.MethodDelete, allow: http.MethodPost},
		{name: "provider_resource_item", path: "/api/admin/provider-resources/rsrc_mock_primary", wrongMethod: http.MethodGet, allow: "PATCH, DELETE"},
		{name: "provider_resource_quota", path: "/api/admin/provider-resources/rsrc_mock_primary/quota", wrongMethod: http.MethodPost, allow: http.MethodGet},
		{name: "provider_resource_quota_reset_credits", path: "/api/admin/provider-resources/rsrc_mock_primary/quota/reset-credits", wrongMethod: http.MethodPost, allow: http.MethodGet},
		{name: "provider_resource_quota_reset", path: "/api/admin/provider-resources/rsrc_mock_primary/quota/reset", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_resource_health", path: "/api/admin/provider-resources/rsrc_mock_primary/health", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_resource_test", path: "/api/admin/provider-resources/rsrc_mock_primary/test", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_resource_image_capability", path: "/api/admin/provider-resources/rsrc_mock_primary/image-capability", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_resource_refresh_token", path: "/api/admin/provider-resources/rsrc_mock_primary/refresh-token", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "provider_model_item", path: "/api/admin/provider-models/pmdl_missing", wrongMethod: http.MethodGet, allow: "PATCH, DELETE"},
		{name: "provider_model_import", path: "/api/admin/provider-models/import", wrongMethod: http.MethodDelete, allow: http.MethodPost},
	}
}
