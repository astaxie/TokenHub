package server

import (
	"encoding/json"
	"testing"
)

func TestSubscriptionWorkerCatalogDoesNotDeclareUnknownProtocols(t *testing.T) {
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"test", "Test"}}
	for _, protocol := range []string{"", "auto", "unknown"} {
		upstream := subscriptionWorkerModel{ID: "model", Protocol: protocol}
		upstream.Capabilities.ToolCall, upstream.Capabilities.Reasoning = true, true
		if entry := adapter.catalog([]subscriptionWorkerModel{upstream}); len(entry.Models[0].SupportedParameters) != 0 {
			t.Fatalf("unknown worker protocol %q advertised controls: %v", protocol, entry.Models[0].SupportedParameters)
		}
	}
}

func TestSubscriptionWorkerCatalogRejectsDroppedPrivateProtocolControls(t *testing.T) {
	for _, test := range []struct {
		provider, protocol, model string
		accepted, rejected        []string
	}{
		{"kiro", "anthropic", "claude-sonnet", []string{"tools", "reasoning_effort"}, []string{"max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "tool_choice", "parallel_tool_calls"}},
		{"cursor", "chat", "claude-sonnet", []string{"tools", "reasoning_effort", "service_tier"}, []string{"max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "tool_choice", "parallel_tool_calls", "response_format"}},
		{"qoder", "chat", "coder", []string{"tools", "reasoning_effort", "max_tokens", "max_completion_tokens"}, []string{"temperature", "top_p", "stop", "tool_choice", "parallel_tool_calls", "response_format"}},
		{"qoder-cn", "chat", "coder", []string{"max_tokens", "tools"}, []string{"temperature", "top_p", "stop", "tool_choice"}},
		{"qwenwork", "chat", "coder", []string{"max_tokens", "tools"}, []string{"temperature", "top_p", "stop", "tool_choice"}},
		{"qwenwork-enterprise", "chat", "coder", []string{"max_tokens", "tools"}, []string{"temperature", "top_p", "stop", "tool_choice"}},
		{"devin", "chat", "coder", []string{"max_tokens", "max_completion_tokens", "temperature", "top_p", "tools", "reasoning_effort"}, []string{"stop", "tool_choice", "parallel_tool_calls", "response_format"}},
		{"trae-cn", "chat", "coder", []string{"max_tokens", "temperature", "tools", "tool_choice", "parallel_tool_calls", "reasoning_effort"}, []string{"top_p", "stop", "response_format", "service_tier"}},
		{"trae-global", "chat", "coder", []string{"max_tokens", "temperature", "tools"}, []string{"top_p", "stop", "response_format", "service_tier"}},
		{"commandcode-plan", "chat", "coder", []string{"max_tokens", "max_completion_tokens", "temperature", "tools", "reasoning_effort"}, []string{"top_p", "stop", "tool_choice", "parallel_tool_calls", "response_format", "service_tier"}},
		{"autoclaw", "chat", "coder", []string{"temperature", "top_p", "tools"}, []string{"max_tokens", "max_completion_tokens"}},
		{"autoclaw-cn", "chat", "coder", []string{"temperature", "top_p", "tools"}, []string{"max_tokens", "max_completion_tokens"}},
		{"factory", "responses", "gpt-6-sol", []string{"max_tokens", "tools", "service_tier"}, []string{"temperature", "top_p"}},
		{"factory", "responses", "grok-4.7", []string{"max_tokens", "tools", "temperature", "top_p"}, []string{"stop"}},
	} {
		t.Run(test.provider+"/"+test.model, func(t *testing.T) {
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{test.provider, "Test"}}
			upstream := subscriptionWorkerModel{ID: test.model, Protocol: test.protocol}
			upstream.Capabilities.ToolCall, upstream.Capabilities.Reasoning = true, true
			model := providerModelFromCatalog("provider", adapter.catalog([]subscriptionWorkerModel{upstream}).Models[0])
			values := map[string]string{"max_tokens": "128", "max_completion_tokens": "128", "temperature": "0.2", "top_p": "0.8", "stop": `"END"`,
				"tools": `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`, "tool_choice": `"auto"`, "parallel_tool_calls": "true",
				"reasoning_effort": `"high"`, "service_tier": `"auto"`, "response_format": `{"type":"json_object"}`}
			accepted := make(map[string]string, len(test.accepted))
			for _, field := range test.accepted {
				accepted[field] = values[field]
			}
			assertSubscriptionCatalogChatControls(t, model, accepted)
			for _, field := range test.rejected {
				var request ChatCompletionRequest
				if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"Hello"}],"`+field+`":`+values[field]+`}`), &request); err != nil {
					t.Fatal(err)
				}
				if result := jevChatCompatibility(model, request); result.Reason != "parameter_not_declared" || result.Parameter != field {
					t.Errorf("dropped %s was admitted: %+v", field, result)
				}
			}
		})
	}
}

func TestSubscriptionWorkerGeminiCatalogOnlyDeclaresEncodedReasoning(t *testing.T) {
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"gcp-ai", "GCP AI"}}
	for _, id := range []string{"account-model", "gemini-2.0-flash", "gemini-3-flash"} {
		t.Run(id, func(t *testing.T) {
			upstream := subscriptionWorkerModel{ID: id, Protocol: "gemini", Variants: map[string]json.RawMessage{
				"high": json.RawMessage(`{"reasoningEffort":"high"}`), "max": json.RawMessage(`{"reasoningEffort":"max"}`)}}
			upstream.Capabilities.Reasoning = true
			model := providerModelFromCatalog("provider", adapter.catalog([]subscriptionWorkerModel{upstream}).Models[0])
			effort := "high"
			request := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}, ReasoningEffort: &effort}
			result := jevChatCompatibility(model, request)
			body, err := buildGeminiRequest(id, request)
			if err != nil {
				t.Fatal(err)
			}
			config, _ := body["generationConfig"].(map[string]any)
			if id == "gemini-3-flash" {
				if result.Reason != "" || model.Metadata["reasoning_effort_options"] != "high" || config["thinkingConfig"] == nil {
					t.Fatalf("encoded reasoning was lost: admission=%+v metadata=%v config=%v", result, model.Metadata, config)
				}
			} else if result.Reason != "parameter_not_declared" || model.Metadata["reasoning_effort_options"] != "" || config["thinkingConfig"] != nil {
				t.Fatalf("unencoded reasoning was advertised: admission=%+v metadata=%v config=%v", result, model.Metadata, config)
			}
		})
	}
}
