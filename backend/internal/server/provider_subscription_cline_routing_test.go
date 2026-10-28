package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestClineWorkerHTTPFailuresPreserveRouting(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, scenario := range []struct {
		name        string
		status      int
		body        string
		disposition ProviderErrorDisposition
	}{
		{"rate-limit", 429, `{"error":{"message":"synthetic-private"}}`, ProviderErrorTransientSame},
		{"incomplete-rate-limit", 429, `{"error":`, ProviderErrorTransientSame},
		{"model-entitlement", 403, `{"error":{"code":"ENTITLEMENT_ERROR","message":"the user is not subscribed to required model plan"}}`, ProviderErrorModelUnsupported},
		{"unknown-permission", 403, `{"error":{"message":"Forbidden"}}`, ProviderErrorAuthBroken},
		{"expired-token", 401, `{"error":{"message":"Unauthorized"}}`, ProviderErrorAuthBroken},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", scenario.name, stream), func(t *testing.T) {
				closed := make(chan struct{})
				upstream := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
					if request.URL.Host != "api.cline.bot" || request.URL.Path != "/api/v1/chat/completions" {
						t.Errorf("Unexpected upstream request: %s", request.URL)
					}
					reader, writer := io.Pipe()
					go func() {
						defer close(closed)
						defer func() { _ = writer.Close() }()
						_, _ = io.WriteString(writer, scenario.body)
						// The diagnostic arrives, but the upstream never finishes.
						<-request.Context().Done()
					}()
					return &http.Response{StatusCode: scenario.status, Header: http.Header{
						"Content-Type": {"application/json"}, "Retry-After": {"37"}, "X-Private-Token": {"synthetic-private"},
					}, Body: reader, Request: request}, nil
				})}
				process, err := startSubscriptionWorker(upstream, `{"type":"api","key":"synthetic-cline-key"}`, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { process.cancel(); <-process.done })
				transport := &subscriptionWorkerTransport{adapter: &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cline", "Cline"}},
					process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "cline-pass/mimo-v2.6-flash", Protocol: "chat"}, stream: stream}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{"messages":[{"role":"user","content":"Hello"}]}`))
				if err != nil {
					t.Fatal(err)
				}
				response, err := transport.RoundTrip(request)
				if response != nil {
					defer response.Body.Close()
					t.Fatal("Failure was exposed as a successful response")
				}
				if ctx.Err() != nil || err == nil || providerErrorDisposition(err) != scenario.disposition {
					t.Fatalf("Failure lost its routing classification or waited for EOF: %v", err)
				}
				public := httptest.NewRecorder()
				errorResponseHeaders(public, err)
				if scenario.status == 429 && (providerErrorRetryAfter(err) != 37*time.Second || public.Header().Get("Retry-After") != "37") {
					t.Fatalf("Rate limit lost its retry delay: %v", err)
				}
				if strings.Contains(err.Error(), "synthetic-") || public.Header().Get("X-Private-Token") != "" {
					t.Fatal("Failure exposed private upstream diagnostics")
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("Discarded diagnostic left the upstream request running")
				}
			})
		}
	}
}
