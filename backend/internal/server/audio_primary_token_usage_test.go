package server

import (
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestTranscriptionStreamPreservesPrimaryTokenObservations(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		events               []string
		input, output, total int64
	}{
		{"input then output", []string{`{"input_tokens":20}`, `{"output_tokens":30}`}, 20, 30, 50},
		{"output then input", []string{`{"output_tokens":30}`, `{"input_tokens":20}`}, 20, 30, 50},
		{"prompt then completion", []string{`{"prompt_tokens":20}`, `{"completion_tokens":30}`}, 20, 30, 50},
		{"completion then prompt", []string{`{"completion_tokens":30}`, `{"prompt_tokens":20}`}, 20, 30, 50},
		{"latest input is cumulative", []string{`{"input_tokens":20,"output_tokens":30}`, `{"prompt_tokens":25}`}, 25, 30, 55},
		{"input zero preserves output", []string{`{"input_tokens":20,"output_tokens":30}`, `{"prompt_tokens":0}`}, 0, 30, 30},
		{"output zero preserves input", []string{`{"input_tokens":20,"output_tokens":30}`, `{"completion_tokens":0}`}, 20, 0, 20},
		{"null observations are absent", []string{`{"input_tokens":20,"output_tokens":30}`, `{"prompt_tokens":null,"completion_tokens":null,"total_tokens":null}`}, 20, 30, 50},
		{"primary aliases take precedence", []string{`{"prompt_tokens":2,"input_tokens":20,"completion_tokens":3,"output_tokens":30}`}, 2, 3, 5},
		{"zero input alias takes precedence", []string{`{"prompt_tokens":0,"input_tokens":20,"output_tokens":30}`}, 0, 30, 30},
		{"zero completion alias retains media fallback", []string{`{"input_tokens":20}`, `{"completion_tokens":0,"output_tokens":30}`}, 20, 30, 50},
		{"total before partial counters", []string{`{"total_tokens":99}`, `{"input_tokens":20}`, `{"output_tokens":30}`}, 20, 30, 99},
		{"total after partial counters", []string{`{"input_tokens":20}`, `{"output_tokens":30}`, `{"total_tokens":99}`}, 20, 30, 99},
		{"zero total before partial counters", []string{`{"total_tokens":0}`, `{"input_tokens":20}`, `{"output_tokens":30}`}, 20, 30, 0},
		{"zero total after partial counters", []string{`{"input_tokens":20}`, `{"output_tokens":30}`, `{"total_tokens":0}`}, 20, 30, 0},
		{"latest explicit total wins", []string{`{"input_tokens":20,"output_tokens":30,"total_tokens":99}`, `{"total_tokens":0}`, `{"output_tokens":35}`, `{"total_tokens":42}`}, 20, 35, 42},
		{"auxiliary details do not create totals", []string{`{"input_audio_tokens":20}`, `{"output_audio_tokens":30}`}, 0, 0, 0},
		{"auxiliary details do not expand totals", []string{`{"input_tokens":20}`, `{"output_audio_tokens":30}`, `{"type":"tokens"}`}, 20, 0, 20},
		{"derived total saturates", []string{`{"input_tokens":9223372036854775807}`, `{"output_tokens":1}`}, math.MaxInt64, 1, math.MaxInt64},
	} {
		for _, nested := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/outer", true: "/nested"}[nested], func(t *testing.T) {
				var stream strings.Builder
				for _, reported := range tc.events {
					event := `{"usage":` + reported + `}`
					if nested {
						event = `{"response":` + event + `}`
					}
					stream.WriteString("data: " + event + "\n\n")
				}
				usage, err := inspectMediaStream([]byte(stream.String()), Provider{}, "/audio/transcriptions")
				if err != nil || usage.PromptTokens != tc.input || usage.CompletionTokens != tc.output || usage.TotalTokens != tc.total {
					t.Fatalf("primary observations changed: usage=%+v want input=%d output=%d total=%d error=%v", usage, tc.input, tc.output, tc.total, err)
				}
			})
		}
	}
}

