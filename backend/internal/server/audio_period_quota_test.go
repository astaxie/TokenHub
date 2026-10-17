package server

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTranscriptionTokenEvidenceEnforcesAPIKeyPeriodQuotas(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		wantTotal int64
	}{
		{"details only", `{"text":"fixture","usage":{"input_token_details":{"audio_tokens":200}}}`, 0},
		{"explicit total with overlapping details", `{"text":"fixture","usage":{"input_tokens":200,"total_tokens":200,"input_token_details":{"audio_tokens":200}}}`, 200},
	} {
		for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
			for _, period := range []string{"daily", "monthly"} {
				for _, stream := range []bool{false, true} {
					t.Run(tc.name+path+"/"+period+map[bool]string{false: "/JSON", true: "/SSE"}[stream], func(t *testing.T) {
						body, contentType := tc.body, "application/json"
						if stream {
							body, contentType = "data: "+body+"\n\n", "text/event-stream"
						}
						var calls atomic.Int32
						app, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
							calls.Add(1)
							w.Header().Set("Content-Type", contentType)
							_, _ = io.WriteString(w, body)
						}, "audio")
						limits := map[string]any{"token_limit_tpm": int64(1_000_000), "limit_" + period + "_tokens": int64(200)}
						if err := store.db.Model(&APIKey{}).Where("name = ?", "Media key").Updates(limits).Error; err != nil {
							t.Fatal(err)
						}
						response := doJSON(t, app.Handler(), http.MethodPost, path, map[string]any{"model": "public-media", "stream": stream}, key)
						if response.Code != http.StatusOK || response.Body != body {
							t.Fatalf("transcription delivery changed: %d %s", response.Code, response.Body)
						}
						var counters []QuotaBucket
						if err := store.db.Where("scope IN ?", []string{"day", "month", "minute"}).Find(&counters).Error; err != nil {
							t.Fatal(err)
						}
						if len(counters) != 3 {
							t.Fatalf("quota bucket count=%d want=3", len(counters))
						}
						for _, counter := range counters {
							if counter.TotalTokens != 200 {
								t.Fatalf("%s quota tokens=%d want=200", counter.Scope, counter.TotalTokens)
							}
						}
						rows := store.ListUsageRecords()
						if len(rows) != 1 || rows[0].InputAudioTokens != 200 || rows[0].TotalTokens != tc.wantTotal || rows[0].CostUSD != 0 || rows[0].ProviderCostUSD != 0 {
							t.Fatalf("provider usage evidence or billing changed: %+v", rows)
						}
						response = doJSON(t, app.Handler(), http.MethodPost, path, map[string]any{"model": "public-media", "stream": stream}, key)
						if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body, `"code":"quota_exceeded"`) || !strings.Contains(response.Body, `"scope":"api_key"`) || calls.Load() != 1 {
							t.Fatalf("exhausted %s token quota was bypassed: status=%d upstream calls=%d body=%s", period, response.Code, calls.Load(), response.Body)
						}
					})
				}
			}
		}
	}
}

func TestAPIKeyPeriodUsageKeepsProviderEvidenceSeparateFromReservations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage Usage
		want  int64
	}{
		{"details without a total", Usage{InputAudioTokens: 200, RateLimitTokens: 900}, 200},
		{"explicit total takes precedence", Usage{TotalTokens: 200, PromptTokens: 150, CompletionTokens: 50, InputAudioTokens: 140, OutputAudioTokens: 40, RateLimitTokens: 900}, 200},
		{"primary counters include details", Usage{PromptTokens: 150, CompletionTokens: 50, InputAudioTokens: 140, OutputAudioTokens: 40, RateLimitTokens: 900}, 200},
		{"independent audio details", Usage{InputAudioTokens: 150, OutputAudioTokens: 50, RateLimitTokens: 900}, 200},
		{"no token evidence", Usage{RateLimitTokens: 900}, 0},
	} {
		for _, protocol := range []string{"audio/transcriptions", "audio/translations"} {
			t.Run(tc.name+"/"+protocol, func(t *testing.T) {
				call := CallContext{RouteProtocol: protocol, ReservedTokens: 1_000, StreamOutputCommitted: true}
				usage := tc.usage
				usage.CostUSD, usage.ProviderCostUSD = .25, .10
				want := usage
				want.TotalTokens = tc.want
				if got := apiKeyPeriodUsage(call, usage); !reflect.DeepEqual(got, want) {
					t.Fatalf("period quota included estimates or changed independent usage: got=%+v want=%+v", got, want)
				}
			})
		}
	}
}

func TestAPIKeyPeriodUsageLeavesOtherEndpointsUnchanged(t *testing.T) {
	usage := Usage{PromptTokens: 30, InputAudioTokens: 200, RateLimitTokens: 900, CostUSD: .25, ProviderCostUSD: .10}
	for _, protocol := range []string{"", "chat/completions", "responses", "audio/speech", "images/generations", "embeddings", "rerank"} {
		t.Run(protocol, func(t *testing.T) {
			call := CallContext{RouteProtocol: protocol, ReservedTokens: 1_000, StreamOutputCommitted: true}
			if got := apiKeyPeriodUsage(call, usage); !reflect.DeepEqual(got, usage) {
				t.Fatalf("unrelated endpoint quota semantics changed: got=%+v want=%+v", got, usage)
			}
		})
	}
}
