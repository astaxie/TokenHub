package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdminCreatesProviderResource(t *testing.T) {
	app := newTestServer()

	resourceResp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"provider_id":     "prv_mock",
		"name":            "Mock Backup Resource",
		"resource_type":   "mock",
		"region":          "sg",
		"environment":     "backup",
		"status":          "active",
		"healthy":         true,
		"priority":        2,
		"weight":          80,
		"rate_limit_rpm":  600,
		"token_limit_tpm": 90000,
		"api_key":         "secret-resource-key",
	}, "")
	if resourceResp.Code != http.StatusCreated {
		t.Fatalf("expected provider resource created, got %d: %s", resourceResp.Code, resourceResp.Body)
	}
	if strings.Contains(resourceResp.Body, "secret-resource-key") {
		t.Fatalf("resource secret should not be returned: %s", resourceResp.Body)
	}
	var resource ProviderResource
	if err := json.Unmarshal([]byte(resourceResp.Body), &resource); err != nil {
		t.Fatal(err)
	}
	if resource.ID == "" || resource.ProviderID != "prv_mock" || resource.APIKey != "" {
		t.Fatalf("unexpected provider resource response: %s", resourceResp.Body)
	}

	resources := doJSON(t, app, http.MethodGet, "/api/admin/provider-resources", nil, "")
	if resources.Code != http.StatusOK {
		t.Fatalf("expected resources list, got %d: %s", resources.Code, resources.Body)
	}
	if !strings.Contains(resources.Body, "Mock Backup Resource") || strings.Contains(resources.Body, "secret-resource-key") {
		t.Fatalf("unexpected resources list: %s", resources.Body)
	}

	health := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/health", map[string]any{
		"healthy": false,
	}, "")
	if health.Code != http.StatusOK {
		t.Fatalf("expected resource health update, got %d: %s", health.Code, health.Body)
	}
	if !strings.Contains(health.Body, `"healthy":false`) {
		t.Fatalf("expected unhealthy resource: %s", health.Body)
	}
}

func TestAdminDeletesProviderAccountRuntimeData(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		Name:    "Delete Account Provider",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID:   provider.ID,
		Name:         "Delete Account Resource",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AccessToken:  "delete-account-access-token",
			RefreshToken: "delete-account-refresh-token",
			AccountID:    "delete-account-id",
			Email:        "delete.account@example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := store.AddRoute(ModelRoute{
		ModelName:          "delete-account-model",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "delete-account-model",
		Status:             StatusActive,
	})
	now := time.Now().UTC()
	for _, record := range []any{
		&InFlightLease{ID: "lease_delete_account", ScopeType: "provider_resource", ScopeID: resource.ID, ExpiresAt: now.Add(time.Minute)},
		&ProviderResourceBucket{ResourceID: resource.ID, Bucket: "minute", Requests: 1, Tokens: 2, UpdatedAt: now},
		&ProviderResourceObservation{ResourceID: resource.ID, AdapterType: ProviderOpenAICodex, QuotaSnapshot: `{"plan_type":"pro"}`, QuotaFetchedAt: &now, UpdatedAt: now},
		&ProviderObservation{ID: "obs_delete_account", ProviderID: provider.ID, ResourceID: resource.ID, AdapterType: ProviderOpenAICodex, Source: "real_request", Operation: "responses", Success: true, ObservedAt: now},
		&AdapterSessionBinding{ID: "binding_delete_account", AdapterType: ProviderOpenAICodex, AffinityKind: AffinityKindCodexSession, ProviderID: provider.ID, AffinityKeyHash: "delete-account-affinity", ResourceID: resource.ID, LastUsedAt: now},
	} {
		if err := store.db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}

	app := New(store).Handler()
	resp := doJSON(t, app, http.MethodDelete, "/api/admin/provider-resources/"+resource.ID, nil, "")
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected account delete 204, got %d: %s", resp.Code, resp.Body)
	}

	checks := []struct {
		name  string
		model any
		where string
		args  []any
	}{
		{name: "provider resource", model: &ProviderResource{}, where: "id = ?", args: []any{resource.ID}},
		{name: "in-flight lease", model: &InFlightLease{}, where: "scope_type = ? AND scope_id = ?", args: []any{"provider_resource", resource.ID}},
		{name: "rate-limit bucket", model: &ProviderResourceBucket{}, where: "resource_id = ?", args: []any{resource.ID}},
		{name: "resource observation", model: &ProviderResourceObservation{}, where: "resource_id = ?", args: []any{resource.ID}},
		{name: "provider observation", model: &ProviderObservation{}, where: "resource_id = ?", args: []any{resource.ID}},
		{name: "session binding", model: &AdapterSessionBinding{}, where: "resource_id = ?", args: []any{resource.ID}},
	}
	for _, check := range checks {
		var count int64
		if err := store.db.Model(check.model).Where(check.where, check.args...).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expected %s data to be deleted, found %d row(s)", check.name, count)
		}
	}
	var detachedRoute ModelRoute
	if err := store.db.First(&detachedRoute, "id = ?", route.ID).Error; err != nil {
		t.Fatal(err)
	}
	if detachedRoute.ProviderResourceID != "" {
		t.Fatalf("expected route to be detached from deleted account, got %q", detachedRoute.ProviderResourceID)
	}
}

