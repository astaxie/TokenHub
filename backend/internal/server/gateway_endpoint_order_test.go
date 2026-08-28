package server

import (
	"context"
	"net/http"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGatewayCacheHitEndpointsRunCanonicalStageOrder(t *testing.T) {
	tests := []struct {
		name          string
		send          func(t *testing.T, handler http.Handler) responseBody
		cacheResponse any
	}{
		{
			name: "chat completions",
			send: func(t *testing.T, handler http.Handler) responseBody {
				return doJSON(t, handler, http.MethodPost, "/v1/chat/completions", map[string]any{
					"model": "gpt-4.1-mini",
					"messages": []map[string]any{
						{"role": "user", "content": "hello"},
					},
				}, "thk_demo_local")
			},
			cacheResponse: map[string]any{"id": "chat_cached", "object": "chat.completion", "choices": []map[string]any{}},
		},
		{
			name: "responses",
			send: func(t *testing.T, handler http.Handler) responseBody {
				return doJSON(t, handler, http.MethodPost, "/v1/responses", map[string]any{
					"model": "gpt-4.1-mini",
					"input": "hello",
				}, "thk_demo_local")
			},
			cacheResponse: map[string]any{"id": "resp_cached", "object": "response", "output_text": "cached"},
		},
		{
			name: "responses compact",
			send: func(t *testing.T, handler http.Handler) responseBody {
				return doJSON(t, handler, http.MethodPost, "/v1/responses/compact", map[string]any{
					"model": "gpt-4.1-mini",
					"input": "hello",
				}, "thk_demo_local")
			},
			cacheResponse: map[string]any{"id": "compact_cached", "object": "response", "output_text": "cached"},
		},
		{
			name: "embeddings",
			send: func(t *testing.T, handler http.Handler) responseBody {
				return doJSON(t, handler, http.MethodPost, "/v1/embeddings", map[string]any{
					"model": "text-embedding-3-small",
					"input": "hello",
				}, "thk_demo_local")
			},
			cacheResponse: map[string]any{
				"object": "list",
				"model":  "text-embedding-3-small",
				"data": []map[string]any{
					{"object": "embedding", "index": 0, "embedding": []float64{0.1, 0.2}},
				},
			},
		},
		{
			name: "gemini native",
			send: func(t *testing.T, handler http.Handler) responseBody {
				return doGeminiJSON(t, handler, http.MethodPost, "/v1beta/models/gpt-4.1-mini:generateContent", map[string]any{
					"contents": []map[string]any{
						{"role": "user", "parts": []map[string]any{{"text": "hello"}}},
					},
				}, "thk_demo_local")
			},
			cacheResponse: map[string]any{
				"candidates": []map[string]any{
					{"content": map[string]any{"parts": []map[string]any{{"text": "cached"}}}},
				},
			},
		},
		{
			name: "anthropic messages",
			send: func(t *testing.T, handler http.Handler) responseBody {
				resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
					"model":      "gpt-4.1-mini",
					"max_tokens": 8,
					"messages":   []map[string]any{{"role": "user", "content": "hello"}},
				}, "", "thk_demo_local")
				return responseBody{Code: resp.Code, Header: resp.Header(), Body: resp.Body.String()}
			},
			cacheResponse: map[string]any{
				"id":      "msg_cached",
				"type":    "message",
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "cached"}},
			},
		},
	}

	want := []pluginmeta.GatewayHookStage{
		pluginmeta.StageAuthContext,
		pluginmeta.StageDecodeNormalize,
		pluginmeta.StageAdmission,
		pluginmeta.StagePrivacyPre,
		pluginmeta.StageGuardrailPre,
		pluginmeta.StageContextOptimize,
		pluginmeta.StageCacheLookup,
		pluginmeta.StageResponsePost,
		pluginmeta.StageGuardrailPost,
		pluginmeta.StageUsageAttribution,
		pluginmeta.StageTraceExport,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newGatewayEndpointOrderTestServer(t)
			got := recordGatewayEndpointOrderStages(t, server, tt.cacheResponse)

			resp := tt.send(t, server.Handler())
			if resp.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
			}
			if !serverGatewayStageSlicesEqual(got(), want) {
				t.Fatalf("gateway stages = %v, want %v", got(), want)
			}
		})
	}
}

func TestGatewayChatRoutePathRunsCanonicalStageOrder(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Gateway Endpoint Order", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "endpoint-order-key",
		Allowed: []string{"gpt-endpoint-order"},
		Status:  StatusActive,
	}, "thk_endpoint_order")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-endpoint-order", Modality: "chat", Status: StatusActive})
	for _, route := range []struct {
		providerID string
		routeID    string
		priority   int
	}{
		{providerID: "prv_endpoint_order_a", routeID: "route_endpoint_order_a", priority: 1},
		{providerID: "prv_endpoint_order_b", routeID: "route_endpoint_order_b", priority: 2},
	} {
		store.AddProvider(Provider{ID: route.providerID, Name: route.providerID, Type: ProviderMock, Status: StatusActive, Healthy: true})
		store.AddRoute(ModelRoute{
			ID:            route.routeID,
			ModelName:     "gpt-endpoint-order",
			ProviderID:    route.providerID,
			ProviderModel: "mock-chat",
			Status:        StatusActive,
			Priority:      route.priority,
			Weight:        100,
		})
	}
	server := New(store)
	got := recordGatewayRoutePathStages(t, server, map[string]any{
		"id":      "chat_route_plugin",
		"object":  "chat.completion",
		"choices": []map[string]any{},
	})

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-endpoint-order",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}

	want := []pluginmeta.GatewayHookStage{
		pluginmeta.StageAuthContext,
		pluginmeta.StageDecodeNormalize,
		pluginmeta.StageAdmission,
		pluginmeta.StagePrivacyPre,
		pluginmeta.StageGuardrailPre,
		pluginmeta.StageContextOptimize,
		pluginmeta.StageCacheLookup,
		pluginmeta.StageRouteCandidates,
		pluginmeta.StageRouteRank,
		pluginmeta.StageRequestTransform,
		pluginmeta.StageProviderCall,
		pluginmeta.StageResponsePost,
		pluginmeta.StageGuardrailPost,
		pluginmeta.StageUsageAttribution,
		pluginmeta.StageCacheWrite,
		pluginmeta.StageTraceExport,
	}
	if !serverGatewayStageSlicesEqual(got(), want) {
		t.Fatalf("gateway stages = %v, want %v", got(), want)
	}
}

