package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaProviderHookStreamInspectsUsageAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name          string
		explicitUsage string
		failed        bool
		wantTokens    int64
		binary        bool
	}{
		{name: "event usage fallback", wantTokens: 7},
		{name: "explicit usage overrides event", explicitUsage: `{"completion_tokens":3,"total_tokens":3}`, wantTokens: 3},
		{name: "explicit zero usage overrides event", explicitUsage: `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`},
		{name: "terminal error retains event usage", failed: true, wantTokens: 7},
		{name: "terminal error retains explicit usage", failed: true, explicitUsage: `{"completion_tokens":3,"total_tokens":3}`, wantTokens: 3},
		{name: "binary SSE event usage fallback", binary: true, wantTokens: 7},
		{name: "binary SSE terminal error", binary: true, failed: true, wantTokens: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("handled stream reached adapter") }, "audio")
			store.AddRoute(ModelRoute{ID: "fallback-media-hook", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "vendor-fallback", Status: StatusActive, Priority: 2, Weight: 100})
			calls := 0
			hook := pluginmeta.GatewayHookDescriptor{HookID: "stream-inspection", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents, pluginmeta.DataUsage}}
			if tc.binary {
				hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}
			}
			registerMediaProviderTestHook(t, server, hook, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				calls++
				events := []gatewayStreamEventView{{Event: "audio.done", Data: `{"usage":{"output_tokens":7,"total_tokens":7}}`}}
				if tc.failed {
					events = append(events, gatewayStreamEventView{Event: "error", Data: `{"error":{"message":"upstream-fixture-secret","type":"server_error"}}`})
				}
				encoded, err := json.Marshal(events)
				if err != nil {
					t.Fatal(err)
				}
				writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataStreamEvents: {Value: encoded}}
				if tc.binary {
					var stream strings.Builder
					for _, event := range events {
						stream.Write(renderSSEEvent(serverSentEvent{Event: event.Event, Data: event.Data}))
					}
					envelope, err := json.Marshal(map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte(stream.String())), "content_type": "text/event-stream"})
					if err != nil {
						t.Fatal(err)
					}
					writes = map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: envelope}}
				}
				if tc.explicitUsage != "" {
					writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(tc.explicitUsage)}
				}
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "stream": !tc.binary, "input": "fixture"}, key)
			if calls != 1 || (tc.failed && response.Code == http.StatusOK) || (!tc.failed && response.Code != http.StatusOK) {
				t.Errorf("stream outcome: calls=%d status=%d body=%s", calls, response.Code, response.Body)
			}
			if strings.Contains(response.Body, "upstream-fixture-secret") {
				t.Error("stream error leaked provider credential")
			}
			logs, err := json.Marshal(store.ListRequestLogs())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(logs), "upstream-fixture-secret") {
				t.Error("stream error leaked provider credential in request log")
			}
			var tokens int64
			for _, record := range store.ListUsageRecords() {
				tokens += record.TotalTokens
			}
			if tokens != tc.wantTokens {
				t.Errorf("recorded tokens=%d want=%d", tokens, tc.wantTokens)
			}
		})
	}
}
