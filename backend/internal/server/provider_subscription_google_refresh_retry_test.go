package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoogleSubscriptionRefreshPreservesTransientFailureMetadata(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		for _, scenario := range []struct{ upstream, public int }{
			{http.StatusRequestTimeout, http.StatusGatewayTimeout},
			{http.StatusTooManyRequests, http.StatusTooManyRequests},
			{http.StatusInternalServerError, http.StatusBadGateway},
			{http.StatusBadGateway, http.StatusBadGateway},
			{http.StatusServiceUnavailable, http.StatusServiceUnavailable},
			{http.StatusGatewayTimeout, http.StatusGatewayTimeout},
		} {
			t.Run(providerType+"/"+fmt.Sprint(scenario.upstream), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", "90")
					w.Header().Set("X-Private-Token", "header-secret")
					w.WriteHeader(scenario.upstream)
					_, _ = fmt.Fprint(w, `{"error":"temporarily_unavailable","error_description":"refresh-secret"}`)
				}))
				t.Cleanup(upstream.Close)
				adapter := googleSubscriptionTestAdapter(upstream, providerType)
				_, err := adapter.refresh(context.Background(), ProviderResourceCredentials{ClientID: "synthetic-client", ClientSecret: "synthetic-secret", RefreshToken: "refresh-secret"})
				if err == nil {
					t.Fatal("temporary token endpoint failure was accepted")
				}
				failure := AsHTTPError(err)
				if failure.Status != scenario.public || failure.UpstreamStatus != scenario.upstream || providerErrorDisposition(err) != ProviderErrorTransientSame {
					t.Errorf("refresh failure lost routing metadata: %+v, disposition=%q", failure, providerErrorDisposition(err))
				}
				if providerErrorRetryAfter(err) != 90*time.Second {
					t.Errorf("refresh failure lost retry delay: %s", providerErrorRetryAfter(err))
				}
				response := httptest.NewRecorder()
				errorResponseHeaders(response, err)
				if strings.Contains(err.Error(), "secret") || response.Header().Get("X-Private-Token") != "" {
					t.Error("refresh failure exposed private credentials or headers")
				}
				if scenario.upstream == http.StatusTooManyRequests && (response.Header().Get("Retry-After") != "90" || shouldFailoverRoutedError(err, true)) {
					t.Error("refresh rate limit did not preserve the bound account retry policy")
				}
			})
		}
	}
}
