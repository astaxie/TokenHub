package server

import (
	"strings"
	"testing"
)

func TestAnthropicReasoningEffortUsesProviderMapping(t *testing.T) {
	provider := Provider{Options: map[string]string{
		reasoningEffortMapOption:         `{"minimal":"low","xhigh":"max"}`,
		reasoningBudgetMapOption:         `{"1024":"minimal","8192":"medium","*":"xhigh"}`,
		reasoningEffortValuesOption:      "none,low,medium,high,max",
		reasoningEffortUnsupportedOption: "reject",
	}}
	req := anthropicMessagesRequest{Raw: map[string]any{
		"thinking": map[string]any{"type": "enabled", "budget_tokens": float64(32000)},
	}}
	converted, err := anthropicToOpenAIChatRequest(req, provider)
	if err != nil {
		t.Fatal(err)
	}
	if converted.ReasoningEffort == nil || *converted.ReasoningEffort != "max" {
		t.Fatalf("reasoning_effort = %v, want max", converted.ReasoningEffort)
	}
}

func TestAnthropicDisabledThinkingCanOmitUnsupportedEffort(t *testing.T) {
	provider := Provider{Options: map[string]string{
		reasoningEffortValuesOption:      "low,medium,high,max",
		reasoningEffortUnsupportedOption: "omit",
	}}
	req := anthropicMessagesRequest{Raw: map[string]any{
		"thinking": map[string]any{"type": "disabled"},
	}}
	converted, err := anthropicToOpenAIChatRequest(req, provider)
	if err != nil {
		t.Fatal(err)
	}
	if converted.ReasoningEffort != nil {
		t.Fatalf("reasoning_effort = %v, want omitted", *converted.ReasoningEffort)
	}
}

func TestAnthropicReasoningAllowlistMissDefaultsToOmit(t *testing.T) {
	provider := Provider{Options: map[string]string{
		reasoningEffortValuesOption: "none,low,medium,high,max",
	}}
	req := anthropicMessagesRequest{Raw: map[string]any{
		"output_config": map[string]any{"effort": "xhigh"},
	}}
	converted, err := anthropicToOpenAIChatRequest(req, provider)
	if err != nil {
		t.Fatal(err)
	}
	if converted.ReasoningEffort != nil {
		t.Fatalf("reasoning_effort = %q, want omitted", *converted.ReasoningEffort)
	}
}

func TestAnthropicReasoningAllowlistMissCanExplicitlyPassthrough(t *testing.T) {
	provider := Provider{Options: map[string]string{
		reasoningEffortValuesOption:      "none,low,medium,high,max",
		reasoningEffortUnsupportedOption: "passthrough",
	}}
	req := anthropicMessagesRequest{Raw: map[string]any{
		"output_config": map[string]any{"effort": "xhigh"},
	}}
	converted, err := anthropicToOpenAIChatRequest(req, provider)
	if err != nil {
		t.Fatal(err)
	}
	if converted.ReasoningEffort == nil || *converted.ReasoningEffort != "xhigh" {
		t.Fatalf("reasoning_effort = %v, want xhigh", converted.ReasoningEffort)
	}
}

func TestOpenAIReasoningResponseBecomesReplayableAnthropicThinking(t *testing.T) {
	provider := Provider{ID: "provider-1", Type: ProviderOpenAICompatible, Options: map[string]string{
		reasoningContentOption: "true",
	}}
	body := map[string]any{
		"id": "chatcmpl-1",
		"choices": []any{map[string]any{
			"message": map[string]any{
				"reasoning_content": "inspect the repository",
				"content":           "done",
			},
			"finish_reason": "stop",
		}},
	}
	converted, err := openAIResponseToAnthropic(body, "claude-test", Usage{}, provider)
	if err != nil {
		t.Fatal(err)
	}
	blocks := converted["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["type"] != "thinking" || blocks[1].(map[string]any)["type"] != "text" {
		t.Fatalf("unexpected content blocks: %#v", blocks)
	}
	thinking := blocks[0].(map[string]any)
	message := map[string]any{"role": "assistant", "content": blocks}
	replayed, err := anthropicMessageToOpenAI(message, provider)
	if err != nil {
		t.Fatal(err)
	}
	if replayed[0].ReasoningContent != thinking["thinking"] {
		t.Fatalf("replayed reasoning = %q, want %q", replayed[0].ReasoningContent, thinking["thinking"])
	}
}

