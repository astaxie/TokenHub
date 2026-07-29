package server

import (
	"strings"
	"testing"
)

func mustBuildGemini(t *testing.T, req ChatCompletionRequest) map[string]any {
	t.Helper()
	payload, err := buildGeminiRequest("gemini-test", req)
	if err != nil {
		t.Fatalf("buildGeminiRequest: %v", err)
	}
	return payload
}

func TestGeminiRequestUsesSystemInstruction(t *testing.T) {
	payload := mustBuildGemini(t, ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hi"},
		},
	})

	instruction, ok := payload["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("system guidance must map to the top-level systemInstruction, got %v", payload)
	}
	parts, _ := instruction["parts"].([]map[string]any)
	if len(parts) != 1 || parts[0]["text"] != "be brief" {
		t.Fatalf("unexpected systemInstruction payload: %v", instruction)
	}

	// Disguising system guidance as a user turn changes how the model weighs it.
	contents, _ := payload["contents"].([]map[string]any)
	if len(contents) != 1 {
		t.Fatalf("expected only the user turn in contents, got %d", len(contents))
	}
	userParts, _ := contents[0]["parts"].([]map[string]any)
	if userParts[0]["text"] != "hi" {
		t.Fatalf("system text must not leak into the user turn: %v", contents[0])
	}
}

func TestGeminiRequestRejectsDisabledParallelToolCalls(t *testing.T) {
	disabled := false
	_, err := buildGeminiRequest("gemini-test", ChatCompletionRequest{
		Messages:          []ChatMessage{{Role: "user", Content: "hi"}},
		ParallelToolCalls: &disabled,
	})
	// Gemini has no generateContent equivalent; silently ignoring the flag would
	// let a client believe a constraint was applied when it was not.
	if err == nil {
		t.Fatalf("expected parallel_tool_calls=false to be rejected for Gemini")
	}
	if AsHTTPError(err).Code != "provider_capability_not_supported" {
		t.Fatalf("unexpected error code: %v", AsHTTPError(err).Code)
	}
}

func TestGeminiRequestMapsToolsAndToolConfig(t *testing.T) {
	payload := mustBuildGemini(t, ChatCompletionRequest{
		Messages:   []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:      []any{map[string]any{"function": map[string]any{"name": "ping", "description": "p"}}},
		ToolChoice: "required",
	})
	tools, _ := payload["tools"].([]map[string]any)
	declarations, _ := tools[0]["functionDeclarations"].([]map[string]any)
	if declarations[0]["name"] != "ping" {
		t.Fatalf("tool must map to a functionDeclaration: %v", tools)
	}
	config, _ := payload["toolConfig"].(map[string]any)
	calling, _ := config["functionCallingConfig"].(map[string]any)
	if calling["mode"] != "ANY" {
		t.Fatalf("tool_choice=required must map to mode ANY, got %v", calling)
	}
}

func TestGeminiToolChoiceFunctionRestrictsAllowedNames(t *testing.T) {
	payload := mustBuildGemini(t, ChatCompletionRequest{
		Messages:   []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:      []any{map[string]any{"function": map[string]any{"name": "ping"}}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "ping"}},
	})
	config, _ := payload["toolConfig"].(map[string]any)
	calling, _ := config["functionCallingConfig"].(map[string]any)
	allowed, _ := calling["allowedFunctionNames"].([]string)
	if calling["mode"] != "ANY" || len(allowed) != 1 || allowed[0] != "ping" {
		t.Fatalf("a named tool_choice must restrict allowedFunctionNames: %v", calling)
	}
}

