package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestResponsesCompactCacheLookupHookCanShortCircuit(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "compact-hit",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      1000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderCallResult(t, map[string]any{
			"id":          "resp_cached_compact",
			"object":      "response",
			"model":       "gpt-4.1-mini",
			"output_text": "cached compact response",
		}, Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}), nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "gpt-4.1-mini",
		"input": "enterprise ai gateway",
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "resp_cached_compact") || !strings.Contains(resp.Body, "cached compact response") {
		t.Fatalf("expected cached compact response: %s", resp.Body)
	}
}
