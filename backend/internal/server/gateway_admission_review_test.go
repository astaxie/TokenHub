package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tokenhub/backend/internal/billing"
	pluginmeta "tokenhub/backend/internal/plugin"
	reconciliationpersistence "tokenhub/backend/internal/reconciliation/persistence"
)

func TestAnthropicProviderCallOnlyRoute(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "nonstream"
		if stream {
			name = "stream"
		}
		for _, matching := range []bool{true, false} {
			protocol := providerRouteProtocolAnthropic
			if !matching {
				protocol = providerRouteProtocolResponses
			}
			t.Run(name+"/"+protocol, func(t *testing.T) {
				server, secret, _, _ := streamReviewServer(t, providerRouteProtocolChatCompletions)
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				var calls atomic.Int32
				registerReviewProviderHook(t, server, protocol, &calls, stream)
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/messages", map[string]any{
					"model": "stream-review", "max_tokens": 32, "stream": stream,
					"messages": []any{map[string]any{"role": "user", "content": "hello"}},
				}, secret)
				if !matching {
					if response.Code != http.StatusNotImplemented || calls.Load() != 0 {
						t.Fatalf("mismatched hook admitted: calls=%d response=%d %s", calls.Load(), response.Code, response.Body)
					}
					return
				}
				if response.Code != http.StatusOK || calls.Load() != 1 || !strings.Contains(response.Body, "plugin-result") {
					t.Fatalf("plugin route failed: calls=%d response=%d %s", calls.Load(), response.Code, response.Body)
				}
				records := server.store.ListUsageRecords()
				if len(records) != 1 || records[0].TotalTokens != 5 {
					t.Fatalf("plugin usage lost: %+v", records)
				}
			})
		}
	}
}

func TestBackgroundResponsesProviderCallOnlyRoute(t *testing.T) {
	for _, protocol := range []string{providerRouteProtocolResponses, providerRouteProtocolAnthropic} {
		t.Run(protocol, func(t *testing.T) {
			config := responseJobTestConfig()
			store, secret := newBackgroundResponseTestStore(t, config)
			if _, err := store.UpdateProvider("prv_background", Provider{Type: "plugin-only-provider", Healthy: true}); err != nil {
				t.Fatal(err)
			}
			server := NewWithConfig(store, config)
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			var calls atomic.Int32
			registerReviewProviderHook(t, server, protocol, &calls, false)
			id := submitBackgroundResponse(t, server.Handler(), secret, "hello")
			if protocol != providerRouteProtocolResponses {
				result := waitForResponseJobStatus(t, server.Handler(), secret, id, "failed")
				if calls.Load() != 0 || !strings.Contains(mustJSON(result), "provider_capability_not_supported") {
					t.Fatalf("mismatched hook admitted: calls=%d result=%+v", calls.Load(), result)
				}
				return
			}
			result := waitForResponseJobStatus(t, server.Handler(), secret, id, "completed")
			if calls.Load() != 1 || !strings.Contains(mustJSON(result), "plugin-result") {
				t.Fatalf("plugin result lost: calls=%d result=%+v", calls.Load(), result)
			}
			records := store.ListUsageRecords()
			if len(records) != 1 || records[0].TotalTokens != 5 {
				t.Fatalf("plugin usage lost: %+v", records)
			}
		})
	}
}

func registerReviewProviderHook(t *testing.T, server *Server, protocol string, calls *atomic.Int32, stream bool) {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.admission-review", HookID: "provider", Stage: pluginmeta.StageProviderCall,
		Priority: 2000, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{protocol}, ProviderTypes: []string{"plugin-only-provider"}},
		Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataStreamEvents, pluginmeta.DataUsage},
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		calls.Add(1)
		if input.Envelope.RouteProtocol != protocol {
			t.Errorf("protocol = %q, want %q", input.Envelope.RouteProtocol, protocol)
		}
		if stream {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataStreamEvents: {Value: json.RawMessage(`[{"event":"content_block_delta","data":"{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"plugin-result\"}}"}]`)},
				pluginmeta.DataUsage:        {Value: json.RawMessage(`{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}`)},
			}}, nil
		}
		return rawProviderCallResult(t, map[string]any{
			"id": "plugin-result", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "plugin-result"}},
		}, Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
	})); err != nil {
		t.Fatal(err)
	}
}

