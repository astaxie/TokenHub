package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestPlaygroundChatStreamRunsPrivacyAndContextOptimizeHooks(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	privacyHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-playground",
		HookID:        "privacy",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	contextHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-playground",
		HookID:        "context",
		Stage:         pluginmeta.StageContextOptimize,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{privacyHook, contextHook} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register %s hook: %v", hook.HookID, err)
		}
	}
	if err := server.gatewayHooks.RegisterHandler(privacyHook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var req ChatCompletionRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &req); err != nil {
			t.Fatalf("decode privacy request: %v", err)
		}
		req.Messages[0].Content = strings.Replace(req.Messages[0].Content.(string), "secret", "masked", 1)
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionContinue,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataRequestBody: {Value: body},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}
	contextSawMasked := false
	if err := server.gatewayHooks.RegisterHandler(contextHook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var req ChatCompletionRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &req); err != nil {
			t.Fatalf("decode context request: %v", err)
		}
		contextSawMasked = strings.Contains(req.Messages[0].Content.(string), "masked")
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register context handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id": defaultProjectID,
		"model":      "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "secret prompt"},
		},
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !contextSawMasked {
		t.Fatal("context optimize hook did not receive the privacy-redacted request")
	}
	if !strings.Contains(resp.Body, "Echo: masked prompt") || strings.Contains(resp.Body, "secret prompt") {
		t.Fatalf("playground stream did not use the privacy-redacted request: %s", resp.Body)
	}
}

func TestPlaygroundRoutedCallRunsRouteCandidateHooks(t *testing.T) {
	store := NewMemoryStore()
	store.AddProvider(Provider{ID: "prv_a", Name: "Provider A", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddProvider(Provider{ID: "prv_b", Name: "Provider B", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{ID: "route_a", ModelName: "gpt-playground-candidates", ProviderID: "prv_a", ProviderModel: "mock-a", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	store.AddRoute(ModelRoute{ID: "route_b", ModelName: "gpt-playground-candidates", ProviderID: "prv_b", ProviderModel: "mock-b", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-playground-router",
		HookID:        "select-b",
		Stage:         pluginmeta.StageRouteCandidates,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route candidates hook: %v", err)
	}
	sawCandidates := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var candidates []gatewayRouteCandidateView
		if err := json.Unmarshal(input.Data[pluginmeta.DataRouteCandidates], &candidates); err != nil {
			t.Fatalf("decode route candidates: %v", err)
		}
		sawCandidates = len(candidates) == 2
		return routeRankPatchResult(t, "route_b"), nil
	})); err != nil {
		t.Fatalf("register route candidates handler: %v", err)
	}

	routed, err := server.preparePlaygroundRoutedCall(context.Background(), ChatCompletionRequest{Model: "gpt-playground-candidates"}, server.newPlaygroundCallContext(AdminUser{ID: "dev_admin"}, "gpt-playground-candidates", time.Now()))
	if err != nil {
		t.Fatalf("prepare playground routed call: %v", err)
	}
	if !sawCandidates {
		t.Fatal("route candidates hook did not receive playground candidates")
	}
	if got := routeIDs(routed.Routes); len(got) != 1 || got[0] != "route_b" {
		t.Fatalf("playground route IDs = %v, want [route_b]", got)
	}
}
