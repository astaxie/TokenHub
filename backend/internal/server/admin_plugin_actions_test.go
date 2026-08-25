package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginActionExecutesThroughBroker(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-action-admin"})
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID: "tokenhub.provider.openai-codex",
		ActionID: "test.echo",
		Kind:     pluginmeta.ActionKindTest,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload map[string]string
		if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
			t.Fatalf("decode action payload: %v", err)
		}
		return pluginmeta.ActionResult{Data: map[string]string{
			"actor_id":    invocation.Actor.ID,
			"resource_id": payload["resource_id"],
		}}, nil
	})); err != nil {
		t.Fatalf("register plugin action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/test.echo", map[string]any{
		"resource_id": "res_codex",
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST plugin action: expected 200, got %d: %s", response.Code, response.Body)
	}
	body := response.Body
	if !strings.Contains(body, `"resource_id":"res_codex"`) || !strings.Contains(body, `"actor_id":"dev_admin"`) {
		t.Fatalf("POST plugin action response did not include broker result: %s", body)
	}
	events := store.ListAuditEvents()
	if len(events) == 0 || events[0].Action != "plugin.action.test.echo" || events[0].ResourceID != "tokenhub.provider.openai-codex" {
		t.Fatalf("plugin action audit events = %+v", events)
	}
}

func TestAdminPluginActionSanitizesResultSecrets(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID: "tokenhub.provider.openai-codex",
		ActionID: "test.secrets",
		Kind:     pluginmeta.ActionKindRead,
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{
			Data: map[string]any{
				"access_token":       "access-secret",
				"credential_summary": map[string]string{"has_refresh_token": "true"},
				"nested":             map[string]any{"api_key": "key-secret", "status": "ok"},
			},
			Metadata: map[string]string{"refresh_token": "refresh-secret", "status": "ok"},
		}, nil
	})); err != nil {
		t.Fatalf("register plugin action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/test.secrets", map[string]any{}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST secret action: expected 200, got %d: %s", response.Code, response.Body)
	}
	for _, secret := range []string{"access-secret", "key-secret", "refresh-secret"} {
		if strings.Contains(response.Body, secret) {
			t.Fatalf("plugin action response leaked %q: %s", secret, response.Body)
		}
	}
	for _, expected := range []string{`"access_token":"[redacted]"`, `"api_key":"[redacted]"`, `"refresh_token":"[redacted]"`, `"has_refresh_token":"true"`} {
		if !strings.Contains(response.Body, expected) {
			t.Fatalf("plugin action response missing %s: %s", expected, response.Body)
		}
	}
}

func TestAdminPluginActionPersistsCredentialRefreshResult(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_kimi_refresh", Name: "Kimi", Type: "kimi_subscription", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_kimi_refresh",
		ProviderID:   provider.ID,
		Name:         "Kimi Account",
		ResourceType: "kimi_oauth_account",
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AuthType: "oauth", AccessToken: "old-access", RefreshToken: "old-refresh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "plugin-action-admin"})
	if err := server.pluginRegistry.Register(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.kimi",
		Name:    "Kimi",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
	}); err != nil {
		t.Fatalf("register plugin descriptor: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi",
		ActionID:   "kimi.credentials.refresh",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "credentials.refresh",
		Subject:    "kimi_subscription",
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{Data: map[string]any{
			"credentials": map[string]string{
				"auth_type":     "personal_access_token",
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"email":         "owner@example.com",
			},
		}}, nil
	})); err != nil {
		t.Fatalf("register plugin action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.kimi/actions/kimi.credentials.refresh", map[string]any{
		"resource_id": resource.ID,
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST credential refresh action: expected 200, got %d: %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body, "new-access") || strings.Contains(response.Body, "new-refresh") || strings.Contains(response.Body, "credentials") {
		t.Fatalf("credential refresh response leaked secrets: %s", response.Body)
	}
	if !strings.Contains(response.Body, `"credential_source":"kimi_oauth_account"`) ||
		!strings.Contains(response.Body, `"has_refresh_token":"true"`) ||
		!strings.Contains(response.Body, `"account_email":"owner@example.com"`) {
		t.Fatalf("credential refresh response missing summary: %s", response.Body)
	}
	stored, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("refreshed resource was not found")
	}
	credentials := store.providerResourceCredentialsForRuntime(stored)
	if credentials.AccessToken != "new-access" || credentials.RefreshToken != "new-refresh" || credentials.AuthType != "personal_access_token" {
		t.Fatalf("stored credentials = %+v", credentials)
	}
}

