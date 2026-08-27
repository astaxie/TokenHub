package server

import (
	"context"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRequestTransformHookRequiresMatchingRouteScope(t *testing.T) {
	server := New(NewMemoryStore())
	imageHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "image-only",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      1000,
		Subject:       "transform_capture",
		Metadata:      map[string]string{"protocol": providerRouteProtocolImageGeneration},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	chatHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "chat-only",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		Subject:       "transform_capture",
		Metadata:      map[string]string{"route_protocol": providerRouteProtocolChatCompletions},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{imageHook, chatHook} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register request transform hook: %v", err)
		}
	}
	imageCalled := false
	if err := server.gatewayHooks.RegisterHandler(imageHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		imageCalled = true
		return rawProviderRequestPatch(t, map[string]any{
			"model":    "gpt-test",
			"messages": []map[string]any{{"role": "user", "content": "[wrong-scope]"}},
		}), nil
	})); err != nil {
		t.Fatalf("register image request transform handler: %v", err)
	}
	chatCalled := false
	if err := server.gatewayHooks.RegisterHandler(chatHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		chatCalled = true
		return rawProviderRequestPatch(t, map[string]any{
			"model":    "gpt-test",
			"messages": []map[string]any{{"role": "user", "content": "[chat-scoped]"}},
		}), nil
	})); err != nil {
		t.Fatalf("register chat request transform handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "original"}},
	}
	err := server.runGatewayChatRequestTransformHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{
		Provider: Provider{ID: "prv_transform", Type: "transform_capture"},
	}, &request)
	if err != nil {
		t.Fatalf("run request transform hook: %v", err)
	}
	if imageCalled || !chatCalled {
		t.Fatalf("request transform route scope called image=%t chat=%t", imageCalled, chatCalled)
	}
	if got := request.Messages[0].Content; got != "[chat-scoped]" {
		t.Fatalf("message content = %v, want chat-scoped", got)
	}
}
