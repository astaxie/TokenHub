package server

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func mustBuildAnthropic(t *testing.T, req ChatCompletionRequest) map[string]any {
	t.Helper()
	payload, err := buildAnthropicRequest("claude-test", req, nil)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	return payload
}

func TestAnthropicRequestMapsToolDefinitions(t *testing.T) {
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "weather?"}},
		Tools: []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Look up weather",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}},
	})

	tools, _ := payload["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("expected one tool, got %v", payload["tools"])
	}
	if tools[0]["name"] != "get_weather" {
		t.Fatalf("tool name was not forwarded: %v", tools[0])
	}
	if _, ok := tools[0]["input_schema"]; !ok {
		t.Fatalf("OpenAI parameters must map to Anthropic input_schema: %v", tools[0])
	}
}

func TestAnthropicRequestFillsMissingToolSchema(t *testing.T) {
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools: []any{map[string]any{
			"function": map[string]any{"name": "ping"},
		}},
	})
	tools, _ := payload["tools"].([]map[string]any)
	schema, _ := tools[0]["input_schema"].(map[string]any)
	// Anthropic rejects a null schema, so an empty object stands in.
	if schema["type"] != "object" {
		t.Fatalf("missing parameters must become an empty object schema, got %v", tools[0])
	}
}

func TestAnthropicRequestRejectsInvalidToolName(t *testing.T) {
	_, err := buildAnthropicRequest("claude-test", ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools: []any{map[string]any{
			"function": map[string]any{"name": "bad name!"},
		}},
	}, nil)
	if err == nil {
		t.Fatalf("expected a tool name outside the permitted character set to be rejected")
	}
}

func TestAnthropicToolChoiceNoneKeepsToolDefinitions(t *testing.T) {
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages:   []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:      []any{map[string]any{"function": map[string]any{"name": "ping"}}},
		ToolChoice: "none",
	})
	// Dropping the tools instead would change the tool system prompt, token
	// usage and prompt-cache prefix, so it is not an equivalent rewrite.
	if _, ok := payload["tools"]; !ok {
		t.Fatalf("tool_choice=none must keep the tool definitions in place")
	}
	choice, _ := payload["tool_choice"].(map[string]any)
	if choice["type"] != "none" {
		t.Fatalf("expected tool_choice type none, got %v", choice)
	}
}

func TestAnthropicToolChoiceVariants(t *testing.T) {
	cases := []struct {
		name     string
		choice   any
		wantType string
		wantName string
	}{
		{"auto", "auto", "auto", ""},
		{"required", "required", "any", ""},
		{"function", map[string]any{"type": "function", "function": map[string]any{"name": "ping"}}, "tool", "ping"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := mustBuildAnthropic(t, ChatCompletionRequest{
				Messages:   []ChatMessage{{Role: "user", Content: "hi"}},
				Tools:      []any{map[string]any{"function": map[string]any{"name": "ping"}}},
				ToolChoice: testCase.choice,
			})
			choice, _ := payload["tool_choice"].(map[string]any)
			if choice["type"] != testCase.wantType {
				t.Fatalf("expected type %q, got %v", testCase.wantType, choice)
			}
			if testCase.wantName != "" && choice["name"] != testCase.wantName {
				t.Fatalf("expected name %q, got %v", testCase.wantName, choice)
			}
		})
	}
}

func TestAnthropicToolChoiceRejectsUndeclaredFunction(t *testing.T) {
	_, err := buildAnthropicRequest("claude-test", ChatCompletionRequest{
		Messages:   []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:      []any{map[string]any{"function": map[string]any{"name": "ping"}}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "absent"}},
	}, nil)
	if err == nil {
		t.Fatalf("expected tool_choice naming an undeclared tool to be rejected")
	}
}

func TestAnthropicDisablesParallelToolUseInsideToolChoice(t *testing.T) {
	disabled := false
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages:          []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:             []any{map[string]any{"function": map[string]any{"name": "ping"}}},
		ParallelToolCalls: &disabled,
	})
	choice, _ := payload["tool_choice"].(map[string]any)
	// Anthropic expects this inside tool_choice, not at the request root.
	if choice["disable_parallel_tool_use"] != true {
		t.Fatalf("expected disable_parallel_tool_use inside tool_choice, got %v", choice)
	}
	if _, present := payload["disable_parallel_tool_use"]; present {
		t.Fatalf("disable_parallel_tool_use must not appear at the request root")
	}
}

