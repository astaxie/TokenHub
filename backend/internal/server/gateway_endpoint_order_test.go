package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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

func TestGatewayStreamingOpenAIEndpointsSkipCacheLookupAndWrite(t *testing.T) {
	tests := []struct {
		name string
		send func(t *testing.T, handler http.Handler) responseBody
	}{
		{
			name: "chat completions",
			send: func(t *testing.T, handler http.Handler) responseBody {
				return doJSON(t, handler, http.MethodPost, "/v1/chat/completions", map[string]any{
					"model":  "gpt-4.1-mini",
					"stream": true,
					"messages": []map[string]any{
						{"role": "user", "content": "hello"},
					},
				}, "thk_demo_local")
			},
		},
		{
			name: "responses",
			send: func(t *testing.T, handler http.Handler) responseBody {
				store := NewMemoryStore()
				project := store.CreateProject(Project{Name: "Streaming Cache Skip Responses", Status: StatusActive})
				_, secret, err := store.CreateAPIKey(project.ID, APIKey{
					Name:    "stream-cache-skip-responses-key",
					Allowed: []string{"gpt-stream-cache-skip"},
					Status:  StatusActive,
				}, "thk_stream_cache_skip_responses")
				if err != nil {
					t.Fatal(err)
				}
				provider := store.AddProvider(Provider{
					ID: "prv_stream_cache_skip", Name: "Streaming Cache Skip",
					Type: "stream_cache_skip_responses", Status: StatusActive, Healthy: true,
				})
				store.AddModel(Model{Name: "gpt-stream-cache-skip", Modality: "chat", Status: StatusActive})
				store.AddRoute(ModelRoute{
					ID: "route_stream_cache_skip", ModelName: "gpt-stream-cache-skip",
					ProviderID: provider.ID, ProviderModel: "upstream-responses", Status: StatusActive, Priority: 1, Weight: 100,
				})
				server := New(store)
				server.adapterRegistry.Register("stream_cache_skip_responses", responsesStreamTransformAdapter{}, AdapterCapabilityResponses, AdapterCapabilityResponseStream)
				registerUnexpectedCacheHooks(t, server)
				return doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
					"model":  "gpt-stream-cache-skip",
					"stream": true,
					"input":  "hello",
				}, secret)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response responseBody
			if tt.name == "chat completions" {
				server := newGatewayEndpointOrderTestServer(t)
				registerUnexpectedCacheHooks(t, server)
				response = tt.send(t, server.Handler())
			} else {
				response = tt.send(t, nil)
			}
			if response.Code != http.StatusOK {
				t.Fatalf("streaming endpoint failed: %d %s", response.Code, response.Body)
			}
			if !strings.Contains(response.Body, "Echo") {
				t.Fatalf("streaming response did not come from provider path: %s", response.Body)
			}
		})
	}
}

func TestGatewayCacheLookupFailOpenOpenAICompatibleEndpointsContinueToProvider(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		payload      map[string]any
		wantContains string
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			payload: map[string]any{
				"model": "gpt-4.1-mini",
				"messages": []map[string]any{
					{"role": "user", "content": "lookup fail-open chat"},
				},
			},
			wantContains: "Echo: lookup fail-open chat",
		},
		{
			name: "responses",
			path: "/v1/responses",
			payload: map[string]any{
				"model": "gpt-4.1-mini",
				"input": "lookup fail-open responses",
			},
			wantContains: "Echo: lookup fail-open responses",
		},
		{
			name: "embeddings",
			path: "/v1/embeddings",
			payload: map[string]any{
				"model": "text-embedding-3-small",
				"input": "lookup fail-open embeddings",
			},
			wantContains: `"object":"list"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newGatewayEndpointOrderTestServer(t)
			calls := registerFailingCacheHook(t, server, pluginmeta.StageCacheLookup)
			response := doJSON(t, server.Handler(), http.MethodPost, tt.path, tt.payload, "thk_demo_local")
			if response.Code != http.StatusOK {
				t.Fatalf("expected provider response after cache lookup fail-open, got %d: %s", response.Code, response.Body)
			}
			if !strings.Contains(response.Body, tt.wantContains) {
				t.Fatalf("provider response body = %s, want %q", response.Body, tt.wantContains)
			}
			if *calls != 1 {
				t.Fatalf("cache lookup hook calls = %d, want 1", *calls)
			}
		})
	}
}

func TestGatewayCacheWriteFailOpenOpenAICompatibleEndpointsPreserveProviderResponse(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		payload      map[string]any
		wantContains string
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			payload: map[string]any{
				"model": "gpt-4.1-mini",
				"messages": []map[string]any{
					{"role": "user", "content": "write fail-open chat"},
				},
			},
			wantContains: "Echo: write fail-open chat",
		},
		{
			name: "responses",
			path: "/v1/responses",
			payload: map[string]any{
				"model": "gpt-4.1-mini",
				"input": "write fail-open responses",
			},
			wantContains: "Echo: write fail-open responses",
		},
		{
			name: "embeddings",
			path: "/v1/embeddings",
			payload: map[string]any{
				"model": "text-embedding-3-small",
				"input": "write fail-open embeddings",
			},
			wantContains: `"object":"list"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newGatewayEndpointOrderTestServer(t)
			calls := registerFailingCacheHook(t, server, pluginmeta.StageCacheWrite)
			response := doJSON(t, server.Handler(), http.MethodPost, tt.path, tt.payload, "thk_demo_local")
			if response.Code != http.StatusOK {
				t.Fatalf("expected provider response after cache write fail-open, got %d: %s", response.Code, response.Body)
			}
			if !strings.Contains(response.Body, tt.wantContains) {
				t.Fatalf("provider response body = %s, want %q", response.Body, tt.wantContains)
			}
			if *calls != 1 {
				t.Fatalf("cache write hook calls = %d, want 1", *calls)
			}
		})
	}
}