func TestAdminPluginActionRejectsInvalidResultSchema(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID: "tokenhub.provider.openai-codex",
		ActionID: "test.invalid-result",
		Kind:     pluginmeta.ActionKindRead,
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []string{"healthy"},
			"properties": map[string]any{
				"healthy": map[string]any{"type": "boolean"},
			},
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{Data: map[string]any{"healthy": "yes"}}, nil
	})); err != nil {
		t.Fatalf("register plugin action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/test.invalid-result", map[string]any{}, "plugin-action-admin")
	assertResponseBodyJSONError(t, response, http.StatusBadGateway, "invalid_plugin_action_result")
}

func TestAdminPluginActionsListsBuiltInActions(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-actions", nil, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin actions: expected 200, got %d: %s", response.Code, response.Body)
	}
	body := response.Body
	if !strings.Contains(body, `"action_id":"openai_codex.quota.read"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.quota.reset_credits.read"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.quota.reset"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.oauth.start"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.oauth.exchange"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.probe.run"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.provider.probe.run"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.models.read"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.models.preview"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.image_capability.configure"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.credentials.refresh"`) ||
		!strings.Contains(body, `"capability":"quota.reset_credits.read"`) ||
		!strings.Contains(body, `"capability":"quota.reset"`) ||
		!strings.Contains(body, `"capability":"provider.probe.run"`) ||
		!strings.Contains(body, `"capability":"models.read"`) ||
		!strings.Contains(body, `"capability":"models.preview"`) ||
		!strings.Contains(body, `"capability":"image.capability.configure"`) ||
		!strings.Contains(body, `"capability":"credentials.refresh"`) {
		t.Fatalf("GET plugin actions did not include built-in Codex actions: %s", body)
	}
}

func TestProviderCredentialModelsPreviewUsesPluginAction(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})
	providerType := "preview_action_provider"
	pluginID := "tokenhub.provider.preview-action"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(pluginID, "Preview Action Provider", []string{providerType}, []string{string(AdapterCapabilityModels)}), AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register preview provider: %v", err)
	}
	var observed ProviderResourceCredentials
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "preview.models",
		Kind:       pluginmeta.ActionKindRead,
		Capability: "models.preview",
		Subject:    providerType,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		if err := json.Unmarshal(invocation.Payload, &observed); err != nil {
			t.Fatalf("decode preview payload: %v", err)
		}
		return pluginmeta.ActionResult{Data: ProviderCatalogEntry{
			ID: "preview-catalog", Name: "Preview Catalog", DisplayName: "Preview Catalog",
			Type: providerType, ModelsCount: 1, Source: "preview-action",
		}}, nil
	})); err != nil {
		t.Fatalf("register preview action: %v", err)
	}

	catalog, supported, err := server.executeProviderCredentialModelsAction(context.Background(), AdminUser{ID: "admin", Role: "admin"}, providerType, ProviderResourceCredentials{
		AccessToken: "preview-token",
		AccountID:   "preview-account",
	})
	if err != nil || !supported {
		t.Fatalf("preview action supported=%v err=%v", supported, err)
	}
	if observed.AccessToken != "preview-token" || observed.AccountID != "preview-account" {
		t.Fatalf("preview credentials = %+v", observed)
	}
	if catalog.ID != "preview-catalog" || catalog.Source != "preview-action" {
		t.Fatalf("catalog = %+v", catalog)
	}
}

