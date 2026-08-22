package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

const localCodexCompatModel = "gpt-5.5"

func TestLocalConsumeCodexResponsesStreamHydratesToolOutput(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_real","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_real","call_id":"call_real","name":"read_tokenhub_backend_go_mod","arguments":""}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_real","delta":"{\"path\":\"backend/go.mod\"}"}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_real","call_id":"call_real","name":"read_tokenhub_backend_go_mod","arguments":"{\"path\":\"backend/go.mod\"}"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_real","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20}}}`,
		"",
	}, "\n")

	response, _, usage, err := consumeCodexResponsesStream(strings.NewReader(stream), nil)
	if err != nil {
		t.Fatalf("consume real Codex event sequence: %v", err)
	}
	output := localCodexCompatSlice(t, response["output"], "response output")
	if len(output) != 1 {
		t.Fatalf("output length = %d, want 1", len(output))
	}
	call := localCodexCompatMap(t, output[0], "function call")
	if call["type"] != "function_call" ||
		call["call_id"] != "call_real" ||
		call["arguments"] != `{"path":"backend/go.mod"}` {
		t.Fatalf("function call was not hydrated: %#v", call)
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 8 || usage.TotalTokens != 20 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestLocalCodexCompatAnthropicRequestPreservesOrderedProtocolItems(t *testing.T) {
	longToolName := "filesystem_read_text_file_with_a_very_long_workspace_specific_operation_name"
	encryptedReasoning := "gAAAAABreasoningContinuationStateForCodex"
	request := localCodexCompatAnthropicRequest(t, map[string]any{
		"model": localCodexCompatModel,
		"system": []any{
			map[string]any{"type": "text", "text": "Work only in the requested repository."},
			map[string]any{"type": "text", "text": "Preserve unrelated changes."},
		},
		"max_tokens":  4096,
		"temperature": 0.2,
		"top_p":       0.8,
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 4096,
		},
		"tools": []any{
			map[string]any{
				"name":        longToolName,
				"description": "Read a UTF-8 text file from the active workspace.",
				"input_schema": map[string]any{
					"$schema": "https://json-schema.org/draft/2020-12/schema",
					"type":    "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
					"required": []any{"path"},
				},
			},
		},
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "Inspect the attached image before editing the file."},
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": "image/png",
							"data":       "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
						},
					},
				},
			},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":      "thinking",
						"thinking":  "I need the current file contents before editing.",
						"signature": encodeProviderSignature(codexSignatureProvider, encryptedReasoning),
					},
					map[string]any{"type": "text", "text": "I will read the file first."},
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_01JZ9FA3R9P4PW9KK8BBTQ3M5E",
						"name":  longToolName,
						"input": map[string]any{"path": "/workspace/service/config.go"},
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_01JZ9FA3R9P4PW9KK8BBTQ3M5E",
						"content":     "package service\n",
					},
					map[string]any{"type": "text", "text": "Make only the requested change."},
				},
			},
		},
	})

	upstream, err := anthropicToCodexResponsesRequest(request)
	if err != nil {
		t.Fatalf("translate Anthropic request: %v", err)
	}
	wire := localCodexCompatResponsesRequestMap(t, upstream)

	if got := wire["instructions"]; got != "Work only in the requested repository.\nPreserve unrelated changes." {
		t.Fatalf("instructions = %#v", got)
	}
	for _, field := range []string{"max_output_tokens", "temperature", "top_p"} {
		if _, exists := wire[field]; exists {
			t.Fatalf("unsupported sampling field %q was forwarded: %#v", field, wire[field])
		}
	}

	tools := localCodexCompatSlice(t, wire["tools"], "tools")
	if len(tools) != 1 {
		t.Fatalf("tools length = %d, want 1", len(tools))
	}
	upstreamTool := localCodexCompatMap(t, tools[0], "tools[0]")
	shortToolName, _ := upstreamTool["name"].(string)
	if shortToolName == "" || shortToolName == longToolName || len(shortToolName) > codexIdentifierLimit {
		t.Fatalf("shortened tool name = %q", shortToolName)
	}
	parameters := localCodexCompatMap(t, upstreamTool["parameters"], "tools[0].parameters")
	if _, exists := parameters["$schema"]; exists {
		t.Fatal("JSON Schema dialect marker was forwarded to Codex")
	}

	input := localCodexCompatSlice(t, wire["input"], "input")
	wantTypes := []string{"message", "reasoning", "message", "function_call", "function_call_output", "message"}
	if len(input) != len(wantTypes) {
		t.Fatalf("input length = %d, want %d: %#v", len(input), len(wantTypes), input)
	}
	for index, wantType := range wantTypes {
		item := localCodexCompatMap(t, input[index], "input item")
		if got, _ := item["type"].(string); got != wantType {
			t.Fatalf("input[%d].type = %q, want %q", index, got, wantType)
		}
	}

	userMessage := localCodexCompatMap(t, input[0], "initial user message")
	userParts := localCodexCompatSlice(t, userMessage["content"], "initial user content")
	if len(userParts) != 2 {
		t.Fatalf("initial user content length = %d, want 2", len(userParts))
	}
	if got := localCodexCompatMap(t, userParts[0], "user text")["text"]; got != "Inspect the attached image before editing the file." {
		t.Fatalf("user text = %#v", got)
	}
	image := localCodexCompatMap(t, userParts[1], "user image")
	imageURL, _ := image["image_url"].(string)
	if image["type"] != "input_image" || !strings.HasPrefix(imageURL, "data:image/png;base64,") {
		t.Fatalf("image part = %#v", image)
	}

	reasoning := localCodexCompatMap(t, input[1], "reasoning")
	if reasoning["encrypted_content"] != encryptedReasoning {
		t.Fatalf("encrypted reasoning = %#v", reasoning["encrypted_content"])
	}
	summary := localCodexCompatSlice(t, reasoning["summary"], "reasoning summary")
	if got := localCodexCompatMap(t, summary[0], "reasoning summary text")["text"]; got != "I need the current file contents before editing." {
		t.Fatalf("reasoning summary = %#v", got)
	}

	functionCall := localCodexCompatMap(t, input[3], "function call")
	if functionCall["name"] != shortToolName {
		t.Fatalf("function call name = %#v, want %q", functionCall["name"], shortToolName)
	}
	if functionCall["call_id"] != "toolu_01JZ9FA3R9P4PW9KK8BBTQ3M5E" {
		t.Fatalf("function call id = %#v", functionCall["call_id"])
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(functionCall["arguments"].(string)), &arguments); err != nil {
		t.Fatalf("decode function arguments: %v", err)
	}
	if arguments["path"] != "/workspace/service/config.go" {
		t.Fatalf("function arguments = %#v", arguments)
	}

	toolOutput := localCodexCompatMap(t, input[4], "function output")
	if toolOutput["call_id"] != functionCall["call_id"] || toolOutput["output"] != "package service\n" {
		t.Fatalf("function output = %#v", toolOutput)
	}
	finalMessage := localCodexCompatMap(t, input[5], "final user message")
	finalParts := localCodexCompatSlice(t, finalMessage["content"], "final user content")
	if got := localCodexCompatMap(t, finalParts[0], "final user text")["text"]; got != "Make only the requested change." {
		t.Fatalf("final user text = %#v", got)
	}
}

func TestLocalCodexCompatLongToolNameRestoredInAnthropicAndChatResponses(t *testing.T) {
	longToolName := "workspace_apply_precise_patch_with_repository_specific_validation_rules"
	anthropicRequest := localCodexCompatAnthropicRequest(t, map[string]any{
		"model":      localCodexCompatModel,
		"max_tokens": 2048,
		"tools": []any{
			map[string]any{
				"name": longToolName,
				"input_schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"patch": map[string]any{"type": "string"}},
					"required":   []any{"patch"},
				},
			},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "Apply the approved patch."},
		},
	})
	upstream, err := anthropicToCodexResponsesRequest(anthropicRequest)
	if err != nil {
		t.Fatalf("translate Anthropic request: %v", err)
	}
	wire := localCodexCompatResponsesRequestMap(t, upstream)
	tools := localCodexCompatSlice(t, wire["tools"], "tools")
	shortName := localCodexCompatMap(t, tools[0], "tool")["name"].(string)
	if shortName == longToolName || len(shortName) > codexIdentifierLimit {
		t.Fatalf("shortened tool name = %q", shortName)
	}

	response := map[string]any{
		"id":     "resp_01JZ9G0A2AHT6EATCFN4CWRG8X",
		"status": "completed",
		"output": []any{
			map[string]any{
				"type":      "function_call",
				"call_id":   "call_01JZ9G0J8JC86VTEAEB6KFM0TW",
				"name":      shortName,
				"arguments": `{"patch":"*** Begin Patch\\n*** End Patch"}`,
			},
		},
	}
	anthropic, err := codexResponsesToAnthropic(response, anthropicRequest, Usage{})
	if err != nil {
		t.Fatalf("convert Codex response to Anthropic: %v", err)
	}
	anthropicContent := localCodexCompatSlice(t, anthropic["content"], "Anthropic content")
	toolUse := localCodexCompatMap(t, anthropicContent[0], "Anthropic tool_use")
	if toolUse["type"] != "tool_use" || toolUse["name"] != longToolName {
		t.Fatalf("Anthropic tool name was not restored: %#v", toolUse)
	}

	chatRequest := ChatCompletionRequest{
		Model: localCodexCompatModel,
		Tools: []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": longToolName,
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{"patch": map[string]any{"type": "string"}},
					},
				},
			},
		},
	}
	chat, err := codexResponsesToChat(response, chatRequest, Usage{})
	if err != nil {
		t.Fatalf("convert Codex response to Chat: %v", err)
	}
	choices := localCodexCompatSlice(t, chat["choices"], "Chat choices")
	message := localCodexCompatMap(t, localCodexCompatMap(t, choices[0], "Chat choice")["message"], "Chat message")
	toolCalls := localCodexCompatSlice(t, message["tool_calls"], "Chat tool calls")
	function := localCodexCompatMap(
		t,
		localCodexCompatMap(t, toolCalls[0], "Chat tool call")["function"],
		"Chat function",
	)
	if function["name"] != longToolName {
		t.Fatalf("Chat tool name was not restored: %#v", function)
	}
}

func TestLocalCodexCompatNonStreamingCompletedAndIncompleteResponses(t *testing.T) {
	request := localCodexCompatAnthropicRequest(t, map[string]any{
		"model":      localCodexCompatModel,
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Summarize the repository state."},
		},
	})
	chatRequest := ChatCompletionRequest{
		Model: localCodexCompatModel,
		Messages: []ChatMessage{
			{Role: "user", Content: "Summarize the repository state."},
		},
	}
	usage := Usage{
		PromptTokens:      21,
		CachedInputTokens: 5,
		CompletionTokens:  8,
		TotalTokens:       29,
	}

	testCases := []struct {
		name                string
		status              string
		incompleteDetails   any
		text                string
		anthropicStopReason string
		chatFinishReason    string
	}{
		{
			name:                "completed",
			status:              "completed",
			text:                "The working tree contains focused compatibility changes.",
			anthropicStopReason: "end_turn",
			chatFinishReason:    "stop",
		},
		{
			name:                "incomplete max output tokens",
			status:              "incomplete",
			incompleteDetails:   map[string]any{"reason": "max_output_tokens"},
			text:                "The working tree contains",
			anthropicStopReason: "max_tokens",
			chatFinishReason:    "length",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			body := map[string]any{
				"id":         "resp_01JZ9H0DM32XZA7RHYF8Z7G0BS",
				"status":     testCase.status,
				"created_at": int64(1785436800),
				"output": []any{
					map[string]any{
						"type": "message",
						"role": "assistant",
						"content": []any{
							map[string]any{"type": "output_text", "text": testCase.text},
						},
					},
				},
			}
			if testCase.incompleteDetails != nil {
				body["incomplete_details"] = testCase.incompleteDetails
			}

			anthropic, err := codexResponsesToAnthropic(body, request, usage)
			if err != nil {
				t.Fatalf("convert to Anthropic: %v", err)
			}
			if anthropic["stop_reason"] != testCase.anthropicStopReason {
				t.Fatalf("Anthropic stop_reason = %#v, want %q", anthropic["stop_reason"], testCase.anthropicStopReason)
			}
			content := localCodexCompatSlice(t, anthropic["content"], "Anthropic content")
			if got := localCodexCompatMap(t, content[0], "Anthropic text")["text"]; got != testCase.text {
				t.Fatalf("Anthropic text = %#v", got)
			}
			anthropicUsage := localCodexCompatMap(t, anthropic["usage"], "Anthropic usage")
			if anthropicUsage["input_tokens"] != int64(16) ||
				anthropicUsage["cache_read_input_tokens"] != int64(5) ||
				anthropicUsage["output_tokens"] != int64(8) {
				t.Fatalf("Anthropic usage = %#v", anthropicUsage)
			}

			chat, err := codexResponsesToChat(body, chatRequest, usage)
			if err != nil {
				t.Fatalf("convert to Chat: %v", err)
			}
			choices := localCodexCompatSlice(t, chat["choices"], "Chat choices")
			choice := localCodexCompatMap(t, choices[0], "Chat choice")
			if choice["finish_reason"] != testCase.chatFinishReason {
				t.Fatalf("Chat finish_reason = %#v, want %q", choice["finish_reason"], testCase.chatFinishReason)
			}
			message := localCodexCompatMap(t, choice["message"], "Chat message")
			if message["content"] != testCase.text {
				t.Fatalf("Chat content = %#v", message["content"])
			}
			gotUsage := usageMapField(t, chat, "usage")
			if gotUsage["prompt_tokens"] != usage.PromptTokens ||
				gotUsage["cached_input_tokens"] != usage.CachedInputTokens ||
				gotUsage["completion_tokens"] != usage.CompletionTokens ||
				gotUsage["total_tokens"] != usage.TotalTokens {
				t.Fatalf("Chat usage = %#v", chat["usage"])
			}
		})
	}

	unsignedReasoning := map[string]any{
		"id":     "resp_unsigned_reasoning",
		"status": "completed",
		"output": []any{
			map[string]any{
				"type":    "reasoning",
				"summary": []any{map[string]any{"type": "summary_text", "text": "Unsigned summary"}},
			},
			map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": "Portable output"}},
			},
		},
	}
	anthropic, err := codexResponsesToAnthropic(unsignedReasoning, request, usage)
	if err != nil {
		t.Fatalf("convert unsigned reasoning response: %v", err)
	}
	content := localCodexCompatSlice(t, anthropic["content"], "unsigned reasoning content")
	if len(content) != 1 || localCodexCompatMap(t, content[0], "portable text")["type"] != "text" {
		t.Fatalf("unsigned Codex reasoning leaked into Anthropic thinking: %#v", content)
	}
}

func TestLocalCodexCompatProtocolNamespacesProduceDistinctAffinityKeys(t *testing.T) {
	const (
		secret    = "local-regression-secret-kept-out-of-version-control"
		apiKeyID  = "key_01JZ9J2S4E5A3W6KJ9T4CBH7ZX"
		sessionID = "session_01JZ9J36WB8XCPQ2PB5VG3PXZK"
	)
	anthropic, err := resolveCodexBridgeAffinity(
		secret,
		apiKeyID,
		codexBridgeProtocolAnthropic,
		sessionID,
	)
	if err != nil {
		t.Fatalf("resolve Anthropic affinity: %v", err)
	}
	anthropicAgain, err := resolveCodexBridgeAffinity(
		secret,
		apiKeyID,
		codexBridgeProtocolAnthropic,
		sessionID,
	)
	if err != nil {
		t.Fatalf("resolve repeated Anthropic affinity: %v", err)
	}
	chat, err := resolveCodexBridgeAffinity(
		secret,
		apiKeyID,
		codexBridgeProtocolChat,
		sessionID,
	)
	if err != nil {
		t.Fatalf("resolve Chat affinity: %v", err)
	}
	nativeHeaders := http.Header{}
	nativeHeaders.Set("session-id", sessionID)
	native, err := resolveCodexSessionAffinity(
		secret,
		apiKeyID,
		nativeHeaders,
		ResponsesRequest{},
	)
	if err != nil {
		t.Fatalf("resolve native Responses affinity: %v", err)
	}
	for name, affinity := range map[string]*RequestAffinity{
		"Anthropic": anthropic,
		"Chat":      chat,
		"Responses": native,
	} {
		if affinity == nil {
			t.Fatalf("%s affinity is nil", name)
		}
		if affinity.AdapterType != ProviderOpenAICodex || affinity.Kind != AffinityKindCodexSession {
			t.Fatalf("%s affinity metadata = %#v", name, affinity)
		}
	}
	if anthropic.KeyHash != anthropicAgain.KeyHash {
		t.Fatalf("same protocol/session affinity is not deterministic: %q != %q", anthropic.KeyHash, anthropicAgain.KeyHash)
	}
	if anthropic.KeyHash == chat.KeyHash ||
		anthropic.KeyHash == native.KeyHash ||
		chat.KeyHash == native.KeyHash {
		t.Fatalf(
			"protocol namespaces collided: Anthropic=%q Chat=%q Responses=%q",
			anthropic.KeyHash,
			chat.KeyHash,
			native.KeyHash,
		)
	}

	metadataOnly := map[string]any{
		"metadata": map[string]any{
			"user_id": `{"device_id":"device-01","session_id":"metadata-session-01"}`,
		},
	}
	anthropicHeaders := codexAnthropicCompatibilityHeaders(http.Header{}, metadataOnly)
	if anthropicHeaders.Get("session-id") != "metadata-session-01" {
		t.Fatalf("Anthropic upstream session-id = %q", anthropicHeaders.Get("session-id"))
	}
	chatHeaders := codexChatCompatibilityHeaders(http.Header{}, ChatCompletionRequest{
		PromptCacheKey: "chat-cache-session-01",
	})
	if chatHeaders.Get("session-id") != "chat-cache-session-01" {
		t.Fatalf("Chat upstream session-id = %q", chatHeaders.Get("session-id"))
	}
}

func TestLocalCodexCompatNormalizesLongUpstreamSessionIdentifiers(t *testing.T) {
	for name, identifier := range map[string]string{
		"64 bytes":  strings.Repeat("a", 64),
		"65 bytes":  strings.Repeat("b", 65),
		"300 bytes": strings.Repeat("c", 300),
		"512 bytes": strings.Repeat("d", 512),
		"UTF-8":     strings.Repeat("界", 170),
	} {
		t.Run(name, func(t *testing.T) {
			normalized := codexShortIdentifier(identifier)
			if len(normalized) > codexIdentifierLimit {
				t.Fatalf("normalized length = %d", len(normalized))
			}
			if !utf8.ValidString(normalized) {
				t.Fatal("normalized identifier is not valid UTF-8")
			}
			if normalized != codexShortIdentifier(identifier) {
				t.Fatal("normalization is not deterministic")
			}
			if len(identifier) <= codexIdentifierLimit && normalized != identifier {
				t.Fatal("identifier within the upstream limit changed")
			}
		})
	}
	prefix := strings.Repeat("same-prefix-", 30)
	if codexShortIdentifier(prefix+"first") == codexShortIdentifier(prefix+"second") {
		t.Fatal("long identifiers with distinct suffixes collided")
	}

	longSession := strings.Repeat("claude-session-", 24)
	if len(longSession) <= codexIdentifierLimit || len(longSession) > sessionIdentifierMaxLength {
		t.Fatalf("invalid test session length: %d", len(longSession))
	}

	incoming := http.Header{}
	incoming.Set("x-claude-code-session-id", longSession)
	compatibilityHeaders := codexAnthropicCompatibilityHeaders(incoming, nil)
	upstreamSession := compatibilityHeaders.Get("session-id")
	if len(upstreamSession) > codexIdentifierLimit {
		t.Fatalf("upstream session-id length = %d", len(upstreamSession))
	}
	if upstreamSession != codexShortIdentifier(longSession) {
		t.Fatalf("upstream session-id = %q", upstreamSession)
	}

	affinity, err := resolveCodexBridgeAffinity(
		"local-regression-secret-kept-out-of-version-control",
		"key_long_session_regression",
		codexBridgeProtocolAnthropic,
		longSession,
	)
	if err != nil {
		t.Fatalf("resolve affinity: %v", err)
	}
	expected := deriveSessionAffinityKey(
		"local-regression-secret-kept-out-of-version-control",
		"key_long_session_regression",
		codexBridgeProtocolAnthropic+"\x00"+longSession,
	)
	if affinity == nil || affinity.KeyHash != expected {
		t.Fatal("affinity must be derived from the original session identifier")
	}
	if _, err := resolveCodexBridgeAffinity(
		"local-regression-secret-kept-out-of-version-control",
		"key_long_session_regression",
		codexBridgeProtocolAnthropic,
		strings.Repeat("x", sessionIdentifierMaxLength+1),
	); err == nil {
		t.Fatal("513-byte session identifier must be rejected locally")
	}

	priorityHeaders := http.Header{}
	priorityHeaders.Set("x-tokenhub-session-id", strings.Repeat("t", 300))
	priorityHeaders.Set("session-id", strings.Repeat("s", 300))
	priorityHeaders.Set("x-claude-code-session-id", strings.Repeat("c", 300))
	priorityResult := codexAnthropicCompatibilityHeaders(priorityHeaders, nil)
	if priorityResult.Get("session-id") != codexShortIdentifier(strings.Repeat("t", 300)) {
		t.Fatal("Anthropic session priority changed during normalization")
	}

	chatRequest, err := chatToCodexResponsesRequest(ChatCompletionRequest{
		Model:          localCodexCompatModel,
		Messages:       []ChatMessage{{Role: "user", Content: "inspect"}},
		PromptCacheKey: longSession,
	})
	if err != nil {
		t.Fatalf("convert Chat request: %v", err)
	}
	var chatBody map[string]any
	encodedChat, err := json.Marshal(chatRequest)
	if err != nil {
		t.Fatalf("marshal Chat bridge request: %v", err)
	}
	if err := json.Unmarshal(encodedChat, &chatBody); err != nil {
		t.Fatalf("decode Chat bridge request: %v", err)
	}
	if chatBody["prompt_cache_key"] != upstreamSession {
		t.Fatalf("Chat prompt_cache_key = %#v", chatBody["prompt_cache_key"])
	}

	nativeIncoming := http.Header{}
	nativeIncoming.Set("session-id", longSession)
	nativeTarget := http.Header{}
	applyCodexRequestHeaders(nativeTarget, nativeIncoming)
	if nativeTarget.Get("session-id") != longSession {
		t.Fatal("native Codex Responses session-id must preserve its wire contract")
	}
}

func TestLocalCodexCompatSerializesClaudeCodeToolsOnly(t *testing.T) {
	request, err := anthropicToCodexResponsesRequest(localCodexCompatAnthropicRequest(t, map[string]any{
		"model":      localCodexCompatModel,
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Inspect the workspace."},
		},
		"tools": []any{
			map[string]any{
				"name":         "read_file",
				"description":  "Read a file.",
				"input_schema": map[string]any{"type": "object"},
			},
			map[string]any{
				"name":         "run_command",
				"description":  "Run a command.",
				"input_schema": map[string]any{"type": "object"},
			},
		},
	}))
	if err != nil {
		t.Fatalf("convert Messages request: %v", err)
	}
	original := cloneRawJSON(request.raw, 0)

	claudeHeaders := http.Header{}
	claudeHeaders.Set("user-agent", "claude-cli/2.1.37 (external, sdk-cli)")
	applyClaudeCodeCodexToolConstraints(&request, claudeHeaders)
	var serialized bool
	if err := json.Unmarshal(request.raw["parallel_tool_calls"], &serialized); err != nil {
		t.Fatalf("decode Claude Code parallel_tool_calls: %v", err)
	}
	if serialized {
		t.Fatal("Claude Code Codex tool calls must be serialized")
	}

	regular := ResponsesRequest{raw: original}
	regularHeaders := http.Header{}
	regularHeaders.Set("user-agent", "real-anthropic-client/1.0")
	applyClaudeCodeCodexToolConstraints(&regular, regularHeaders)
	var parallel bool
	if err := json.Unmarshal(regular.raw["parallel_tool_calls"], &parallel); err != nil {
		t.Fatalf("decode regular client parallel_tool_calls: %v", err)
	}
	if !parallel {
		t.Fatal("regular Messages clients must retain parallel tool calls")
	}

	noTools, err := anthropicToCodexResponsesRequest(localCodexCompatAnthropicRequest(t, map[string]any{
		"model":      localCodexCompatModel,
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Reply with a marker."},
		},
	}))
	if err != nil {
		t.Fatalf("convert no-tools request: %v", err)
	}
	applyClaudeCodeCodexToolConstraints(&noTools, claudeHeaders)
	if _, exists := noTools.raw["parallel_tool_calls"]; exists {
		t.Fatal("no-tools Claude Code request must not gain parallel_tool_calls")
	}
}

func TestLocalCodexCompatAnthropicRouteCompatibility(t *testing.T) {
	validRequest := localCodexCompatAnthropicRequest(t, map[string]any{
		"model":      localCodexCompatModel,
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "Inspect this repository."},
				},
			},
		},
	})
	codexRoute := RouteSelection{
		Provider: Provider{
			ID:   "prv_codex_subscription",
			Type: ProviderOpenAICodex,
		},
		ProviderModel: localCodexCompatModel,
	}
	if err := validateAnthropicRouteCompatibility(codexRoute, validRequest); err != nil {
		t.Fatalf("valid Codex route was rejected: %v", err)
	}

	filtered, err := compatibleAnthropicRoutes(
		RoutedCall{Routes: []RouteSelection{
			{
				Provider: Provider{
					ID:   "prv_unsupported_transport",
					Type: "unsupported_transport",
				},
				ProviderModel: localCodexCompatModel,
			},
			codexRoute,
		}},
		validRequest,
	)
	if err != nil {
		t.Fatalf("filter compatible routes: %v", err)
	}
	if len(filtered.Routes) != 1 || filtered.Routes[0].Provider.Type != ProviderOpenAICodex {
		t.Fatalf("compatible routes = %#v", filtered.Routes)
	}

	unsupportedRequest := localCodexCompatAnthropicRequest(t, map[string]any{
		"model":      localCodexCompatModel,
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type": "url",
							"url":  "https://static.openai.com/assets/openai-avatar.png",
						},
					},
				},
			},
		},
	})
	err = validateAnthropicRouteCompatibility(codexRoute, unsupportedRequest)
	if err == nil || AsHTTPError(err).Code != "unsupported_content_block" {
		t.Fatalf("assistant image compatibility error = %#v", err)
	}
}

func TestLocalCodexCompatSSETextToolAndIncomplete(t *testing.T) {
	t.Run("text incomplete", func(t *testing.T) {
		source := localCodexCompatSSE(t,
			map[string]any{
				"type":     "response.created",
				"response": map[string]any{"id": "resp_01JZ9KFS49P80A59M4KJJHV2Y3"},
			},
			map[string]any{
				"type":         "response.output_text.delta",
				"output_index": 0,
				"item_id":      "msg_01JZ9KG3WXQ4606G7QAYV4N9VH",
				"delta":        "The compatibility bridge",
			},
			map[string]any{
				"type":         "response.output_text.delta",
				"output_index": 0,
				"item_id":      "msg_01JZ9KG3WXQ4606G7QAYV4N9VH",
				"delta":        " preserved the stream.",
			},
			map[string]any{
				"type":         "response.output_text.done",
				"output_index": 0,
				"item_id":      "msg_01JZ9KG3WXQ4606G7QAYV4N9VH",
				"text":         "The compatibility bridge preserved the stream.",
			},
			map[string]any{
				"type": "response.incomplete",
				"response": map[string]any{
					"id":                 "resp_01JZ9KFS49P80A59M4KJJHV2Y3",
					"status":             "incomplete",
					"incomplete_details": map[string]any{"reason": "max_output_tokens"},
					"output":             []any{},
					"usage": map[string]any{
						"input_tokens":  18,
						"output_tokens": 7,
						"total_tokens":  25,
					},
				},
			},
		)
		recorder := httptest.NewRecorder()
		sink := newCodexAnthropicStreamSink(recorder, localCodexCompatModel, 18, nil)
		usage, err := consumeCodexCompatibilityStream(strings.NewReader(source), sink)
		if err != nil {
			t.Fatalf("consume incomplete text stream: %v", err)
		}
		if usage.PromptTokens != 18 || usage.CompletionTokens != 7 || usage.TotalTokens != 25 {
			t.Fatalf("stream usage = %#v", usage)
		}
		events := localCodexCompatDecodedSSE(t, recorder.Body.String())
		var text strings.Builder
		stopReason := ""
		messageStopped := false
		var messageDeltaUsage map[string]any
		for _, event := range events {
			switch event["type"] {
			case "content_block_delta":
				delta := localCodexCompatMap(t, event["delta"], "content block delta")
				if delta["type"] == "text_delta" {
					text.WriteString(delta["text"].(string))
				}
			case "message_delta":
				stopReason, _ = localCodexCompatMap(t, event["delta"], "message delta")["stop_reason"].(string)
				messageDeltaUsage = localCodexCompatMap(t, event["usage"], "message delta usage")
			case "message_stop":
				messageStopped = true
			}
		}
		if text.String() != "The compatibility bridge preserved the stream." {
			t.Fatalf("streamed text = %q", text.String())
		}
		if stopReason != "max_tokens" || !messageStopped {
			t.Fatalf("terminal Anthropic events: stop_reason=%q message_stop=%v", stopReason, messageStopped)
		}
		if len(messageDeltaUsage) != 1 || messageDeltaUsage["output_tokens"] != float64(7) {
			t.Fatalf("terminal Anthropic usage = %#v", messageDeltaUsage)
		}
	})

	t.Run("unsigned reasoning omitted", func(t *testing.T) {
		source := localCodexCompatSSE(t,
			map[string]any{
				"type":     "response.created",
				"response": map[string]any{"id": "resp_unsigned_reasoning_stream"},
			},
			map[string]any{
				"type":         "response.reasoning_summary_text.delta",
				"output_index": 0,
				"item_id":      "rs_unsigned",
				"delta":        "Unsigned summary",
			},
			map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item": map[string]any{
					"id":      "rs_unsigned",
					"type":    "reasoning",
					"summary": []any{map[string]any{"type": "summary_text", "text": "Unsigned summary"}},
				},
			},
			map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_unsigned_reasoning_stream",
					"status": "completed",
					"output": []any{},
					"usage":  map[string]any{"input_tokens": 3, "output_tokens": 1, "total_tokens": 4},
				},
			},
		)
		recorder := httptest.NewRecorder()
		sink := newCodexAnthropicStreamSink(recorder, localCodexCompatModel, 3, nil)
		if _, err := consumeCodexCompatibilityStream(strings.NewReader(source), sink); err != nil {
			t.Fatalf("consume unsigned reasoning stream: %v", err)
		}
		if strings.Contains(recorder.Body.String(), `"type":"thinking"`) {
			t.Fatalf("unsigned reasoning leaked into Anthropic stream: %s", recorder.Body.String())
		}
	})

	t.Run("tool call", func(t *testing.T) {
		longToolName := "workspace_read_file_with_full_path_and_repository_boundary_validation"
		nameMap := codexToolNameMap([]string{longToolName})
		shortToolName := nameMap[longToolName]
		source := localCodexCompatSSE(t,
			map[string]any{
				"type":     "response.created",
				"response": map[string]any{"id": "resp_01JZ9M1X4W0K2VDRJ3MY6AHQ6S"},
			},
			map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"type":      "function_call",
					"id":        "fc_01JZ9M2B6W8JKR8GGZR59A8NFX",
					"call_id":   "call_01JZ9M2MM4K9T3RKC8RP8KMZGK",
					"name":      shortToolName,
					"arguments": "",
				},
			},
			map[string]any{
				"type":         "response.function_call_arguments.delta",
				"output_index": 0,
				"item_id":      "fc_01JZ9M2B6W8JKR8GGZR59A8NFX",
				"delta":        `{"path":"backend/`,
			},
			map[string]any{
				"type":         "response.function_call_arguments.delta",
				"output_index": 0,
				"item_id":      "fc_01JZ9M2B6W8JKR8GGZR59A8NFX",
				"delta":        `internal/server/http.go"}`,
			},
			map[string]any{
				"type":         "response.function_call_arguments.done",
				"output_index": 0,
				"item_id":      "fc_01JZ9M2B6W8JKR8GGZR59A8NFX",
				"arguments":    `{"path":"backend/internal/server/http.go"}`,
			},
			map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item": map[string]any{
					"type":      "function_call",
					"id":        "fc_01JZ9M2B6W8JKR8GGZR59A8NFX",
					"call_id":   "call_01JZ9M2MM4K9T3RKC8RP8KMZGK",
					"name":      shortToolName,
					"arguments": `{"path":"backend/internal/server/http.go"}`,
				},
			},
			map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_01JZ9M1X4W0K2VDRJ3MY6AHQ6S",
					"status": "completed",
					"output": []any{},
					"usage": map[string]any{
						"input_tokens":  11,
						"output_tokens": 4,
						"total_tokens":  15,
					},
				},
			},
		)
		recorder := httptest.NewRecorder()
		sink := newCodexAnthropicStreamSink(
			recorder,
			localCodexCompatModel,
			11,
			reverseCodexToolNames(nameMap),
		)
		usage, err := consumeCodexCompatibilityStream(strings.NewReader(source), sink)
		if err != nil {
			t.Fatalf("consume tool stream: %v", err)
		}
		if usage.PromptTokens != 11 || usage.CompletionTokens != 4 || usage.TotalTokens != 15 {
			t.Fatalf("stream usage = %#v", usage)
		}
		events := localCodexCompatDecodedSSE(t, recorder.Body.String())
		restoredName := ""
		callID := ""
		var arguments strings.Builder
		stopReason := ""
		for _, event := range events {
			switch event["type"] {
			case "content_block_start":
				block := localCodexCompatMap(t, event["content_block"], "content block start")
				if block["type"] == "tool_use" {
					restoredName, _ = block["name"].(string)
					callID, _ = block["id"].(string)
				}
			case "content_block_delta":
				delta := localCodexCompatMap(t, event["delta"], "content block delta")
				if delta["type"] == "input_json_delta" {
					arguments.WriteString(delta["partial_json"].(string))
				}
			case "message_delta":
				stopReason, _ = localCodexCompatMap(t, event["delta"], "message delta")["stop_reason"].(string)
			}
		}
		if restoredName != longToolName {
			t.Fatalf("streamed tool name = %q, want %q", restoredName, longToolName)
		}
		if callID != "call_01JZ9M2MM4K9T3RKC8RP8KMZGK" {
			t.Fatalf("streamed call id = %q", callID)
		}
		if arguments.String() != `{"path":"backend/internal/server/http.go"}` {
			t.Fatalf("streamed arguments = %q", arguments.String())
		}
		if stopReason != "tool_use" {
			t.Fatalf("tool stream stop_reason = %q", stopReason)
		}
	})
}

