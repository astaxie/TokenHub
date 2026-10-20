package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionInvalidHookStreamRetainsIndependentUsage(t *testing.T) {
	for _, tc := range []struct {
		name, events string
		stream       bool
	}{
		{"malformed events", `[{"event":`, true},
		{"null events", `null`, true},
		{"invalid event name", `[{"event":"private\ntranscript","data":"{}"}]`, true},
		{"events on nonstream request", `[{"data":"{}"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {
				t.Error("invalid hook stream reached the native adapter")
			}, "audio")
			store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
			store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
			if err := store.db.Model(&APIKey{}).Where("name = ?", "Media key").Updates(map[string]any{"token_limit_tpm": 1_000_000, "limit_daily_tokens": 200}).Error; err != nil {
				t.Fatal(err)
			}
			hookCalls := 0
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{
				HookID: "invalid-transcription-stream", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataStreamEvents, pluginmeta.DataUsage},
			}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				hookCalls++
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
					pluginmeta.DataStreamEvents: {Value: json.RawMessage(tc.events)},
					pluginmeta.DataUsage:        {Value: json.RawMessage(`{"prompt_tokens":200,"total_tokens":200,"audio_duration_seconds":12.5,"audio_duration_source":"plugin","audio_duration_unit":"audio_second"}`)},
				}}, nil
			})
			payload := map[string]any{"model": "public-media", "stream": tc.stream}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", payload, key)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "gateway_hook_stream_event_invalid") || strings.Contains(response.Body, "private") || hookCalls != 1 {
				t.Fatalf("invalid stream failure changed: status=%d hooks=%d body=%s", response.Code, hookCalls, response.Body)
			}
			rows := store.ListUsageRecords()
			if len(rows) != 1 || rows[0].TotalTokens != 200 || rows[0].CostUSD != .25 || rows[0].ProviderCostUSD != .125 {
				t.Fatalf("independent token and duration usage was lost: %+v", rows)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 || attempts[0].TotalTokens != 200 || attempts[0].CostUSD != .25 {
				t.Fatalf("attempt usage was lost: %+v", attempts)
			}
			response = doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", payload, key)
			if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body, "quota_exceeded") || hookCalls != 1 {
				t.Fatalf("invalid stream bypassed daily quota: status=%d hooks=%d body=%s", response.Code, hookCalls, response.Body)
			}
		})
	}
}