func TestAnthropicRequestLiftsSystemToTopLevel(t *testing.T) {
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hi"},
		},
	})
	if _, ok := payload["system"]; !ok {
		t.Fatalf("system guidance must become the top-level system field")
	}
	messages, _ := payload["messages"].([]map[string]any)
	for _, message := range messages {
		if message["role"] == "system" {
			t.Fatalf("Anthropic messages must not contain a system role: %v", messages)
		}
	}
}

func TestAnthropicRequestConvertsToolCallsAndMergesResults(t *testing.T) {
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "weather in both cities?"},
			{Role: "assistant", Content: "checking", ToolCalls: []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"BJ"}`}},
				map[string]any{"id": "call_2", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"SH"}`}},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
			{Role: "tool", ToolCallID: "call_2", Content: "rainy"},
		},
	})

	messages, _ := payload["messages"].([]map[string]any)
	if len(messages) != 3 {
		t.Fatalf("expected user, assistant and a single merged tool-result turn, got %d", len(messages))
	}

	assistant := messages[1]
	blocks, _ := assistant["content"].([]map[string]any)
	if len(blocks) != 3 {
		t.Fatalf("assistant turn must keep its text block alongside both tool_use blocks: %v", blocks)
	}
	if blocks[0]["type"] != "text" {
		t.Fatalf("text must precede tool_use blocks: %v", blocks)
	}
	toolUse := blocks[1]
	input, _ := toolUse["input"].(map[string]any)
	// OpenAI encodes arguments as a JSON string; Anthropic needs a decoded object.
	if input["city"] != "BJ" {
		t.Fatalf("tool arguments must be decoded into an object, got %v", toolUse)
	}

	results := messages[2]
	if results["role"] != "user" {
		t.Fatalf("tool results must be delivered in a user turn: %v", results)
	}
	resultBlocks, _ := results["content"].([]map[string]any)
	if len(resultBlocks) != 2 {
		t.Fatalf("parallel tool results must be merged into one turn, got %d", len(resultBlocks))
	}
	if resultBlocks[0]["tool_use_id"] != "call_1" {
		t.Fatalf("tool_use_id must be preserved: %v", resultBlocks[0])
	}
}

func TestAnthropicRequestRejectsToolCallWithInvalidArguments(t *testing.T) {
	_, err := buildAnthropicRequest("claude-test", ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "assistant", ToolCalls: []any{
				map[string]any{"id": "call_1", "function": map[string]any{"name": "ping", "arguments": `{"truncated":`}},
			}},
		},
	}, nil)
	if err == nil {
		t.Fatalf("truncated tool arguments must be rejected rather than forwarded")
	}
}

func TestAnthropicRequestConvertsImageContent(t *testing.T) {
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "what is this?"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
		}}},
	})
	messages, _ := payload["messages"].([]map[string]any)
	blocks, _ := messages[0]["content"].([]map[string]any)
	if len(blocks) != 3 {
		t.Fatalf("expected text plus two image blocks, got %v", blocks)
	}
	base64Source, _ := blocks[1]["source"].(map[string]any)
	if base64Source["type"] != "base64" || base64Source["media_type"] != "image/png" || base64Source["data"] != "AAAA" {
		t.Fatalf("data URI must map to a base64 image source: %v", base64Source)
	}
	urlSource, _ := blocks[2]["source"].(map[string]any)
	if urlSource["type"] != "url" || urlSource["url"] != "https://example.com/a.png" {
		t.Fatalf("http image URLs must map to a url image source: %v", urlSource)
	}
}

func TestAnthropicRequestRejectsUnsupportedContentPart(t *testing.T) {
	_, err := buildAnthropicRequest("claude-test", ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: []any{
			map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "AAAA"}},
		}}},
	}, nil)
	if err == nil {
		t.Fatalf("unsupported modalities must be rejected instead of silently dropped")
	}
}

