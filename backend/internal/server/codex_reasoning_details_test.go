package server

import (
	"encoding/json"
	"testing"
)

func TestChatCodexReasoningContinuationReadsToolBoundDetail(t *testing.T) {
	message := ChatMessage{
		ToolCalls: []any{map[string]any{"id": "call_1"}},
		raw: map[string]json.RawMessage{
			"reasoning_details": json.RawMessage(`[{"type":"reasoning.encrypted","id":"call_1","data":"codex:opaque-state"}]`),
		},
	}

	encrypted, ok := chatCodexReasoningContinuation(message)
	if !ok || encrypted != "opaque-state" {
		t.Fatalf("reasoning continuation = %q, %v", encrypted, ok)
	}
}

func TestChatCodexReasoningContinuationRejectsUnboundOrForeignDetail(t *testing.T) {
	for _, details := range []string{
		`[{"type":"reasoning.encrypted","id":"call_other","data":"codex:opaque-state"}]`,
		`[{"type":"reasoning.encrypted","id":"call_1","data":"anthropic:opaque-state"}]`,
	} {
		message := ChatMessage{
			ToolCalls: []any{map[string]any{"id": "call_1"}},
			raw: map[string]json.RawMessage{
				"reasoning_details": json.RawMessage(details),
			},
		}
		if encrypted, ok := chatCodexReasoningContinuation(message); ok {
			t.Fatalf("accepted invalid reasoning continuation %q from %s", encrypted, details)
		}
	}
}
