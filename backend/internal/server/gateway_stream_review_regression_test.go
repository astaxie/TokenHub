package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestStreamingProviderHooksAndPostPoliciesAcrossEndpoints(t *testing.T) {
	for _, protocol := range []string{providerRouteProtocolChatCompletions, providerRouteProtocolResponses, providerRouteProtocolAnthropic, providerRouteProtocolGemini} {
		for _, deny := range []bool{false, true} {
			name := protocol + "/allow"
			if deny {
				name = protocol + "/deny"
			}
			t.Run(name, func(t *testing.T) {
				server, secret, path, payload := streamReviewServer(t, protocol)
				called, guarded, post := 0, 0, 0
				register := func(hook pluginmeta.GatewayHookDescriptor, handler pluginmeta.GatewayHookHandlerFunc) {
					t.Helper()
					hook.PluginID, hook.Priority = "test.stream.review", 2000
					hook.Scope.RouteProtocols = []string{protocol}
					if err := server.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					if err := server.gatewayHooks.RegisterHandler(hook, handler); err != nil {
						t.Fatal(err)
					}
				}
				register(pluginmeta.GatewayHookDescriptor{HookID: "provider", Stage: pluginmeta.StageProviderCall, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents, pluginmeta.DataUsage}}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					called++
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
						pluginmeta.DataStreamEvents: {Value: json.RawMessage(`[{"data":"{\"text\":\"unsafe-output\"}"}]`)},
						pluginmeta.DataUsage:        {Value: json.RawMessage(`{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}`)},
					}}, nil
				})
				register(pluginmeta.GatewayHookDescriptor{HookID: "post", Stage: pluginmeta.StageResponsePost, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					post++
					return streamEventPatchResult(t, map[string]any{"data": `{"text":"checked-output"}`}), nil
				})
				register(pluginmeta.GatewayHookDescriptor{HookID: "guard", Stage: pluginmeta.StageGuardrailPost, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					guarded++
					if !strings.Contains(string(input.Data[pluginmeta.DataStreamEvents]), "checked-output") {
						t.Fatalf("post guard saw untransformed event: %s", input.Data[pluginmeta.DataStreamEvents])
					}
					decision := pluginmeta.HookDecisionContinue
					if deny {
						decision = pluginmeta.HookDecisionDeny
					}
					return pluginmeta.GatewayHookResult{Decision: decision}, nil
				})
				response := doJSON(t, server.Handler(), http.MethodPost, path, payload, secret)
				if called != 1 || post != 1 || guarded != 1 {
					t.Fatalf("stages provider=%d post=%d guard=%d: %d %s", called, post, guarded, response.Code, response.Body)
				}
				if strings.Contains(response.Body, "unsafe-output") || deny && strings.Contains(response.Body, "checked-output") {
					t.Fatalf("unapproved content escaped: %s", response.Body)
				}
				if !deny && (response.Code != 200 || !strings.Contains(response.Body, "checked-output")) {
					t.Fatalf("plugin stream not emitted: %d %s", response.Code, response.Body)
				}
				if deny && response.Code != http.StatusForbidden {
					t.Fatalf("denial did not fail closed: %d %s", response.Code, response.Body)
				}
				if !deny {
					records := server.store.ListUsageRecords()
					if len(records) != 1 || records[0].TotalTokens != 5 {
						t.Fatalf("plugin usage lost: %+v", records)
					}
				}
			})
		}
	}
}

func streamReviewServer(t *testing.T, protocol string) (*Server, string, string, map[string]any) {
	t.Helper()
	model := "stream-review"
	payload := map[string]any{"model": model, "stream": true, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
	path := "/v1/" + protocol
	if protocol == providerRouteProtocolAnthropic {
		server, _, secret := newAnthropicGatewayServer(t, "https://provider.example", ProviderAnthropic)
		payload["model"], payload["max_tokens"] = "claude-tokenhub-test", 32
		return server, secret, "/v1/messages", payload
	}
	if protocol == providerRouteProtocolGemini {
		server, secret := newGeminiCodexTestServer(t, func(map[string]any) string { t.Error("built-in adapter called after provider hook"); return "" })
		return server, secret, "/v1beta/models/gpt-5.5:streamGenerateContent?alt=sse", map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}}
	}
	if protocol == providerRouteProtocolResponses {
		payload = map[string]any{"model": model, "stream": true, "input": "hello"}
	}
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Streaming review", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "test", Allowed: []string{model}, Status: StatusActive}, "thk_stream_review")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "review-provider", Type: "plugin-only-provider", Name: "Review provider", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "review-route", ModelName: model, ProviderID: provider.ID, ProviderModel: model, Status: StatusActive, Weight: 100})
	return New(store), secret, path, payload
}

func TestGatewayRankingCarriesRequestEndpointProtocol(t *testing.T) {
	server, secret, path, payload := streamReviewServer(t, providerRouteProtocolChatCompletions)
	// Ranking only runs with multiple candidates.
	server.store.AddRoute(ModelRoute{ID: "other-route", ModelName: "stream-review", ProviderID: "review-provider", ProviderModel: "other", Status: StatusActive, Priority: 2, Weight: 100})
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.image-ranking", HookID: "images", Stage: pluginmeta.StageRouteRank, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolImageGeneration}}}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		calls++
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatal(err)
	}
	_ = doJSON(t, server.Handler(), http.MethodPost, path, payload, secret)
	if calls != 0 {
		t.Fatalf("image-only ranking hook ran %d times for chat", calls)
	}
	routes, _ := server.store.SelectRouteCandidates("stream-review")
	server.runGatewayRouteRankHooks(t.Context(), CallContext{RouteProtocol: providerRouteProtocolImageGeneration}, routes)
	if calls != 1 {
		t.Fatalf("image ranking hook did not run for image endpoint: %d", calls)
	}
}
