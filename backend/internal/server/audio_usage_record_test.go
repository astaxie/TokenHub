package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTranscriptionDetailOnlyUsageIsDurable(t *testing.T) {
	for _, field := range []string{
		"input_audio_tokens", "output_audio_tokens", "reasoning_output_tokens",
		"accepted_prediction_tokens", "rejected_prediction_tokens",
	} {
		for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
			for _, stream := range []bool{false, true} {
				t.Run(field+path+map[bool]string{false: "/JSON", true: "/SSE"}[stream], func(t *testing.T) {
					const privateText = "private-synthetic-transcript"
					body := `{"text":"` + privateText + `","usage":{"` + field + `":7}}`
					contentType := "application/json"
					if stream {
						body, contentType = "data: "+body+"\n\n", "text/event-stream"
					}
					app, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", contentType)
						_, _ = io.WriteString(w, body)
					}, "audio")
					app.store = audioNoPayloadStore{GormStore: store}
					response := doJSON(t, app.Handler(), http.MethodPost, path, map[string]any{"model": "public-media", "stream": stream}, key)
					if response.Code != http.StatusOK || response.Body != body {
						t.Fatalf("transcription delivery changed: %d %s", response.Code, response.Body)
					}
					rows := store.ListUsageRecords()
					if len(rows) != 1 || rows[0].TotalTokens != 0 || rows[0].CostUSD != 0 || rows[0].ProviderCostUSD != 0 {
						t.Fatalf("missing detail record or invented usage: %+v", rows)
					}
					audit := doJSON(t, app.Handler(), http.MethodGet, "/api/admin/audit/requests", nil, "media-admin")
					var page struct {
						Data []map[string]any `json:"data"`
					}
					if audit.Code != http.StatusOK || json.Unmarshal([]byte(audit.Body), &page) != nil || len(page.Data) != 1 {
						t.Fatalf("request usage unavailable: %d %s", audit.Code, audit.Body)
					}
					if page.Data[0][field] != float64(7) || page.Data[0]["usage_record_count"] != float64(1) {
						t.Fatalf("detail missing from request audit: %s", audit.Body)
					}
					if strings.Contains(audit.Body, privateText) {
						t.Fatalf("transcript appeared in request audit: %s", audit.Body)
					}
					var payloadCount int64
					if err := store.db.Model(&RequestPayloadLog{}).Count(&payloadCount).Error; err != nil || payloadCount != 0 {
						t.Fatalf("usage retention enabled payload storage: count=%d error=%v", payloadCount, err)
					}
				})
			}
		}
	}
}
