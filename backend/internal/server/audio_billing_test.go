package server

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAudioDurationDurablePricingWithoutPayloadPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, payload, tenantRate, providerRate, pendingReason string
		wantTenant, wantProvider                               float64
		seconds                                                *float64
		stream                                                 bool
	}{
		{"paid JSON", `{"text":"sensitive transcript","usage":{"type":"duration","seconds":12.5}}`, "0.02", "0.01", "", 0.25, 0.125, audioTestSeconds(12.5), false},
		{"paid SSE", `{"text":"sensitive transcript","usage":{"type":"duration","seconds":12.5}}`, "0.02", "0.01", "", 0.25, 0.125, audioTestSeconds(12.5), true},
		{"verbose JSON", `{"text":"sensitive transcript","duration":12.5}`, "0.02", "0.01", "", 0.25, 0.125, audioTestSeconds(12.5), false},
		{"measured zero", `{"text":"sensitive transcript","usage":{"type":"duration","seconds":0}}`, "0.02", "0.01", "", 0, 0, audioTestSeconds(0), false},
		{"free tenant", `{"text":"sensitive transcript","usage":{"type":"duration","seconds":12.5}}`, "0", "0.01", "", 0, 0.125, audioTestSeconds(12.5), false},
		{"missing price", `{"text":"sensitive transcript","usage":{"type":"duration","seconds":12.5,"input_tokens":100,"total_tokens":100}}`, "", "", "native_unit_price_required", 0, 0, audioTestSeconds(12.5), false},
		{"unreported duration", `{"text":"sensitive transcript","usage":{"type":"duration"}}`, "0.02", "0.01", "usage_presence_unknown", 0, 0, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := tc.payload, "application/json"
			if tc.stream {
				body, contentType = "data: "+body+"\n\n", "text/event-stream"
			}
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, body)
			}, "audio")
			model := Model{Name: "public-media", Modality: "audio", Status: StatusActive, InputPriceUSDPer1M: 99, Metadata: map[string]string{}}
			provider := ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", InputPriceUSDPer1M: 99, Metadata: map[string]string{}}
			if tc.tenantRate != "" {
				model.Metadata[audioSecondPriceKey] = tc.tenantRate
			}
			if tc.providerRate != "" {
				provider.Metadata[audioSecondPriceKey] = tc.providerRate
			}
			store.AddModel(model)
			store.AddProviderModel(provider)
			server.store = audioNoPayloadStore{GormStore: store}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": tc.stream}, key)
			if response.Code != http.StatusOK || response.Body != body {
				t.Fatalf("media delivery changed: %d %s", response.Code, response.Body)
			}
			rows := store.ListUsageRecords()
			if len(rows) != 1 || math.Abs(rows[0].CostUSD-tc.wantTenant) > 1e-12 || math.Abs(rows[0].ProviderCostUSD-tc.wantProvider) > 1e-12 {
				t.Fatalf("unexpected billing: %+v", rows)
			}
			wantTokens := int64(0)
			if tc.name == "missing price" {
				wantTokens = 100
			}
			if rows[0].TotalTokens != wantTokens {
				t.Fatalf("duration invented tokens: %+v", rows[0])
			}
			var count int64
			if err := store.db.Model(&RequestPayloadLog{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("payloads persisted: count=%d error=%v", count, err)
			}
			var entry meteringEntry
			if err := store.db.First(&entry, "id = ?", rows[0].RequestID+":shadow").Error; err != nil {
				t.Fatal(err)
			}
			if strings.Contains(entry.Payload, "sensitive transcript") {
				t.Fatalf("metering retained transcript: %s", entry.Payload)
			}
			var settlement struct {
				Tenant   meteringShadowCharge    `json:"tenant"`
				Attempts []meteringAttemptCharge `json:"attempts"`
			}
			if err := json.Unmarshal([]byte(entry.Payload), &settlement); err != nil {
				t.Fatal(err)
			}
			if len(settlement.Attempts) != 1 {
				t.Fatalf("missing attempt: %+v", settlement)
			}
			for _, charge := range []meteringShadowCharge{settlement.Tenant, settlement.Attempts[0].Charge} {
				if charge.AudioEvidence == nil || (charge.AudioEvidence.Seconds != nil) != (tc.seconds != nil) {
					t.Fatalf("lost duration evidence: %+v", charge)
				}
				if tc.seconds != nil && *charge.AudioEvidence.Seconds != *tc.seconds {
					t.Fatalf("incorrect evidence: %+v", charge)
				}
				if tc.pendingReason != "" {
					if charge.Status != "pending" || charge.Reason != tc.pendingReason || charge.Charge != nil {
						t.Fatalf("unknown usage or price billed: %+v", charge)
					}
				} else if charge.Status != "estimated" || charge.Charge == nil || len(charge.Charge.Lines) != 1 || charge.Charge.Lines[0].Kind != "audio_second" {
					t.Fatalf("duration not natively priced: %+v", charge)
				}
			}
		})
	}
}

