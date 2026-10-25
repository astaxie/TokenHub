package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func subscriptionWorkerJSONFixture(t *testing.T, protocol, body string) (*SubscriptionWorkerAdapter, Provider) {
	t.Helper()
	pool := newSubscriptionWorkerPool(&http.Client{})
	t.Cleanup(pool.Close)
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if command["operation"] == "models" {
				return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"test-model","protocol":"` + protocol + `"}]}`)}}
			}
			return []subscriptionWorkerEvent{{Event: "response", Status: 200, Headers: http.Header{"Content-Type": []string{"application/json"}}}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(body))}, {Event: "end"}}
		}), nil
	}
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, Pool: pool, RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-secret"}`}, nil
	}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
	return adapter, Provider{Type: adapter.Definition.providerType(), Options: map[string]string{"resource_id": "account"}}
}

func TestSubscriptionWorkerRejectsCollectedErrorEnvelopes(t *testing.T) {
	for _, protocol := range []string{"chat", "anthropic", "gemini", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			adapter, provider := subscriptionWorkerJSONFixture(t, protocol, `{"error":{"type":"authentication_error","message":"synthetic-secret"},"usage":{"input_tokens":9,"output_tokens":3}}`)
			output, usage, err := adapter.Chat(context.Background(), provider, "test-model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}})
			if err == nil || output != nil || providerErrorDisposition(err) != ProviderErrorAuthBroken || usage.PromptTokens != 9 || usage.CompletionTokens != 3 || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("JSON error became a successful completion or lost usage: %+v, %v", usage, err)
			}
			if _, err := adapter.Probe(context.Background(), provider, ProviderResource{ID: "account"}, ProviderProbeRequest{Model: "test-model"}); err == nil {
				t.Fatal("JSON error became a successful resource probe")
			}
			var streamed bytes.Buffer
			usage, err = adapter.ChatStream(context.Background(), provider, "test-model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}, &streamed)
			if err == nil || streamed.Len() != 0 || providerErrorDisposition(err) != ProviderErrorAuthBroken || usage.PromptTokens != 9 || usage.CompletionTokens != 3 || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("Streaming request accepted or exposed a JSON error: %q, %+v, %v", streamed.String(), usage, err)
			}
		})
	}
}

func TestSubscriptionWorkerJSONErrorsFailOverWithReportedUsage(t *testing.T) {
	server, store, key := classificationGateway(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	project, apiKey, err := server.authenticate(request)
	if err != nil {
		t.Fatal(err)
	}
	call, err := server.admitRoutedCall(httptest.NewRecorder(), request, project, apiKey, "classified-model", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	routed, err := server.prepareAdmittedRoutedCall(request.Context(), call, call.Model.Name)
	if err != nil {
		t.Fatal(err)
	}
	adapter, provider := subscriptionWorkerJSONFixture(t, "chat", `{"error":{"type":"rate_limit_error"},"usage":{"prompt_tokens":9,"completion_tokens":3}}`)
	_, _, _, attempts, err := executeRoutedWithStore(request.Context(), store, routed, false, func(ctx context.Context, _ RouteSelection, _ bool, attempt int) (any, Usage, error) {
		if attempt == 1 {
			return adapter.Chat(ctx, provider, "test-model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}})
		}
		return map[string]any{"choices": []any{}}, Usage{}, nil
	})
	if err != nil || len(attempts) != 2 || attempts[0].Status != 429 || attempts[0].Usage.TotalTokens != 12 || attempts[1].Status != 200 {
		t.Fatalf("Embedded JSON error did not fail over with its reported usage: %+v, %v", attempts, err)
	}
	if resourceFailureCount(t, store, "rsrc_classified_0") != 1 {
		t.Fatal("Embedded JSON failure incorrectly marked the resource healthy")
	}
}
