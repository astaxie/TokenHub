package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompatibleProviderOptionalCredentialsAcrossDiscoveryAndInference(t *testing.T) {
	for _, key := range []string{"", "test-provider-key"} {
		name := "without credentials"
		if key != "" {
			name = "with credentials"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := ""
				if key != "" {
					want = "Bearer " + key
				}
				if got := r.Header.Get("Authorization"); got != want {
					t.Errorf("authorization = %q, want %q", got, want)
				}
				switch r.URL.Path {
				case "/v1/models":
					writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "local-model"}}})
				case "/v1/chat/completions":
					writeJSON(w, http.StatusOK, map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hello"}}}})
				default:
					t.Errorf("unexpected request path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			body := map[string]any{"name": "Local Model", "type": ProviderOpenAICompatible, "base_url": upstream.URL + "/v1", "api_key": key}
			app := newTestServer()
			for _, endpoint := range []string{"/api/admin/providers/test-connection", "/api/admin/provider-catalog/custom"} {
				response := doJSON(t, app, http.MethodPost, endpoint, body, "")
				if response.Code != http.StatusOK {
					t.Fatalf("%s returned %d: %s", endpoint, response.Code, response.Body)
				}
			}
			adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
			_, _, err := adapter.Chat(context.Background(), Provider{Type: ProviderOpenAICompatible, BaseURL: upstream.URL + "/v1", APIKey: key}, "local-model", ChatCompletionRequest{})
			if err != nil {
				t.Fatalf("inference failed: %v", err)
			}
		})
	}
}

func TestCompatibleProviderWithoutKeyReportsUpstreamAuthenticationFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "credentials required", http.StatusUnauthorized)
	}))
	defer upstream.Close()
	response := doJSON(t, newTestServer(), http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"type": ProviderOpenAICompatible, "base_url": upstream.URL + "/v1",
	}, "")
	if response.Code == http.StatusOK || strings.Contains(response.Body, "provider_api_key_required") {
		t.Fatalf("expected an upstream authentication error, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "provider_models_authentication_failed") {
		t.Fatalf("expected actionable upstream authentication error, got %s", response.Body)
	}
}
