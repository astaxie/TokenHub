package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminCustomProviderCatalogClassifiesUpstreamFailures(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		responseStatus int
		code           string
	}{
		{name: "authentication failure", upstreamStatus: http.StatusUnauthorized, responseStatus: http.StatusBadGateway, code: "provider_models_authentication_failed"},
		{name: "rate limit", upstreamStatus: http.StatusTooManyRequests, responseStatus: http.StatusTooManyRequests, code: "provider_models_rate_limited"},
		{name: "generic upstream failure", upstreamStatus: http.StatusInternalServerError, responseStatus: http.StatusBadGateway, code: "provider_models_upstream_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(tt.upstreamStatus), tt.upstreamStatus)
			}))
			defer upstream.Close()

			response := doJSON(t, newTestServer(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
				"name":     "Failing Provider",
				"type":     ProviderOpenAICompatible,
				"base_url": upstream.URL + "/v1",
				"api_key":  "invalid-secret",
			}, "")

			if response.Code != tt.responseStatus || !strings.Contains(response.Body, `"code":"`+tt.code+`"`) {
				t.Fatalf("expected status %d and code %q, got %d: %s", tt.responseStatus, tt.code, response.Code, response.Body)
			}
			if strings.Contains(response.Body, http.StatusText(tt.upstreamStatus)) {
				t.Fatalf("upstream status text must not be exposed: %s", response.Body)
			}
		})
	}
}