func TestGeminiRequestBuildsFunctionResponseWithIDAndName(t *testing.T) {
	payload := mustBuildGemini(t, ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "weather?"},
			{Role: "assistant", ToolCalls: []any{
				map[string]any{"id": "call_1", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"BJ"}`}},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		},
	})
	contents, _ := payload["contents"].([]map[string]any)
	if contents[1]["role"] != "model" {
		t.Fatalf("assistant turns must use the model role: %v", contents[1])
	}
	resultParts, _ := contents[2]["parts"].([]map[string]any)
	response, _ := resultParts[0]["functionResponse"].(map[string]any)
	if response["id"] != "call_1" || response["name"] != "get_weather" {
		t.Fatalf("functionResponse must carry the matching id and name: %v", response)
	}
	// Gemini requires an object payload, not a bare string.
	if _, ok := response["response"].(map[string]any); !ok {
		t.Fatalf("functionResponse response must be an object: %v", response)
	}
}

func TestGeminiRequestConvertsInlineImageAndRejectsRemoteURL(t *testing.T) {
	payload := mustBuildGemini(t, ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64,BBBB"}},
		}}},
	})
	contents, _ := payload["contents"].([]map[string]any)
	parts, _ := contents[0]["parts"].([]map[string]any)
	inline, _ := parts[0]["inlineData"].(map[string]any)
	if inline["mimeType"] != "image/jpeg" || inline["data"] != "BBBB" {
		t.Fatalf("data URIs must map to inlineData: %v", parts[0])
	}

	// Gemini's fileData only accepts URIs it already hosts, so an arbitrary
	// image URL cannot be forwarded and must not be silently dropped.
	_, err := buildGeminiRequest("gemini-test", ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
		}}},
	})
	if err == nil {
		t.Fatalf("expected a remote image URL to be rejected for Gemini")
	}
}

func TestGeminiRequestForwardsMatchingThoughtSignature(t *testing.T) {
	build := func(signature string) map[string]any {
		payload := mustBuildGemini(t, ChatCompletionRequest{
			Messages: []ChatMessage{{Role: "assistant", ToolCalls: []any{
				map[string]any{
					"id":                "call_1",
					"function":          map[string]any{"name": "ping", "arguments": "{}"},
					"thought_signature": signature,
				},
			}}},
		})
		contents, _ := payload["contents"].([]map[string]any)
		parts, _ := contents[0]["parts"].([]map[string]any)
		return parts[0]
	}

	matching := build(encodeProviderSignature(geminiSignatureProvider, "thought-1"))
	if matching["thoughtSignature"] != "thought-1" {
		t.Fatalf("a matching signature must be replayed verbatim: %v", matching)
	}

	foreign := build(encodeProviderSignature(anthropicSignatureProvider, "thought-1"))
	if _, present := foreign["thoughtSignature"]; present {
		t.Fatalf("another provider's signature must not be replayed: %v", foreign)
	}
}

func TestGeminiResponsePreservesNativeToolCallID(t *testing.T) {
	body := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{
				map[string]any{"functionCall": map[string]any{"id": "native-id", "name": "ping", "args": map[string]any{"a": 1}}},
			}},
			"finishReason": "STOP",
		}},
	}
	converted, err := geminiChatResponse(body, "model-x", Usage{})
	if err != nil {
		t.Fatalf("geminiChatResponse: %v", err)
	}
	choices, _ := converted["choices"].([]map[string]any)
	message, _ := choices[0]["message"].(map[string]any)
	calls, _ := message["tool_calls"].([]any)
	call, _ := calls[0].(map[string]any)
	if call["id"] != "native-id" {
		t.Fatalf("a provider-supplied tool call id must be preserved, got %v", call["id"])
	}
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Fatalf("STOP with a function call must map to tool_calls: %v", choices[0])
	}
}

func TestGeminiResponseGeneratesToolCallIDWhenAbsent(t *testing.T) {
	body := map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": "ping"}}}},
			"finishReason": "STOP",
		}},
	}
	converted, _ := geminiChatResponse(body, "model-x", Usage{})
	choices, _ := converted["choices"].([]map[string]any)
	message, _ := choices[0]["message"].(map[string]any)
	calls, _ := message["tool_calls"].([]any)
	call, _ := calls[0].(map[string]any)
	if id, _ := call["id"].(string); id == "" {
		t.Fatalf("clients need a handle to correlate results with, got an empty id")
	}
}

func TestGeminiResponseSeparatesThoughtParts(t *testing.T) {
	body := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{
				map[string]any{"text": "internal", "thought": true},
				map[string]any{"text": "answer"},
			}},
			"finishReason": "STOP",
		}},
	}
	converted, _ := geminiChatResponse(body, "model-x", Usage{})
	choices, _ := converted["choices"].([]map[string]any)
	message, _ := choices[0]["message"].(map[string]any)
	if message["reasoning_content"] != "internal" {
		t.Fatalf("thought parts must surface as reasoning_content: %v", message)
	}
	if message["content"] != "answer" {
		t.Fatalf("thought text must not leak into the answer: %v", message)
	}
}

func TestGeminiFinishReasonMapping(t *testing.T) {
	safe := map[string]string{
		"STOP":       "stop",
		"MAX_TOKENS": "length",
	}
	for reason, want := range safe {
		got, err := geminiFinishReason(reason, false)
		if err != nil {
			t.Fatalf("%s: %v", reason, err)
		}
		if got != want {
			t.Fatalf("%s: expected %q, got %q", reason, want, got)
		}
	}

	for _, reason := range []string{"SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII"} {
		got, err := geminiFinishReason(reason, false)
		if err != nil {
			t.Fatalf("%s: %v", reason, err)
		}
		if got != "content_filter" {
			t.Fatalf("%s must map to content_filter, got %q", reason, got)
		}
	}

	// Protocol failures must not be reported as a normal completion.
	for _, reason := range []string{"MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "MALFORMED_RESPONSE", "SOMETHING_NEW"} {
		if _, err := geminiFinishReason(reason, false); err == nil {
			t.Fatalf("%s must surface as an upstream error", reason)
		}
	}
}

func TestGeminiResponseSurfacesPromptBlock(t *testing.T) {
	body := map[string]any{
		"candidates":     []any{},
		"promptFeedback": map[string]any{"blockReason": "SAFETY"},
	}
	_, err := geminiChatResponse(body, "model-x", Usage{})
	if err == nil {
		t.Fatalf("a blocked prompt must surface as an error rather than an empty reply")
	}
	if AsHTTPError(err).Code != "content_filter" {
		t.Fatalf("unexpected error code: %v", AsHTTPError(err).Code)
	}
}

func TestGeminiStreamEmitsCompleteToolArgumentsInOneDelta(t *testing.T) {
	// Gemini does not stream partial tool arguments: each functionCall part
	// arrives as a complete JSON object.
	raw := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"c1","name":"ping","args":{"a":1}},"thoughtSignature":"ts-1"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":9,"totalTokenCount":14}}`,
		``,
		``,
	}, "\n")

	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	usage, err := streamGeminiChat(strings.NewReader(raw), encoder)
	if err != nil {
		t.Fatalf("streamGeminiChat: %v", err)
	}

	var (
		text          strings.Builder
		argumentParts []string
		signature     string
		finish        string
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
		if value, ok := delta["content"].(string); ok {
			text.WriteString(value)
		}
		calls, ok := delta["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, item := range calls {
			call, _ := item.(map[string]any)
			if value, ok := call["thought_signature"].(string); ok {
				signature = value
			}
			function, ok := call["function"].(map[string]any)
			if !ok {
				continue
			}
			if value, ok := function["arguments"].(string); ok && value != "" {
				argumentParts = append(argumentParts, value)
			}
		}
	}

	if text.String() != "Hello" {
		t.Fatalf("streamed text must concatenate, got %q", text.String())
	}
	if len(argumentParts) != 1 || argumentParts[0] != `{"a":1}` {
		t.Fatalf("Gemini tool arguments must arrive as a single complete delta, got %v", argumentParts)
	}
	if signature != encodeProviderSignature(geminiSignatureProvider, "ts-1") {
		t.Fatalf("thought signature must be tagged with its provider, got %q", signature)
	}
	if finish != "tool_calls" {
		t.Fatalf("expected finish_reason tool_calls, got %q", finish)
	}
	if usage.PromptTokens != 5 || usage.CompletionTokens != 9 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestGeminiRequestReplaysToolCallIDAlongsideResponse(t *testing.T) {
	payload := mustBuildGemini(t, ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "assistant", ToolCalls: []any{
				map[string]any{"id": "call_1", "function": map[string]any{"name": "ping", "arguments": "{}"}},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "pong"},
		},
	})
	contents, _ := payload["contents"].([]map[string]any)
	modelParts, _ := contents[0]["parts"].([]map[string]any)
	call, _ := modelParts[0]["functionCall"].(map[string]any)
	resultParts, _ := contents[1]["parts"].([]map[string]any)
	response, _ := resultParts[0]["functionResponse"].(map[string]any)

	// The response references the call by id; replaying the call without one
	// leaves the result pointing at a call the provider cannot find.
	if call["id"] != "call_1" {
		t.Fatalf("the assistant tool call must keep its id: %v", call)
	}
	if response["id"] != call["id"] {
		t.Fatalf("call and response ids must match: %v vs %v", call["id"], response["id"])
	}
}

