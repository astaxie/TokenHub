package server

import (
	"context"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGatewayRouteHooksForRouteFiltersRouteScopedStages(t *testing.T) {
	server := New(NewMemoryStore())
	stages := []pluginmeta.GatewayHookStage{
		pluginmeta.StageRequestTransform,
		pluginmeta.StageStreamTransform,
		pluginmeta.StageResponsePost,
		pluginmeta.StageGuardrailPost,
		pluginmeta.StageCacheWrite,
		pluginmeta.StageUsageAttribution,
	}
	for _, stage := range stages {
		imageHook := pluginmeta.GatewayHookDescriptor{
			PluginID: "tokenhub.test-scope",
			HookID:   string(stage) + "-image",
			Stage:    stage,
			Priority: 1000,
			Subject:  "transform_capture",
			Metadata: map[string]string{"protocol": providerRouteProtocolImageGeneration},
		}
		chatHook := pluginmeta.GatewayHookDescriptor{
			PluginID: "tokenhub.test-scope",
			HookID:   string(stage) + "-chat",
			Stage:    stage,
			Priority: 2000,
			Subject:  "transform_capture",
			Metadata: map[string]string{"route_protocol": providerRouteProtocolChatCompletions},
		}
		for _, hook := range []pluginmeta.GatewayHookDescriptor{imageHook, chatHook} {
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatalf("register %s hook %s: %v", stage, hook.HookID, err)
			}
		}

		hooks := server.gatewayRouteHooksForRoute(stage, RouteSelection{
			Provider: Provider{ID: "prv_transform", Type: "transform_capture"},
		}, providerRouteProtocolChatCompletions, true)
		pluginHooks := make([]pluginmeta.GatewayHookDescriptor, 0, len(hooks))
		for _, hook := range hooks {
			if hook.PluginID != tokenHubCoreGatewayChainPluginID {
				pluginHooks = append(pluginHooks, hook)
			}
		}
		if len(pluginHooks) != 1 || pluginHooks[0].HookID != chatHook.HookID {
			t.Fatalf("%s scoped plugin hooks = %#v, want only %q", stage, pluginHooks, chatHook.HookID)
		}
	}
}

func TestGatewayRouteHooksForRouteFiltersExplicitProviderScope(t *testing.T) {
	server := New(NewMemoryStore())
	matching := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-scope",
		HookID:   "matching",
		Stage:    pluginmeta.StageRequestTransform,
		Priority: 1000,
		Scope: pluginmeta.GatewayHookScope{
			ProviderTypes:  []string{"transform_capture"},
			ProviderIDs:    []string{"prv_transform"},
			ResourceIDs:    []string{"res_transform"},
			ResourceTypes:  []string{"subscription"},
			RouteProtocols: []string{providerRouteProtocolChatCompletions},
		},
	}
	wrongProvider := matching
	wrongProvider.HookID = "wrong-provider"
	wrongProvider.Scope.ProviderIDs = []string{"prv_other"}
	wrongResource := matching
	wrongResource.HookID = "wrong-resource"
	wrongResource.Scope.ResourceTypes = []string{"account"}
	wrongProtocol := matching
	wrongProtocol.HookID = "wrong-protocol"
	wrongProtocol.Scope.RouteProtocols = []string{providerRouteProtocolImageGeneration}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{wrongProtocol, matching, wrongResource, wrongProvider} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register request transform hook %s: %v", hook.HookID, err)
		}
	}

	hooks := server.gatewayRouteHooksForRoute(pluginmeta.StageRequestTransform, RouteSelection{
		Provider: Provider{ID: "prv_transform", Type: "transform_capture"},
		Resource: &ProviderResource{ID: "res_transform", ResourceType: "subscription"},
	}, providerRouteProtocolChatCompletions, true)
	pluginHooks := make([]pluginmeta.GatewayHookDescriptor, 0, len(hooks))
	for _, hook := range hooks {
		if hook.PluginID != tokenHubCoreGatewayChainPluginID {
			pluginHooks = append(pluginHooks, hook)
		}
	}
	if len(pluginHooks) != 1 || pluginHooks[0].HookID != matching.HookID {
		t.Fatalf("explicit provider scope hooks = %#v, want only %q", pluginHooks, matching.HookID)
	}
}

func TestRouteScopedResponsePostHookExecutorSkipsWrongProtocol(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-scope",
		HookID:        "image-post",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Subject:       "transform_capture",
		Metadata:      map[string]string{"protocol": providerRouteProtocolImageGeneration},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register response post hook: %v", err)
	}
	called := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called = true
		return rawProviderResponsePatch(t, map[string]any{"id": "wrong-protocol"}), nil
	})); err != nil {
		t.Fatalf("register response post handler: %v", err)
	}

	response, err := server.runGatewayResponsePostHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{
		Provider: Provider{ID: "prv_transform", Type: "transform_capture"},
	}, map[string]any{"id": "upstream"}, providerRouteProtocolChatCompletions)
	if err != nil {
		t.Fatalf("run response post hook: %v", err)
	}
	body, ok := response.(map[string]any)
	if called || !ok || body["id"] != "upstream" {
		t.Fatalf("response post scope called=%t response=%#v, want unchanged upstream", called, response)
	}
}