func TestAdminPluginActionStartsOpenAICodexOAuth(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{
		AdminToken:         "plugin-action-admin",
		CORSAllowedOrigins: []string{"http://localhost:3001"},
	})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.oauth.start", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST OAuth action: expected 200, got %d: %s", response.Code, response.Body)
	}
	var result struct {
		Data        providerAccountOAuthGenerateResponse `json:"data"`
		RedirectURL string                               `json:"redirect_url"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.AuthURL == "" || result.RedirectURL != result.Data.AuthURL || result.Data.SessionID == "" || result.Data.State == "" {
		t.Fatalf("unexpected OAuth action result: %+v", result)
	}
	authURL, err := url.Parse(result.Data.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Query().Get("client_id") != openAIAccountOAuthClientID || authURL.Query().Get("state") != result.Data.State {
		t.Fatalf("unexpected OAuth action auth URL: %s", result.Data.AuthURL)
	}
	if events := store.ListAuditEvents(); len(events) == 0 || events[0].Action != "plugin.action.openai_codex.oauth.start" {
		t.Fatalf("plugin action audit events = %+v", events)
	}
}

func TestAdminPluginActionRefreshesOpenAICodexCredentials(t *testing.T) {
	tokenCalls := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "plugin-refresh-secret" {
			t.Fatalf("unexpected refresh form: %v", r.Form)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"plugin-fresh-secret","refresh_token":"plugin-fresh-refresh","token_type":"bearer","expires_in":3600}`)
	}))
	t.Cleanup(tokenServer.Close)
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	t.Cleanup(func() { openAIAccountOAuthTokenEndpoint = previousEndpoint })

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_plugin_refresh", Name: "Plugin Refresh Provider", Type: ProviderOpenAICodex,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_plugin_refresh", ProviderID: provider.ID, Name: "Plugin Refresh Resource",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "plugin-old-secret", RefreshToken: "plugin-refresh-secret",
			AccountID: "plugin-refresh-account", Email: "plugin-refresh@example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "plugin-action-admin"})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.credentials.refresh", map[string]any{
		"resource_id": resource.ID,
		"force":       true,
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST credential refresh action: expected 200, got %d: %s", response.Code, response.Body)
	}
	if tokenCalls != 1 {
		t.Fatalf("credential refresh upstream calls = %d, want 1", tokenCalls)
	}
	for _, secret := range []string{"plugin-old-secret", "plugin-refresh-secret", "plugin-fresh-secret", "plugin-fresh-refresh"} {
		if strings.Contains(response.Body, secret) {
			t.Fatalf("credential refresh action leaked %q: %s", secret, response.Body)
		}
	}
	if !strings.Contains(response.Body, `"account_id":"plugin-refresh-account"`) || !strings.Contains(response.Body, `"has_refresh_token":"true"`) {
		t.Fatalf("credential refresh action response missing summary: %s", response.Body)
	}
	if events := store.ListAuditEvents(); len(events) == 0 || events[0].Action != "plugin.action.openai_codex.credentials.refresh" {
		t.Fatalf("plugin action audit events = %+v", events)
	}
}