func TestGeminiToolSchemaSelectsFieldByComplexity(t *testing.T) {
	simple := mustBuildGemini(t, ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools: []any{map[string]any{"function": map[string]any{
			"name":       "ping",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
		}}},
	})
	tools, _ := simple["tools"].([]map[string]any)
	declarations, _ := tools[0]["functionDeclarations"].([]map[string]any)
	if _, ok := declarations[0]["parameters"]; !ok {
		t.Fatalf("a plain schema should use the OpenAPI parameters field: %v", declarations[0])
	}

	// $defs/$ref cannot be expressed by the OpenAPI Schema subset that the
	// "parameters" field accepts, so a valid OpenAI tool would be rejected.
	complex := mustBuildGemini(t, ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools: []any{map[string]any{"function": map[string]any{
			"name": "ping",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"a": map[string]any{"$ref": "#/$defs/A"}},
				"$defs":      map[string]any{"A": map[string]any{"type": "string"}},
			},
		}}},
	})
	tools, _ = complex["tools"].([]map[string]any)
	declarations, _ = tools[0]["functionDeclarations"].([]map[string]any)
	if _, ok := declarations[0]["parametersJsonSchema"]; !ok {
		t.Fatalf("a JSON Schema construct must use parametersJsonSchema: %v", declarations[0])
	}
	if _, ok := declarations[0]["parameters"]; ok {
		t.Fatalf("the two schema fields are mutually exclusive: %v", declarations[0])
	}
}

