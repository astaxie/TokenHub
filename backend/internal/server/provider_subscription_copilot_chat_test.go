package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCopilotSubscriptionSelectsModelEndpointForChatAndStreaming(t *testing.T) {
	for _, endpoint := range []string{"/responses", "/v1/messages"} {
		for _, providerModel := range []string{"restricted-model", "auto"} {
			t.Run(endpoint+"/"+providerModel, func(t *testing.T) {
				var catalogs, calls atomic.Int32
				adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						_, _ = fmt.Fprintf(w, `{"token":"copilot-session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
						return
					case "/models":
						catalogs.Add(1)
						_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "restricted-model", "supported_endpoints": []string{endpoint}, "capabilities": map[string]any{"type": "chat"}}}})
						return
					case "/auto":
						_ = json.NewEncoder(w).Encode(map[string]any{"session_token": "auto-secret", "selected_model": map[string]any{"id": "restricted-model", "supported_endpoints": []string{endpoint}}})
						return
					}
					if providerModel == "auto" && r.Header.Get("Copilot-Session-Token") != "auto-secret" {
						t.Error("missing Auto session token")
					}
					if r.URL.Path != endpoint {
						t.Errorf("model called %s instead of %s", r.URL.Path, endpoint)
						w.WriteHeader(400)
						return
					}
					calls.Add(1)
					var request map[string]any
					if json.NewDecoder(r.Body).Decode(&request) != nil {
						t.Error("invalid request body")
						return
					}
					if request["model"] != "restricted-model" {
						t.Errorf("wrong provider model: %v", request["model"])
					}
					stream, _ := request["stream"].(bool)
					if endpoint == "/responses" {
						if request["input"] == nil || request["messages"] != nil {
							t.Error("chat request was not converted to Responses")
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n")
							return
						}
						_, _ = fmt.Fprint(w, `{"id":"resp-1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
						return
					}
					if request["messages"] == nil || r.Header.Get("Anthropic-Version") != "2023-06-01" {
						t.Error("chat request was not converted to Anthropic Messages")
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
						return
					}
					_, _ = fmt.Fprint(w, `{"id":"msg-1","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`)
				})
				request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
				body, usage, err := adapter.Chat(context.Background(), provider, providerModel, request)
				if err != nil || usage.TotalTokens != 6 {
					t.Fatalf("chat bridge failed: %+v %v", usage, err)
				}
				encoded, _ := json.Marshal(body)
				if !bytes.Contains(encoded, []byte("hello")) {
					t.Fatalf("chat text missing: %s", encoded)
				}
				var output bytes.Buffer
				usage, err = adapter.ChatStream(context.Background(), provider, providerModel, request, &output)
				if err != nil || usage.TotalTokens != 6 || !strings.Contains(output.String(), "hello") || !strings.Contains(output.String(), "[DONE]") {
					t.Fatalf("stream bridge failed: usage=%+v error=%v output=%s", usage, err, output.String())
				}
				if catalogs.Load() != 1 || calls.Load() != 2 {
					t.Fatalf("endpoint cache behavior changed: catalogs=%d calls=%d", catalogs.Load(), calls.Load())
				}
				probe, err := adapter.Probe(context.Background(), provider, ProviderResource{ID: "account"}, ProviderProbeRequest{Model: providerModel, Prompt: "Hello"})
				if err != nil || probe.OutputText != "hello" {
					t.Fatalf("probe did not extract the converted model output: %q, %v", probe.OutputText, err)
				}
			})
		}
	}
}