func TestAnthropicRebuildsThinkingOnlyWithMatchingSignature(t *testing.T) {
	build := func(signature string) []map[string]any {
		payload := mustBuildAnthropic(t, ChatCompletionRequest{
			Messages: []ChatMessage{{
				Role:               "assistant",
				Content:            "answer",
				ReasoningContent:   "deliberation",
				ReasoningSignature: signature,
			}},
		})
		messages, _ := payload["messages"].([]map[string]any)
		blocks, _ := messages[0]["content"].([]map[string]any)
		return blocks
	}

	withSignature := build(encodeProviderSignature(anthropicSignatureProvider, "sig-abc"))
	if withSignature[0]["type"] != "thinking" || withSignature[0]["signature"] != "sig-abc" {
		t.Fatalf("a matching signature must rebuild the thinking block: %v", withSignature)
	}

	// A signature minted by another provider cannot be replayed: Anthropic
	// rejects the request outright, so the block is dropped instead.
	foreign := build(encodeProviderSignature(geminiSignatureProvider, "sig-abc"))
	for _, block := range foreign {
		if block["type"] == "thinking" {
			t.Fatalf("a foreign signature must not produce a thinking block: %v", foreign)
		}
	}

	missing := build("")
	for _, block := range missing {
		if block["type"] == "thinking" {
			t.Fatalf("a missing signature must not produce a thinking block: %v", missing)
		}
	}
}

func TestAnthropicResponseRestoresToolCallsAndReasoning(t *testing.T) {
	body := map[string]any{
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "hmm", "signature": "sig-1"},
			map[string]any{"type": "text", "text": "calling"},
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": map[string]any{"city": "BJ"}},
		},
		"stop_reason": "tool_use",
	}
	converted, err := anthropicChatResponse(body, "model-x", Usage{})
	if err != nil {
		t.Fatalf("anthropicChatResponse: %v", err)
	}
	choices, _ := converted["choices"].([]map[string]any)
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Fatalf("stop_reason tool_use must map to finish_reason tool_calls: %v", choices[0])
	}
	message, _ := choices[0]["message"].(map[string]any)
	if message["reasoning_content"] != "hmm" {
		t.Fatalf("thinking text must surface as reasoning_content: %v", message)
	}
	signature, _ := message["reasoning_signature"].(string)
	if !strings.HasPrefix(signature, anthropicSignatureProvider+":") {
		t.Fatalf("the signature must be tagged with its originating provider: %v", message)
	}
	calls, _ := message["tool_calls"].([]any)
	call, _ := calls[0].(map[string]any)
	function, _ := call["function"].(map[string]any)
	arguments, _ := function["arguments"].(string)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
		t.Fatalf("tool call arguments must be a JSON string: %v", err)
	}
	if decoded["city"] != "BJ" {
		t.Fatalf("tool input was not preserved: %v", decoded)
	}
}

func TestAnthropicResponseUsesNullContentForToolOnlyTurn(t *testing.T) {
	body := map[string]any{
		"content":     []any{map[string]any{"type": "tool_use", "id": "t", "name": "ping", "input": map[string]any{}}},
		"stop_reason": "tool_use",
	}
	converted, _ := anthropicChatResponse(body, "model-x", Usage{})
	choices, _ := converted["choices"].([]map[string]any)
	message, _ := choices[0]["message"].(map[string]any)
	if message["content"] != nil {
		t.Fatalf("a tool-only turn must report null content, got %v", message["content"])
	}
}

func TestAnthropicRebuildsSignedThinkingBlockWithEmptyText(t *testing.T) {
	// Models may return a signed thinking block with no visible text. The block
	// still has to be replayed verbatim, so the signature alone gates rebuilding.
	payload := mustBuildAnthropic(t, ChatCompletionRequest{
		Messages: []ChatMessage{{
			Role:               "assistant",
			Content:            "answer",
			ReasoningSignature: encodeProviderSignature(anthropicSignatureProvider, "sig-empty"),
		}},
	})
	messages, _ := payload["messages"].([]map[string]any)
	blocks, _ := messages[0]["content"].([]map[string]any)
	if blocks[0]["type"] != "thinking" || blocks[0]["signature"] != "sig-empty" {
		t.Fatalf("a signed block with empty thinking text must still be replayed: %v", blocks)
	}
}