func TestReconciliationBillingBridgeUsesAttributionSnapshot(t *testing.T) {
	for _, test := range []struct {
		name               string
		metadata           map[string]string
		provider, resource string
	}{
		{"snapshot only", map[string]string{"tokenhub_provider_id": "snapshot-provider", "tokenhub_resource_id": "snapshot-resource"}, "snapshot-provider", "snapshot-resource"},
		{"snapshot wins", map[string]string{"tokenhub_provider_id": "snapshot-provider", "tokenhub_resource_id": "snapshot-resource", "provider_id": "legacy-provider", "provider_resource_id": "legacy-resource"}, "snapshot-provider", "snapshot-resource"},
		{"legacy fallback", map[string]string{"provider_id": "legacy-provider", "provider_resource_id": "legacy-resource"}, "legacy-provider", "legacy-resource"},
		{"empty snapshot", map[string]string{"tokenhub_provider_id": "", "tokenhub_resource_id": "", "provider_id": "legacy-provider", "provider_resource_id": "legacy-resource"}, "", ""},
		{"missing metadata", nil, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := reconciliationpersistence.NewBillingReader(projectionBillingReader{records: []billing.Record{{Metadata: test.metadata}}})
			records, err := bridge.ListRecordsInRange("connector", time.Time{}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].ProviderID != test.provider || records[0].ProviderResourceID != test.resource {
				t.Fatalf("attribution projection = %+v, want %q/%q", records, test.provider, test.resource)
			}
		})
	}
}

// Embedding Store hides GormStore's atomic image admission extension.
type reviewImageAdmissionStore struct{ Store }

func TestImageAdmissionCarriesProtocolThroughScopedHooks(t *testing.T) {
	for _, mode := range []string{"atomic", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "Image protocol review", Status: StatusActive})
			_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "images", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_protocol_review")
			if err != nil {
				t.Fatal(err)
			}
			provider := store.AddProvider(Provider{ID: "image-review", Name: "Image review", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
			resource, err := store.AddProviderResource(ProviderResource{ID: "image-review-resource", ProviderID: provider.ID, Name: "Image review key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true})
			if err != nil {
				t.Fatal(err)
			}
			store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
			for _, id := range []string{"image-review-route-a", "image-review-route-b"} {
				store.AddRoute(ModelRoute{ID: id, ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: openAIImageModelName, Status: StatusActive, Weight: 100})
			}
			var admissionStore Store = store
			if mode == "fallback" {
				admissionStore = reviewImageAdmissionStore{store}
			}
			server := NewWithConfig(admissionStore, Config{AdminToken: "test-admin-token", SecretKey: "image-protocol-review", ImageStorageDir: t.TempDir()})
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			imageBytes := realPNGFixture(t)
			server.imageRunner = func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
				return imageBytes, "review image", Usage{}, nil
			}
			stages := []pluginmeta.GatewayHookStage{pluginmeta.StageAuthContext, pluginmeta.StageDecodeNormalize, pluginmeta.StageAdmission, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize, pluginmeta.StageCacheLookup, pluginmeta.StageRouteCandidates, pluginmeta.StageRouteRank}
			counts := make([]atomic.Int32, len(stages))
			for index, stage := range stages {
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.image-protocol", HookID: string(stage), Stage: stage, Priority: 2000, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolImageGeneration}}}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					counts[index].Add(1)
					if input.Envelope.RouteProtocol != providerRouteProtocolImageGeneration {
						t.Errorf("%s protocol = %q", stage, input.Envelope.RouteProtocol)
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
				})); err != nil {
					t.Fatal(err)
				}
			}
			response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": openAIImageModelName, "prompt": "protocol review", "response_format": "b64_json"}, secret, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("image request failed: %d %s", response.Code, response.Body)
			}
			for index, stage := range stages {
				if counts[index].Load() != 1 {
					t.Errorf("%s calls = %d, want 1", stage, counts[index].Load())
				}
			}
		})
	}
}
