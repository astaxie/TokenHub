package server

import (
	"slices"
	"strings"
	"testing"
)

func TestDecodeAnthropicTurnEventSequence(t *testing.T) {
	sse := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_d1","model":"upstream-model","usage":{"input_tokens":5}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_time"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"tz\""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":":\"utc\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	events := decodeAnthropicTurn("claude-fake", strings.NewReader(sse))
	kinds := make([]anthropicTurnEventKind, 0, len(events))
	var textParts []string
	var arguments strings.Builder
	for _, event := range events {
		kinds = append(kinds, event.Kind)
		switch event.Kind {
		case anthropicTurnTextDelta:
			textParts = append(textParts, event.Text)
		case anthropicTurnToolArguments:
			arguments.WriteString(event.Arguments)
		case anthropicTurnToolFinished:
			if event.CallID != "toolu_1" || event.ToolName != "get_time" {
				t.Fatalf("tool finished = %v", event)
			}
			if event.Arguments != `{"tz":"utc"}` {
				t.Fatalf("split arguments did not reassemble: %q", event.Arguments)
			}
		case anthropicTurnFinished:
			if event.StopReason != "tool_use" || event.InputTokens != 5 || event.OutputTokens != 7 {
				t.Fatalf("finish = %v", event)
			}
			if event.ResponseID != "resp_d1" {
				t.Fatalf("response id = %q, want resp_d1", event.ResponseID)
			}
		}
	}
	want := []anthropicTurnEventKind{
		anthropicTurnStarted, anthropicTurnTextDelta, anthropicTurnToolOpened,
		anthropicTurnToolArguments, anthropicTurnToolArguments, anthropicTurnToolFinished,
		anthropicTurnFinished,
	}
	if !slices.Equal(kinds, want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	if strings.Join(textParts, "") != "你好" {
		t.Fatalf("text deltas = %v", textParts)
	}
	if arguments.String() != `{"tz":"utc"}` {
		t.Fatalf("arguments deltas = %q", arguments.String())
	}
}
