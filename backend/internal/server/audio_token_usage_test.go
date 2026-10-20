package server

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestTranscriptionStreamPreservesAuxiliaryTokenObservations(t *testing.T) {
	const details = `{"usage":{"input_token_details":{"audio_tokens":3,"cached_tokens":2,"cache_write_tokens":4,"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":2},"output_tokens_details":{"audio_tokens":5,"reasoning_tokens":6,"accepted_prediction_tokens":7,"rejected_prediction_tokens":8}}}`
	const aliases = `{"usage":{"input_audio_tokens":3,"total_cached_tokens":2,"cache_write_input_tokens":4,"cache_write_5m_input_tokens":1,"cache_write_1h_input_tokens":2,"output_audio_tokens":5,"reasoning_output_tokens":6,"output_accepted_prediction_tokens":7,"output_rejected_prediction_tokens":8}}`
	const totals = `{"usage":{"input_tokens":20,"output_tokens":30,"total_tokens":50}}`
	wantDetails := Usage{InputAudioTokens: 3, CachedInputTokens: 2, CacheWriteInputTokens: 4, CacheWrite5mInputTokens: 1, CacheWrite1hInputTokens: 2, OutputAudioTokens: 5, ReasoningOutputTokens: 6, AcceptedPredictionTokens: 7, RejectedPredictionTokens: 8}
	wantTotals := wantDetails
	wantTotals.PromptTokens, wantTotals.CompletionTokens, wantTotals.TotalTokens = 20, 30, 50
	wantZeroAudio := wantTotals
	wantZeroAudio.InputAudioTokens = 0
	for _, tc := range []struct {
		name   string
		events []string
		want   Usage
	}{
		{"details only", []string{details}, wantDetails},
		{"top-level detail aliases", []string{aliases}, wantDetails},
		{"details then primary counters", []string{details, totals}, wantTotals},
		{"primary counters then details", []string{totals, details}, wantTotals},
		{"typed details retain primary counters", []string{totals, strings.Replace(details, `"usage":{`, `"usage":{"type":"tokens",`, 1)}, wantTotals},
		{"explicit audio zero retains other details", []string{details, totals, `{"usage":{"input_audio_tokens":0}}`}, wantZeroAudio},
		{"zero detail alias takes precedence", []string{details, totals, `{"usage":{"input_token_details":{"audio_tokens":0},"input_audio_tokens":99}}`}, wantZeroAudio},
		{"empty token event retains counters", []string{details, totals, `{"usage":{"type":"tokens"}}`}, wantTotals},
	} {
		for _, nested := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/outer", true: "/nested"}[nested], func(t *testing.T) {
				var stream strings.Builder
				for _, event := range tc.events {
					if nested {
						event = `{"response":` + event + `}`
					}
					stream.WriteString("data: " + event + "\n\n")
				}
				usage, err := inspectMediaStream([]byte(stream.String()), Provider{}, "/audio/transcriptions")
				if err != nil || !reflect.DeepEqual(usage, tc.want) {
					t.Fatalf("independent observations changed: usage=%+v want=%+v error=%v", usage, tc.want, err)
				}
			})
		}
	}
}

func TestTranscriptionAuxiliaryAliasesAndEnvelopePrecedence(t *testing.T) {
	const body = `{"usage":{"input_audio_tokens":0,"cached_input_tokens":0,"cache_write_input_tokens":0,"cache_write_5m_input_tokens":0,"cache_write_1h_input_tokens":0,"output_audio_tokens":0,"reasoning_output_tokens":0,"output_accepted_prediction_tokens":0,"output_rejected_prediction_tokens":0},"response":{"usage":{"input_audio_tokens":3,"cached_input_tokens":2,"cache_write_input_tokens":4,"cache_write_5m_input_tokens":1,"cache_write_1h_input_tokens":2,"output_audio_tokens":5,"reasoning_output_tokens":6,"output_accepted_prediction_tokens":7,"output_rejected_prediction_tokens":8}}}`
	usage, err := inspectMediaStream([]byte("data: "+body+"\n\n"), Provider{}, "/audio/transcriptions")
	if err != nil || !reflect.DeepEqual(usage, Usage{}) {
		t.Fatalf("outer zero observations did not take precedence: usage=%+v error=%v", usage, err)
	}
	const split = `{"usage":{"input_token_details":{"audio_tokens":3}},"response":{"usage":{"input_tokens":20,"output_tokens":30,"total_tokens":50,"output_tokens_details":{"audio_tokens":5}}}}`
	usage, err = inspectMediaStream([]byte("data: "+split+"\n\n"), Provider{}, "/audio/transcriptions")
	if err != nil || usage.InputAudioTokens != 3 || usage.OutputAudioTokens != 5 || usage.TotalTokens != 50 {
		t.Fatalf("independent envelope observations were lost: usage=%+v error=%v", usage, err)
	}
}