func TestAdminRejectsDuplicateProviderResourceName(t *testing.T) {
	app := newTestServer()
	for index, name := range []string{"OpenAI Codex Primary Account", "  openai codex primary account  "} {
		resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
			"id":            "rsrc_openai_account_" + strconv.Itoa(index+1),
			"provider_id":   "prv_mock",
			"name":          name,
			"resource_type": ProviderResourceOpenAISubscription,
			"status":        StatusActive,
			"healthy":       true,
		}, "")
		if index == 0 && resp.Code != http.StatusCreated {
			t.Fatalf("expected first provider resource created, got %d: %s", resp.Code, resp.Body)
		}
		if index == 1 {
			if resp.Code != http.StatusConflict {
				t.Fatalf("expected duplicate provider resource name conflict, got %d: %s", resp.Code, resp.Body)
			}
			if !strings.Contains(resp.Body, `"code":"provider_resource_name_conflict"`) {
				t.Fatalf("expected provider resource name conflict code, got: %s", resp.Body)
			}
		}
	}

	secondary := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"id":            "rsrc_openai_account_secondary",
		"provider_id":   "prv_mock",
		"name":          "OpenAI Codex Secondary Account",
		"resource_type": ProviderResourceOpenAISubscription,
		"status":        StatusActive,
		"healthy":       true,
	}, "")
	if secondary.Code != http.StatusCreated {
		t.Fatalf("expected secondary provider resource created, got %d: %s", secondary.Code, secondary.Body)
	}
	rename := doJSON(t, app, http.MethodPatch, "/api/admin/provider-resources/rsrc_openai_account_secondary", map[string]any{
		"name": " OPENAI CODEX PRIMARY ACCOUNT ",
	}, "")
	if rename.Code != http.StatusConflict || !strings.Contains(rename.Body, `"code":"provider_resource_name_conflict"`) {
		t.Fatalf("expected provider resource rename conflict, got %d: %s", rename.Code, rename.Body)
	}
}

