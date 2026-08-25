package server

import (
	"context"
	"encoding/json"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

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
