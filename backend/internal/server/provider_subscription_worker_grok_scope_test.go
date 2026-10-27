package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGrokScopeFailureSurvivesWorkerProbeAndStream(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, authorized := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("authorized=%t/stream=%t", authorized, stream), func(t *testing.T) {
				var models, inference atomic.Int32
				client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
					status, body := http.StatusOK, ""
					headers := http.Header{"Content-Type": {"application/json"}}
					switch request.URL.Path {
					case "/v1/models":
						models.Add(1)
						body = `{"data":[{"id":"grok-4.7","api_backend":"responses","context_window":131072}]}`
					case "/v1/responses":
						inference.Add(1)
						if authorized {
							body = subscriptionProtocolFixture("responses", true)
							headers.Set("Content-Type", "text/event-stream")
						} else {
							status = http.StatusForbidden
							body = `{"code":"permission-denied","error":"OAuth2 token missing required scope: api:access","private":"synthetic-private-token"}`
						}
					default:
						t.Errorf("unexpected request, missing consent must not trigger token renewal: %s", request.URL.Path)
						status = http.StatusNotFound
					}
					return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})}
				pool := newSubscriptionWorkerPool(client)
				t.Cleanup(pool.Close)
				auth, _ := json.Marshal(map[string]any{"type": "oauth", "access": "synthetic-private-token", "refresh": "synthetic-refresh", "expires": time.Now().Add(time.Hour).UnixMilli()})
				adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}, Pool: pool,
					RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
						return ProviderResourceCredentials{SessionJSON: string(auth)}, nil
					}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
				provider := Provider{Type: "grok_subscription", Options: map[string]string{"resource_id": "grok-account"}}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var err error
				var output bytes.Buffer
				var probe ProviderProbeResult
				if stream {
					_, err = adapter.ChatStream(ctx, provider, "grok-4.7", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}, &output)
				} else {
					probe, err = adapter.Probe(ctx, provider, ProviderResource{ID: "grok-account", ResourceType: "grok_account"}, ProviderProbeRequest{})
				}
				if authorized {
					if err != nil || stream && !strings.Contains(output.String(), "Hello") || !stream && probe.OutputText != "Hello" {
						t.Fatalf("authorized inference failed: %v", err)
					}
				} else {
					failure := AsHTTPError(err)
					if failure.Code != "grok_api_scope_missing" || failure.Status != http.StatusConflict || failure.UpstreamStatus != http.StatusForbidden || providerErrorDisposition(err) != ProviderErrorAuthBroken {
						t.Fatalf("missing scope lost its diagnostic or disposition: %+v", failure)
					}
					if !strings.Contains(failure.Message, "api:access") || strings.Contains(failure.Message, "synthetic-private") || output.Len() != 0 {
						t.Fatal("scope failure leaked upstream content or committed a stream")
					}
				}
				if models.Load() < 1 || inference.Load() != 1 {
					t.Fatalf("unexpected request counts: models=%d inference=%d", models.Load(), inference.Load())
				}
			})
		}
	}
}