func newGatewayEndpointOrderTestServer(t *testing.T) *Server {
	t.Helper()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	return New(store)
}

func recordGatewayEndpointOrderStages(t *testing.T, server *Server, cacheResponse any) func() []pluginmeta.GatewayHookStage {
	t.Helper()
	stages := []pluginmeta.GatewayHookStage{}
	record := func(stage pluginmeta.GatewayHookStage) {
		stages = append(stages, stage)
	}
	for _, stage := range []pluginmeta.GatewayHookStage{
		pluginmeta.StageAuthContext,
		pluginmeta.StageDecodeNormalize,
		pluginmeta.StageAdmission,
		pluginmeta.StagePrivacyPre,
		pluginmeta.StageGuardrailPre,
		pluginmeta.StageContextOptimize,
		pluginmeta.StageCacheLookup,
		pluginmeta.StageResponsePost,
		pluginmeta.StageGuardrailPost,
		pluginmeta.StageUsageAttribution,
		pluginmeta.StageTraceExport,
	} {
		hook := pluginmeta.GatewayHookDescriptor{
			PluginID: "tokenhub.test-endpoint-order",
			HookID:   string(stage),
			Stage:    stage,
			Priority: 1000,
		}
		switch stage {
		case pluginmeta.StageCacheLookup:
			hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}
			hook.FailurePolicy = pluginmeta.FailurePolicyFailOpen
		case pluginmeta.StageTraceExport:
			hook.FailurePolicy = pluginmeta.FailurePolicyObserveOnly
		default:
			hook.FailurePolicy = pluginmeta.FailurePolicyFailClosed
		}
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register %s hook: %v", stage, err)
		}
		currentHook := hook
		if err := server.gatewayHooks.RegisterHandler(currentHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			record(currentHook.Stage)
			if currentHook.Stage == pluginmeta.StageCacheLookup {
				return rawProviderCallResult(t, cacheResponse, Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
			}
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		})); err != nil {
			t.Fatalf("register %s handler: %v", stage, err)
		}
	}
	return func() []pluginmeta.GatewayHookStage {
		return append([]pluginmeta.GatewayHookStage(nil), stages...)
	}
}

func recordGatewayRoutePathStages(t *testing.T, server *Server, providerResponse any) func() []pluginmeta.GatewayHookStage {
	t.Helper()
	stages := []pluginmeta.GatewayHookStage{}
	record := func(stage pluginmeta.GatewayHookStage) {
		stages = append(stages, stage)
	}
	for _, stage := range []pluginmeta.GatewayHookStage{
		pluginmeta.StageAuthContext,
		pluginmeta.StageDecodeNormalize,
		pluginmeta.StageAdmission,
		pluginmeta.StagePrivacyPre,
		pluginmeta.StageGuardrailPre,
		pluginmeta.StageContextOptimize,
		pluginmeta.StageCacheLookup,
		pluginmeta.StageRouteCandidates,
		pluginmeta.StageRouteRank,
		pluginmeta.StageRequestTransform,
		pluginmeta.StageProviderCall,
		pluginmeta.StageResponsePost,
		pluginmeta.StageGuardrailPost,
		pluginmeta.StageUsageAttribution,
		pluginmeta.StageCacheWrite,
		pluginmeta.StageTraceExport,
	} {
		hook := pluginmeta.GatewayHookDescriptor{
			PluginID:      "tokenhub.test-route-order",
			HookID:        string(stage),
			Stage:         stage,
			Priority:      1000,
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
		}
		switch stage {
		case pluginmeta.StageCacheLookup:
			hook.FailurePolicy = pluginmeta.FailurePolicyFailOpen
		case pluginmeta.StageRouteRank:
			hook.FailurePolicy = pluginmeta.FailurePolicyFailOpen
		case pluginmeta.StageProviderCall:
			hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}
			hook.FailurePolicy = pluginmeta.FailurePolicySkipRoute
		case pluginmeta.StageCacheWrite:
			hook.FailurePolicy = pluginmeta.FailurePolicyFailOpen
		case pluginmeta.StageTraceExport:
			hook.FailurePolicy = pluginmeta.FailurePolicyObserveOnly
		}
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register %s hook: %v", stage, err)
		}
		currentHook := hook
		if err := server.gatewayHooks.RegisterHandler(currentHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			record(currentHook.Stage)
			if currentHook.Stage == pluginmeta.StageProviderCall {
				return rawProviderCallResult(t, providerResponse, Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
			}
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		})); err != nil {
			t.Fatalf("register %s handler: %v", stage, err)
		}
	}
	return func() []pluginmeta.GatewayHookStage {
		return append([]pluginmeta.GatewayHookStage(nil), stages...)
	}
}

func serverGatewayStageSlicesEqual(a []pluginmeta.GatewayHookStage, b []pluginmeta.GatewayHookStage) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
