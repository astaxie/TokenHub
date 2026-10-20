package server

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestTranscriptionNullTokenObservationsPreserveDurationBilling(t *testing.T) {
	for _, tc := range []struct {
		name, reported string
		tokenBilling   bool
	}{
		{"null prompt", `{"prompt_tokens":null}`, false},
		{"null input", `{"input_tokens":null}`, false},
		{"null completion", `{"completion_tokens":null}`, false},
		{"null output", `{"output_tokens":null}`, false},
		{"null total", `{"total_tokens":null}`, false},
		{"all primary counters null", `{"prompt_tokens":null,"input_tokens":null,"completion_tokens":null,"output_tokens":null,"total_tokens":null}`, false},
		{"zero input", `{"input_tokens":0}`, true},
		{"zero total", `{"total_tokens":0}`, true},
		{"zero among null counters", `{"input_tokens":null,"output_tokens":0,"total_tokens":null}`, true},
		{"explicit token unit", `{"type":"tokens"}`, true},
		{"explicit token unit with null counters", `{"type":"tokens","input_tokens":null,"output_tokens":null,"total_tokens":null}`, true},
	} {
		for _, format := range []string{"JSON", "SSE duration first", "SSE usage first"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				body := `{"text":"Synthetic transcript","duration":12.5,"usage":` + tc.reported + `}`
				contentType := "application/json"
				if format != "JSON" {
					contentType = "text/event-stream"
					durationEvent := "data: {\"duration\":12.5}\n\n"
					usageEvent := "data: {\"usage\":" + tc.reported + "}\n\n"
					body = durationEvent + usageEvent
					if format == "SSE usage first" {
						body = usageEvent + durationEvent
					}
				}
				app, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", contentType)
					_, _ = io.WriteString(w, body)
				}, "audio")
				store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, InputPriceUSDPer1M: 100, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
				store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", InputPriceUSDPer1M: 50, Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
				response := doJSON(t, app.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": format != "JSON"}, key)
				if response.Code != http.StatusOK || response.Body != body {
					t.Fatalf("transcription delivery changed: status=%d", response.Code)
				}
				wantUnit, wantCost := "audio_second", .25
				if tc.tokenBilling {
					wantUnit, wantCost = "token", 0
				}
				rows := store.ListUsageRecords()
				if len(rows) != 1 || rows[0].TotalTokens != 0 || rows[0].CostUSD != wantCost || rows[0].ProviderCostUSD != wantCost/2 {
					t.Fatalf("duration billing changed: rows=%+v want tenant=%g provider=%g", rows, wantCost, wantCost/2)
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
					t.Fatalf("provider attempt count=%d want=1", len(settlement.Attempts))
				}
				for _, charge := range []meteringShadowCharge{settlement.Tenant, settlement.Attempts[0].Charge} {
					evidence := charge.AudioEvidence
					if evidence == nil || evidence.Seconds == nil || *evidence.Seconds != 12.5 || evidence.BillingUnit != wantUnit {
						t.Fatalf("independent duration evidence changed: %+v want unit=%s", charge, wantUnit)
					}
				}
			})
		}
	}
}