func TestAdminCreatesOpenAISubscriptionProviderResource(t *testing.T) {
	store := NewMemoryStore()
	store.AddProvider(Provider{
		ID:      "prv_openai_sub",
		Name:    "OpenAI Subscription Pool",
		Type:    ProviderOpenAI,
		Status:  StatusActive,
		Healthy: true,
	})
	app := New(store).Handler()
	idToken := testJWT(map[string]any{
		"email": "codex.user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acc_openai_sub",
			"chatgpt_user_id":    "usr_openai_sub",
			"chatgpt_plan_type":  "plus",
			"user_id":            "user_openai_sub",
			"organizations": []map[string]any{
				{"id": "org_openai_default", "is_default": true},
			},
		},
	})

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"provider_id":   "prv_openai_sub",
		"name":          "OpenAI Plus Account A",
		"resource_type": ProviderResourceOpenAISubscription,
		"status":        StatusActive,
		"healthy":       true,
		"priority":      1,
		"weight":        100,
		"credentials": map[string]any{
			"auth_type":     "oauth",
			"access_token":  "access-secret",
			"refresh_token": "refresh-secret",
			"id_token":      idToken,
			"client_id":     "app_EMoamEEZ73f0CkXaXp7hrann",
			"scopes":        "openid profile email offline_access",
		},
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected openai subscription resource created, got %d: %s", resp.Code, resp.Body)
	}
	for _, secret := range []string{"access-secret", "refresh-secret", idToken} {
		if strings.Contains(resp.Body, secret) {
			t.Fatalf("resource response leaked secret %q: %s", secret, resp.Body)
		}
	}
	if !strings.Contains(resp.Body, `"credential_summary"`) ||
		!strings.Contains(resp.Body, `"account_email":"codex.user@example.com"`) ||
		!strings.Contains(resp.Body, `"organization_id":"org_openai_default"`) ||
		!strings.Contains(resp.Body, `"has_refresh_token":"true"`) {
		t.Fatalf("expected OpenAI account summary, got: %s", resp.Body)
	}

	var resource ProviderResource
	if err := json.Unmarshal([]byte(resp.Body), &resource); err != nil {
		t.Fatal(err)
	}
	var persisted ProviderResource
	if err := store.db.First(&persisted, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.APIKey == "access-secret" || !strings.HasPrefix(persisted.APIKey, "enc:v1:") {
		t.Fatalf("access token should be stored encrypted, got %q", persisted.APIKey)
	}
	if persisted.CredentialBlob == "" || persisted.CredentialBlob == "refresh-secret" || !strings.HasPrefix(persisted.CredentialBlob, "enc:v1:") {
		t.Fatalf("refresh token blob should be stored encrypted, got %q", persisted.CredentialBlob)
	}

	list := doJSON(t, app, http.MethodGet, "/api/admin/provider-resources", nil, "")
	if list.Code != http.StatusOK {
		t.Fatalf("expected resources list, got %d: %s", list.Code, list.Body)
	}
	for _, secret := range []string{"access-secret", "refresh-secret", idToken} {
		if strings.Contains(list.Body, secret) {
			t.Fatalf("resource list leaked secret %q: %s", secret, list.Body)
		}
	}
}

func TestProviderCredentialsAreEncryptedAndUsable(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Encrypted Credentials App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "encrypted-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_encrypted")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_encrypted",
		Name:    "Encrypted Provider",
		Type:    "capture",
		APIKey:  "provider-secret",
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_encrypted",
		ProviderID:   provider.ID,
		Name:         "Encrypted Resource",
		ResourceType: "api_key",
		APIKey:       "resource-secret",
		Status:       StatusActive,
		Healthy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var persisted ProviderResource
	if err := store.db.First(&persisted, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.APIKey == "resource-secret" || !strings.HasPrefix(persisted.APIKey, "enc:v1:") {
		t.Fatalf("resource secret should be stored encrypted, got %q", persisted.APIKey)
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{
		Name:    "Encrypted Resource Updated",
		Status:  StatusActive,
		Healthy: true,
	}); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "encrypted-chat",
		Status:             StatusActive,
	})
	adapter := &captureAdapter{}
	server := New(store)
	registerTestAdapter(server, "capture", adapter)
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "secret route"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenKey != "resource-secret" {
		t.Fatalf("expected decrypted resource secret, got %q", adapter.seenKey)
	}
	if strings.Contains(resp.Body, "resource-secret") {
		t.Fatalf("secret should not be returned: %s", resp.Body)
	}
}

