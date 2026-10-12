package server

import (
	"io"
	"net/http"
	"testing"
)

// Transcriptions use input_token_details (singular), unlike Chat/Responses.
func TestMediaTranscriptionPreservesAudioTokenBreakdown(t *testing.T) {
	const payload = `{"text":"fixture","usage":{"type":"tokens","input_tokens":80,"input_token_details":{"audio_tokens":75,"text_tokens":5},"output_tokens":20,"total_tokens":100}}`
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "SSE"}[stream], func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: transcript.text.done\ndata: "+payload+"\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, payload)
			}, "audio")
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": stream}, key)
			if response.Code != http.StatusOK {
				t.Fatalf("transcription failed: %d %s", response.Code, response.Body)
			}
			records := store.ListUsageRecords()
			if len(records) != 1 {
				t.Fatalf("usage records=%d, want 1", len(records))
			}
			if got := records[0]; got.InputTokens != 80 || got.InputAudioTokens != 75 || got.OutputTokens != 20 || got.TotalTokens != 100 {
				t.Fatalf("transcription token breakdown lost: %+v", got)
			}
		})
	}
}

func TestMediaTranscriptionDurationDoesNotInventTokens(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"fixture","usage":{"type":"duration","seconds":30}}`)
	}, "audio")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
	if response.Code != http.StatusOK || len(store.ListRequestLogs()) != 1 {
		t.Fatalf("duration transcription failed: %d %s", response.Code, response.Body)
	}
	for _, usage := range store.ListUsageRecords() {
		if usage.TotalTokens != 0 || usage.InputAudioTokens != 0 || usage.CostUSD != 0 {
			t.Fatalf("duration was converted into token usage: %+v", usage)
		}
	}
}
