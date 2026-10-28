package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

func TestSubscriptionWorkerCatalogAdmitsRepresentedChatControls(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"test", "Test"}}
			upstream := subscriptionWorkerModel{ID: "account-model", Protocol: protocol}
			if protocol == "gemini" {
				upstream.ID = "gemini-3-flash"
			}
			upstream.Capabilities.ToolCall, upstream.Capabilities.Reasoning = true, true
			upstream.Variants = map[string]json.RawMessage{"high": json.RawMessage(`{"reasoningEffort":"high"}`)}
			entry := adapter.catalog([]subscriptionWorkerModel{upstream})
			model := providerModelFromCatalog("provider", entry.Models[0])
			fields := map[string]string{
				"max_tokens": "128", "max_completion_tokens": "128", "temperature": "0.2", "top_p": "0.8",
				"tools":       `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`,
				"tool_choice": `"auto"`, "parallel_tool_calls": "true", "reasoning_effort": `"high"`,
			}
			if protocol != "responses" {
				fields["stop"] = `"END"`
			}
			if protocol == "chat" || protocol == "responses" {
				fields["response_format"], fields["service_tier"] = `{"type":"json_object"}`, `"auto"`
			}
			if protocol == "chat" {
				fields["presence_penalty"], fields["frequency_penalty"] = "0.1", "0.2"
			}
			assertSubscriptionCatalogChatControls(t, model, fields)
			for _, field := range []string{"max_output_tokens", "text", "vendor_extension"} {
				if slices.Contains(model.SupportedParameters, field) {
					t.Fatalf("public Chat codec advertised an unrepresented field %s", field)
				}
			}
			if (protocol == "anthropic" || protocol == "gemini") && slices.Contains(model.SupportedParameters, "response_format") {
				t.Fatal("codec advertised a structured output control it does not convert")
			}
			if protocol == "responses" && slices.Contains(model.SupportedParameters, "stop") {
				t.Fatal("Responses bridge advertised an unsupported stop control")
			}
		})
	}
}

func TestSubscriptionWorkerCatalogDoesNotInventOptionalCapabilities(t *testing.T) {
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"test", "Test"}}
	entry := adapter.catalog([]subscriptionWorkerModel{{ID: "basic-model", Protocol: "chat"}})
	model := providerModelFromCatalog("provider", entry.Models[0])
	for _, field := range []string{"tools", "tool_choice", "parallel_tool_calls", "reasoning_effort"} {
		if slices.Contains(model.SupportedParameters, field) {
			t.Fatalf("catalog invented optional capability %s", field)
		}
	}
}

func TestCopilotSubscriptionCatalogAdmitsSelectedCodecControls(t *testing.T) {
	adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
			return
		}
		_, _ = fmt.Fprint(w, `{"data":[
			{"id":"chat-model","supported_endpoints":["/v1/messages","/responses","/chat/completions"]},
			{"id":"response-model","supported_endpoints":["/v1/messages","/responses"]},
			{"id":"anthropic-model","supported_endpoints":["/v1/messages"]}
		]}`)
	})
	catalog, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog.Models {
		t.Run(entry.ID, func(t *testing.T) {
			model := providerModelFromCatalog(provider.ID, entry)
			fields := map[string]string{
				"max_tokens": "128", "max_completion_tokens": "128", "temperature": "0.2", "top_p": "0.8",
				"tools":       `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`,
				"tool_choice": `"auto"`, "parallel_tool_calls": "true", "reasoning_effort": `"high"`,
			}
			if entry.ID == "chat-model" || entry.ID == "anthropic-model" {
				fields["stop"] = `"END"`
			}
			if entry.ID == "chat-model" || entry.ID == "response-model" {
				fields["response_format"], fields["service_tier"] = `{"type":"json_object"}`, `"auto"`
			}
			if entry.ID == "chat-model" {
				fields["presence_penalty"], fields["frequency_penalty"] = "0.1", "0.2"
			}
			assertSubscriptionCatalogChatControls(t, model, fields)
			if entry.ID == "chat-model" || entry.ID == "response-model" {
				var request ResponsesRequest
				if err := json.Unmarshal([]byte(`{"model":"public","input":"Hello","max_output_tokens":128,"text":{"format":{"type":"json_object"}},"reasoning":{"effort":"high"},"include":["reasoning.encrypted_content"],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`), &request); err != nil {
					t.Fatal(err)
				}
				if result := jevResponsesCompatibility(model, request); result.Reason != "" {
					t.Fatalf("native Responses controls rejected: %+v", result)
				}
			}
			if entry.ID == "auto" || entry.ID == "response-model" {
				if slices.Contains(model.SupportedParameters, "stop") || slices.Contains(model.SupportedParameters, "presence_penalty") {
					t.Fatal("catalog advertised a control that its Responses selection drops")
				}
			}
		})
	}
}

func assertSubscriptionCatalogChatControls(t *testing.T, model ProviderModel, fields map[string]string) {
	t.Helper()
	if model.Metadata["request_parameter_profile"] != "" {
		t.Fatal("subscription admission must not use a blanket parameter profile")
	}
	for field, value := range fields {
		var request ChatCompletionRequest
		body := `{"model":"public","messages":[{"role":"user","content":"Hello"}],"` + field + `":` + value + `}`
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatal(err)
		}
		if result := jevChatCompatibility(model, request); result.Reason != "" {
			t.Errorf("supported %s rejected after catalog import: %+v", field, result)
		}
		if !semanticModelFitsRequest(model, request, 5) {
			t.Errorf("supported %s rejected by legacy semantic routing", field)
		}
	}
}

func TestSubscriptionCatalogControlsReachProtocolConverters(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			var request ChatCompletionRequest
			if err := json.Unmarshal([]byte(`{"model":"gemini-3-flash","messages":[{"role":"user","content":"Hello"}],"max_completion_tokens":128,"temperature":0.2,"top_p":0.8,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":"auto","reasoning_effort":"high"}`), &request); err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			var err error
			switch protocol {
			case "chat", "responses":
				var converted any = withoutGatewayExtensions(request, true)
				if protocol == "responses" {
					converted, err = chatToSubscriptionResponsesRequest(request)
				}
				if err == nil {
					var raw []byte
					raw, err = json.Marshal(converted)
					if err == nil {
						err = json.Unmarshal(raw, &body)
					}
				}
			case "anthropic":
				body, err = buildAnthropicRequest("claude-sonnet", request, request.ReasoningEffort)
			case "gemini":
				body, err = buildGeminiRequest(request.Model, request)
			}
			if err != nil {
				t.Fatal(err)
			}
			if body["tools"] == nil {
				t.Fatal("declared tools were dropped by protocol conversion")
			}
			var budget, reasoning any
			switch protocol {
			case "chat":
				budget, reasoning = body["max_completion_tokens"], body["reasoning_effort"]
			case "responses":
				budget, reasoning = body["max_output_tokens"], body["reasoning"].(map[string]any)["effort"]
			case "anthropic":
				budget, reasoning = body["max_tokens"], body["output_config"].(map[string]any)["effort"]
			case "gemini":
				body = body["generationConfig"].(map[string]any)
				budget, reasoning = body["maxOutputTokens"], body["thinkingConfig"].(map[string]any)["thinkingLevel"]
			}
			topP := body["top_p"]
			if protocol == "gemini" {
				topP = body["topP"]
			}
			if int64FromAny(budget) != 128 || body["temperature"] != 0.2 || topP != 0.8 || reasoning != "high" {
				t.Fatalf("declared budget, sampling or reasoning was lost: %v", body)
			}
		})
	}
}