func TestTranscriptionAuxiliaryTokensKeepDurationBillingAndQuotaConsistent(t *testing.T) {
	for _, tc := range []struct {
		name, body, unit string
		wantCost         float64
		wantTokens       int64
	}{
		{"details only", `{"text":"fixture","usage":{"input_token_details":{"audio_tokens":200}}}`, "", 0, 200},
		{"details with verbose duration", `{"text":"fixture","duration":12.5,"usage":{"input_token_details":{"audio_tokens":200}}}`, "token", 0, 200},
		{"zero details with verbose duration", `{"text":"fixture","duration":12.5,"usage":{"input_token_details":{"audio_tokens":0}}}`, "token", 0, 0},
		{"details with native duration", `{"text":"fixture","usage":{"type":"duration","seconds":12.5,"input_token_details":{"audio_tokens":200}}}`, "audio_second", .25, 200},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/JSON", true: "/SSE"}[stream], func(t *testing.T) {
				body, contentType := tc.body, "application/json"
				if stream {
					body, contentType = "data: "+body+"\n\n", "text/event-stream"
				}
				app, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", contentType)
					_, _ = io.WriteString(w, body)
				}, "audio")
				store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, InputPriceUSDPer1M: 100, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
				store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", InputPriceUSDPer1M: 50, Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
				if err := store.db.Model(&APIKey{}).Where("name = ?", "Media key").Update("token_limit_tpm", 1_000_000).Error; err != nil {
					t.Fatal(err)
				}
				traces := &recordingTraceEmitter{}
				app.traceEmitter = traces
				response := doJSON(t, app.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": stream}, key)
				if response.Code != http.StatusOK || response.Body != body {
					t.Fatalf("delivery changed: %d %s", response.Code, response.Body)
				}
				completed := traces.take()
				if len(completed) != 1 {
					t.Fatalf("completion count=%d want=1", len(completed))
				}
				usage := completed[0].Usage
				if usage.InputAudioTokens != tc.wantTokens || usage.PromptTokens != 0 || usage.TotalTokens != 0 || usage.AudioDurationUnit != tc.unit || usage.CostUSD != tc.wantCost {
					t.Fatalf("evidence lost or charges invented: %+v", usage)
				}
				rows := store.ListUsageRecords()
				if len(rows) != 1 {
					t.Fatalf("missing provider usage record: %+v", rows)
				}
				for _, row := range rows {
					if row.CostUSD != tc.wantCost || row.ProviderCostUSD != tc.wantCost/2 || row.InputAudioTokens != tc.wantTokens {
						t.Fatalf("independent tenant and provider charges changed: %+v", row)
					}
				}
				var counters []struct{ TotalTokens int64 }
				if err := store.db.Table("quota_buckets").Where("scope = ?", "minute").Find(&counters).Error; err != nil {
					t.Fatal(err)
				}
				if len(counters) != 1 || counters[0].TotalTokens != tc.wantTokens {
					t.Fatalf("auxiliary audio quota changed: %+v want=%d", counters, tc.wantTokens)
				}
			})
		}
	}
}

func TestTranscriptionZeroPrimaryUsageRetainsIndependentAudioDetails(t *testing.T) {
	const stream = "data: {\"usage\":{\"input_audio_tokens\":3}}\n\ndata: {\"usage\":{\"total_tokens\":0}}\n\n"
	usage, err := inspectMediaStream([]byte(stream), Provider{}, "/audio/transcriptions")
	// Preserve the existing quota fallback: absent positive primary counters use
	// independently reported details, without changing an explicit total to three.
	if err != nil || usage.TotalTokens != 0 || usage.InputAudioTokens != 3 || meteredTokens(usage) != 3 {
		t.Fatalf("zero primary count changed independent details: usage=%+v error=%v", usage, err)
	}
}
