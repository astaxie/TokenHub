package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestXAIGrokCatalogReturnsStaticModels(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	resp := doJSON(t, app, http.MethodGet, "/api/admin/provider-catalog/"+xaiGrokProviderCatalogID, nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", resp.Code, resp.Body)
	}
	var payload struct {
		Data   ProviderCatalogEntry `json:"data"`
		Source string               `json:"source"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source != "xai-grok-subscription" || payload.Data.Type != ProviderXAIGrok || payload.Data.ModelsCount < 4 {
		t.Fatalf("unexpected catalog: %+v", payload)
	}
	found := map[string]bool{}
	for _, model := range payload.Data.Models {
		found[model.ID] = true
		if model.Category != "grok" || model.Metadata["billing_mode"] != "subscription" {
			t.Fatalf("unexpected model: %+v", model)
		}
	}
	for _, id := range []string{"grok-4.5", "grok-4.6", "grok-composer-2.5-fast", "grok-build-0.1"} {
		if !found[id] {
			t.Fatalf("missing Super Grok model %s", id)
		}
	}
}

func TestXAIDeviceOAuthStartAndPoll(t *testing.T) {
	var polls atomic.Int32
	idToken := xaiTestIDToken("owner@example.com", "xai-sub-1")
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			_ = json.NewEncoder(w).Encode(map[string]string{
				"device_authorization_endpoint": "http://" + r.Host + "/device",
				"token_endpoint":                "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/device":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("client_id") != xaiOAuthClientID {
				t.Fatalf("unexpected client_id %q", r.Form.Get("client_id"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "device-code-1",
				"user_code":                 "ABCD-1234",
				"verification_uri":          "https://auth.x.ai/device",
				"verification_uri_complete": "https://auth.x.ai/device?user_code=ABCD-1234",
				"expires_in":                600,
				"interval":                  1,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != xaiOAuthDeviceGrantType || r.Form.Get("device_code") != "device-code-1" {
				t.Fatalf("unexpected token form: %v", r.Form)
			}
			if polls.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "xai-access",
				"refresh_token": "xai-refresh",
				"id_token":      idToken,
				"token_type":    "Bearer",
				"expires_in":    3600,
				"scope":         xaiOAuthScope,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(oauth.Close)
	previousDiscovery := xaiOIDCDiscoveryURL
	xaiOIDCDiscoveryURL = oauth.URL + "/.well-known/openid-configuration"
	t.Cleanup(func() { xaiOIDCDiscoveryURL = previousDiscovery })

	store := NewMemoryStore()
	server := New(store)
	server.upstreamClient = oauth.Client()
	app := server.Handler()

	started := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/xai/start-device", map[string]any{}, "")
	if started.Code != http.StatusOK {
		t.Fatalf("start device status=%d body=%s", started.Code, started.Body)
	}
	var start xaiGrokOAuthStartResponse
	if err := json.Unmarshal([]byte(started.Body), &start); err != nil {
		t.Fatal(err)
	}
	if start.UserCode != "ABCD-1234" || start.SessionID == "" || start.State == "" || strings.Contains(started.Body, "device-code-1") {
		t.Fatalf("unexpected start payload: %s", started.Body)
	}

	pending := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/xai/poll", map[string]any{
		"session_id": start.SessionID, "state": start.State,
	}, "")
	if pending.Code != http.StatusAccepted {
		t.Fatalf("pending poll status=%d body=%s", pending.Code, pending.Body)
	}

	authorized := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/xai/poll", map[string]any{
		"session_id": start.SessionID, "state": start.State,
	}, "")
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized poll status=%d body=%s", authorized.Code, authorized.Body)
	}
	var token xaiGrokOAuthPollResponse
	if err := json.Unmarshal([]byte(authorized.Body), &token); err != nil {
		t.Fatal(err)
	}
	if token.Status != "authorized" || token.AccessToken != "xai-access" || token.RefreshToken != "xai-refresh" || token.AccountEmail != "owner@example.com" || token.AccountID != "xai-sub-1" {
		t.Fatalf("unexpected token payload: %+v", token)
	}
}

func TestXAIGrokChatCompletionsUsesCLIChatProxy(t *testing.T) {
	var seen *http.Request
	server, secret := newXAIGrokRouteTestServer(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Clone(req.Context())
		body := strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"pong"}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_grok","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"grok-4.5","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "pong") {
		t.Fatalf("missing chat text: %s", recorder.Body.String())
	}
	if seen == nil {
		t.Fatal("upstream was not called")
	}
	if seen.URL.Host != "cli-chat-proxy.grok.com" || !strings.HasSuffix(seen.URL.Path, "/responses") {
		t.Fatalf("unexpected upstream URL: %s", seen.URL)
	}
	if seen.Header.Get("Authorization") != "Bearer grok-access" ||
		seen.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" ||
		seen.Header.Get("x-grok-client-version") != xaiGrokClientVersion ||
		seen.Header.Get("x-grok-client-identifier") != "grok-shell" {
		t.Fatalf("missing Grok CLI headers: %#v", seen.Header)
	}
}

func newXAIGrokRouteTestServer(t *testing.T, transport http.RoundTripper) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Grok Route Test", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "Grok Key", Allowed: []string{"grok-4.5"}, Status: StatusActive,
	}, "thk_grok_route")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_grok_route", Name: "Super Grok", Type: ProviderXAIGrok,
		BaseURL: xaiCLIChatProxyBaseURL, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_grok_route", ProviderID: provider.ID, Name: "Grok Account",
		ResourceType: ProviderResourceXAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "grok-access", RefreshToken: "grok-refresh", Email: "owner@example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "grok-4.5", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID: "route_grok", ModelName: "grok-4.5", ProviderID: provider.ID,
		ProviderResourceID: resource.ID, ProviderModel: "grok-4.5",
		Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
	})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "grok-route-secret"})
	if transport != nil {
		server.xaiGrokSubscription.Client = &http.Client{Transport: transport}
	}
	server.xaiGrokSubscription.MaxRequestRetries = 1
	return server, secret
}

func TestXAIGrokRejectsDisallowedHost(t *testing.T) {
	adapter := XAIGrokSubscriptionAdapter{
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{AccessToken: "token"}, nil
		},
	}
	_, err := adapter.OpenResponses(context.Background(), Provider{
		BaseURL: "https://example.invalid/v1",
		Options: map[string]string{"resource_id": "rsrc_x"},
	}, "grok-4.5", ResponsesRequest{Input: "hi"}, nil)
	if AsHTTPError(err).Code != "xai_grok_endpoint_host_not_allowed" {
		t.Fatalf("expected host rejection, got %v", err)
	}
}

func TestXAIGrokProviderRejectsAPIKeyResource(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_grok_conflict", Name: "Super Grok", Type: ProviderXAIGrok, Status: StatusActive})
	_, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "API Key", ResourceType: ProviderResourceAPIKey, APIKey: "sk-test",
	})
	if AsHTTPError(err).Code != "provider_adapter_resource_conflict" {
		t.Fatalf("expected resource conflict, got %v", err)
	}
}

func xaiTestIDToken(email, subject string) string {
	payload, _ := json.Marshal(map[string]string{"email": email, "sub": subject})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