func TestOpenAISubscriptionResourceSuppliesRouteCredentials(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "OpenAI Subscription App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "openai-subscription-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_openai_subscription")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_capture_openai", Name: "Capture OpenAI", Type: "capture", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_openai_account",
		ProviderID:   provider.ID,
		Name:         "OpenAI Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-4.1-mini"),
		Credentials: &ProviderResourceCredentials{
			AuthType:       "oauth",
			AccessToken:    "openai-access-token",
			RefreshToken:   "openai-refresh-token",
			Email:          "owner@example.com",
			AccountID:      "acc_capture",
			OrganizationID: "org_capture",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "gpt-4.1-mini",
		Status:             StatusActive,
	})
	adapter := &captureAdapter{}
	server := New(store)
	registerTestAdapter(server, "capture", adapter)
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "hello from subscription"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenKey != "openai-access-token" {
		t.Fatalf("expected OpenAI account access token, got %q", adapter.seenKey)
	}
	if adapter.seenOptions["credential_source"] != ProviderResourceOpenAISubscription ||
		adapter.seenOptions["account_email"] != "owner@example.com" ||
		adapter.seenOptions["organization_id"] != "org_capture" {
		t.Fatalf("expected OpenAI account options, got %+v", adapter.seenOptions)
	}
}

func TestOpenAISubscriptionResourceRefreshesBeforeGatewayCall(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("grant_type") != "refresh_token" ||
			r.FormValue("refresh_token") != "refresh-old" ||
			r.FormValue("client_id") != openAIAccountOAuthClientID ||
			r.FormValue("scope") != openAIAccountOAuthRefreshScope {
			t.Fatalf("unexpected refresh form: %s", r.Form.Encode())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-refreshed",
			"id_token": testJWT(map[string]any{
				"email": "refreshed.owner@example.com",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "acc_refreshed",
					"chatgpt_plan_type":  "pro",
					"organizations": []map[string]any{
						{"id": "org_refreshed", "is_default": true},
					},
				},
			}),
			"token_type": "Bearer",
			"expires_in": 3600,
		})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Refreshing Account App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "refreshing-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_refreshing")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_refreshing", Name: "Refreshing Provider", Type: "capture", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_refreshing",
		ProviderID:   provider.ID,
		Name:         "Refreshing OpenAI Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-4.1-mini"),
		Credentials: &ProviderResourceCredentials{
			AuthType:     "oauth",
			AccessToken:  "access-expired",
			RefreshToken: "refresh-old",
			ClientID:     openAIAccountOAuthClientID,
			ExpiresAt:    time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "gpt-4.1-mini",
		Status:             StatusActive,
	})
	adapter := &captureAdapter{}
	server := New(store)
	registerTestAdapter(server, "capture", adapter)
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "refresh before call"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenKey != "access-refreshed" {
		t.Fatalf("expected refreshed access token, got %q", adapter.seenKey)
	}
	if adapter.seenOptions["account_email"] != "refreshed.owner@example.com" ||
		adapter.seenOptions["account_id"] != "acc_refreshed" ||
		adapter.seenOptions["organization_id"] != "org_refreshed" ||
		adapter.seenOptions["has_refresh_token"] != "true" {
		t.Fatalf("expected refreshed account options, got %+v", adapter.seenOptions)
	}
}