func TestAdminPluginActionRunsOpenAICodexQuotaResetActions(t *testing.T) {
	upstream := &quotaResetUpstream{availableCount: 1, creditID: "plugin-reset-credit"}
	server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())

	credits := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.quota.reset_credits.read", map[string]any{
		"resource_id": quotaResetTestResourceID,
	}, "dev_admin_token")
	if credits.Code != http.StatusOK {
		t.Fatalf("POST quota reset credits action: expected 200, got %d: %s", credits.Code, credits.Body)
	}
	if !strings.Contains(credits.Body, `"available_count":1`) || !strings.Contains(credits.Body, `"id":"plugin-reset-credit"`) {
		t.Fatalf("quota reset credits action response = %s", credits.Body)
	}
	if strings.Contains(credits.Body, "access_initial") {
		t.Fatalf("quota reset credits action leaked access token: %s", credits.Body)
	}

	reset := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.quota.reset", map[string]any{
		"resource_id":              quotaResetTestResourceID,
		"confirm":                  true,
		"idempotency_key":          "plugin-action-reset-1",
		"expected_available_count": 1,
		"credit_id":                "plugin-reset-credit",
		"danger_confirmation":      openAIAccountQuotaResetDangerValue,
	}, "dev_admin_token")
	if reset.Code != http.StatusOK {
		t.Fatalf("POST quota reset action: expected 200, got %d: %s", reset.Code, reset.Body)
	}
	if !strings.Contains(reset.Body, `"code":"reset"`) || !strings.Contains(reset.Body, `"windows_reset":2`) {
		t.Fatalf("quota reset action response = %s", reset.Body)
	}
	if upstream.getCalls != 2 || upstream.consumeCalls != 1 {
		t.Fatalf("quota reset action upstream calls: gets=%d consumes=%d", upstream.getCalls, upstream.consumeCalls)
	}
	var sawCreditsAudit, sawResetAudit bool
	for _, event := range store.ListAuditEvents() {
		sawCreditsAudit = sawCreditsAudit || event.Action == "plugin.action.openai_codex.quota.reset_credits.read"
		sawResetAudit = sawResetAudit || event.Action == "plugin.action.openai_codex.quota.reset"
	}
	if !sawCreditsAudit || !sawResetAudit {
		t.Fatalf("quota reset action audit events = %+v", store.ListAuditEvents())
	}
}

func TestAdminPluginActionConfiguresOpenAICodexImageCapability(t *testing.T) {
	imageBytes := realPNGFixture(t)
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		var request codexSubscriptionImageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode capability request: %v", err)
		}
		if r.URL.Path != "/backend-api/codex/images/generations" || request.Model != codexImageUpstreamModel {
			t.Fatalf("unexpected image capability request path=%s body=%+v", r.URL.Path, request)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	t.Cleanup(upstream.Close)
	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.image_capability.configure", map[string]any{
		"resource_id": resource.ID,
		"enabled":     true,
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST image capability action: expected 200, got %d: %s", response.Code, response.Body)
	}
	if upstreamCalls != 1 || !strings.Contains(response.Body, `"tested":true`) ||
		!strings.Contains(response.Body, `"capability":"supported"`) ||
		!strings.Contains(response.Body, `"resource_id":"`+resource.ID+`"`) {
		t.Fatalf("image capability action response/calls: calls=%d body=%s", upstreamCalls, response.Body)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok || updated.Options[codexImageCapabilityOption] != codexImageCapabilitySupported {
		t.Fatalf("image capability action did not record support: %+v", updated.Options)
	}
	if events := store.ListAuditEvents(); len(events) == 0 || events[0].Action != "plugin.action.openai_codex.image_capability.configure" {
		t.Fatalf("image capability action audit events = %+v", events)
	}
}