func TestTranscriptionPrimaryTokensCombineEnvelopeObservations(t *testing.T) {
	for _, tc := range []struct {
		name, event          string
		input, output, total int64
	}{
		{"outer input and nested output", `{"usage":{"input_tokens":20},"response":{"usage":{"output_tokens":30}}}`, 20, 30, 50},
		{"outer output and nested input", `{"usage":{"output_tokens":30},"response":{"usage":{"input_tokens":20}}}`, 20, 30, 50},
		{"outer zero input retains nested output", `{"usage":{"prompt_tokens":0},"response":{"usage":{"input_tokens":20,"output_tokens":30}}}`, 0, 30, 30},
		{"nested total retains authority", `{"usage":{"input_tokens":20},"response":{"usage":{"output_tokens":30,"total_tokens":99}}}`, 20, 30, 99},
		{"outer total retains nested counters", `{"usage":{"total_tokens":99},"response":{"usage":{"input_tokens":20,"output_tokens":30}}}`, 20, 30, 99},
		{"outer zero total takes precedence", `{"usage":{"total_tokens":0},"response":{"usage":{"input_tokens":20,"output_tokens":30,"total_tokens":99}}}`, 20, 30, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, err := inspectMediaStream([]byte("data: "+tc.event+"\n\n"), Provider{}, "/audio/transcriptions")
			if err != nil || usage.PromptTokens != tc.input || usage.CompletionTokens != tc.output || usage.TotalTokens != tc.total {
				t.Fatalf("envelope observations changed: usage=%+v want input=%d output=%d total=%d error=%v", usage, tc.input, tc.output, tc.total, err)
			}
		})
	}
}

func TestTranscriptionPartialPrimaryTokensPersistBillingAndQuota(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		total, quota int64
	}{
		{"derived total", "data: {\"usage\":{\"input_tokens\":20}}\n\ndata: {\"usage\":{\"output_tokens\":30}}\n\n", 50, 50},
		{"explicit total", "data: {\"usage\":{\"total_tokens\":99}}\n\ndata: {\"usage\":{\"completion_tokens\":30}}\n\ndata: {\"usage\":{\"prompt_tokens\":20}}\n\n", 99, 99},
		// Shared pricing and quota accounting retain their zero-total fallback.
		{"explicit zero total", "data: {\"usage\":{\"input_tokens\":20}}\n\ndata: {\"usage\":{\"output_tokens\":30}}\n\ndata: {\"usage\":{\"total_tokens\":0}}\n\n", 50, 50},
		{"split envelope", "data: {\"usage\":{\"input_tokens\":20},\"response\":{\"usage\":{\"output_tokens\":30}}}\n\n", 50, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.stream)
			}, "audio")
			store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, InputPriceUSDPer1M: 1, OutputPriceUSDPer1M: 2})
			store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", InputPriceUSDPer1M: 3, OutputPriceUSDPer1M: 4})
			if err := store.db.Model(&APIKey{}).Where("name = ?", "Media key").Update("token_limit_tpm", 1_000_000).Error; err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": true}, key)
			if response.Code != http.StatusOK || response.Body != tc.stream {
				t.Fatalf("stream delivery changed: status=%d", response.Code)
			}
			rows := store.ListUsageRecords()
			if len(rows) != 1 || rows[0].InputTokens != 20 || rows[0].OutputTokens != 30 || rows[0].TotalTokens != tc.total || math.Abs(rows[0].CostUSD-.00008) > 1e-12 || math.Abs(rows[0].ProviderCostUSD-.00018) > 1e-12 {
				t.Fatalf("primary usage or independent prices changed: %+v", rows)
			}
			var counters []struct{ TotalTokens int64 }
			if err := store.db.Table("quota_buckets").Where("scope = ?", "minute").Find(&counters).Error; err != nil {
				t.Fatal(err)
			}
			if len(counters) != 1 || counters[0].TotalTokens != tc.quota {
				t.Fatalf("primary token quota changed: counters=%+v want=%d", counters, tc.quota)
			}
		})
	}
}
