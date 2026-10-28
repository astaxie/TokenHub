package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeSubscriptionHTTPFailuresPreserveRoutingAndRetryMetadata(t *testing.T) {
	for _, protocol := range []string{"copilot/chat", "copilot/stream", "chatgpt/responses", "chatgpt/stream"} {
		for _, failure := range []struct {
			status      int
			public      int
			disposition ProviderErrorDisposition
			failover    bool
		}{
			{http.StatusUnauthorized, http.StatusBadGateway, ProviderErrorAuthBroken, true},
			{http.StatusForbidden, http.StatusBadGateway, ProviderErrorAuthBroken, true},
			{http.StatusNotFound, http.StatusBadGateway, ProviderErrorModelUnsupported, true},
			{http.StatusTooManyRequests, http.StatusTooManyRequests, ProviderErrorTransientSame, true},
			{http.StatusBadRequest, http.StatusBadRequest, ProviderErrorClient, false},
		} {
			t.Run(fmt.Sprintf("%s/%d", protocol, failure.status), func(t *testing.T) {
				var attempts atomic.Int32
				handler := func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						_, _ = fmt.Fprintf(w, `{"token":"exchanged-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					case "/models":
						_, _ = io.WriteString(w, `{"data":[{"id":"model","supported_endpoints":["/chat/completions"]}]}`)
					default:
						attempts.Add(1)
						w.Header().Set("Retry-After", "90")
						w.Header().Set("X-Private-Token", "private-header-secret")
						w.WriteHeader(failure.status)
						_, _ = io.WriteString(w, `{"error":{"message":"direct-token github-secret exchanged-secret"}}`)
					}
				}
				var err error
				if strings.HasPrefix(protocol, "copilot/") {
					adapter, provider := copilotSubscriptionFixture(t, handler)
					request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
					if protocol == "copilot/stream" {
						_, err = adapter.ChatStream(context.Background(), provider, "model", request, io.Discard)
					} else {
						_, _, err = adapter.Chat(context.Background(), provider, "model", request)
					}
				} else {
					adapter, provider := chatGPTSubscriptionFixture(t, handler)
					request := ResponsesRequest{Input: "Hello"}
					if protocol == "chatgpt/stream" {
						_, err = adapter.OpenResponses(context.Background(), provider, "model", request, nil)
					} else {
						_, _, err = adapter.Responses(context.Background(), provider, "model", request)
					}
				}
				if err == nil {
					t.Fatal("upstream failure was accepted")
				}
				wantAttempts := int32(1)
				if failure.status == http.StatusUnauthorized {
					wantAttempts = 2
				}
				if attempts.Load() != wantAttempts {
					t.Errorf("credential rejection retry attempts = %d, want %d", attempts.Load(), wantAttempts)
				}
				httpError := AsHTTPError(err)
				if httpError.Status != failure.public || httpError.UpstreamStatus != failure.status || providerErrorDisposition(err) != failure.disposition {
					t.Errorf("failure metadata = %+v, disposition = %s", httpError, providerErrorDisposition(err))
				}
				if shouldFailoverRoutedError(err, false) != failure.failover {
					t.Errorf("failure changed the router's failover decision: %v", err)
				}
				if failure.status == http.StatusTooManyRequests && shouldFailoverRoutedError(err, true) {
					t.Error("temporary rate limit changed a bound account route")
				}
				response := httptest.NewRecorder()
				errorResponseHeaders(response, err)
				if failure.status == http.StatusTooManyRequests && (providerErrorRetryAfter(err) != 90*time.Second || response.Header().Get("Retry-After") != "90") {
					t.Errorf("rate limit lost upstream retry delay: duration=%s headers=%v", providerErrorRetryAfter(err), response.Header())
				}
				if failure.status != http.StatusTooManyRequests && response.Header().Get("Retry-After") != "" {
					t.Error("non-rate-limit failure exposed Retry-After")
				}
				if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "direct-token") || response.Header().Get("X-Private-Token") != "" {
					t.Error("upstream failure exposed private credentials or headers")
				}
			})
		}
	}
}