func TestAdminPluginActionRunsOpenAICodexProviderProbe(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_plugin_provider_probe",
		Name:    "Plugin Provider Probe",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_plugin_provider_probe",
		ProviderID:   provider.ID,
		Name:         "Plugin Provider Probe Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AccessToken: "plugin-provider-probe-secret", AccountID: "plugin-provider-probe-account"},
	}); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "plugin-action-admin", SecretKey: "plugin-provider-probe-secret-key"})
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != openAICodexDefaultProbeModel {
			t.Fatalf("unexpected provider probe payload: %#v", payload)
		}
		stream := strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"Codex provider works."}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_probe","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
			Request:    req,
		}, nil
	})}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.provider.probe.run", map[string]any{
		"provider_id": provider.ID,
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST provider probe action: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"provider_id":"`+provider.ID+`"`) ||
		!strings.Contains(response.Body, `"succeeded":1`) ||
		!strings.Contains(response.Body, `"healthy":true`) {
		t.Fatalf("provider probe action response = %s", response.Body)
	}
	if strings.Contains(response.Body, "plugin-provider-probe-secret") {
		t.Fatalf("provider probe action leaked credentials: %s", response.Body)
	}
	if events := store.ListAuditEvents(); len(events) == 0 || events[0].Action != "plugin.action.openai_codex.provider.probe.run" {
		t.Fatalf("provider probe action audit events = %+v", events)
	}
}

func TestAdminPluginActionReadsOpenAICodexModels(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_plugin_models",
		Name:    "Plugin Models",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_plugin_models",
		ProviderID:   provider.ID,
		Name:         "Plugin Models Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AccessToken: "plugin-models-secret", AccountID: "plugin-models-account"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "plugin-action-admin", SecretKey: "plugin-models-secret-key"})
	server.codexSubscription.ModelsURL = "https://chatgpt.example/backend-api/codex/models"
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("authorization") != "Bearer plugin-models-secret" || req.Header.Get("chatgpt-account-id") != "plugin-models-account" {
			t.Fatalf("models action missing Codex credentials: %#v", req.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"models":[{"slug":"gpt-plugin-model","display_name":"GPT Plugin Model","visibility":"list","supported_in_api":true,"priority":1}]}`,
			)),
			Request: req,
		}, nil
	})}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.models.read", map[string]any{
		"resource_id": resource.ID,
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST models action: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"id":"gpt-plugin-model"`) ||
		!strings.Contains(response.Body, `"source":"openai-codex-live"`) ||
		!strings.Contains(response.Body, `"models_count":1`) {
		t.Fatalf("models action response = %s", response.Body)
	}
	if strings.Contains(response.Body, "plugin-models-secret") {
		t.Fatalf("models action leaked credentials: %s", response.Body)
	}
	if events := store.ListAuditEvents(); len(events) == 0 || events[0].Action != "plugin.action.openai_codex.models.read" {
		t.Fatalf("models action audit events = %+v", events)
	}
}

func TestAdminProviderResourceTestRouteUsesProbeAction(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-action-admin"})
	providerType := "probe_action_provider"
	pluginID := "tokenhub.provider.probe-action"
	adapterCalls := 0
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(pluginID, "Probe Action Provider", []string{providerType}, []string{string(AdapterCapabilityProbe)}), AdapterRegistration{
		Type:         providerType,
		Adapter:      recoveryProbeAdapter{calls: &adapterCalls},
		Capabilities: []AdapterCapability{AdapterCapabilityProbe},
	}); err != nil {
		t.Fatalf("register probe action provider: %v", err)
	}
	actionCalls := 0
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "probe_action.run",
		Kind:       pluginmeta.ActionKindTest,
		Capability: "probe.run",
		Subject:    providerType,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"model":       map[string]any{"type": "string"},
			},
		},
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		actionCalls++
		var payload struct {
			ResourceID string `json:"resource_id"`
			Model      string `json:"model"`
		}
		if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
			t.Fatalf("decode probe action payload: %v", err)
		}
		return pluginmeta.ActionResult{Data: ProviderProbeResult{
			ResourceID: payload.ResourceID,
			Model:      payload.Model,
			OutputText: "from action",
			LatencyMS:  7,
		}}, nil
	})); err != nil {
		t.Fatalf("register probe action: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_probe_action", Name: "Probe Action Provider", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_probe_action", ProviderID: provider.ID, Name: "Probe Action Resource",
		ResourceType: ProviderResourceAPIKey, APIKey: "resource-secret", Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/test", map[string]any{
		"model": "probe-model",
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST provider resource test: expected 200, got %d: %s", response.Code, response.Body)
	}
	if actionCalls != 1 || adapterCalls != 0 {
		t.Fatalf("probe action calls=%d adapter calls=%d, want action only", actionCalls, adapterCalls)
	}
	if !strings.Contains(response.Body, `"output_text":"from action"`) || !strings.Contains(response.Body, `"latency_ms":7`) {
		t.Fatalf("provider resource test did not return action probe result: %s", response.Body)
	}
	if events := store.ListAuditEvents(); len(events) == 0 || events[0].Action != "test" || events[0].ResourceID != resource.ID {
		t.Fatalf("provider resource test audit events = %+v", events)
	}
}

func TestAdminPluginActionsLoadsExternalManifestActions(t *testing.T) {
	pluginRoot := t.TempDir()
	pluginDir := filepath.Join(pluginRoot, "sync")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(`