func TestAnthropicStreamRejectsTruncatedStream(t *testing.T) {
	// The connection drops after partial content and before message_stop.
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":4}}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("a stream that ends before message_stop must be reported as truncated")
	}
	if strings.Contains(writer.builder.String(), "[DONE]") {
		t.Fatalf("a truncated stream must not be presented to the client as complete")
	}
}

func TestAnthropicStreamRejectsUnopenedToolBlockArguments(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		``,
		`data: {"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	// Routing this to an arbitrary slot would corrupt an unrelated tool call.
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("tool arguments for an unopened block must be rejected")
	}
}

func TestAnthropicStreamRejectsTruncatedToolArguments(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"ping"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	// Arguments that never became valid JSON are unusable, and the truncation is
	// invisible once the fragments have already been forwarded.
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("tool arguments that do not parse must be rejected")
	}
}

func TestSSEDecoderRejectsOversizedEvent(t *testing.T) {
	// A single unbounded event would otherwise grow until the process runs out
	// of memory. The payload deliberately has no newline: the limit must trip
	// while reading, not after the whole line has been allocated.
	reader := io.LimitReader(neverEndingReader{}, int64(maxSSEEventBytes)*4)
	if _, err := newSSEDecoder(reader).Next(); err == nil {
		t.Fatalf("an unterminated event beyond the size limit must be rejected")
	}
}

// neverEndingReader emits a stream that never contains a newline.
type neverEndingReader struct{}

func (neverEndingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestAnthropicStreamRejectsMissingOrInvalidBlockIndex(t *testing.T) {
	cases := map[string]string{
		"missing index":     `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}`,
		"non-numeric index": `data: {"type":"content_block_delta","index":"0","delta":{"type":"text_delta","text":"x"}}`,
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			raw := strings.Join([]string{
				`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
				``,
				event,
				``,
				``,
			}, "\n")
			encoder := newOpenAIChatStreamEncoder(&recordingWriter{}, "model-x", false)
			// Defaulting a bad index to 0 would misroute deltas into the first block.
			if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err == nil {
				t.Fatalf("expected the malformed index to be rejected")
			}
		})
	}
}

