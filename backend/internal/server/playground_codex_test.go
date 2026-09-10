package server

import "testing"

func TestPlaygroundResponsesRequestForRouteSkipsNonCodexProviders(t *testing.T) {
	request, useResponses, err := playgroundResponsesRequestForRoute(
		NewAdapterRegistry(),
		RouteSelection{Provider: Provider{Type: ProviderOpenAI}},
		ChatCompletionRequest{Messages: []ChatMessage{{Role: "tool", Content: "not valid for Codex"}}},
	)
	if err != nil || useResponses || request.Input != nil {
		t.Fatalf("non-Codex route unexpectedly converted request: request=%+v use_responses=%t err=%v", request, useResponses, err)
	}
}

func TestPlaygroundResponsesRequestForRouteUsesPluginProtocol(t *testing.T) {
	registry := NewAdapterRegistry()
	const providerType = "plugin_codex_responses"
	descriptor := providerPluginDescriptorWithRouteProtocol(
		"tokenhub.provider.playground-codex-responses",
		"Playground Codex Responses",
		providerType,
		AdapterCapabilityResponses,
		providerRouteProtocolCodexResponses,
	)
	if err := registry.RegisterPlugin(descriptor, AdapterRegistration{
		Type:         providerType,
		Adapter:      routeProtocolTestAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityResponses},
	}); err != nil {
		t.Fatalf("register plugin provider: %v", err)
	}

	request, useResponses, err := playgroundResponsesRequestForRoute(
		registry,
		RouteSelection{Provider: Provider{Type: providerType}},
		ChatCompletionRequest{Model: "plugin-model", Messages: []ChatMessage{{Role: "user", Content: "hello"}}},
	)
	if err != nil || !useResponses {
		t.Fatalf("plugin Codex protocol route was not converted: request=%+v use_responses=%t err=%v", request, useResponses, err)
	}
	input, ok := request.Input.([]map[string]any)
	if !ok || request.Model != "plugin-model" || len(input) != 1 {
		t.Fatalf("unexpected converted request: %+v", request)
	}
}

func TestPlaygroundChatResponsesRequestRejectsUnsupportedInstructionContent(t *testing.T) {
	_, err := playgroundChatResponsesRequest(ChatCompletionRequest{Messages: []ChatMessage{{
		Role: "system", Content: []any{playgroundImagePart("data:image/png;base64,YWJj")},
	}}})
	if got := AsHTTPError(err).Code; got != "unsupported_content_block" {
		t.Fatalf("error code = %q, want unsupported_content_block: %v", got, err)
	}
}

func TestPlaygroundChatResponsesRequestRejectsUnsupportedRole(t *testing.T) {
	_, err := playgroundChatResponsesRequest(ChatCompletionRequest{Messages: []ChatMessage{{Role: "tool", Content: "result"}}})
	if got := AsHTTPError(err).Code; got != "invalid_message" {
		t.Fatalf("error code = %q, want invalid_message: %v", got, err)
	}
}