schema_version: 1
id: tokenhub.sync
name: Sync Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - management_action
capabilities:
  actions:
    - id: sync.run
      kind: mutate
      title: Run sync
`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin", PluginDir: pluginRoot})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-actions", nil, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin actions: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"plugin_id":"tokenhub.sync"`) || !strings.Contains(response.Body, `"action_id":"sync.run"`) {
		t.Fatalf("GET plugin actions did not include external manifest action: %s", response.Body)
	}

	execute := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.sync/actions/sync.run", map[string]any{}, "plugin-action-admin")
	assertResponseBodyJSONError(t, execute, http.StatusNotImplemented, "plugin_action_unavailable")
}

func TestAdminPluginBackgroundJobsLoadsExternalManifestJobs(t *testing.T) {
	pluginRoot := t.TempDir()
	pluginDir := filepath.Join(pluginRoot, "jobs")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(`
schema_version: 1
id: tokenhub.jobs
name: Jobs Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - background
capabilities:
  background_jobs:
    - id: quota.refresh
      title: Refresh quota
      capability: quota.refresh
      subject: openai_codex
      schedule: "*/10 * * * *"
      max_concurrency: 1
`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin", PluginDir: pluginRoot})
	if err := server.pluginBackgroundJobs.Register(pluginmeta.BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "*/10 * * * *",
		MaxConcurrency: 1,
	}, pluginmeta.BackgroundJobHandlerFunc(func(context.Context, pluginmeta.BackgroundJobInvocation) (pluginmeta.BackgroundJobResult, error) {
		return pluginmeta.BackgroundJobResult{Data: map[string]bool{"refreshed": true}}, nil
	})); err != nil {
		t.Fatalf("bind background job handler: %v", err)
	}
	if _, err := server.pluginBackgroundRunner.Run(t.Context(), pluginmeta.BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh", Trigger: "manual"}); err != nil {
		t.Fatalf("run background job: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-background-jobs", nil, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin background jobs: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"plugin_id":"tokenhub.jobs"`) || !strings.Contains(response.Body, `"job_id":"quota.refresh"`) {
		t.Fatalf("GET plugin background jobs did not include external manifest job: %s", response.Body)
	}
	if !strings.Contains(response.Body, `"runs"`) || !strings.Contains(response.Body, `"status":"succeeded"`) {
		t.Fatalf("GET plugin background jobs did not include last run status: %s", response.Body)
	}
}

func TestAdminPluginActionRejectsUnknownPlugin(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.missing/actions/test.echo", map[string]any{}, "plugin-action-admin")
	assertResponseBodyJSONError(t, response, http.StatusNotFound, "plugin_not_found")
}

func TestAdminPluginActionRejectsUnknownAction(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/missing", map[string]any{}, "plugin-action-admin")
	assertResponseBodyJSONError(t, response, http.StatusNotFound, "plugin_action_not_found")
}

func assertResponseBodyJSONError(t *testing.T, response responseBody, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("expected %d, got %d: %s", wantStatus, response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"code":"`+wantCode+`"`) {
		t.Fatalf("response body = %s, want code %s", response.Body, wantCode)
	}
}