func TestOpenAIReasoningStreamEmitsThinkingBeforeText(t *testing.T) {
	provider := Provider{ID: "provider-1", Type: ProviderOpenAICompatible}
	writer := &recordingWriter{}
	converter := newOpenAIAnthropicStreamConverter(writer, "claude-test", 3, provider)
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"plan\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	if _, err := converter.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	if err := converter.Finalize(Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}); err != nil {
		t.Fatal(err)
	}
	output := writer.builder.String()
	wants := []string{`"type":"thinking"`, `"type":"thinking_delta"`, `"type":"signature_delta"`, `"type":"text"`, `"type":"text_delta"`}
	last := -1
	for _, want := range wants {
		index := strings.Index(output, want)
		if index <= last {
			t.Fatalf("event %s is missing or out of order:\n%s", want, output)
		}
		last = index
	}
}

func TestOpenAIReasoningStreamOrdersThinkingTextAndToolUse(t *testing.T) {
	provider := Provider{ID: "provider-1", Type: ProviderOpenAICompatible}
	writer := &recordingWriter{}
	converter := newOpenAIAnthropicStreamConverter(writer, "claude-test", 3, provider)
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"plan\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"calling\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"
	if _, err := converter.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	if err := converter.Finalize(Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}); err != nil {
		t.Fatal(err)
	}
	output := writer.builder.String()
	wants := []string{`"type":"thinking"`, `"type":"text"`, `"type":"tool_use"`, `"stop_reason":"tool_use"`}
	last := -1
	for _, want := range wants {
		index := strings.Index(output, want)
		if index <= last {
			t.Fatalf("block %s is missing or out of order:\n%s", want, output)
		}
		last = index
	}
}

func TestAnthropicThinkingRejectsForeignReplaySignature(t *testing.T) {
	provider := Provider{ID: "provider-1", Type: ProviderOpenAICompatible, Options: map[string]string{reasoningContentOption: "true"}}
	message := map[string]any{"role": "assistant", "content": []any{map[string]any{
		"type": "thinking", "thinking": "private chain", "signature": gatewayReasoningSignature(Provider{ID: "provider-2"}, "private chain"),
	}}}
	converted, err := anthropicMessageToOpenAI(message, provider)
	if err != nil {
		t.Fatal(err)
	}
	if converted[0].ReasoningContent != "" {
		t.Fatalf("foreign reasoning was replayed: %q", converted[0].ReasoningContent)
	}
}

func TestNativeAnthropicFallbackStripsOpenAICompatibleReplayMarker(t *testing.T) {
	provider := Provider{ID: "provider-openai-compatible"}
	raw := map[string]any{"messages": []any{map[string]any{
		"role": "assistant",
		"content": []any{
			map[string]any{
				"type":      "thinking",
				"thinking":  "private chain",
				"signature": gatewayReasoningSignature(provider, "private chain"),
			},
			map[string]any{"type": "text", "text": "portable answer"},
		},
	}}}
	payload := nativeAnthropicPayload(raw)
	messages, _ := anySlice(payload["messages"])
	message := messages[0].(map[string]any)
	content, _ := anySlice(message["content"])
	if len(content) != 1 || content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("native fallback forwarded a foreign thinking marker: %#v", content)
	}
}

func TestOpenAIReasoningOnlyResponseDoesNotAddEmptyText(t *testing.T) {
	provider := Provider{ID: "provider-1"}
	body := map[string]any{"choices": []any{map[string]any{
		"message":       map[string]any{"reasoning_content": "only reasoning", "content": ""},
		"finish_reason": "length",
	}}}
	converted, err := openAIResponseToAnthropic(body, "claude-test", Usage{}, provider)
	if err != nil {
		t.Fatal(err)
	}
	blocks := converted["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["type"] != "thinking" {
		t.Fatalf("unexpected reasoning-only blocks: %#v", blocks)
	}
}
