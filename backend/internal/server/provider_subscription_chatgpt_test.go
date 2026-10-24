package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func chatGPTSubscriptionFixture(t *testing.T, handler http.HandlerFunc) (*ChatGPTSubscriptionAdapter, Provider) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	a := &ChatGPTSubscriptionAdapter{Client: upstream.Client(), StreamClient: upstream.Client(), CredentialRefreshClient: upstream.Client(), APIBaseURL: upstream.URL, TokenURL: upstream.URL + "/token",
		RefreshCredentials: func(_ context.Context, id string, _ bool) (ProviderResourceCredentials, error) {
			if id != "account" {
				t.Errorf("wrong account %s", id)
			}
			return ProviderResourceCredentials{AccessToken: "direct-token"}, nil
		}}
	return a, Provider{Type: ProviderChatGPTSubscription, Options: map[string]string{"resource_id": "account"}}
}

func TestChatGPTSubscriptionDirectResponseEnvelope(t *testing.T) {
	a, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer direct-token" {
			t.Errorf("wrong direct API request: %s", r.URL.Path)
		}
		if r.Header.Get("ChatGPT-Account-ID") != "" || r.Header.Get("Originator") != "" {
			t.Error("direct API received Codex headers")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["instructions"] != "Use the caller's instructions." || body["model"] != "gpt-model" || body["store"] != false || body["stream"] != true {
			t.Errorf("incorrect envelope: %v", body)
		}
		if _, ok := body["max_output_tokens"]; ok {
			t.Error("unsupported output cap sent")
		}
		if _, ok := body["temperature"]; ok {
			t.Error("unsupported temperature sent")
		}
		items, _ := body["input"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["role"] != "user" {
			t.Errorf("input not normalized: %v", body["input"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-direct\",\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n")
	})
	temperature := 0.5
	body, usage, err := a.Responses(context.Background(), provider, "gpt-model", ResponsesRequest{Input: "hello", Instructions: "Use the caller's instructions.", MaxTokens: 80, Temperature: &temperature})
	if err != nil || usage.TotalTokens != 6 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	if body.(map[string]any)["output_text"] != "hello" {
		t.Fatalf("missing output: %v", body)
	}
}

func TestChatGPTSubscriptionBodyToolsAndSystemRole(t *testing.T) {
	request := ResponsesRequest{Input: []any{map[string]any{"role": "system", "content": "caller policy"}, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "done"}}}
	request.raw = map[string]json.RawMessage{"tools": json.RawMessage(`[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`)}
	data, err := chatGPTSubscriptionBody(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"role":"developer"`) || !strings.Contains(string(data), `"name":"lookup"`) || !strings.Contains(string(data), `"type":"function_call_output"`) {
		t.Fatalf("tool continuation was lost: %s", data)
	}
	request.raw["tools"] = json.RawMessage(`[{"type":"file_search","vector_store_ids":["vs_1"]}]`)
	if _, err := chatGPTSubscriptionBody(request); err == nil {
		t.Fatal("unsupported hosted tool was accepted")
	}
}

func TestChatGPTSubscriptionRefreshRotationAndScope(t *testing.T) {
	a, _ := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != "registered-client" || r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("resource") != chatGPTSubscriptionBaseURL {
			t.Errorf("incorrect refresh grant")
		}
		_, _ = fmt.Fprintf(w, `{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":3600,"scope":"openid %s"}`, chatGPTSubscriptionDirectScope)
	})
	current := ProviderResourceCredentials{ClientID: "registered-client", RefreshToken: "old-refresh", Email: "account@example.test"}
	fresh, err := a.Refresh(context.Background(), current)
	if err != nil || fresh.RefreshToken != "rotated-refresh" || fresh.AccessToken != "new-access" || fresh.Email != current.Email || fresh.ExpiresAt == "" {
		t.Fatalf("refresh failed: %v", err)
	}
	registration := a.ProviderResourceCredentialRefreshHandlers()[0]
	if registration.Profile != chatGPTSubscriptionRefreshProfile || registration.ProviderType != ProviderChatGPTSubscription {
		t.Fatal("refresh registration mismatch")
	}
	if _, err := chatGPTSubscriptionTokenCredentials(oauthTokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 60, Scope: "openid"}, current); err == nil {
		t.Fatal("direct scope was not required")
	}
}

