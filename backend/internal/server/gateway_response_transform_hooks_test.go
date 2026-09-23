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

func TestResponseTransformFailOpenPreservesProviderResponse(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-transform",
		HookID:        "fail-open",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register response transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, errors.New("response sanitizer unavailable")
	})); err != nil {
		t.Fatalf("register response transform handler: %v", err)
	}

	upstream := map[string]any{"id": "upstream"}
	got, err := server.runGatewayResponsePostHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, upstream, providerRouteProtocolChatCompletions)
	if err != nil {
		t.Fatalf("response transform fail-open returned error: %v", err)
	}
	body, ok := got.(map[string]any)
	if !ok || body["id"] != "upstream" {
		t.Fatalf("response transform output = %#v, want original upstream response", got)
	}
}

func TestResponseTransformInvalidPatchReturnsStableError(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-transform",
		HookID:        "invalid-patch",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register response transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionContinue,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"id":`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register response transform handler: %v", err)
	}

	_, err := server.runGatewayResponsePostHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, map[string]any{"id": "upstream"}, providerRouteProtocolChatCompletions)
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_response_invalid" {
		t.Fatalf("response transform error = %#v, want 502 gateway_hook_response_invalid", httpErr)
	}
}

func TestResponseTransformPolicyRejectsUsageAndCacheWrites(t *testing.T) {
	tests := []pluginmeta.GatewayDataClass{
		pluginmeta.DataUsage,
		pluginmeta.DataCacheKey,
		pluginmeta.DataCacheValue,
	}
	for _, dataClass := range tests {
		t.Run(string(dataClass), func(t *testing.T) {
			server := New(NewMemoryStore())
			err := server.gatewayChain.RegisterHook(pluginmeta.GatewayHookDescriptor{
				PluginID:      "tokenhub.test-response-transform-policy",
				HookID:        "writes-" + string(dataClass),
				Stage:         pluginmeta.StageResponsePost,
				Priority:      1000,
				Writes:        []pluginmeta.GatewayDataClass{dataClass},
				FailurePolicy: pluginmeta.FailurePolicyFailOpen,
			})
			if err == nil || !strings.Contains(err.Error(), `stage "response_post" cannot write data class`) {
				t.Fatalf("register response transform write %q error = %v, want stage policy rejection", dataClass, err)
			}
		})
	}
}

func TestResponseTransformFailClosedDoesNotFailOverAfterProviderSuccess(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Response Transform Failover", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "response-transform-failover-key",
		Allowed: []string{"gpt-response-transform-failover"},
		Status:  StatusActive,
	}, "thk_response_transform_failover")
	if err != nil {
		t.Fatal(err)
	}
	first := store.AddProvider(Provider{ID: "prv_response_transform_first", Name: "First", Type: responseTransformFailoverProbeType, Status: StatusActive, Healthy: true})
	second := store.AddProvider(Provider{ID: "prv_response_transform_second", Name: "Second", Type: responseTransformFailoverProbeType, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-response-transform-failover", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_response_transform_first", ModelName: "gpt-response-transform-failover", ProviderID: first.ID, ProviderModel: "first", Status: StatusActive, Priority: 1, Weight: 100, Strategy: "priority_only"})
	store.AddRoute(ModelRoute{ID: "route_response_transform_second", ModelName: "gpt-response-transform-failover", ProviderID: second.ID, ProviderModel: "second", Status: StatusActive, Priority: 2, Weight: 100, Strategy: "priority_only"})
	server := New(store)
	adapter := &responseTransformFailoverProbeAdapter{}
	server.adapterRegistry.Register(responseTransformFailoverProbeType, adapter, AdapterCapabilityChat)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-transform",
		HookID:        "fail-closed",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register response transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, errors.New("response transform rejected")
	})); err != nil {
		t.Fatalf("register response transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-response-transform-failover",
		"messages": []map[string]any{
			{"role": "user", "content": "do not fail over after response transform"},
		},
	}, secret)
	if resp.Code != http.StatusInternalServerError || !strings.Contains(resp.Body, "gateway_hook_failed") {
		t.Fatalf("response transform failure = %d %s, want 500 gateway_hook_failed", resp.Code, resp.Body)
	}
	if got := strings.Join(adapter.seenProviderIDs, ","); got != first.ID {
		t.Fatalf("provider attempts = %q, want only %q", got, first.ID)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].ProviderID != first.ID || logs[0].StatusCode != http.StatusInternalServerError || logs[0].ErrorCode != "gateway_hook_failed" {
		t.Fatalf("request logs = %+v, want response transform failure on first provider only", logs)
	}
}

func TestResponseTransformCacheHitRunsBeforeUsageAttribution(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Response Transform Cache Hit", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "response-transform-cache-hit-key",
		Allowed: []string{"gpt-response-transform-cache-hit"},
		Status:  StatusActive,
	}, "thk_response_transform_cache_hit")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-response-transform-cache-hit", Modality: "chat", Status: StatusActive})
	server := New(store)
	stages := []pluginmeta.GatewayHookStage{}
	cacheHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-transform-cache",
		HookID:        "lookup",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      1000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	responseHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-transform-cache",
		HookID:        "response-post",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	usageHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-transform-cache",
		HookID:        "usage",
		Stage:         pluginmeta.StageUsageAttribution,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{cacheHook, responseHook, usageHook} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register %s hook: %v", hook.Stage, err)
		}
	}
	if err := server.gatewayHooks.RegisterHandler(cacheHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageCacheLookup)
		return rawProviderCallResult(t, map[string]any{
			"id":     "cached_before_response_transform",
			"object": "chat.completion",
			"model":  "gpt-response-transform-cache-hit",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "cached before response transform",
				},
				"finish_reason": "stop",
			}},
		}, Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(responseHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageResponsePost)
		return rawProviderResponsePatch(t, map[string]any{
			"id":     "cached_after_response_transform",
			"object": "chat.completion",
			"model":  "gpt-response-transform-cache-hit",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "cached after response transform",
				},
				"finish_reason": "stop",
			}},
		}), nil
	})); err != nil {
		t.Fatalf("register response transform handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(usageHook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageUsageAttribution)
		raw := input.Data[pluginmeta.DataProviderResponse]
		if !strings.Contains(string(raw), "cached_after_response_transform") {
			t.Fatalf("usage attribution saw provider response %s, want response_post output", raw)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register usage attribution handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-response-transform-cache-hit",
		"messages": []map[string]any{
			{"role": "user", "content": "cache hit response transform"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("cache hit response = %d %s, want 200", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "cached_after_response_transform") || strings.Contains(resp.Body, "cached_before_response_transform") {
		t.Fatalf("cache hit response body = %s, want response_post output only", resp.Body)
	}
	want := []pluginmeta.GatewayHookStage{pluginmeta.StageCacheLookup, pluginmeta.StageResponsePost, pluginmeta.StageUsageAttribution}
	if !serverGatewayStageSlicesEqual(stages, want) {
		t.Fatalf("cache hit stages = %v, want %v", stages, want)
	}
}

const responseTransformFailoverProbeType = "response_transform_failover_probe"

type responseTransformFailoverProbeAdapter struct {
	MockAdapter
	seenProviderIDs []string
}

func (a *responseTransformFailoverProbeAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	a.seenProviderIDs = append(a.seenProviderIDs, provider.ID)
	return a.MockAdapter.Chat(ctx, provider, providerModel, req)
}