func TestOpenAIProviderAccountOAuthGenerateAuthURLAndCallback(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/generate-auth-url", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected auth URL generated, got %d: %s", resp.Code, resp.Body)
	}
	var payload providerAccountOAuthGenerateResponse
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AuthURL == "" || payload.SessionID == "" || payload.State == "" || payload.RedirectURI == "" {
		t.Fatalf("unexpected auth URL payload: %+v", payload)
	}
	authURL, err := url.Parse(payload.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Host != "auth.openai.com" ||
		authURL.Query().Get("client_id") != openAIAccountOAuthClientID ||
		authURL.Query().Get("redirect_uri") != openAIAccountOAuthRedirectURI ||
		authURL.Query().Get("code_challenge_method") != "S256" ||
		authURL.Query().Get("codex_cli_simplified_flow") != "true" ||
		authURL.Query().Get("state") != payload.State {
		t.Fatalf("unexpected authorize URL: %s", payload.AuthURL)
	}

	callback := httptest.NewRequest(http.MethodGet, "/api/admin/provider-account-oauth/openai/oauth/callback?code=oauth-code&state="+url.QueryEscape(payload.State), nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, callback)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected callback redirect, got %d: %s", rr.Code, rr.Body.String())
	}
	location := rr.Header().Get("location")
	redirect, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	if redirect.String() == "" ||
		redirect.Query().Get("provider_account_oauth") != "1" ||
		redirect.Query().Get("provider_account_oauth_session_id") != payload.SessionID ||
		redirect.Query().Get("provider_account_oauth_state") != payload.State ||
		redirect.Query().Get("code") != "oauth-code" {
		t.Fatalf("unexpected callback redirect: %s", location)
	}
}

func TestOpenAIProviderAccountOAuthCallbackSurfacesDatabaseFailure(t *testing.T) {
	store := NewMemoryStore()
	session := providerAccountOAuthSession{
		ID:           "oauth-db-error",
		State:        "oauth-db-error-state",
		CodeVerifier: "oauth-db-error-verifier",
		CreatedAt:    time.Now().UTC(),
	}
	if err := store.SaveProviderAccountOAuthSession(session); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	callback := httptest.NewRequest(http.MethodGet, "/api/admin/provider-account-oauth/openai/oauth/callback?code=oauth-code&state="+url.QueryEscape(session.State), nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, callback)
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "internal_error") {
		t.Fatalf("expected database failure to surface as 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

type oauthRestoreFailureStore struct {
	Store
	saveCalls int
}

func (s *oauthRestoreFailureStore) SaveProviderAccountOAuthSession(session providerAccountOAuthSession) error {
	s.saveCalls++
	if s.saveCalls > 1 {
		return errors.New("restore failed")
	}
	return s.Store.SaveProviderAccountOAuthSession(session)
}

func TestOpenAIProviderAccountOAuthExchangeSurfacesSessionRestoreFailure(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "token endpoint unavailable", http.StatusBadGateway)
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	baseStore := NewMemoryStore()
	store := &oauthRestoreFailureStore{Store: baseStore}
	app := New(store).Handler()
	generated := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/generate-auth-url", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "")
	if generated.Code != http.StatusOK {
		t.Fatalf("expected generate 200, got %d: %s", generated.Code, generated.Body)
	}
	var auth providerAccountOAuthGenerateResponse
	if err := json.Unmarshal([]byte(generated.Body), &auth); err != nil {
		t.Fatal(err)
	}
	exchanged := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/exchange-code", map[string]any{
		"session_id": auth.SessionID,
		"state":      auth.State,
		"code":       "oauth-code",
	}, "")
	if exchanged.Code != http.StatusInternalServerError || !strings.Contains(exchanged.Body, "internal_error") {
		t.Fatalf("expected restore failure to surface as 500, got %d: %s", exchanged.Code, exchanged.Body)
	}
}

