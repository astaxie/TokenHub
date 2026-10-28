package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCopilotSubscriptionInitiatorDistinguishesAnthropicToolResults(t *testing.T) {
	for _, test := range []struct{ name, content, want string }{
		{"tool results", `[{"type":"tool_result","content":"done"},{"type":"tool_result","content":"done"}]`, "agent"},
		{"empty blocks", `[]`, "user"},
		{"empty string", `""`, "user"},
		{"mixed user text", `[{"type":"tool_result","content":"done"},{"type":"text","text":"Continue differently"}]`, "user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"messages":[{"role":"user","content":` + test.content + `}]}`)
			if got := copilotSubscriptionInitiator(body); got != test.want {
				t.Fatalf("initiator = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCopilotSubscriptionVisionRecognizesContentBlocks(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       bool
	}{
		{"chat image", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"test"}}]}]}`, true},
		{"response image", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"test"}]}]}`, true},
		{"response tool image", `{"input":[{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","image_url":"test"}]}]}`, true},
		{"anthropic tool image", `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"test"}}]}]}]}`, true},
		{"tool arguments", `{"input":[{"type":"function_call","arguments":{"type":"input_image"}}]}`, false},
		{"tool schema", `{"tools":[{"parameters":{"type":"image"}}]}`, false},
		{"quoted image text", `{"messages":[{"role":"user","content":"{\"type\":\"image\"}"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := copilotSubscriptionHasImage([]byte(test.body)); got != test.want {
				t.Fatalf("image detected=%t, want %t", got, test.want)
			}
		})
	}
}

func TestCopilotSubscriptionAnthropicHeadersAndResponsesTokenLimit(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name, endpoint, messages, initiator, vision string
			maxTokens, maxCompletionTokens, wantLimit   int
		}{
			{"tool continuation", "/v1/messages", `[{"role":"user","content":"Look up"},{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-1","content":"done"}]`, "agent", "", 0, 0, 0},
			{"image", "/v1/messages", `[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}}]}]`, "user", "true", 0, 0, 0},
			{"image text", "/v1/messages", `[{"role":"user","content":"Explain \"image_url\" and {\"type\":\"image\"}."}]`, "user", "", 0, 0, 0},
			{"max tokens", "/responses", `[{"role":"user","content":"Hello"}]`, "user", "", 128, 0, 128},
			{"larger completion limit", "/responses", `[{"role":"user","content":"Hello"}]`, "user", "", 64, 128, 128},
			{"larger legacy limit", "/responses", `[{"role":"user","content":"Hello"}]`, "user", "", 128, 64, 128},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
						return
					case "/models":
						_, _ = fmt.Fprintf(w, `{"data":[{"id":"test-model","supported_endpoints":[%q],"capabilities":{"type":"chat"}}]}`, test.endpoint)
						return
					}
					if r.URL.Path != test.endpoint {
						t.Errorf("unexpected endpoint %s", r.URL.Path)
					}
					if got := r.Header.Get("X-Initiator"); got != test.initiator {
						t.Errorf("X-Initiator = %q, want %q", got, test.initiator)
					}
					if got := r.Header.Get("Copilot-Vision-Request"); got != test.vision {
						t.Errorf("Copilot-Vision-Request = %q, want %q", got, test.vision)
					}
					var body struct {
						MaxTokens int `json:"max_output_tokens"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.MaxTokens != test.wantLimit {
						t.Errorf("max_output_tokens = %d, want %d", body.MaxTokens, test.wantLimit)
					}
					if test.endpoint == "/responses" {
						response := `{"id":"resp-1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`
						if stream {
							_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
						} else {
							_, _ = fmt.Fprint(w, response)
						}
						return
					}
					if stream {
						_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
					} else {
						_, _ = fmt.Fprint(w, `{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`)
					}
				})
				var request ChatCompletionRequest
				requestJSON := fmt.Sprintf(`{"model":"public","messages":%s,"max_tokens":%d,"max_completion_tokens":%d}`, test.messages, test.maxTokens, test.maxCompletionTokens)
				if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
					t.Fatal(err)
				}
				var err error
				if stream {
					var output bytes.Buffer
					_, err = adapter.ChatStream(context.Background(), provider, "test-model", request, &output)
				} else {
					_, _, err = adapter.Chat(context.Background(), provider, "test-model", request)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
