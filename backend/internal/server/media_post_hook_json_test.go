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

func TestMediaPostHooksPreserveJSONResponseObject(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		for _, tc := range []struct {
			name, body string
			status     int
		}{
			{"null", `null`, http.StatusBadGateway},
			{"array", `[{"text":"fixture"}]`, http.StatusBadGateway},
			{"string", `"fixture"`, http.StatusBadGateway},
			{"number", `9007199254740993`, http.StatusBadGateway},
			{"boolean", `true`, http.StatusBadGateway},
			{"object", `{"id":9007199254740993,"text":"rewritten"}`, http.StatusOK},
		} {
			t.Run(string(stage)+"/"+tc.name, func(t *testing.T) {
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"text":"fixture","usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`)
				}, "audio")
				store.AddRoute(ModelRoute{ID: "fallback-post-json", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-json", HookID: "replace", Stage: stage, Priority: 1000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					return rawProviderResponsePatch(t, json.RawMessage(tc.body)), nil
				})); err != nil {
					t.Fatal(err)
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
				if response.Code != tc.status || calls != 1 {
					t.Fatalf("post-hook JSON outcome: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				if tc.status == http.StatusOK && response.Body != tc.body {
					t.Errorf("valid JSON object changed: %s", response.Body)
				}
				if tc.status == http.StatusBadGateway && !strings.Contains(response.Body, "gateway_hook_response_invalid") {
					t.Errorf("unexpected validation error: %s", response.Body)
				}
				if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 10 {
					t.Fatalf("post-hook validation lost upstream usage: %+v", records)
				}
			})
		}
	}
}
