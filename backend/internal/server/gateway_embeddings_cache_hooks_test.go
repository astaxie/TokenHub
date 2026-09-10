package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestEmbeddingsCacheLookupHookCanShortCircuit(t *testing.T) {
	server := newEmbeddingsCacheHookTestServer(t)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "embedding-hit",
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
			"object": "list",
			"model":  "text-embedding-3-small",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": []float64{0.25, 0.75}},
			},
		}, Usage{PromptTokens: 2, TotalTokens: 2}), nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{
		"model": "text-embedding-3-small",
		"input": "enterprise ai gateway",
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `0.25`) || !strings.Contains(resp.Body, `"model":"text-embedding-3-small"`) {
		t.Fatalf("expected cached embedding response: %s", resp.Body)
	}
}

func TestEmbeddingsCacheWriteHookRunsAfterProviderSuccess(t *testing.T) {
	server := newEmbeddingsCacheHookTestServer(t)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "embedding-write",
		Stage:         pluginmeta.StageCacheWrite,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache write hook: %v", err)
	}
	sawEmbeddingResponse := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var response map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderResponse], &response); err != nil {
			t.Fatalf("decode provider response: %v", err)
		}
		_, sawEmbeddingResponse = response["data"]
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register cache write handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{
		"model": "text-embedding-3-small",
		"input": "enterprise ai gateway",
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !sawEmbeddingResponse {
		t.Fatal("cache write hook did not receive the embedding provider response")
	}
}

func TestEmbeddingsPrivacyPreHookCanRewriteInputBeforeProvider(t *testing.T) {
	server := newEmbeddingsCacheHookTestServer(t)
	capture := &captureAdapter{}
	server.adapterRegistry.Register(ProviderMock, capture, AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityEmbeddings)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-embedding-privacy",
		HookID:        "mask-input",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var request EmbeddingsRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &request); err != nil {
			t.Fatalf("decode embedding request: %v", err)
		}
		request.Input = "[masked embedding]"
		return rawRequestBodyPatch(t, request), nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{
		"model": "text-embedding-3-small",
		"input": "sensitive embedding input",
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if capture.seenEmbeddingsInput != "[masked embedding]" {
		t.Fatalf("provider input = %#v, want masked embedding", capture.seenEmbeddingsInput)
	}
}

func newEmbeddingsCacheHookTestServer(t *testing.T) *Server {
	t.Helper()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	return New(store)
}