func TestChatGPTSubscriptionRefreshFailureIsSecretSafe(t *testing.T) {
	a, _ := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant","error_description":"old-refresh direct-token"}`)
	})
	_, err := a.Refresh(context.Background(), ProviderResourceCredentials{ClientID: "registered", RefreshToken: "old-refresh"})
	if err == nil || AsHTTPError(err).Code != "provider_resource_reauthorization_required" || strings.Contains(err.Error(), "old-refresh") {
		t.Fatalf("wrong refresh error: %v", err)
	}
}

func TestChatGPTSubscriptionRetriesUnauthorizedAndListsAccountModels(t *testing.T) {
	var calls, forced atomic.Int32
	a, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(401)
			_, _ = fmt.Fprint(w, `{"error":"direct-token"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			t.Error("retry did not use fresh token")
		}
		w.Header().Set("ETag", "models-v2")
		_, _ = fmt.Fprint(w, `{"models":[{"slug":"gpt-visible","display_name":"Visible model","visibility":"list","context_window":100000,"supported_reasoning_levels":[{"effort":"high"}]},{"slug":"gpt-hidden","visibility":"hidden"}]}`)
	})
	a.RefreshCredentials = func(_ context.Context, _ string, force bool) (ProviderResourceCredentials, error) {
		if force {
			forced.Add(1)
			return ProviderResourceCredentials{AccessToken: "fresh-token"}, nil
		}
		return ProviderResourceCredentials{AccessToken: "direct-token"}, nil
	}
	provider.Options = nil
	catalog, status, err := a.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "models-v1")
	if err != nil || status != 200 || len(catalog.Models) != 1 || catalog.Models[0].ID != "gpt-visible" || catalog.ETag != "models-v2" || forced.Load() != 1 {
		t.Fatalf("catalog=%+v status=%d forced=%d err=%v", catalog, status, forced.Load(), err)
	}
}

func TestChatGPTSubscriptionDynamicClientAuthorization(t *testing.T) {
	verifier := strings.Repeat("v", 64)
	address, err := ChatGPTSubscriptionAuthorizeURL("http://127.0.0.1:1455/auth/callback", "state", "nonce", verifier, "urn:uuid:fixture-host")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(address)
	query := parsed.Query()
	if query.Get("client_id") != "dynamic_agent_client" || query.Get("code_challenge") != openAICodeChallenge(verifier) || query.Get("nonce") != "nonce" || !strings.Contains(query.Get("scope"), chatGPTSubscriptionDirectScope) {
		t.Fatalf("incorrect authorization parameters: %v", query)
	}
	if _, err := ChatGPTSubscriptionAuthorizeURL("https://attacker.invalid", "state", "nonce", verifier, "urn:uuid:fixture"); err == nil {
		t.Fatal("non-loopback callback was accepted")
	}
	claims := map[string]any{"iss": "https://auth.openai.com", "aud": "registered-client", "nonce": "nonce", "exp": time.Now().Add(time.Hour).Unix(), "sub": "user-1", "email": "account@example.test"}
	raw, _ := json.Marshal(claims)
	idToken := "header." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
	a, _ := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("client_id") != "registered-client" || r.Form.Get("code_verifier") != verifier {
			t.Error("wrong dynamic client exchange")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "id_token": idToken, "expires_in": 3600, "scope": chatGPTSubscriptionDirectScope})
	})
	credentials, err := a.ExchangeAuthorization(context.Background(), ChatGPTSubscriptionAuthorization{Code: "code", ClientID: "registered-client", Verifier: verifier, RedirectURI: "http://127.0.0.1:1455/auth/callback", Nonce: "nonce"})
	if err != nil || credentials.ClientID != "registered-client" || credentials.UserID != "user-1" || credentials.Email != "account@example.test" {
		t.Fatalf("exchange failed: %v", err)
	}
	for _, bad := range []struct{ client, nonce string }{{"other-client", "nonce"}, {"registered-client", "wrong-nonce"}} {
		if _, err := chatGPTSubscriptionIDClaims(idToken, bad.client, bad.nonce); err == nil {
			t.Fatal("unbound identity was accepted")
		}
	}
}

func TestChatGPTSubscriptionRejectsCredentialRedirect(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer target.Close()
	a, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	if _, _, err := a.Responses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}); err == nil {
		t.Fatal("redirect was accepted")
	}
	if received.Load() != 0 {
		t.Fatal("token followed a redirect")
	}
	provider.BaseURL = "https://api.openai.com.attacker.invalid/v1"
	if _, _, err := a.Responses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}); err == nil {
		t.Fatal("untrusted endpoint was accepted")
	}
}
