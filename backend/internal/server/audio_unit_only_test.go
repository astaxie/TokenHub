package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAudioPluginUnitWithoutDuration(t *testing.T) {
	const privateText = "private-audio-evidence-synthetic"
	for _, tc := range []struct {
		name, fields, source, unit string
		invalid, tokenBilling      bool
	}{
		{name: "unreported duration", fields: `"audio_duration_unit":"audio_second",`, source: "unreported", unit: "audio_second"},
		{name: "null duration", fields: `"audio_duration_unit":"audio_second","audio_duration_seconds":null,`, source: "unreported", unit: "audio_second"},
		{name: "ordinary token usage", tokenBilling: true},
		{name: "explicit token unit", fields: `"audio_duration_unit":"token",`, tokenBilling: true},
		{name: "invalid unit", fields: `"audio_duration_unit":"` + privateText + `",`, source: "invalid", unit: "invalid", invalid: true},
		{name: "invalid source", fields: `"audio_duration_unit":"audio_second","audio_duration_source":"` + privateText + `",`, source: "invalid", unit: "audio_second", invalid: true},
	} {
		for _, stream := range []bool{false, true} {
			name := tc.name + "/JSON"
			if stream {
				name = tc.name + "/SSE"
			}
			t.Run(name, func(t *testing.T) {
				server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {
					t.Error("unexpected upstream call")
				}, "audio")
				store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, InputPriceUSDPer1M: 100, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
				store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", InputPriceUSDPer1M: 50, Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
				server.store = audioNoPayloadStore{GormStore: store}
				output := pluginmeta.DataProviderResponse
				body := json.RawMessage(`{"text":"fixture"}`)
				if stream {
					output = pluginmeta.DataStreamEvents
					body = json.RawMessage(`[{"event":"transcript.done","data":"{\"text\":\"fixture\"}"}]`)
				}
				usage := json.RawMessage(`{` + tc.fields + `"prompt_tokens":100,"total_tokens":100}`)
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "audio-unit-only", Writes: []pluginmeta.GatewayDataClass{output, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
						output:               {Value: body},
						pluginmeta.DataUsage: {Value: usage},
					}}, nil
				})
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": stream}, key)
				wantStatus := http.StatusOK
				if tc.invalid {
					wantStatus = http.StatusBadGateway
				}
				if response.Code != wantStatus || strings.Contains(response.Body, privateText) || tc.invalid && !strings.Contains(response.Body, "gateway_hook_response_invalid") {
					t.Fatalf("unexpected response: %d %s", response.Code, response.Body)
				}
				rows := store.ListUsageRecords()
				if len(rows) != 1 || rows[0].InputTokens != 100 || rows[0].TotalTokens != 100 {
					t.Fatalf("independent token usage was lost: %+v", rows)
				}
				if tc.tokenBilling && (rows[0].CostUSD != .01 || rows[0].ProviderCostUSD != .005) {
					t.Fatalf("token billing changed: %+v", rows)
				}
				if tc.unit == "audio_second" && (rows[0].CostUSD != 0 || rows[0].ProviderCostUSD != 0) {
					t.Fatalf("unmeasured native duration fell back to token billing: %+v", rows)
				}
				if tc.invalid && (rows[0].CostUSD != 0 || rows[0].ProviderCostUSD != 0) {
					t.Fatalf("invalid audio evidence fell back to token billing: %+v", rows)
				}
				var entry meteringEntry
				if err := store.db.First(&entry, "id = ?", rows[0].RequestID+":shadow").Error; err != nil {
					t.Fatal(err)
				}
				if strings.Contains(entry.Payload, privateText) {
					t.Fatalf("private plugin content persisted in metering: %s", entry.Payload)
				}
				var settlement struct {
					Tenant   meteringShadowCharge    `json:"tenant"`
					Attempts []meteringAttemptCharge `json:"attempts"`
				}
				if err := json.Unmarshal([]byte(entry.Payload), &settlement); err != nil {
					t.Fatal(err)
				}
				if len(settlement.Attempts) != 1 {
					t.Fatalf("missing provider attempt: %+v", settlement)
				}
				for _, charge := range []meteringShadowCharge{settlement.Tenant, settlement.Attempts[0].Charge} {
					if tc.tokenBilling {
						if charge.AudioEvidence != nil || charge.Status != "estimated" || charge.Charge == nil || len(charge.Charge.Lines) != 1 || charge.Charge.Lines[0].Kind != "input" || charge.Charge.Lines[0].Units != 100 {
							t.Fatalf("token settlement changed: %+v", charge)
						}
						continue
					}
					evidence := charge.AudioEvidence
					if evidence == nil || evidence.Seconds != nil || evidence.Source != tc.source || evidence.BillingUnit != tc.unit {
						t.Fatalf("unit-only duration evidence changed: %+v", charge)
					}
					if charge.Status != "pending" || charge.Charge != nil {
						t.Fatalf("unreported or invalid duration received a settlement charge: %+v", charge)
					}
					if !tc.invalid && (charge.Reason != "usage_presence_unknown" || charge.Units.Input != 100) {
						t.Fatalf("unreported duration lost its pending reason or token evidence: %+v", charge)
					}
				}
			})
		}
	}
}
