package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAnthropicMessagesAcceptsSystemWithoutLegacyBeta(t *testing.T) {
	for _, beta := range []string{"", "claude-code-20250219,interleaved-thinking-2025-05-14,mid-conversation-tool-changes-2026-07-01", anthropicMidConversationSystemBeta} {
		for _, provider := range []string{ProviderAnthropic, ProviderOpenAICompatible} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/beta=%s/stream=%t", provider, beta, stream), func(t *testing.T) {
					system := []any{map[string]any{"type": "text", "text": "Top-level."}}
					messages := []any{
						map[string]any{"role": "user", "content": "First question."},
						map[string]any{"role": "system", "content": []any{map[string]any{
							"type": "text", "text": "Mid-conversation.", "cache_control": map[string]any{"type": "ephemeral"},
						}}},
						map[string]any{"role": "assistant", "content": "First answer."},
						map[string]any{"role": "user", "content": "Second question."},
						map[string]any{"role": "system", "content": "Latest."},
					}
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var payload map[string]any
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							t.Errorf("decode upstream request: %v", err)
							http.Error(w, "invalid request", http.StatusBadRequest)
							return
						}
						if provider == ProviderAnthropic {
							if !reflect.DeepEqual(payload["messages"], messages) || !reflect.DeepEqual(payload["system"], system) {
								t.Errorf("native system content or message order changed: %#v", payload)
							}
							if r.Header.Get("anthropic-beta") != beta {
								t.Errorf("native beta = %q, want %q", r.Header.Get("anthropic-beta"), beta)
							}
						} else {
							converted, _ := payload["messages"].([]any)
							if len(converted) != 6 {
								t.Errorf("converted messages = %#v", converted)
								http.Error(w, "unexpected message count", http.StatusBadRequest)
								return
							}
							for i, expectedRole := range []string{"system", "user", "system", "assistant", "user", "system"} {
								message, _ := converted[i].(map[string]any)
								if message["role"] != expectedRole {
									t.Errorf("message %d role = %v, want %s", i, message["role"], expectedRole)
								}
							}
							for i, expectedText := range map[int]string{0: "Top-level.", 2: "Mid-conversation.", 5: "Latest."} {
								message, _ := converted[i].(map[string]any)
								if message["content"] != expectedText {
									t.Errorf("system message %d = %#v, want %q", i, message, expectedText)
								}
							}
						}
						if stream {
							w.Header().Set("content-type", "text/event-stream")
							if provider == ProviderAnthropic {
								_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_system\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"upstream-model\",\"content\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n")
								_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
								_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
								_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
								_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
								_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
							} else {
								_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_system\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
								_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_system\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n\n")
								_, _ = io.WriteString(w, "data: [DONE]\n\n")
							}
							return
						}
						w.Header().Set("content-type", "application/json")
						if provider == ProviderAnthropic {
							_, _ = io.WriteString(w, `{"id":"msg_system","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`)
						} else {
							_, _ = io.WriteString(w, `{"id":"chatcmpl_system","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`)
						}
					}))
					defer upstream.Close()
					handler, _, secret := newAnthropicGateway(t, upstream.URL, provider)
					resp := doAnthropicRequestWithBeta(t, handler, "/v1/messages", map[string]any{
						"model": "claude-tokenhub-test", "max_tokens": 32, "stream": stream,
						"system": system, "messages": messages,
					}, "Bearer "+secret, "", beta)
					if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"text":"ok"`) {
						t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
					}
				})
			}
		}
	}
}

func TestAnthropicCountTokensAcceptsSystemWithoutLegacyBeta(t *testing.T) {
	handler, store, secret := newAnthropicGateway(t, "http://127.0.0.1:1", ProviderOpenAICompatible)
	resp := doAnthropicRequest(t, handler, "/v1/messages/count_tokens", map[string]any{
		"model": "claude-tokenhub-test",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello."},
			map[string]any{"role": "system", "content": "Reply briefly."},
		},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"input_tokens":`) {
		t.Fatalf("response = %d %s", resp.Code, resp.Body.String())
	}
	if len(store.ListUsageRecords()) != 0 {
		t.Fatal("token counting must not create billed usage")
	}
}
