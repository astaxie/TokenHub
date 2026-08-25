package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestPrivacyPreHookCanRewriteChatRequestBody(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy",
		HookID:        "mask",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataRequestHeaders},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("request body was not available in the hook envelope")
		}
		var headers map[string][]string
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestHeaders], &headers); err != nil {
			t.Fatalf("decode headers: %v", err)
		}
		if got := headers["Authorization"]; len(got) != 1 || got[0] != "[redacted]" {
			t.Fatalf("authorization header = %v, want redacted", got)
		}
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[masked]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "secret"}},
	}
	err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{
		"Authorization": []string{"Bearer thk_secret"},
	}, request, func(data json.RawMessage) error {
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(request.Model, request.Stream, patched.Model, patched.Stream); err != nil {
			return err
		}
		request = patched
		return nil
	})
	if err != nil {
		t.Fatalf("run privacy hook: %v", err)
	}
	if got := request.Messages[0].Content; got != "[masked]" {
		t.Fatalf("message content = %v, want masked", got)
	}
}

func TestPrivacyPreHookCannotChangeRequestedModel(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy",
		HookID:        "reroute",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-other",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "secret"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "secret"}},
	}
	err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, request, func(data json.RawMessage) error {
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		return validateGatewayHookRequestInvariant(request.Model, request.Stream, patched.Model, patched.Stream)
	})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_patch_invalid" {
		t.Fatalf("privacy hook error = %d/%s, want 502/gateway_hook_patch_invalid", httpErr.Status, httpErr.Code)
	}
}

func TestPrivacyPreHookDenyReturnsForbidden(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy",
		HookID:        "deny",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, func(json.RawMessage) error {
		t.Fatal("deny decision should not apply a request patch")
		return nil
	})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusForbidden || httpErr.Code != "gateway_hook_denied" {
		t.Fatalf("privacy hook error = %d/%s, want 403/gateway_hook_denied", httpErr.Status, httpErr.Code)
	}
}

func TestCacheLookupHookCanShortCircuitWithProviderResponse(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "lookup",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyReturnFallback,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("request body was not available to cache lookup")
		}
		response, err := json.Marshal(map[string]any{"id": "cached_response"})
		if err != nil {
			t.Fatal(err)
		}
		usage, err := json.Marshal(Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9})
		if err != nil {
			t.Fatal(err)
		}
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionShortCircuit,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataProviderResponse: {Value: response},
				pluginmeta.DataUsage:            {Value: usage},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	response, usage, hit, err := server.runGatewayCacheLookupHooks(context.Background(), gatewayPluginTestCall(), ChatCompletionRequest{Model: "gpt-test"})
	if err != nil {
		t.Fatalf("run cache lookup hook: %v", err)
	}
	if !hit {
		t.Fatal("cache lookup did not report a hit")
	}
	body, ok := response.(map[string]any)
	if !ok || body["id"] != "cached_response" {
		t.Fatalf("cache response = %#v, want cached_response", response)
	}
	if usage.TotalTokens != 9 {
		t.Fatalf("cache usage = %+v, want total tokens 9", usage)
	}
}

func TestCacheLookupShortCircuitRequiresProviderResponse(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "empty",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyReturnFallback,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit}, nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	_, _, hit, err := server.runGatewayCacheLookupHooks(context.Background(), gatewayPluginTestCall(), ChatCompletionRequest{Model: "gpt-test"})
	if hit {
		t.Fatal("invalid cache lookup reported a hit")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_cache_hit_invalid" {
		t.Fatalf("cache lookup error = %d/%s, want 502/gateway_hook_cache_hit_invalid", httpErr.Status, httpErr.Code)
	}
}

func TestCacheWriteHookReceivesRequestResponseAndUsage(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "write",
		Stage:         pluginmeta.StageCacheWrite,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache write hook: %v", err)
	}
	var sawRequest, sawResponse, sawUsage bool
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		sawRequest = len(input.Data[pluginmeta.DataRequestBody]) > 0
		sawResponse = len(input.Data[pluginmeta.DataProviderResponse]) > 0
		sawUsage = len(input.Data[pluginmeta.DataUsage]) > 0
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register cache write handler: %v", err)
	}

	server.runGatewayCacheWriteHooks(context.Background(), gatewayPluginTestCall(), ChatCompletionRequest{Model: "gpt-test"}, map[string]any{"id": "provider_response"}, Usage{TotalTokens: 7})
	if !sawRequest || !sawResponse || !sawUsage {
		t.Fatalf("cache write saw request=%v response=%v usage=%v, want all true", sawRequest, sawResponse, sawUsage)
	}
}

func TestRouteRankHookCanReorderCoreApprovedCandidates(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "reverse",
		Stage:         pluginmeta.StageRouteRank,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route rank hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if _, ok := input.Data[pluginmeta.DataRouteCandidates]; !ok {
			t.Fatal("route candidates were not available to the hook")
		}
		return routeRankPatchResult(t, "route_b", "route_a"), nil
	})); err != nil {
		t.Fatalf("register route rank handler: %v", err)
	}

	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_a", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
		{Route: ModelRoute{ID: "route_b", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
	}
	planned := server.planRouteOrder(CallContext{RequestID: "req_route_rank"}, routes)
	if got := routeIDs(planned); len(got) != 2 || got[0] != "route_b" || got[1] != "route_a" {
		t.Fatalf("planned route IDs = %v, want [route_b route_a]", got)
	}
}

func TestRouteRankHookCannotAddUnapprovedCandidates(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "unsafe",
		Stage:         pluginmeta.StageRouteRank,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route rank hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return routeRankPatchResult(t, "route_b", "route_unapproved"), nil
	})); err != nil {
		t.Fatalf("register route rank handler: %v", err)
	}

	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_a", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
		{Route: ModelRoute{ID: "route_b", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
	}
	planned := server.planRouteOrder(CallContext{RequestID: "req_route_rank_unsafe"}, routes)
	if got := routeIDs(planned); len(got) != 2 || got[0] != "route_a" || got[1] != "route_b" {
		t.Fatalf("planned route IDs = %v, want safe baseline [route_a route_b]", got)
	}
}

func rawRequestBodyPatch(t *testing.T, value any) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataRequestBody: {Value: data},
		},
	}
}

func routeRankPatchResult(t *testing.T, routeIDs ...string) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(map[string]any{"route_ids": routeIDs})
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataRouteCandidates: {Value: data},
		},
	}
}

func routeIDs(routes []RouteSelection) []string {
	ids := make([]string, 0, len(routes))
	for _, route := range routes {
		ids = append(ids, route.Route.ID)
	}
	return ids
}

func gatewayPluginTestCall() CallContext {
	return CallContext{
		RequestID: "req_plugin_test",
		Project:   Project{ID: "proj_plugin_test", Status: StatusActive},
		Key:       APIKey{ID: "key_plugin_test", ProjectID: "proj_plugin_test", Status: StatusActive},
		Model:     Model{Name: "gpt-test"},
	}
}
