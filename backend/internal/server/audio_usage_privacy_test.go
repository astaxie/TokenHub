package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAudioPluginEvidencePrivacyWithoutPayloadPersistence(t *testing.T) {
	const privateText = "private-transcript-synthetic"
	for _, tc := range []struct {
		name, privateField, source, unit string
		invalid                          bool
	}{
		{"invalid source", "audio_duration_source", "invalid", "audio_second", true},
		{"invalid unit", "audio_duration_unit", "invalid", "invalid", true},
		{"invalid raw representation", "audio_duration_raw", "plugin", "audio_second", false},
		{"valid plugin evidence", "", "plugin", "audio_second", false},
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
				store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
				store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
				server.store = audioNoPayloadStore{GormStore: store}
				output := pluginmeta.DataProviderResponse
				responseBody := json.RawMessage(`{"text":"fixture"}`)
				if stream {
					output = pluginmeta.DataStreamEvents
					responseBody = json.RawMessage(`[{"event":"transcript.done","data":"{\"text\":\"fixture\"}"}]`)
				}
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "audio-evidence-privacy", Writes: []pluginmeta.GatewayDataClass{output, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					evidence := map[string]any{"audio_duration_seconds": 12.5, "audio_duration_source": "plugin", "audio_duration_unit": "audio_second", "audio_duration_raw": "12.5"}
					if tc.privateField != "" {
						evidence[tc.privateField] = privateText
					}
					raw, err := json.Marshal(evidence)
					if err != nil {
						t.Fatal(err)
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
						output:               {Value: responseBody},
						pluginmeta.DataUsage: {Value: raw},
					}}, nil
				})
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": stream}, key)
				wantStatus := http.StatusOK
				wantTenant, wantProvider := .25, .125
				if tc.invalid {
					wantStatus = http.StatusBadGateway
					wantTenant, wantProvider = 0, 0
				}
				if response.Code != wantStatus || strings.Contains(response.Body, privateText) || tc.invalid && !strings.Contains(response.Body, "gateway_hook_response_invalid") {
					t.Fatalf("unexpected response: %d %s", response.Code, response.Body)
				}
				rows := store.ListUsageRecords()
				if len(rows) != 1 || rows[0].CostUSD != wantTenant || rows[0].ProviderCostUSD != wantProvider || rows[0].TotalTokens != 0 {
					t.Fatalf("unexpected audio billing: %+v", rows)
				}
				var count int64
				if err := store.db.Model(&RequestPayloadLog{}).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("payload retention was not disabled: count=%d error=%v", count, err)
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
					evidence := charge.AudioEvidence
					if evidence == nil || evidence.Seconds == nil || *evidence.Seconds != 12.5 || evidence.Source != tc.source || evidence.BillingUnit != tc.unit {
						t.Fatalf("duration evidence changed: %+v", charge)
					}
					if tc.invalid {
						if charge.Status != "pending" || charge.Charge != nil {
							t.Fatalf("invalid evidence was priced: %+v", charge)
						}
					} else if charge.Status != "estimated" || charge.Charge == nil || len(charge.Charge.Lines) != 1 || charge.Charge.Lines[0].Quantity != "12.5" {
						t.Fatalf("valid duration price was lost: %+v", charge)
					}
				}
			})
		}
	}
}
