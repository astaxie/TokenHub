package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaSSEUsageAndTerminalErrors(t *testing.T) {
	for _, scenario := range []string{"transcription", "image", "nested usage", "terminal error", "named error"} {
		t.Run(scenario, func(t *testing.T) {
			stream := "event: transcript.text.done\ndata: {\"text\":\"fixture\",\"usage\":{\"input_tokens\":3,\"output_tokens\":7,\"total_tokens\":10}}\n\n"
			if scenario == "nested usage" {
				stream = "event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":7,\"total_tokens\":10}}}\n\n"
			}
			if scenario == "terminal error" {
				stream += "event: error\ndata: {\"error\":{\"message\":\"upstream-fixture-secret\",\"type\":\"server_error\"}}\n\n"
			}
			if scenario == "named error" {
				stream += "event: audio.failed\ndata: {\"message\":\"upstream-fixture-secret\"}\n\n"
			}
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, stream)
			}, "audio")
			store.AddRoute(ModelRoute{ID: "fallback-media-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			path := "/v1/audio/transcriptions"
			if scenario == "image" {
				path = "/v1/images/generations"
			}
			response := doJSON(t, server.Handler(), http.MethodPost, path, map[string]any{"model": "public-media", "stream": true}, key)
			failed := strings.Contains(scenario, "error")
			if calls != 1 || (failed && response.Code == http.StatusOK) || (!failed && (response.Code != http.StatusOK || response.Body != stream)) {
				t.Fatalf("SSE result: calls=%d status=%d body=%s", calls, response.Code, response.Body)
			}
			if strings.Contains(response.Body, "upstream-fixture-secret") {
				t.Fatal("stream error leaked the upstream credential")
			}
			records := store.ListUsageRecords()
			if len(records) != 1 || records[0].TotalTokens != 10 {
				t.Fatalf("reported SSE usage lost: %+v", records)
			}
			logs, _ := json.Marshal(store.ListRequestLogs())
			if strings.Contains(string(logs), "upstream-fixture-secret") {
				t.Fatal("stream error leaked the upstream credential into logs")
			}
		})
	}
}

func TestMediaSSEPreservesLargeImageEvents(t *testing.T) {
	stream := "id: frame-1\r\nevent: image_generation.completed\r\ndata: {\"b64_json\":\"" + strings.Repeat("A", maxSSEEventBytes+4) + "\",\"usage\":{\"input_tokens\":3,\"output_tokens\":7,\"total_tokens\":10}}\r\n\r\n"
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}, "image")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media", "stream": true}, key)
	if response.Code != http.StatusOK || response.Body != stream {
		t.Fatalf("large image event changed: status=%d bytes=%d, want %d", response.Code, len(response.Body), len(stream))
	}
	if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 10 {
		t.Fatalf("large image event lost usage: %+v", records)
	}
}

func TestMediaSSEHooksProcessEventsBeforeDelivery(t *testing.T) {
	for _, source := range []string{"adapter", "adapter malformed MIME parameters", "provider hook"} {
		for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageStreamTransform, pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
			for _, deny := range []bool{false, true} {
				t.Run(source+"/"+string(stage)+map[bool]string{false: "/rewrite", true: "/deny"}[deny], func(t *testing.T) {
					server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
						if source == "provider hook" {
							t.Error("provider hook reached the adapter")
						}
						contentType := "text/event-stream"
						if source == "adapter malformed MIME parameters" {
							contentType += "; charset="
						}
						w.Header().Set("Content-Type", contentType)
						_, _ = io.WriteString(w, "data: {\"text\":\"private\",\"usage\":{\"total_tokens\":5,\"output_tokens\":5}}\n\n")
					}, "audio")
					if source == "provider hook" {
						registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "stream", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
							return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
								pluginmeta.DataStreamEvents: {Value: json.RawMessage(`[{"data":"{\"text\":\"private\"}"}]`)},
								pluginmeta.DataUsage:        {Value: json.RawMessage(`{"completion_tokens":5,"total_tokens":5}`)},
							}}, nil
						})
					}
					hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-stream-policy", HookID: "event", Stage: stage, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
					if stage != pluginmeta.StageStreamTransform {
						hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}
					}
					if err := server.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					calls := 0
					if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						calls++
						var event gatewayStreamEventView
						if err := json.Unmarshal(input.Data[pluginmeta.DataStreamEvents], &event); err != nil {
							t.Errorf("hook did not receive an SSE event: %v", err)
						}
						if deny {
							return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
						}
						if stage != pluginmeta.StageStreamTransform {
							return rawProviderResponsePatch(t, json.RawMessage(strings.ReplaceAll(event.Data, "private", "masked"))), nil
						}
						return streamEventPatchResult(t, map[string]any{"data": strings.ReplaceAll(event.Data, "private", "masked")}), nil
					})); err != nil {
						t.Fatal(err)
					}
					response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "stream": true}, key)
					if calls != 1 || strings.Contains(response.Body, "private") || (deny && response.Code != http.StatusForbidden) || (!deny && (response.Code != http.StatusOK || !strings.Contains(response.Body, "masked"))) {
						t.Fatalf("SSE hook bypass: calls=%d status=%d body=%s", calls, response.Code, response.Body)
					}
					if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 5 {
						t.Fatalf("SSE hook lost upstream usage: %+v", records)
					}
				})
			}
		}
	}
}

type cancellingMediaBody struct{ cancel context.CancelFunc }

func (b cancellingMediaBody) Read([]byte) (int, error) { b.cancel(); return 0, context.Canceled }
func (b cancellingMediaBody) Close() error             { return nil }

func TestMediaClientCancellationDoesNotPenalizeProvider(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			server, store, key := classificationGateway(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			adapter := OpenAICompatibleAdapter{Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if phase == "headers" {
					cancel()
					return nil, context.Canceled
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"audio/mpeg"}}, Body: cancellingMediaBody{cancel: cancel}}, nil
			})}}
			server.adapterRegistry.Register(ProviderOpenAICompatible, adapter, AdapterCapabilityMedia)
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"classified-model","input":"fixture"}`)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(httptest.NewRecorder(), req)
			if calls != 1 || resourceFailureCount(t, store, "rsrc_classified_0") != 0 {
				t.Fatalf("client cancellation penalized upstream: calls=%d failures=%d", calls, resourceFailureCount(t, store, "rsrc_classified_0"))
			}
		})
	}
}
