package server

import (
	"context"
	"io"
	"net/http"
	"strconv"
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

func TestResponsesCompactCacheLookupFailOpenContinuesToProvider(t *testing.T) {
	server, secret := newResponsesCompactCacheFailOpenTestServer(t, "compact lookup fallback")
	calls := registerFailingCacheHook(t, server, pluginmeta.StageCacheLookup)

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "gpt-compact-cache-fail-open",
		"input": "lookup failure",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 after compact cache lookup fail-open, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "compact lookup fallback") {
		t.Fatalf("compact response did not come from provider path: %s", resp.Body)
	}
	if *calls != 1 {
		t.Fatalf("cache lookup hook calls = %d, want 1", *calls)
	}
}

func TestResponsesCompactCacheWriteFailOpenPreservesProviderResponse(t *testing.T) {
	server, secret := newResponsesCompactCacheFailOpenTestServer(t, "compact write fallback")
	calls := registerFailingCacheHook(t, server, pluginmeta.StageCacheWrite)

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "gpt-compact-cache-fail-open",
		"input": "write failure",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 after compact cache write fail-open, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "compact write fallback") {
		t.Fatalf("compact response did not survive cache write failure: %s", resp.Body)
	}
	if *calls != 1 {
		t.Fatalf("cache write hook calls = %d, want 1", *calls)
	}
}

func newResponsesCompactCacheFailOpenTestServer(t *testing.T, outputText string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Compact Cache Fail Open", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "compact-cache-fail-open-key",
		Allowed: []string{"gpt-compact-cache-fail-open"},
		Status:  StatusActive,
	}, "thk_compact_cache_fail_open")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_compact_cache_fail_open", Name: "Compact Cache Fail Open", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_compact_cache_fail_open",
		ProviderID:   provider.ID,
		Name:         "Compact Cache Fail Open Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-compact-cache-upstream"),
		Credentials:  &ProviderResourceCredentials{AccessToken: "compact-cache-access", AccountID: "compact-cache-account"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-compact-cache-fail-open", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID: "route_compact_cache_fail_open", ModelName: "gpt-compact-cache-fail-open",
		ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "gpt-compact-cache-upstream",
		Status: StatusActive, Priority: 1, Weight: 100,
	})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "compact-cache-fail-open-secret"})
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/backend-api/codex/responses/compact" {
			t.Fatalf("unexpected compact path: %s", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"id":"resp_compact_cache_fail_open",
				"status":"completed",
				"output_text":` + strconv.Quote(outputText) + `,
				"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}
			}`)),
			Request: request,
		}, nil
	})}
	return server, secret
}
