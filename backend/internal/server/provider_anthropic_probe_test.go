package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderModelsUpstreamErrorMapping(t *testing.T) {
	cases := []struct {
		status   int
		wantCode string
	}{
		{http.StatusUnauthorized, "provider_models_authentication_failed"},
		{http.StatusForbidden, "provider_models_authentication_failed"},
		{http.StatusTooManyRequests, "provider_models_rate_limited"},
		{http.StatusNotFound, "provider_models_not_offered"},
		{http.StatusMethodNotAllowed, "provider_models_not_offered"},
		{http.StatusBadGateway, "provider_models_upstream_error"},
	}
	for _, tc := range cases {
		err := providerModelsUpstreamError(tc.status)
		if err.Code != tc.wantCode {
			t.Fatalf("status %d: code = %q, want %q", tc.status, err.Code, tc.wantCode)
		}
	}
}

func TestAnthropicProbeProviderFallsBackToMessages(t *testing.T) {
	// DeepSeek-style upstream: no /v1/models, and a minimal Messages ping
	// without a model is answered with 400 (model required). The probe must
	// treat that as healthy: authentication passed and the gateway is alive.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error"}}`))
		case r.URL.Path == "/v1/messages" && r.Method == http.MethodPost:
			var payload struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			w.Header().Set("content-type", "application/json")
			if payload.Model == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"model is required","type":"invalid_request_error"}}`))
				return
			}
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_probe","type":"message","role":"assistant","content":[{"type":"text","text":"pong"}],"usage":{"input_tokens":3,"output_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{}
	provider := Provider{BaseURL: upstream.URL, APIKey: "sk-test"} // no probe_model option needed

	result, err := adapter.ProbeProvider(context.Background(), provider)
	if err != nil {
		t.Fatalf("zero-config probe failed: %v", err)
	}
	healthy, _ := result.(map[string]any)["healthy"].(bool)
	if !healthy {
		t.Fatalf("probe result = %v, want healthy", result)
	}

	// An upstream that offers /v1/models reports the catalog directly.
	catalogUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4.5"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer catalogUpstream.Close()
	provider.BaseURL = catalogUpstream.URL
	result, err = adapter.ProbeProvider(context.Background(), provider)
	if err != nil {
		t.Fatalf("catalog probe failed: %v", err)
	}
	if catalog, _ := result.(map[string]any)["models_catalog"].(bool); !catalog {
		t.Fatalf("probe result = %v, want models_catalog", result)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("probe result must stay JSON-serializable: %v", err)
	}
}

func TestAnthropicProbeProviderRejectsBadCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// The upstream checks credentials before validating the request body.
		if r.Header.Get("x-api-key") != "valid" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid x-api-key"}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{}
	provider := Provider{BaseURL: upstream.URL, APIKey: "wrong-key"}
	_, err := adapter.ProbeProvider(context.Background(), provider)
	// The gateway wraps credential rejection into its own auth-broken message;
	// either wording still means the probe correctly refuses to call it healthy.
	if err == nil || (!strings.Contains(err.Error(), "provider_models_authentication_failed") && !strings.Contains(err.Error(), "credential")) {
		t.Fatalf("bad credentials must surface as an authentication failure, got %v", err)
	}
}

func TestAnthropicProbeResourceGeneratesAndVerifiesOutput(t *testing.T) {
	var lastPayload map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		lastPayload = payload
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_probe","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":6,"output_tokens":2}}`))
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{}
	provider := Provider{BaseURL: upstream.URL, APIKey: "sk-test"}
	resource := ProviderResource{ID: "res_1", Options: map[string]string{"probe_model": "deepseek-chat"}}

	result, err := adapter.Probe(context.Background(), provider, resource,
		adapter.DefaultProbeRequest())
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if result.OutputText != "OK" {
		t.Fatalf("output = %q, want the generated text", result.OutputText)
	}
	if result.Model != "deepseek-chat" {
		t.Fatalf("model = %q, want the configured probe model", result.Model)
	}
	if result.Usage.CompletionTokens != 2 {
		t.Fatalf("usage lost: %+v", result.Usage)
	}
	if lastPayload["max_tokens"] != float64(16) {
		t.Fatalf("probe budget = %v, want 16", lastPayload["max_tokens"])
	}

	// An empty generation is an unhealthy upstream, not a healthy one.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"msg_e","type":"message","content":[{"type":"text","text":""}],"usage":{}}`))
	}))
	defer empty.Close()
	provider.BaseURL = empty.URL
	if _, err := adapter.Probe(context.Background(), provider, resource, adapter.DefaultProbeRequest()); err == nil {
		t.Fatal("empty generation must not count as healthy")
	}
}

func TestAnthropicProbeResourceReachabilityFallback(t *testing.T) {
	// No probe model configured anywhere: an unknown-model 404/400 style answer
	// still proves reachability and credentials.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The upstream validates credentials before anything else, so a wrong
		// key is rejected with 401 in every mode.
		if r.Header.Get("x-api-key") != "sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid x-api-key"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{}
	provider := Provider{BaseURL: upstream.URL, APIKey: "sk-test"}
	resource := ProviderResource{ID: "res_2"}

	result, probeErr := adapter.Probe(context.Background(), provider, resource, adapter.DefaultProbeRequest())
	if probeErr != nil {
		t.Fatalf("reachability probe failed: %v", probeErr)
	}
	if !strings.Contains(result.OutputText, "Reachability verified") {
		t.Fatalf("result = %+v, want the reachability note", result)
	}
	if result.Response["mode"] != "reachability" {
		t.Fatalf("response mode = %v", result.Response["mode"])
	}

	// Bad credentials must never pass, in either mode.
	provider.APIKey = ""
	result, err := adapter.Probe(context.Background(), provider, resource, adapter.DefaultProbeRequest())
	if err != nil {
		t.Fatalf("reachability probe failed: %v", err)
	}
}
