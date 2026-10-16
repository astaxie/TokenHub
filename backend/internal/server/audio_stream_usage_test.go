package server

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestTranscriptionStreamPreservesIndependentUsageAndBilling(t *testing.T) {
	const tokens = `{"usage":{"type":"tokens","input_tokens":3,"total_tokens":3}}`
	const zeroTokens = `{"usage":{"type":"tokens","input_tokens":0,"total_tokens":0}}`
	const duration = `{"duration":12.5}`
	const native = `{"usage":{"type":"duration","seconds":12.5}}`
	for _, tc := range []struct {
		name, unit string
		events     []string
		tokens     int64
		cost       float64
	}{
		{"tokens then verbose duration", "token", []string{tokens, duration}, 3, .000003},
		{"verbose duration then tokens", "token", []string{duration, tokens}, 3, .000003},
		{"zero tokens then duration", "token", []string{zeroTokens, duration}, 0, 0},
		{"duration then zero tokens", "token", []string{duration, zeroTokens}, 0, 0},
		{"native duration then tokens", "audio_second", []string{native, tokens}, 3, .25},
		{"tokens then native duration", "audio_second", []string{tokens, native}, 3, .25},
		{"native duration then absent duration", "audio_second", []string{native, `{"usage":{"type":"duration"}}`}, 0, .25},
		{"native duration overrides later verbose duration", "audio_second", []string{native, `{"duration":99}`}, 0, .25},
		{"native duration overrides earlier verbose duration", "audio_second", []string{`{"duration":99}`, native}, 0, .25},
		{"latest native duration", "audio_second", []string{`{"usage":{"type":"duration","seconds":99}}`, native}, 0, .25},
		{"latest cumulative token count", "token", []string{tokens, `{"usage":{"type":"tokens","input_tokens":5,"total_tokens":5}}`, duration}, 5, .000005},
	} {
		for _, nested := range []bool{false, true} {
			name := tc.name
			if nested {
				name += " nested"
			}
			t.Run(name, func(t *testing.T) {
				var stream strings.Builder
				for _, event := range tc.events {
					if nested {
						event = `{"response":` + event + `}`
					}
					stream.WriteString("data: " + event + "\n\n")
				}
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, stream.String())
				}, "audio")
				store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, InputPriceUSDPer1M: 1, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
				store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", InputPriceUSDPer1M: 2, Metadata: map[string]string{audioSecondPriceKey: "0.04"}})
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": true}, key)
				if response.Code != http.StatusOK || response.Body != stream.String() {
					t.Fatalf("stream delivery changed: %d %s", response.Code, response.Body)
				}
				rows := store.ListUsageRecords()
				if len(rows) != 1 || rows[0].TotalTokens != tc.tokens || math.Abs(rows[0].CostUSD-tc.cost) > 1e-12 || math.Abs(rows[0].ProviderCostUSD-2*tc.cost) > 1e-12 {
					t.Fatalf("split stream evidence changed billing: %+v", rows)
				}
				var entry meteringEntry
				if err := store.db.First(&entry, "id = ?", rows[0].RequestID+":shadow").Error; err != nil {
					t.Fatal(err)
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
					if evidence == nil || evidence.Seconds == nil || *evidence.Seconds != 12.5 || evidence.BillingUnit != tc.unit {
						t.Fatalf("lost independent duration evidence: %+v", charge)
					}
				}
			})
		}
	}
}