func localCodexCompatAnthropicRequest(t *testing.T, body map[string]any) anthropicMessagesRequest {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode Anthropic request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(encoded))
	request.Header.Set("content-type", "application/json")
	decoded, err := New(NewMemoryStore()).decodeAnthropicMessagesRequest(httptest.NewRecorder(), request, true)
	if err != nil {
		t.Fatalf("decode Anthropic request: %v", err)
	}
	return decoded
}

func TestLocalCodexCompatNativeAnthropicDropsForeignReasoning(t *testing.T) {
	codexBlock := map[string]any{
		"type":      "thinking",
		"thinking":  "Codex private reasoning",
		"signature": encodeProviderSignature(codexSignatureProvider, "encrypted-codex-state"),
	}
	raw := map[string]any{
		"model": "shared-model",
		"messages": []any{map[string]any{
			"role": "assistant",
			"content": []any{
				codexBlock,
				map[string]any{"type": "text", "text": "portable answer"},
				map[string]any{
					"type":      "thinking",
					"thinking":  "Anthropic reasoning",
					"signature": encodeProviderSignature(anthropicSignatureProvider, "native-signature"),
				},
				map[string]any{
					"type":      "thinking",
					"thinking":  "Already native",
					"signature": "raw-native-signature",
				},
			},
		}},
	}

	payload := nativeAnthropicPayload(raw)
	messages := localCodexCompatSlice(t, payload["messages"], "messages")
	message := localCodexCompatMap(t, messages[0], "message")
	content := localCodexCompatSlice(t, message["content"], "content")
	if len(content) != 3 {
		t.Fatalf("native content blocks = %#v, want foreign reasoning removed", content)
	}
	decoded := localCodexCompatMap(t, content[1], "decoded Anthropic thinking")
	if decoded["signature"] != "native-signature" {
		t.Fatalf("Anthropic signature was not decoded: %#v", decoded)
	}
	originalMessages := localCodexCompatSlice(t, raw["messages"], "original messages")
	original := localCodexCompatMap(t, originalMessages[0], "original message")
	if len(localCodexCompatSlice(t, original["content"], "original content")) != 4 {
		t.Fatal("native payload conversion mutated the caller request")
	}
}

