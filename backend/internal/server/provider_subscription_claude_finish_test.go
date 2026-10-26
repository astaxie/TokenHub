package server

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestClaudeSubscriptionPreservesFinishReason(t *testing.T) {
	for _, source := range []string{"result", "stream", "assistant"} {
		for _, test := range []struct{ reason, want string }{
			{"max_tokens", "length"},
			{"model_context_window_exceeded", "length"},
			{"refusal", "content_filter"},
			{"end_turn", "stop"},
		} {
			for _, stream := range []bool{false, true} {
				name := source + "/" + test.reason + "/" + map[bool]string{false: "chat", true: "stream"}[stream]
				t.Run(name, func(t *testing.T) {
					adapter, provider, ctx := claudeSubscriptionFixture(t)
					adapter.ExecutableArgs = []string{"-test.run=^TestClaudeSubscriptionFinishHelperProcess$", "--", "tokenhub-claude-finish-helper"}
					request := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Write a story"}}}
					var output bytes.Buffer
					var err error
					if stream {
						_, err = adapter.ChatStream(ctx, provider, source+":"+test.reason, request, &output)
					} else {
						var response any
						response, _, err = adapter.Chat(ctx, provider, source+":"+test.reason, request)
						if err == nil {
							err = json.NewEncoder(&output).Encode(response)
						}
					}
					if err != nil || !strings.Contains(output.String(), `"finish_reason":"`+test.want+`"`) {
						t.Fatalf("finish reason = %s, error = %v; want %s", output.String(), err, test.want)
					}
				})
			}
		}
	}
}

func TestClaudeSubscriptionFinishHelperProcess(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "tokenhub-claude-finish-helper") {
		return
	}
	var model string
	for i, arg := range os.Args {
		if arg == "--model" && i+1 < len(os.Args) {
			model = os.Args[i+1]
		}
	}
	source, reason, ok := strings.Cut(model, ":")
	var input any
	if !ok || json.NewDecoder(os.Stdin).Decode(&input) != nil {
		os.Exit(12)
	}
	emit := func(value any) {
		if json.NewEncoder(os.Stdout).Encode(value) != nil {
			os.Exit(12)
		}
	}
	emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start", "message": map[string]any{"id": "reply"}}})
	message := map[string]any{"id": "reply", "content": []any{map[string]any{"type": "text", "text": "A story"}}}
	delta := map[string]any{}
	result := map[string]any{"type": "result", "is_error": false}
	switch source {
	case "result":
		result["stop_reason"] = reason
	case "stream":
		delta["stop_reason"] = reason
	case "assistant":
		message["stop_reason"] = reason
	}
	emit(map[string]any{"type": "assistant", "message": message})
	emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_delta", "delta": delta}})
	emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_stop"}})
	emit(result)
	os.Exit(0)
}
