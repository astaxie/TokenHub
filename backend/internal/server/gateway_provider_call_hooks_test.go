package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestProviderCallHookCanShortCircuitProviderInvocation(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-provider",
		HookID:        "invoke",
		Stage:         pluginmeta.StageProviderCall,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderCredentials, pluginmeta.DataProviderRequest, pluginmeta.DataRouteCandidates},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicySkipRoute,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register provider call hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("provider request was not available in the hook envelope")
		}
		if _, ok := input.Data[pluginmeta.DataProviderCredentials]; !ok {
			t.Fatal("provider credentials were not available to provider call hook")
		}
		if _, ok := input.Data[pluginmeta.DataRouteCandidates]; !ok {
			t.Fatal("route candidate was not available to provider call hook")
		}
		return rawProviderCallResult(t, map[string]any{"id": "plugin_response"}, Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
	})); err != nil {
		t.Fatalf("register provider call handler: %v", err)
	}

	response, usage, handled, err := server.runGatewayProviderCallHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{
		Route:    ModelRoute{ID: "route_provider_plugin"},
		Provider: Provider{ID: "prv_provider_plugin", Type: "provider_plugin"},
	}, ChatCompletionRequest{Model: "gpt-test"}, providerRouteProtocolChatCompletions)
	if err != nil {
		t.Fatalf("run provider call hook: %v", err)
	}
	if !handled {
		t.Fatal("provider call hook did not short-circuit")
	}
	body, ok := response.(map[string]any)
	if !ok || body["id"] != "plugin_response" {
		t.Fatalf("provider response = %#v, want plugin_response", response)
	}
	if usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v, want 5 total tokens", usage)
	}
}

func TestProviderCallHookRequiresMatchingProtocolMetadata(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-provider",
		HookID:        "image-only",
		Stage:         pluginmeta.StageProviderCall,
		Priority:      2000,
		Metadata:      map[string]string{"protocol": providerRouteProtocolImageGeneration},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicySkipRoute,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register provider call hook: %v", err)
	}
	called := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called = true
		return rawProviderCallResult(t, map[string]any{"id": "wrong_protocol"}, Usage{}), nil
	})); err != nil {
		t.Fatalf("register provider call handler: %v", err)
	}

	_, _, handled, err := server.runGatewayProviderCallHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{
		Route:    ModelRoute{ID: "route_provider_plugin"},
		Provider: Provider{ID: "prv_provider_plugin", Type: "provider_plugin"},
	}, ChatCompletionRequest{Model: "gpt-test"}, providerRouteProtocolChatCompletions)
	if err != nil {
		t.Fatalf("run provider call hook: %v", err)
	}
	if handled || called {
		t.Fatalf("protocol-scoped provider_call hook handled=%t called=%t for chat protocol", handled, called)
	}
	_, _, handled, err = server.runGatewayProviderCallHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{
		Route:    ModelRoute{ID: "route_provider_plugin"},
		Provider: Provider{ID: "prv_provider_plugin", Type: "provider_plugin"},
	}, ChatCompletionRequest{Model: "gpt-test"}, providerRouteProtocolImageGeneration)
	if err != nil {
		t.Fatalf("run matching provider call hook: %v", err)
	}
	if !handled || !called {
		t.Fatalf("protocol-scoped provider_call hook handled=%t called=%t for image protocol", handled, called)
	}
}

func TestProviderCallHookServesGatewayChatWithoutBuiltinAdapter(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Provider Plugin App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "provider-plugin-key",
		Allowed: []string{"gpt-provider-plugin"},
		Status:  StatusActive,
	}, "thk_provider_plugin")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_provider_plugin", Name: "Provider Plugin", Type: "third_party_provider_plugin", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-provider-plugin", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_provider_plugin", ModelName: "gpt-provider-plugin", ProviderID: provider.ID, ProviderModel: "upstream-plugin", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-provider",
		HookID:        "chat",
		Stage:         pluginmeta.StageProviderCall,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicySkipRoute,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register provider call hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderCallResult(t, map[string]any{
			"id":      "chat_plugin_provider",
			"object":  "chat.completion",
			"created": float64(1),
			"model":   "gpt-provider-plugin",
			"choices": []map[string]any{{
				"index": float64(0),
				"message": map[string]any{
					"role":    "assistant",
					"content": "[served-by-provider-plugin]",
				},
				"finish_reason": "stop",
			}},
		}, Usage{PromptTokens: 4, CompletionTokens: 6, TotalTokens: 10}), nil
	})); err != nil {
		t.Fatalf("register provider call handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-provider-plugin",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "[served-by-provider-plugin]") || !strings.Contains(resp.Body, "chat_plugin_provider") {
		t.Fatalf("response body was not served by provider plugin: %s", resp.Body)
	}
}