func TestOpenAIProviderAccountOAuthExchangeCode(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("user-agent") != "codex-cli/0.91.0" {
			t.Fatalf("expected Codex user-agent, got %q", r.Header.Get("user-agent"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("grant_type") != "authorization_code" ||
			r.FormValue("client_id") != openAIAccountOAuthClientID ||
			r.FormValue("code") != "oauth-code" ||
			r.FormValue("redirect_uri") != openAIAccountOAuthRedirectURI ||
			r.FormValue("code_verifier") == "" {
			t.Fatalf("unexpected token form: %s", r.Form.Encode())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-from-code",
			"refresh_token": "refresh-from-code",
			"id_token": testJWT(map[string]any{
				"email": "codex.owner@example.com",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "acc_oauth",
					"chatgpt_user_id":    "usr_oauth",
					"chatgpt_plan_type":  "plus",
					"organizations": []map[string]any{
						{"id": "org_oauth", "is_default": true},
					},
				},
			}),
			"token_type": "Bearer",
			"expires_in": 3600,
			"scope":      openAIAccountOAuthScopes,
		})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	store := NewMemoryStore()
	app := New(store).Handler()
	generated := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/generate-auth-url", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "")
	if generated.Code != http.StatusOK {
		t.Fatalf("expected generate 200, got %d: %s", generated.Code, generated.Body)
	}
	var auth providerAccountOAuthGenerateResponse
	if err := json.Unmarshal([]byte(generated.Body), &auth); err != nil {
		t.Fatal(err)
	}
	exchanged := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/exchange-code", map[string]any{
		"session_id": auth.SessionID,
		"state":      auth.State,
		"code":       "oauth-code",
	}, "")
	if exchanged.Code != http.StatusOK {
		t.Fatalf("expected exchange 200, got %d: %s", exchanged.Code, exchanged.Body)
	}
	var info providerAccountOAuthTokenInfo
	if err := json.Unmarshal([]byte(exchanged.Body), &info); err != nil {
		t.Fatal(err)
	}
	if info.AccessToken != "access-from-code" ||
		info.RefreshToken != "refresh-from-code" ||
		info.AccountEmail != "codex.owner@example.com" ||
		info.AccountID != "acc_oauth" ||
		info.OrganizationID != "org_oauth" ||
		info.ClientID != openAIAccountOAuthClientID {
		t.Fatalf("unexpected exchanged token info: %+v", info)
	}
}

func TestProviderAndResourceTestEndpoints(t *testing.T) {
	app := newTestServer()
	provider := doJSON(t, app, http.MethodPost, "/api/admin/providers/prv_mock/test", nil, "")
	if provider.Code != http.StatusOK {
		t.Fatalf("expected provider test 200, got %d: %s", provider.Code, provider.Body)
	}
	if !strings.Contains(provider.Body, `"healthy":true`) {
		t.Fatalf("expected healthy provider response: %s", provider.Body)
	}

	resource := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/rsrc_mock_primary/test", nil, "")
	if resource.Code != http.StatusOK {
		t.Fatalf("expected resource test 200, got %d: %s", resource.Code, resource.Body)
	}
	if !strings.Contains(resource.Body, `"healthy":true`) || !strings.Contains(resource.Body, `"last_checked_at"`) {
		t.Fatalf("expected checked healthy resource: %s", resource.Body)
	}
}

func TestProviderTestPerformsUpstreamProbeWithoutResourceHeaders(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			http.Error(w, "missing credentials", http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "upstream-model"}}})
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_real_probe", Name: "Real probe", Type: ProviderOpenAICompatible, BaseURL: upstream.URL,
		APIKey: "provider-secret", Status: StatusActive, Healthy: false,
	})
	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/providers/"+provider.ID+"/test", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("provider test = %d: %s", response.Code, response.Body)
	}
	if requests != 1 {
		t.Fatalf("upstream requests = %d, want 1", requests)
	}
	updated, ok := store.GetProvider(provider.ID)
	if !ok || !updated.Healthy {
		t.Fatalf("successful upstream probe did not restore Provider health: %+v", updated)
	}
}

func TestProviderTestMarksProviderUnhealthyWhenAllResourceProbesFail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "revoked credentials", http.StatusUnauthorized)
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_failed_resource_probe", Name: "Failed resource probe", Type: ProviderOpenAICompatible,
		BaseURL: upstream.URL, Status: StatusActive, Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_failed_resource_probe", ProviderID: provider.ID, Name: "Revoked", ResourceType: ProviderResourceAPIKey,
		APIKey: "revoked-secret", Status: StatusActive, Healthy: true,
	}); err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/providers/"+provider.ID+"/test", nil, "")
	if response.Code == http.StatusOK {
		t.Fatalf("provider test unexpectedly succeeded: %s", response.Body)
	}
	updated, ok := store.GetProvider(provider.ID)
	if !ok || updated.Healthy {
		t.Fatalf("failed upstream probes did not mark Provider unhealthy: %+v", updated)
	}
}