func TestAnthropicStreamRejectsReopenedToolBlock(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"ping"}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"b","name":"ping"}}`,
		``,
		``,
	}, "\n")
	encoder := newOpenAIChatStreamEncoder(&recordingWriter{}, "model-x", false)
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("reopening a content block must be rejected")
	}
}

func TestAnthropicStreamRejectsMessageStopWithOpenToolBlock(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"ping"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	// The block never closed, so its arguments were never validated.
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("an unterminated tool block must be rejected")
	}
	if strings.Contains(writer.builder.String(), "[DONE]") {
		t.Fatalf("a stream with an unterminated tool call must not be reported as complete")
	}
}

func TestAnthropicStreamEmitsEmptyObjectForArgumentlessTool(t *testing.T) {
	// A tool taking no arguments produces no partial_json deltas.
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"now"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err != nil {
		t.Fatalf("streamAnthropicChat: %v", err)
	}

	var arguments strings.Builder
	for _, frame := range writer.frames(t) {
		calls, ok := frameDelta(t, frame)["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, item := range calls {
			call, _ := item.(map[string]any)
			if function, ok := call["function"].(map[string]any); ok {
				if value, ok := function["arguments"].(string); ok {
					arguments.WriteString(value)
				}
			}
		}
	}
	// An empty arguments string is not parsable by the client.
	if arguments.String() != "{}" {
		t.Fatalf("an argumentless tool call must yield {}, got %q", arguments.String())
	}
}

func TestAnthropicStreamConvertsTextAndToolArguments(t *testing.T) {
	// Anthropic content indexes are sparse from OpenAI's perspective: index 0 is
	// a thinking block and index 1 is text, so the tool at index 2 must still be
	// reported as tool_calls[0].
	raw := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"pondering"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"tail"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hi"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"ping"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"1}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":2}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	usage, err := streamAnthropicChat(strings.NewReader(raw), encoder)
	if err != nil {
		t.Fatalf("streamAnthropicChat: %v", err)
	}

	var (
		reasoning   strings.Builder
		text        strings.Builder
		arguments   strings.Builder
		signature   string
		toolIndexes []int
		finish      string
	)
	for _, frame := range writer.frames(t) {
		choices, _ := frame["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if reason, ok := choice["finish_reason"].(string); ok && reason != "" {
			finish = reason
		}
		delta, _ := choice["delta"].(map[string]any)
		if value, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(value)
		}
		if value, ok := delta["reasoning_signature"].(string); ok {
			signature = value
		}
		if value, ok := delta["content"].(string); ok {
			text.WriteString(value)
		}
		if calls, ok := delta["tool_calls"].([]any); ok {
			for _, item := range calls {
				call, _ := item.(map[string]any)
				index, _ := call["index"].(float64)
				toolIndexes = append(toolIndexes, int(index))
				if function, ok := call["function"].(map[string]any); ok {
					if value, ok := function["arguments"].(string); ok {
						arguments.WriteString(value)
					}
				}
			}
		}
	}

	if reasoning.String() != "pondering" {
		t.Fatalf("thinking deltas must surface as reasoning_content, got %q", reasoning.String())
	}
	if signature != encodeProviderSignature(anthropicSignatureProvider, "sig-tail") {
		t.Fatalf("signature fragments must be concatenated and tagged, got %q", signature)
	}
	if text.String() != "Hi" {
		t.Fatalf("unexpected text: %q", text.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(arguments.String()), &decoded); err != nil {
		t.Fatalf("partial_json fragments must reassemble into valid JSON: %v (%q)", err, arguments.String())
	}
	for _, index := range toolIndexes {
		if index != 0 {
			t.Fatalf("the only tool call must be reported at dense index 0, saw %d", index)
		}
	}
	if finish != "tool_calls" {
		t.Fatalf("expected finish_reason tool_calls, got %q", finish)
	}
	// message_start reports input 10 / output 1, message_delta reports a
	// cumulative output of 7. Summing would yield 8.
	if usage.CompletionTokens != 7 {
		t.Fatalf("streaming usage is a cumulative snapshot, expected 7 output tokens, got %d", usage.CompletionTokens)
	}
	if usage.PromptTokens != 10 {
		t.Fatalf("expected the input token count to be retained, got %d", usage.PromptTokens)
	}
}

func TestAnthropicStreamAssignsDenseIndexesToParallelToolCalls(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"a","name":"first"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		``,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"b","name":"second"}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"x\":2}"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err != nil {
		t.Fatalf("streamAnthropicChat: %v", err)
	}

	// Arguments must not bleed between concurrent tool calls.
	arguments := map[int]*strings.Builder{}
	for _, frame := range writer.frames(t) {
		delta := frameDelta(t, frame)
		calls, ok := delta["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, item := range calls {
			call, _ := item.(map[string]any)
			index := int(call["index"].(float64))
			if arguments[index] == nil {
				arguments[index] = &strings.Builder{}
			}
			if function, ok := call["function"].(map[string]any); ok {
				if value, ok := function["arguments"].(string); ok {
					arguments[index].WriteString(value)
				}
			}
		}
	}
	if len(arguments) != 2 {
		t.Fatalf("expected two distinct tool slots, got %d", len(arguments))
	}
	if arguments[0].String() != "{}" {
		t.Fatalf("first tool arguments were polluted: %q", arguments[0].String())
	}
	if arguments[1].String() != `{"x":2}` {
		t.Fatalf("second tool arguments were polluted: %q", arguments[1].String())
	}
}

func TestAnthropicStreamSurfacesMidStreamError(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		``,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"upstream overloaded"}}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamAnthropicChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("a mid-stream error event must surface as an error")
	}
	// The client already received content, so it must not be told the stream
	// completed successfully.
	if strings.Contains(writer.builder.String(), "[DONE]") {
		t.Fatalf("a failed stream must not emit the completion sentinel")
	}
}

func TestAnthropicStreamRejectsEmptyStream(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamAnthropicChat(strings.NewReader(""), encoder); err == nil {
		t.Fatalf("a stream that closes before sending content must be an error")
	}
}