func TestForegroundCacheHitPersistsUsageLogAndPluginAudit(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Foreground Cache Hit", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "foreground-cache-hit-key",
		Allowed: []string{"gpt-cache-hit-persistence"},
		Status:  StatusActive,
	}, "thk_foreground_cache_hit")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-cache-hit-persistence", Modality: "chat", Status: StatusActive})
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache-persistence",
		HookID:        "lookup",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      1000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if input.Envelope.Protocol != "gateway" || input.Envelope.Operation != "cache_lookup" {
			t.Fatalf("cache lookup envelope = %+v, want gateway/cache_lookup", input.Envelope)
		}
		result := rawProviderCallResult(t, map[string]any{
			"id":     "chatcmpl_cached_persistence",
			"object": "chat.completion",
			"model":  "gpt-cache-hit-persistence",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": "cached persistence response"}, "finish_reason": "stop"},
			},
		}, Usage{PromptTokens: 7, CompletionTokens: 8, TotalTokens: 15})
		result.AuditEvents = []json.RawMessage{json.RawMessage(`{"outcome":"hit","cache_key":"semantic-test"}`)}
		return result, nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-cache-hit-persistence",
		"messages": []map[string]any{
			{"role": "user", "content": "cache hit should not need a route"},
		},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("cache hit failed: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "cached persistence response") {
		t.Fatalf("cache hit response body = %s", response.Body)
	}

	records := store.ListUsageRecords()
	if len(records) != 1 || records[0].InputTokens != 7 || records[0].OutputTokens != 8 || records[0].TotalTokens != 15 || records[0].ProviderID != "" {
		t.Fatalf("usage records = %+v, want cache usage without provider attribution", records)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].ProviderID != "" || logs[0].RequestID != records[0].RequestID {
		t.Fatalf("request logs = %+v, want one cache-hit log without provider attribution", logs)
	}
	requireGatewayCacheLookupAuditEvent(t, store.ListAuditEvents(), logs[0].RequestID, "short_circuited")
}

func newGatewayEndpointOrderTestServer(t *testing.T) *Server {
	t.Helper()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	return New(store)
}

func registerFailingCacheHook(t *testing.T, server *Server, stage pluginmeta.GatewayHookStage) *int {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache-fail-open",
		HookID:        "fail-open-" + string(stage),
		Stage:         stage,
		Priority:      1000,
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if stage == pluginmeta.StageCacheWrite {
		hook.Reads = []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage}
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register failing %s hook: %v", stage, err)
	}
	calls := 0
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		calls++
		return pluginmeta.GatewayHookResult{}, errors.New("cache hook unavailable")
	})); err != nil {
		t.Fatalf("register failing %s handler: %v", stage, err)
	}
	return &calls
}

func requireGatewayCacheLookupAuditEvent(t *testing.T, events []AuditEvent, requestID string, status string) AuditEvent {
	t.Helper()
	for _, event := range events {
		if event.Action == "plugin.gateway.cache_lookup" && event.ResourceID == requestID && event.Status == status {
			if !strings.Contains(event.AfterSnapshot, `"stage":"cache_lookup"`) ||
				!strings.Contains(event.AfterSnapshot, `"decision":"short_circuit"`) ||
				!strings.Contains(event.AfterSnapshot, `"cache_key":"semantic-test"`) {
				t.Fatalf("cache lookup audit snapshot = %s", event.AfterSnapshot)
			}
			return event
		}
	}
	t.Fatalf("missing cache lookup audit event request=%s status=%s: %+v", requestID, status, events)
	return AuditEvent{}
}

func registerUnexpectedCacheHooks(t *testing.T, server *Server) {
	t.Helper()
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageCacheLookup, pluginmeta.StageCacheWrite} {
		hook := pluginmeta.GatewayHookDescriptor{
			PluginID:      "tokenhub.test-cache-skip",
			HookID:        string(stage),
			Stage:         stage,
			Priority:      1000,
			FailurePolicy: pluginmeta.FailurePolicyFailOpen,
		}
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register unexpected %s hook: %v", stage, err)
		}
		currentHook := hook
		if err := server.gatewayHooks.RegisterHandler(currentHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			t.Fatalf("%s hook should not run for streaming requests", currentHook.Stage)
			return pluginmeta.GatewayHookResult{}, nil
		})); err != nil {
			t.Fatalf("register unexpected %s handler: %v", stage, err)
		}
	}
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