func TestGeminiSchemaRoutingChecksValueShapes(t *testing.T) {
	cases := []struct {
		name      string
		schema    map[string]any
		needsJSON bool
		rationale string
	}{
		{
			name:      "plain object",
			schema:    map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
			needsJSON: false,
			rationale: "a plain OpenAPI schema must not be pushed to the richer field",
		},
		{
			name:      "type union",
			schema:    map[string]any{"type": []any{"string", "null"}},
			needsJSON: true,
			rationale: "OpenAPI models type as a single value, not a union",
		},
		{
			name:      "non-string enum",
			schema:    map[string]any{"type": "integer", "enum": []any{float64(1), float64(2)}},
			needsJSON: true,
			rationale: "OpenAPI enum is a list of strings",
		},
		{
			name:      "type union nested under properties",
			schema:    map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": []any{"string", "null"}}}},
			needsJSON: true,
			rationale: "nested schemas must be inspected too",
		},
		{
			name:      "arbitrary example payload",
			schema:    map[string]any{"type": "object", "example": map[string]any{"city": "Beijing"}},
			needsJSON: false,
			rationale: "annotation values hold data, not schema keywords",
		},
		{
			name:      "nested items",
			schema:    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			needsJSON: false,
			rationale: "a simple array schema is expressible in OpenAPI",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := schemaNeedsJSONSchema(testCase.schema); got != testCase.needsJSON {
				t.Fatalf("%s: expected needsJSONSchema=%v, got %v", testCase.rationale, testCase.needsJSON, got)
			}
		})
	}
}

func TestGeminiStreamRejectsTruncatedStream(t *testing.T) {
	// Gemini leaves finishReason empty while generation is in progress.
	raw := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hel\"}]}}]}\n\n"
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamGeminiChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("a stream that ends without a finish reason must be reported as truncated")
	}
	if strings.Contains(writer.builder.String(), "[DONE]") {
		t.Fatalf("a truncated stream must not be presented to the client as complete")
	}
}

func TestGeminiStreamRejectsEmptyStream(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamGeminiChat(strings.NewReader(""), encoder); err == nil {
		t.Fatalf("a stream that closes before sending content must be an error")
	}
}

func TestGeminiStreamSurfacesErrorPayload(t *testing.T) {
	raw := "data: {\"error\":{\"message\":\"quota exhausted\"}}\n\n"
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if _, err := streamGeminiChat(strings.NewReader(raw), encoder); err == nil {
		t.Fatalf("an error payload must surface as an error")
	}
	if strings.Contains(writer.builder.String(), "[DONE]") {
		t.Fatalf("a failed stream must not emit the completion sentinel")
	}
}
