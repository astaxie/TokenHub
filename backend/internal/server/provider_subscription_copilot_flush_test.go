package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type copilotSubscriptionFlushWriter struct {
	bytes.Buffer
	flushed chan string
}

func (w *copilotSubscriptionFlushWriter) Flush() {
	w.flushed <- w.String()
}

func TestCopilotSubscriptionChatFlushesBeforeUpstreamCompletes(t *testing.T) {
	for _, protocol := range []struct {
		endpoint string
		initial  string
		terminal string
	}{
		{
			endpoint: "/chat/completions",
			initial:  "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n",
			terminal: "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\ndata: [DONE]\n\n",
		},
		{
			endpoint: "/responses",
			initial:  "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n",
			terminal: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n",
		},
		{
			endpoint: "/v1/messages",
			initial:  "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
			terminal: "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
		},
	} {
		for _, model := range []string{"selected", "auto"} {
			t.Run(protocol.endpoint+"/"+model, func(t *testing.T) {
				release := make(chan struct{})
				adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					case "/models":
						_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "selected", "supported_endpoints": []string{protocol.endpoint}}}})
					case "/auto":
						_ = json.NewEncoder(w).Encode(map[string]any{"session_token": "auto-session", "selected_model": map[string]any{"id": "selected", "supported_endpoints": []string{protocol.endpoint}}})
					case protocol.endpoint:
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, protocol.initial)
						w.(http.Flusher).Flush()
						select {
						case <-release:
							_, _ = io.WriteString(w, protocol.terminal)
						case <-r.Context().Done():
						}
					default:
						t.Errorf("unexpected upstream endpoint: %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				})
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				output := &copilotSubscriptionFlushWriter{flushed: make(chan string, 32)}
				completed := make(chan error, 1)
				go func() {
					usage, err := adapter.ChatStream(ctx, provider, model, ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}, output)
					if err == nil && usage.TotalTokens != 6 {
						err = fmt.Errorf("stream usage changed: %+v", usage)
					}
					completed <- err
				}()
				timer := time.NewTimer(time.Second)
				defer timer.Stop()
				flushedText := false
			waitForText:
				for {
					select {
					case text := <-output.flushed:
						if strings.Contains(text, "hello") {
							flushedText = true
							break waitForText
						}
					case <-timer.C:
						break waitForText
					}
				}
				close(release)
				if err := <-completed; err != nil {
					t.Fatal(err)
				}
				if !flushedText {
					t.Fatal("stream text was not flushed before the upstream terminal event")
				}
			})
		}
	}
}
