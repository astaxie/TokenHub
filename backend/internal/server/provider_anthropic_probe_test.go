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
	// DeepSeek-style upstream: no /v1/models, but a working Messages endpoint.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error"}}`))
		case r.URL.Path == "/v1/messages" && r.Method == http.MethodPost:
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_probe","type":"message","role":"assistant","content":[{"type":"text","text":"pong"}],"usage":{"input_tokens":3,"output_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{}
	provider := Provider{BaseURL: upstream.URL, APIKey: "sk-test", Options: map[string]string{"probe_model": "deepseek-chat"}}

	result, err := adapter.ProbeProvider(context.Background(), provider)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	healthy, _ := result.(map[string]any)["healthy"].(bool)
	if !healthy {
		t.Fatalf("probe result = %v, want healthy", result)
	}

	// Without probe_model the probe must explain what to configure instead of
	// silently failing.
	delete(provider.Options, "probe_model")
	_, err = adapter.ProbeProvider(context.Background(), provider)
	if err == nil || !strings.Contains(err.Error(), "probe_model") {
		t.Fatalf("expected probe_model guidance, got %v", err)
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
