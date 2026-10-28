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

func TestFactoryResponsesPreservesRequestedServiceTier(t *testing.T) {
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
				if request.URL.Path != "/api/llm/o/v1/responses" {
					t.Errorf("unexpected upstream request: %s", request.URL)
				}
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					return nil, err
				}
				mu.Lock()
				fetched = payload
				mu.Unlock()
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(subscriptionProtocolFixture("responses", true))), Request: request}, nil
			})}
			pool := newSubscriptionWorkerPool(client)
			defer pool.Close()
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"factory", "Factory"}, Pool: pool,
				RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
					return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-factory","activeOrganizationId":"fixture-org","expires":4102444800000}`}, nil
				}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
			provider := Provider{Options: map[string]string{"resource_id": "account"}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, tier := range []string{"priority", "auto", "default", ""} {
				t.Run("tier="+tier, func(t *testing.T) {
					var request ChatCompletionRequest
					body := `{"model":"public-model","messages":[{"role":"user","content":"Hello"}]}`
					if tier != "" {
						body = strings.TrimSuffix(body, "}") + `,"service_tier":"` + tier + `"}`
					}
					if err := json.Unmarshal([]byte(body), &request); err != nil {
						t.Fatal(err)
					}
					var err error
					if stream {
						var output bytes.Buffer
						_, err = adapter.ChatStream(ctx, provider, "gpt-5.5", request, &output)
					} else {
						_, _, err = adapter.Chat(ctx, provider, "gpt-5.5", request)
					}
					if err != nil {
						t.Fatal(err)
					}
					mu.Lock()
					defer mu.Unlock()
					model := "gpt-5.5"
					if tier == "priority" {
						model += "-fast"
					}
					var expectedTier any
					if tier != "" {
						expectedTier = tier
					}
					if fetched["model"] != model || fetched["service_tier"] != expectedTier {
						t.Fatalf("requested service tier did not reach the Factory model selector: %#v", fetched)
					}
				})
			}
		})
	}
}
