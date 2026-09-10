package server

import (
	"context"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRouteRankHookUsesRequestContext(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "ctx-aware-rank",
		Stage:         pluginmeta.StageRouteRank,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route rank hook: %v", err)
	}
	sawCancelledContext := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(ctx context.Context, _ pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		select {
		case <-ctx.Done():
			sawCancelledContext = true
		default:
		}
		return routeRankPatchResult(t, "route_b", "route_a"), nil
	})); err != nil {
		t.Fatalf("register route rank handler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_a", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
		{Route: ModelRoute{ID: "route_b", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
	}
	planned := server.planRouteOrderWithContext(ctx, CallContext{RequestID: "req_route_rank_ctx"}, routes)
	if !sawCancelledContext {
		t.Fatal("route rank hook did not receive the request context")
	}
	if got := routeIDs(planned); len(got) != 2 || got[0] != "route_a" || got[1] != "route_b" {
		t.Fatalf("planned route IDs = %v, want fail-open baseline [route_a route_b]", got)
	}
}

func TestRouteRankHookRequiresChainRegistration(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "unregistered-rank",
		Stage:         pluginmeta.StageRouteRank,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	called := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called = true
		return routeRankPatchResult(t, "route_b", "route_a"), nil
	})); err != nil {
		t.Fatalf("register route rank handler: %v", err)
	}

	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_a", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
		{Route: ModelRoute{ID: "route_b", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
	}
	planned := server.planRouteOrderWithContext(context.Background(), CallContext{RequestID: "req_route_rank_unregistered"}, routes)
	if called {
		t.Fatal("route rank handler ran without chain registration")
	}
	if got := routeIDs(planned); len(got) != 2 || got[0] != "route_a" || got[1] != "route_b" {
		t.Fatalf("planned route IDs = %v, want manifest baseline [route_a route_b]", got)
	}
}