func localCodexCompatResponsesRequestMap(t *testing.T, request ResponsesRequest) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode Responses request: %v", err)
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode Responses request: %v", err)
	}
	return decoded
}

func localCodexCompatMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", name, value)
	}
	return result
}

func localCodexCompatSlice(t *testing.T, value any, name string) []any {
	t.Helper()
	result, ok := anySlice(value)
	if !ok {
		t.Fatalf("%s = %#v, want array", name, value)
	}
	return result
}

func localCodexCompatSSE(t *testing.T, payloads ...map[string]any) string {
	t.Helper()
	var result strings.Builder
	for _, payload := range payloads {
		eventType, _ := payload["type"].(string)
		if eventType == "" {
			t.Fatalf("SSE payload has no type: %#v", payload)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode SSE payload: %v", err)
		}
		result.WriteString("event: ")
		result.WriteString(eventType)
		result.WriteByte('\n')
		result.WriteString("data: ")
		result.Write(encoded)
		result.WriteString("\n\n")
	}
	return result.String()
}

func localCodexCompatDecodedSSE(t *testing.T, wire string) []map[string]any {
	t.Helper()
	decoder := newSSEDecoder(strings.NewReader(wire))
	events := make([]map[string]any, 0, 8)
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			return events
		}
		if err != nil {
			t.Fatalf("decode SSE frame: %v", err)
		}
		payload, err := decodeSSEData(event)
		if err != nil {
			t.Fatalf("decode SSE data: %v", err)
		}
		if payload != nil {
			events = append(events, payload)
		}
	}
}
