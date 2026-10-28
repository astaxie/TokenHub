package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestChatGPTSubscriptionBridgesPreserveDefaultReasoningEffort(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				adapter, _ := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					reasoning, _ := body["reasoning"].(map[string]any)
					if reasoning["effort"] != nil {
						t.Errorf("ChatGPT bridge overrode omitted reasoning effort: %#v", reasoning)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, subscriptionProtocolFixture("responses", true))
				})
				server, store, key := classificationGateway(t, chatGPTSubscriptionBaseURL, chatGPTSubscriptionBaseURL, ProviderChatGPTSubscription)
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				for _, resource := range store.ListProviderResources() {
					resource.ResourceType = ProviderResourceChatGPTSubscription
					resource.Credentials = &ProviderResourceCredentials{AccessToken: "synthetic-bridge-token"}
					if _, err := store.UpdateProviderResource(resource.ID, resource); err != nil {
						t.Fatal(err)
					}
				}
				adapter.RefreshCredentials = store.RefreshProviderResourceCredentials
				registerTestAdapter(server, ProviderChatGPTSubscription, adapter)
				path := "/v1/chat/completions"
				payload := map[string]any{
					"model": "classified-model", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}, "stream": stream,
				}
				if protocol == "messages" {
					path = "/v1/messages"
					payload["max_tokens"] = 32
				}
				response := doJSON(t, server.Handler(), http.MethodPost, path, payload, key)
				if response.Code != http.StatusOK || !strings.Contains(response.Body, "Hello") {
					t.Fatalf("ChatGPT bridge failed: status=%d body=%s", response.Code, response.Body)
				}
			})
		}
	}
}

func TestResponsesBridgeReasoningKeepsExplicitControlsAndCodexDefaults(t *testing.T) {
	for _, providerType := range []string{ProviderChatGPTSubscription, ProviderOpenAICodex, "custom-codex-bridge"} {
		t.Run(providerType, func(t *testing.T) {
			for _, effort := range []string{"", "high"} {
				request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
				if effort != "" {
					request.ReasoningEffort = &effort
				}
				upstream, err := chatToBridgedResponsesRequest(providerType, request)
				if err != nil {
					t.Fatal(err)
				}
				want := effort
				if want == "" && providerType != ProviderChatGPTSubscription {
					want = "medium"
				}
				assertBridgeReasoningEffort(t, upstream, want)
			}
			for _, test := range []struct {
				name, effort string
				controls     map[string]any
			}{
				{name: "omitted"},
				{name: "effort", effort: "high", controls: map[string]any{"effort": "high"}},
				{name: "output config", effort: "max", controls: map[string]any{"output_config": map[string]any{"effort": "max"}}},
				{name: "adaptive", effort: "high", controls: map[string]any{"thinking": map[string]any{"type": "adaptive"}}},
				{name: "disabled", effort: "none", controls: map[string]any{"thinking": map[string]any{"type": "disabled"}}},
				{name: "budget", effort: "medium", controls: map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 4096}}},
			} {
				t.Run(test.name, func(t *testing.T) {
					request := anthropicMessagesRequest{Model: "public", Messages: []any{map[string]any{"role": "user", "content": "Hello"}}, Raw: test.controls}
					upstream, err := anthropicToBridgedResponsesRequest(providerType, request)
					if err != nil {
						t.Fatal(err)
					}
					want := test.effort
					if want == "" && providerType != ProviderChatGPTSubscription {
						want = "medium"
					}
					assertBridgeReasoningEffort(t, upstream, want)
				})
			}
		})
	}
}

func assertBridgeReasoningEffort(t *testing.T, request ResponsesRequest, want string) {
	t.Helper()
	if request.Reasoning == nil {
		t.Fatal("reasoning summary configuration was lost")
	}
	actual := request.Reasoning.Effort
	if want == "" && actual != nil || want != "" && (actual == nil || *actual != want) {
		t.Fatalf("reasoning effort = %v, want %q", actual, want)
	}
}
