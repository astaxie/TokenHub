package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestDirectMediaRejectsMalformedJSONEvents(t *testing.T) {
	for _, source := range []string{"adapter", "provider hook"} {
		for _, tc := range []struct {
			name, data string
			wantTokens int64
		}{
			{"trailing object", `{"usage":{"output_tokens":12,"total_tokens":12}}{}`, 12},
			{"trailing multiline object", "{\"usage\":{\"output_tokens\":12,\"total_tokens\":12}}\n{\"usage\":{\"total_tokens\":999}}", 12},
			{"trailing nested usage", `{"response":{"usage":{"output_tokens":12,"total_tokens":12}}}{}`, 12},
			{"trailing garbage on error", `{"error":{"message":"upstream-fixture-secret"},"usage":{"output_tokens":12,"total_tokens":12}}garbage`, 12},
			{"truncated object", `{"error":{"message":"upstream-fixture-secret"`, 10},
			{"null", `null`, 10},
			{"array", `[]`, 10},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				events := []gatewayStreamEventView{
					{Data: `{"usage":{"output_tokens":10,"total_tokens":10}}`},
					{Data: tc.data},
					{Data: "[DONE]"},
				}
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					if source == "provider hook" {
						t.Error("handled stream reached adapter")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					for _, event := range events {
						_, _ = w.Write(renderSSEEvent(serverSentEvent{Event: event.Event, Data: event.Data}))
					}
				}, "audio")
				store.AddRoute(ModelRoute{ID: "fallback-malformed-media", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				if source == "provider hook" {
					registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "invalid-events", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						calls++
						encoded, err := json.Marshal(events)
						if err != nil {
							t.Fatal(err)
						}
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataStreamEvents: {Value: encoded}}}, nil
					})
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "stream": true, "input": "fixture"}, key)
				if calls != 1 || response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "invalid_media_response") {
					t.Errorf("malformed stream accepted or retried: calls=%d status=%d body=%s", calls, response.Code, response.Body)
				}
				logs, _ := json.Marshal(store.ListRequestLogs())
				if strings.Contains(response.Body+string(logs), "upstream-fixture-secret") {
					t.Error("malformed stream leaked provider credentials")
				}
				if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != tc.wantTokens {
					t.Errorf("reported usage lost: %+v", records)
				}
			})
		}
	}
}

func TestDirectMediaJSONEventsPreserveHeartbeatsAndDone(t *testing.T) {
	stream := ": keepalive\n\ndata: {\"id\":9223372036854775808123,\"usage\":{\"output_tokens\":7,\"total_tokens\":7}}\n\ndata: [DONE]\n\n"
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, stream)
	}, "image")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media", "stream": true, "prompt": "fixture"}, key)
	if response.Code != http.StatusOK || response.Body != stream {
		t.Fatalf("valid stream changed: status=%d body=%s", response.Code, response.Body)
	}
	if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 7 {
		t.Fatalf("valid stream usage lost: %+v", records)
	}
}

func TestDirectMediaRejectsInvalidPostHookEvents(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageStreamTransform, pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		for _, data := range []string{`null`, `[]`, `{"error":{"message":"upstream-fixture-secret"}}garbage`, `{"type":"audio.failed","message":"upstream-fixture-secret"}`} {
			if stage != pluginmeta.StageStreamTransform && !json.Valid([]byte(data)) {
				continue
			}
			t.Run(string(stage)+"/"+data, func(t *testing.T) {
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"text\":\"fixture\",\"usage\":{\"output_tokens\":7,\"total_tokens\":7}}\n\n")
				}, "audio")
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-invalid-event", HookID: "replace", Stage: stage, Priority: 1000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if stage != pluginmeta.StageStreamTransform {
					hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}
				}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					if stage != pluginmeta.StageStreamTransform {
						return rawProviderResponsePatch(t, json.RawMessage(data)), nil
					}
					return streamEventPatchResult(t, map[string]any{"data": data}), nil
				})); err != nil {
					t.Fatal(err)
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "stream": true, "input": "fixture"}, key)
				if response.Code != http.StatusBadGateway || calls != 1 || !strings.Contains(response.Body, "gateway_hook_response_invalid") || strings.Contains(response.Body, "upstream-fixture-secret") {
					t.Errorf("invalid post-hook event accepted: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 7 {
					t.Errorf("post-hook validation lost original usage: %+v", records)
				}
			})
		}
	}
}
