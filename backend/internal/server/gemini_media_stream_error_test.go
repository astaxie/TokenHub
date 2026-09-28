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

func TestGeminiMediaStreamErrorEnvelopesFailBeforeDelivery(t *testing.T) {
	for _, source := range []struct {
		name                string
		hook, explicitUsage bool
	}{
		{name: "adapter"},
		{name: "provider hook", hook: true},
		{name: "provider hook explicit zero", hook: true, explicitUsage: true},
	} {
		for _, tc := range []struct {
			name, fields string
			failed       bool
		}{
			{"error type", `"type":"error","message":"upstream-fixture-secret"`, true},
			{"failed type", `"type":"response.failed","message":"upstream-fixture-secret"`, true},
			{"mixed case type", `"type":" Audio.FAILED ","message":"upstream-fixture-secret"`, true},
			{"nested error", `"response":{"error":{"message":"upstream-fixture-secret"}}`, true},
			{"null nested error", `"type":"progress","response":{"error":null}`, false},
		} {
			t.Run(source.name+"/"+tc.name, func(t *testing.T) {
				final := "{" + tc.fields + `,"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":12,"totalTokenCount":17}}`
				body := "data: " + geminiMediaFixtureResponse + "\n\ndata: " + final + "\n\n"
				calls := 0
				server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					if source.hook {
						t.Error("handled stream reached the adapter")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, body)
				})
				store.AddRoute(ModelRoute{ID: "fallback-gemini-stream-error", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				if source.hook {
					registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "native-stream-error", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolGemini}}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						calls++
						events, err := json.Marshal([]gatewayStreamEventView{{Data: geminiMediaFixtureResponse}, {Data: final}})
						if err != nil {
							t.Fatal(err)
						}
						writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataStreamEvents: {Value: events}}
						if source.explicitUsage {
							writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(`{"total_tokens":0}`)}
						}
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
					})
				}
				response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:streamGenerateContent?alt=sse", json.RawMessage(geminiMediaFixtureRequest), key)
				status := http.StatusOK
				if tc.failed {
					status = http.StatusBadGateway
				}
				if response.Code != status || calls != 1 {
					t.Errorf("stream outcome: status=%d want=%d calls=%d body=%s", response.Code, status, calls, response.Body)
				}
				if !tc.failed && response.Body != body {
					t.Error("valid native stream changed")
				}
				logs, _ := json.Marshal(store.ListRequestLogs())
				if strings.Contains(response.Body+string(logs), "upstream-fixture-secret") {
					t.Error("stream error leaked provider credentials")
				}
				records := store.ListUsageRecords()
				if source.explicitUsage {
					for _, record := range records {
						if record.TotalTokens != 0 {
							t.Errorf("explicit zero usage lost: %+v", records)
						}
					}
				} else if len(records) != 1 || records[0].InputTokens != 5 || records[0].OutputTokens != 12 || records[0].TotalTokens != 17 {
					t.Errorf("latest native usage lost: %+v", records)
				}
			})
		}
	}
}
