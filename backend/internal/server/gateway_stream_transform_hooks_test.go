package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestChatStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "rewrite-chat-chunks",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register stream transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var event gatewayStreamEventView
		if err := json.Unmarshal(input.Data[pluginmeta.DataStreamEvents], &event); err != nil {
			t.Fatalf("decode stream event: %v", err)
		}
		if !strings.Contains(event.Data, "Echo:") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "Echo:", "Plugin:", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":  "gpt-4.1-mini",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Plugin: hello") {
		t.Fatalf("stream body was not transformed: %s", resp.Body)
	}
	if strings.Contains(resp.Body, "Echo: hello") {
		t.Fatalf("stream body still contains the untransformed chunk: %s", resp.Body)
	}
}

func TestPlaygroundChatStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "rewrite-playground-chunks",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register stream transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var event gatewayStreamEventView
		if err := json.Unmarshal(input.Data[pluginmeta.DataStreamEvents], &event); err != nil {
			t.Fatalf("decode stream event: %v", err)
		}
		if !strings.Contains(event.Data, "Echo:") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "Echo:", "Playground plugin:", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id": defaultProjectID,
		"model":      "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Playground plugin: hello") {
		t.Fatalf("playground stream body was not transformed: %s", resp.Body)
	}
}

func streamEventPatchResult(t *testing.T, value any) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataStreamEvents: {Value: data},
		},
	}
}
