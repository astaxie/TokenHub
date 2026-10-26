package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenDesignAnthropicModelPreservesAdvertisedReasoningEffort(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, stream := range []bool{false, true} {
		name := "collected"
		if stream {
			name = "streamed"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var fetched map[string]any
			client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
				status, body, contentType := http.StatusOK, "", "application/json"
				switch request.URL.Path {
				case "/v1/models":
					status, body = http.StatusServiceUnavailable, `{}`
				case "/v1/messages":
					var payload map[string]any
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						return nil, err
					}
					mu.Lock()
					fetched = payload
					mu.Unlock()
					body = subscriptionProtocolFixture("anthropic", stream)
					if stream {
						contentType = "text/event-stream"
					}
				default:
					t.Errorf("unexpected upstream request: %s", request.URL)
					status, body = http.StatusNotFound, `{}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})}
			pool := newSubscriptionWorkerPool(client)
			defer pool.Close()
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"opendesign", "OpenDesign"}, Pool: pool,
				RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
					return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"synthetic-runtime-key"}`}, nil
				}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
			provider := Provider{Options: map[string]string{"resource_id": "account"}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, effort := range []string{"low", "high", "max", "ultra", ""} {
				t.Run("effort="+effort, func(t *testing.T) {
					request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
					if effort != "" {
						request.ReasoningEffort = &effort
					}
					var err error
					if stream {
						var output bytes.Buffer
						_, err = adapter.ChatStream(ctx, provider, "glm-5.3-flash", request, &output)
					} else {
						_, _, err = adapter.Chat(ctx, provider, "glm-5.3-flash", request)
					}
					if err != nil {
						t.Fatal(err)
					}
					mu.Lock()
					defer mu.Unlock()
					config, _ := fetched["output_config"].(map[string]any)
					var expected any
					if effort != "" && effort != "ultra" {
						expected = effort
					}
					if fetched["model"] != "glm-5.3-flash" || config["effort"] != expected {
						t.Fatalf("native Messages effort does not match the model's declaration: %#v", fetched)
					}
				})
			}
		})
	}
}
