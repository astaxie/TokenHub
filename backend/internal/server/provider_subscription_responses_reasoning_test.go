package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSubscriptionResponsesUsesOnlyCallerReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "  ", "low", "high"} {
		t.Run("effort="+effort, func(t *testing.T) {
			request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
			if effort != "" {
				request.ReasoningEffort = &effort
			}
			converted, err := chatToSubscriptionResponsesRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(converted)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			reasoning, _ := body["reasoning"].(map[string]any)
			var expected any
			if strings.TrimSpace(effort) != "" {
				expected = effort
			}
			if reasoning["effort"] != expected || reasoning["summary"] != "auto" {
				t.Fatalf("Responses reasoning does not preserve the caller's choice: %#v", reasoning)
			}
		})
	}
}

func TestOpenDesignResponsesPreservesDefaultReasoningEffort(t *testing.T) {
	client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		status, body, contentType := http.StatusOK, "", "application/json"
		switch request.URL.Path {
		case "/v1/models":
			status, body = http.StatusServiceUnavailable, `{}`
		case "/v1/responses":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			reasoning, _ := payload["reasoning"].(map[string]any)
			// These bundled OpenDesign models support low, high and max, so
			// injecting Codex's medium default makes an ordinary call invalid.
			if reasoning["effort"] != nil {
				t.Errorf("omitted caller effort became an unsupported OpenDesign effort: %#v", reasoning)
			}
			body, contentType = subscriptionProtocolFixture("responses", true), "text/event-stream"
		default:
			t.Errorf("unexpected upstream path: %s", request.URL.Path)
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
	request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
	for _, model := range []string{"claude-haiku-5.5", "deepseek-v4-pro"} {
		t.Run(model, func(t *testing.T) {
			if _, _, err := adapter.Chat(context.Background(), provider, model, request); err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.ChatStream(context.Background(), provider, model, request, io.Discard); err != nil {
				t.Fatal(err)
			}
		})
	}
}
