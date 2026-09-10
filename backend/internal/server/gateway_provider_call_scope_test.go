package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestProviderCallCapabilityHonorsRequestScope(t *testing.T) {
	for _, endpoint := range []string{"chat", "chat-stream", "embeddings", "responses", "responses-stream", "responses-background", "anthropic", "anthropic-stream", "gemini", "gemini-stream", "images"} {
		for _, scopeCase := range []string{"matching", "unscoped", "project-mismatch", "key-mismatch", "operation-mismatch", "legacy-matching", "legacy-mismatch", "split-matches", "response-only", "stream-only", "no-response", "separate-mode-hooks", "output-scope-split"} {
			t.Run(endpoint+"/"+scopeCase, func(t *testing.T) {
				config := responseJobTestConfig()
				config.ImageStorageDir = t.TempDir()
				store, secret := newBackgroundResponseTestStore(t, config)
				if _, err := store.UpdateProvider("prv_background", Provider{Type: "plugin-only-provider", Healthy: true}); err != nil {
					t.Fatal(err)
				}
				key := store.ListAPIKeys()[0]
				if endpoint == "images" {
					var err error
					key, secret, err = store.CreateAPIKey(key.ProjectID, APIKey{Name: "image scope", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_scope")
					if err != nil {
						t.Fatal(err)
					}
					store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
					store.AddRoute(ModelRoute{ID: "image-scope-route", ModelName: openAIImageModelName, ProviderID: "prv_background", ProviderResourceID: "rsrc_background", ProviderModel: "image-upstream", Status: StatusActive, Weight: 100})
				}
				server := NewWithConfig(store, config)
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				protocol := providerRouteProtocolResponses
				path := "/v1/responses"
				stream := strings.HasSuffix(endpoint, "-stream")
				payload := map[string]any{"model": "gpt-background", "input": "hello", "stream": stream}
				switch {
				case strings.HasPrefix(endpoint, "chat"):
					protocol, path = providerRouteProtocolChatCompletions, "/v1/chat/completions"
					payload = map[string]any{"model": "gpt-background", "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
				case endpoint == "embeddings":
					protocol, path = providerRouteProtocolEmbeddings, "/v1/embeddings"
				case strings.HasPrefix(endpoint, "anthropic"):
					protocol, path = providerRouteProtocolAnthropic, "/v1/messages"
					payload = map[string]any{"model": "gpt-background", "max_tokens": 32, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
				case strings.HasPrefix(endpoint, "gemini"):
					protocol, path = providerRouteProtocolGemini, "/v1beta/models/gpt-background:generateContent"
					if stream {
						path = "/v1beta/models/gpt-background:streamGenerateContent?alt=sse"
					}
					payload = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}}
				case endpoint == "images":
					protocol, path = providerRouteProtocolImageGeneration, "/v1/images/generations"
					payload = map[string]any{"model": openAIImageModelName, "prompt": "scope test", "response_format": "b64_json"}
				}
				scope := pluginmeta.GatewayHookScope{
					ProviderTypes: []string{"plugin-only-provider"}, RouteProtocols: []string{protocol},
					ProviderIDs: []string{"prv_background"}, ResourceIDs: []string{"rsrc_background"}, ResourceTypes: []string{"mock"},
					ProjectIDs: []string{key.ProjectID}, APIKeyIDs: []string{key.ID}, Operations: []string{"provider_call"},
				}
				var metadata map[string]string
				matching := scopeCase == "matching" || scopeCase == "unscoped" || scopeCase == "legacy-matching"
				outputs := []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataStreamEvents}
				switch scopeCase {
				case "response-only":
					outputs, matching = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, !stream
				case "stream-only":
					outputs, matching = []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}, stream
				case "no-response":
					outputs = nil
				case "separate-mode-hooks":
					outputs, matching = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, true
				case "output-scope-split":
					outputs = []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}
					if stream {
						outputs = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}
					}
				case "unscoped":
					scope = pluginmeta.GatewayHookScope{}
				case "project-mismatch", "split-matches":
					scope.ProjectIDs = []string{"another-project"}
				case "key-mismatch":
					otherKey, _, err := store.CreateAPIKey(key.ProjectID, APIKey{Name: "other key", Status: StatusActive}, "thk_other_scope")
					if err != nil {
						t.Fatal(err)
					}
					scope.APIKeyIDs = []string{otherKey.ID}
				case "operation-mismatch":
					scope.Operations = []string{"request_transform"}
				case "legacy-matching", "legacy-mismatch":
					metadata = map[string]string{"project_id": key.ProjectID, "api_key_id": key.ID, "operation": "provider_call"}
					if scopeCase == "legacy-mismatch" {
						metadata["project_id"] = "another-project"
					}
					scope.ProjectIDs, scope.APIKeyIDs, scope.Operations = nil, nil, nil
				}
				var calls atomic.Int32
				imageBytes := realPNGFixture(t)
				register := func(id string, scope pluginmeta.GatewayHookScope, outputs []pluginmeta.GatewayDataClass) {
					t.Helper()
					hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.provider-scope", HookID: id, Stage: pluginmeta.StageProviderCall, Priority: 2000, Scope: scope, Metadata: metadata, Writes: append([]pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, outputs...)}
					if err := server.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						calls.Add(1)
						if input.Envelope.Operation != "provider_call" || input.Envelope.RouteProtocol != protocol {
							t.Errorf("unexpected provider call envelope: %+v", input.Envelope)
						}
						if len(outputs) == 0 {
							return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
						}
						if len(outputs) == 1 && outputs[0] == pluginmeta.DataStreamEvents || len(outputs) == 2 && stream {
							return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
								pluginmeta.DataStreamEvents: {Value: json.RawMessage(`[{"data":"{\"text\":\"scope-result\"}"}]`)},
								pluginmeta.DataUsage:        {Value: json.RawMessage(`{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}`)},
							}}, nil
						}
						var result any = map[string]any{"id": "scope-result", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "scope-result"}}, "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "scope-result"}}}}}
						if endpoint == "images" {
							result = gatewayImageProviderResponse{DataBase64: encodeBase64(imageBytes), RevisedPrompt: "scope-result"}
						}
						return rawProviderCallResult(t, result, Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
					})); err != nil {
						t.Fatal(err)
					}
				}
				register("provider", scope, outputs)
				if scopeCase == "split-matches" {
					scope.ProjectIDs, scope.APIKeyIDs = []string{key.ProjectID}, []string{"another-key"}
					register("other-provider", scope, outputs)
				}
				if scopeCase == "separate-mode-hooks" {
					register("stream-provider", scope, []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents})
				}
				if scopeCase == "output-scope-split" {
					scope.ProjectIDs = []string{"another-project"}
					register("other-provider", scope, []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataStreamEvents})
				}
				status, body := 0, ""
				if endpoint == "responses-background" {
					id := submitBackgroundResponse(t, server.Handler(), secret, "hello")
					want := "failed"
					if matching {
						want = "completed"
					}
					body = mustJSON(waitForResponseJobStatus(t, server.Handler(), secret, id, want))
				} else {
					response := doJSON(t, server.Handler(), http.MethodPost, path, payload, secret)
					status, body = response.Code, response.Body
				}
				if matching {
					if calls.Load() != 1 || !strings.Contains(body, "scope-result") || status != 0 && status != http.StatusOK {
						t.Fatalf("matching hook failed: calls=%d status=%d body=%s", calls.Load(), status, body)
					}
				} else {
					wantCode := "provider_capability_not_supported"
					if endpoint == "images" {
						wantCode = ErrProviderMissing.Code
					}
					if calls.Load() != 0 || !strings.Contains(body, wantCode) || strings.Contains(body, "provider_adapter_missing") {
						t.Errorf("unsupported route admitted: calls=%d status=%d body=%s", calls.Load(), status, body)
					}
					var attempts int64
					if err := store.db.Model(&RouteAttemptLog{}).Count(&attempts).Error; err != nil {
						t.Fatal(err)
					}
					if attempts != 0 {
						t.Errorf("unsupported route recorded %d attempts", attempts)
					}
				}
				for _, resource := range store.ListProviderResources() {
					if resource.FailureCount != 0 || !resource.Healthy || resource.CooldownUntil != nil {
						t.Errorf("scope check penalized resource: %+v", resource)
					}
				}
			})
		}
	}
}

func TestProviderCallScopeDoesNotHideAdapterCapability(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			server, store, secret := newBackgroundResponseTestServer(t)
			hook := pluginmeta.GatewayHookDescriptor{
				PluginID: "test.unrelated-provider", HookID: "provider", Stage: pluginmeta.StageProviderCall,
				Scope: pluginmeta.GatewayHookScope{ProjectIDs: []string{"another-project"}},
			}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				t.Error("unrelated provider hook ran")
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
			})); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"model": "gpt-background", "input": "hello"}
			if path == "/v1/messages" {
				payload = map[string]any{"model": "gpt-background", "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
			}
			response := doJSON(t, server.Handler(), http.MethodPost, path, payload, secret)
			if response.Code != http.StatusOK || !strings.Contains(response.Body, "hello") {
				t.Fatalf("built-in route failed: %d %s", response.Code, response.Body)
			}
			if got := resourceFailureCount(t, store, "rsrc_background"); got != 0 {
				t.Fatalf("built-in resource failure count = %d", got)
			}
		})
	}
}