func TestProviderResourceBulkOperations(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{Name: "Bulk Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		Name:           "Bulk Resource",
		ProviderID:     provider.ID,
		ResourceType:   "api_key",
		Status:         StatusActive,
		Healthy:        true,
		RateLimitRPM:   1,
		TokenLimitTPM:  1,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, "", AttemptFailed, Usage{})
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, "", AttemptFailed, Usage{})
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, "", AttemptFailed, Usage{})
	if _, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID); AsHTTPError(err).Code != "provider_resource_cooling_down" {
		t.Fatalf("expected cooldown before clear_error, got %v", err)
	}
	app := New(store).Handler()

	disabled := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
		"action": "disable",
		"ids":    []string{resource.ID},
	}, "")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body, `"success":1`) {
		t.Fatalf("disable failed: %d %s", disabled.Code, disabled.Body)
	}
	found := findResource(t, store, resource.ID)
	if found.Status != StatusDisabled || found.Healthy {
		t.Fatalf("expected disabled unhealthy resource, got %+v", found)
	}

	cleared := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
		"action": "clear_error",
		"ids":    []string{resource.ID, resource.ID},
	}, "")
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body, `"success":1`) {
		t.Fatalf("clear error failed: %d %s", cleared.Code, cleared.Body)
	}
	leaseID, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID)
	if err != nil {
		t.Fatalf("capacity should be available after clear_error: %v", err)
	}
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, leaseID, AttemptSucceeded, Usage{TotalTokens: 5})
	if _, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID); AsHTTPError(err).Code != "provider_resource_rpm_exceeded" {
		t.Fatalf("expected rpm limit before reset, got %v", err)
	}
	reset := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
		"action": "reset_usage",
		"ids":    []string{resource.ID},
	}, "")
	if reset.Code != http.StatusOK || !strings.Contains(reset.Body, `"success":1`) {
		t.Fatalf("reset usage failed: %d %s", reset.Code, reset.Body)
	}
	leaseID, _, err = store.CheckProviderResourceCapacity(context.Background(), resource.ID)
	if err != nil {
		t.Fatalf("capacity should be available after reset_usage: %v", err)
	}
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, leaseID, AttemptSucceeded, Usage{})
}

func TestProviderResourceImport(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{Name: "Import Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/import", map[string]any{
		"resources": []map[string]any{
			{
				"provider_id":     provider.ID,
				"name":            "Imported Primary",
				"group":           "prod-east",
				"resource_type":   "api_key",
				"api_key":         "import-secret-1",
				"region":          "us-east-1",
				"environment":     "prod",
				"priority":        1,
				"weight":          80,
				"rate_limit_rpm":  120,
				"token_limit_tpm": 60000,
				"max_concurrency": 8,
			},
			{
				"provider_id": "missing-provider",
				"name":        "Broken Resource",
			},
		},
	}, "")
	if resp.Code != http.StatusMultiStatus || !strings.Contains(resp.Body, `"success":1`) || !strings.Contains(resp.Body, `"failed":1`) {
		t.Fatalf("expected partial import result, got %d %s", resp.Code, resp.Body)
	}
	if strings.Contains(resp.Body, "import-secret-1") {
		t.Fatalf("resource secret should not be returned: %s", resp.Body)
	}
	resources := store.ListProviderResources()
	var imported ProviderResource
	for _, item := range resources {
		if item.Name == "Imported Primary" {
			imported = item
			break
		}
	}
	if imported.ID == "" || imported.Group != "prod-east" || imported.RateLimitRPM != 120 || imported.APIKey != "" {
		t.Fatalf("expected imported resource with redacted key, got %+v", imported)
	}
}
