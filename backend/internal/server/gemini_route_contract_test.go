package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGemini4SkipsIncompatibleRoutes(t *testing.T) {
	var neonCalls, carbonCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/gemini-4-neon:generateContent":
			neonCalls.Add(1)
		case "/models/gemini-4-carbon:generateContent":
			carbonCalls.Add(1)
		default:
			t.Errorf("unexpected Gemini URL: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`)
	}))
	defer upstream.Close()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Gemini route fixture", Status: StatusActive})
	_, token, err := store.CreateAPIKey(project.ID, APIKey{Name: "test", Allowed: []string{"gemini-alias"}, Status: StatusActive}, "thk_pr441_route_test")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gemini-alias", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "fixture", Name: "Gemini fixture", Type: ProviderGemini, Status: StatusActive, Healthy: true, APIKey: "synthetic", BaseURL: upstream.URL})
	resource, err := store.AddProviderResource(ProviderResource{ID: "carbon", Name: "Carbon fixture", ProviderID: provider.ID,
		ResourceType: ProviderResourceAPIKey, APIKey: "synthetic", Healthy: true, Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{ModelName: "gemini-alias", ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "gemini-4-carbon",
		Status: StatusActive, Weight: 100, Priority: 1, Strategy: RouteStrategyPriorityOnly})
	s := NewWithConfig(store, Config{})
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	request := ChatCompletionRequest{Model: "gemini-alias", MaxTokens: 40000, Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
	rejected := doJSON(t, s.Handler(), http.MethodPost, "/v1/chat/completions", request, token)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body, "model_output_limit_exceeded") || neonCalls.Load() != 0 || carbonCalls.Load() != 0 {
		t.Fatalf("expected a local budget error without dispatch: %d %s", rejected.Code, rejected.Body)
	}
	if after, ok := store.GetProviderResource(resource.ID); !ok || after.FailureCount != 0 {
		t.Fatalf("local validation affected provider health: %+v", after)
	}
	store.AddRoute(ModelRoute{ModelName: "gemini-alias", ProviderID: provider.ID, ProviderModel: "gemini-4-neon",
		Status: StatusActive, Weight: 100, Priority: 2, Strategy: RouteStrategyPriorityOnly})
	accepted := doJSON(t, s.Handler(), http.MethodPost, "/v1/chat/completions", request, token)
	if accepted.Code != http.StatusOK || neonCalls.Load() != 1 {
		t.Fatalf("compatible route was not called: status=%d calls=%d response=%s", accepted.Code, neonCalls.Load(), accepted.Body)
	}
	effort := "xhigh"
	request.MaxTokens = 512
	request.ReasoningEffort = &effort
	accepted = doJSON(t, s.Handler(), http.MethodPost, "/v1/chat/completions", request, token)
	if accepted.Code != http.StatusOK || neonCalls.Load() != 2 {
		t.Fatalf("compatible reasoning route was not called: status=%d calls=%d response=%s", accepted.Code, neonCalls.Load(), accepted.Body)
	}
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.gemini-contract", HookID: "normalize-budget", Stage: pluginmeta.StageRequestTransform,
		Scope:  pluginmeta.GatewayHookScope{ProviderIDs: []string{provider.ID}},
		Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
	}
	if err := s.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := s.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, _ pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderRequestPatch(t, ChatCompletionRequest{Model: request.Model, MaxTokens: 512, Messages: request.Messages}), nil
	})); err != nil {
		t.Fatal(err)
	}
	request.MaxTokens = 40000
	request.ReasoningEffort = nil
	accepted = doJSON(t, s.Handler(), http.MethodPost, "/v1/chat/completions", request, token)
	if accepted.Code != http.StatusOK || carbonCalls.Load() != 1 || neonCalls.Load() != 2 {
		t.Fatalf("route-specific transform could not use Carbon: status=%d calls=%d response=%s", accepted.Code, carbonCalls.Load(), accepted.Body)
	}
}