func TestAudioDurationRejectsMalformedUpstreamUsage(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"fixture","usage":{"type":"duration","seconds":-2}}`)
	}, "audio")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "invalid_provider_usage") {
		t.Fatalf("invalid duration accepted: %d %s", response.Code, response.Body)
	}
	rows := store.ListUsageRecords()
	if len(rows) != 1 || rows[0].CostUSD != 0 || rows[0].TotalTokens != 0 {
		t.Fatalf("invalid duration billed: %+v", rows)
	}
	var entry meteringEntry
	if err := store.db.First(&entry, "id = ?", rows[0].RequestID+":shadow").Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.Payload, `"source":"invalid"`) {
		t.Fatalf("lost invalid usage evidence: %s", entry.Payload)
	}
}

func TestAudioDurationBudgetAndImmutableProviderPrice(t *testing.T) {
	calls := 0
	var store *GormStore
	server, fixtureStore, secret := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "10"}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"fixture","usage":{"type":"duration","seconds":12.5}}`)
	}, "audio")
	store = fixtureStore
	store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
	store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
	projects := store.ListProjects()
	if len(projects) != 1 {
		t.Fatalf("unexpected projects: %+v", projects)
	}
	if err := store.db.Model(&APIKey{}).Where("project_id = ?", projects[0].ID).Update("limit_daily_cost_usd", 0.20).Error; err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, secret)
		if response.Code != want {
			t.Fatalf("request %d: status=%d want=%d body=%s", i, response.Code, want, response.Body)
		}
	}
	rows := store.ListUsageRecords()
	if calls != 1 || len(rows) != 1 || rows[0].CostUSD != .25 || rows[0].ProviderCostUSD != .125 {
		t.Fatalf("budget or captured prices changed: calls=%d usage=%+v", calls, rows)
	}
	var entry meteringEntry
	if err := store.db.First(&entry, "id = ?", rows[0].RequestID+":admission").Error; err != nil {
		t.Fatal(err)
	}
	var admission meteringRequestSnapshot
	if err := json.Unmarshal([]byte(entry.Payload), &admission); err != nil {
		t.Fatal(err)
	}
	if admission.Price.AudioPricePerSecond != ".02" && admission.Price.AudioPricePerSecond != "0.02" {
		t.Fatalf("admission price missing: %+v", admission)
	}
}

func TestAudioPriceMetadataValidation(t *testing.T) {
	for _, value := range []string{"", "-1", "NaN", "Inf", "1e-3"} {
		metadata := map[string]string{audioSecondPriceKey: value}
		if validateAudioPriceMetadata(metadata) == nil || validateProviderModelCosts(ProviderModel{Metadata: metadata}) == nil {
			t.Fatalf("invalid audio price accepted: %q", value)
		}
	}
	for _, value := range []string{"0", "0.0001", "12.5"} {
		if err := validateAudioPriceMetadata(map[string]string{audioSecondPriceKey: value}); err != nil {
			t.Fatal(err)
		}
	}
}

func audioTestSeconds(value float64) *float64 { return &value }

type audioNoPayloadStore struct{ *GormStore }

func (audioNoPayloadStore) RecordRequestPayload(string, string, bool, string, bool) {}

func TestAudioDurationStatementIncludesNativeQuantityAndPrice(t *testing.T) {
	model := Model{Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.02"}}
	usage := captureAudioDuration(Usage{}, map[string]any{"duration": 12.5}, true)
	usage = priceUsage(model, usage)
	price := legacyMeteringPrice(model, time.Now().UTC(), false)
	evidence := &statementEvidence{Admission: &meteringRequestSnapshot{RequestID: "audio-statement", LegacyPrice: &price}, Settlement: &struct {
		Tenant   meteringShadowCharge    `json:"tenant"`
		Attempts []meteringAttemptCharge `json:"attempts"`
	}{Tenant: shadowPrice(&price, usage, usage.CostUSD)}}
	var result statementResult
	appendTenantStatement(&result, evidence, "audio-model")
	if len(result.Rows) != 1 || result.Rows[0].Status != "estimated" || len(result.Rows[0].Lines) != 1 || result.Rows[0].Lines[0].Quantity != "12.5" || result.Rows[0].Lines[0].Amount != "0.250000000000" {
		t.Fatalf("statement lost native quantity: %+v", result.Rows)
	}
}
