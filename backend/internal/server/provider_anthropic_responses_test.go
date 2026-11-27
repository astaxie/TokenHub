package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAnthropicResponsesStringInputConversion(t *testing.T) {
	store := true
	req := ResponsesRequest{
		Model:        "claude-sonnet-4.5",
		Input:        "Say hello",
		Instructions: "Be terse.",
		MaxTokens:    512,
		Store:        &store,
		raw: map[string]json.RawMessage{
			"tools":       []byte(`[{"type":"function","name":"get_time","description":"Read the clock","parameters":{"type":"object","properties":{}}}]`),
			"tool_choice": []byte(`"required"`),
		},
	}
	payload, err := buildAnthropicResponsesPayload(req)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if payload["max_tokens"] != int64(512) {
		t.Fatalf("max_tokens = %v, want 512", payload["max_tokens"])
	}
	if payload["system"] != "Be terse." {
		t.Fatalf("system = %v, want the instructions", payload["system"])
	}
	tools, ok := payload["tools"].([]map[string]any)
	if !ok || len(tools) != 1 || tools[0]["name"] != "get_time" {
		t.Fatalf("tools = %v, want one get_time entry", payload["tools"])
	}
	choice, ok := payload["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "any" {
		t.Fatalf("tool_choice = %v, want any", payload["tool_choice"])
	}
}

func TestAnthropicResponsesDefaultMaxTokensAndAutoChoice(t *testing.T) {
	req := ResponsesRequest{Model: "claude-sonnet-4.5", Input: "hi", raw: map[string]json.RawMessage{
		"tools":       []byte(`[{"type":"function","name":"noop","parameters":{}}]`),
		"tool_choice": []byte(`"auto"`),
	}}
	payload, err := buildAnthropicResponsesPayload(req)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if payload["max_tokens"] != int64(anthropicResponsesDefaultMaxTokens) {
		t.Fatalf("max_tokens = %v, want the documented default", payload["max_tokens"])
	}
	if choice, ok := payload["tool_choice"].(map[string]any); !ok || choice["type"] != "auto" {
		t.Fatalf("tool_choice = %v, want auto", payload["tool_choice"])
	}
}

func TestAnthropicResponsesToolChoiceNoneDropsTools(t *testing.T) {
	req := ResponsesRequest{Model: "claude-sonnet-4.5", Input: "hi", raw: map[string]json.RawMessage{
		"tools":       []byte(`[{"type":"function","name":"noop","parameters":{}}]`),
		"tool_choice": []byte(`"none"`),
	}}
	payload, err := buildAnthropicResponsesPayload(req)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if _, present := payload["tools"]; present {
		t.Fatalf("tool_choice none must drop tools, got %v", payload["tools"])
	}
}

func TestAnthropicResponsesToolRoundTripItems(t *testing.T) {
	req := ResponsesRequest{
		Model: "claude-sonnet-4.5",
		Input: []any{
			map[string]any{"type": "message", "role": "user", "content": "What time is it?"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "get_time", "arguments": `{"tz":"utc"}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "12:00"},
			map[string]any{"type": "reasoning"}, // provider-internal, skipped
		},
	}
	payload, err := buildAnthropicResponsesPayload(req)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	messages, ok := payload["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages = %T, want a slice", payload["messages"])
	}
	// The tool loop becomes three messages: the question, the assistant tool
	// use, and the tool result delivered as a user message.
	if len(messages) != 3 {
		t.Fatalf("expected question, tool use, and tool result messages, got %d: %v", len(messages), messages)
	}
	if messages[0]["role"] != "user" || messages[1]["role"] != "assistant" || messages[2]["role"] != "user" {
		t.Fatalf("roles = %v/%v/%v, want user/assistant/user", messages[0]["role"], messages[1]["role"], messages[2]["role"])
	}
	last, ok := messages[2]["content"].([]any)
	if !ok || len(last) != 1 {
		t.Fatalf("tool result content = %v", messages[1]["content"])
	}
	toolResult, _ := last[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "call_1" {
		t.Fatalf("tool result block = %v", toolResult)
	}
}

func TestAnthropicResponsesUnknownItemRejected(t *testing.T) {
	req := ResponsesRequest{Model: "claude-sonnet-4.5", Input: []any{
		map[string]any{"type": "item_reference", "id": "itm_1"},
	}}
	_, err := buildAnthropicResponsesPayload(req)
	if err == nil || !strings.Contains(err.Error(), "item_reference") {
		t.Fatalf("expected a named unsupported-item error, got %v", err)
	}
	statusCode := http.StatusBadRequest
	if httpErr := AsHTTPError(err); httpErr.Status != statusCode {
		t.Fatalf("status = %d, want %d", httpErr.Status, statusCode)
	}
}

func TestAnthropicResponsesImageConversion(t *testing.T) {
	req := ResponsesRequest{Model: "claude-sonnet-4.5", Input: []any{
		map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "What is this?"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aGVsbG8="},
			map[string]any{"type": "input_image", "image_url": "https://example.test/cat.png"},
		}},
	}}
	payload, err := buildAnthropicResponsesPayload(req)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	messages := payload["messages"].([]map[string]any)
	content := messages[0]["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("content = %v, want text + image + omission note", content)
	}
	image, _ := content[1].(map[string]any)
	if image["type"] != "image" {
		t.Fatalf("data URL image must convert to an Anthropic image block, got %v", image)
	}
	remote, _ := content[2].(map[string]any)
	if remote["type"] != "text" {
		t.Fatalf("remote image URLs must degrade to an explicit note, got %v", remote)
	}
}

func TestAnthropicResponseToResponsesObjectShape(t *testing.T) {
	body := map[string]any{
		"id":   "msg_01ABC",
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "text", "text": "你好，世界"},
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "get_time", "input": map[string]any{"tz": "utc"}},
		},
		"stop_reason": "max_tokens",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
	}
	response := anthropicResponseToResponsesObject("claude-sonnet-4.5", body, Usage{PromptTokens: 10, CompletionTokens: 5})
	if response["id"] != "resp_01ABC" {
		t.Fatalf("id = %v, want resp_01ABC", response["id"])
	}
	if response["status"] != "incomplete" {
		t.Fatalf("status = %v, want incomplete for max_tokens", response["status"])
	}
	details, _ := response["incomplete_details"].(map[string]any)
	if details["reason"] != "max_output_tokens" {
		t.Fatalf("incomplete_details = %v", details)
	}
	output, _ := response["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output = %v, want a message and a function_call", output)
	}
	functionCall, _ := output[1].(map[string]any)
	if functionCall["call_id"] != "toolu_1" {
		t.Fatalf("function_call call_id must keep the Anthropic tool id, got %v", functionCall["call_id"])
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(functionCall["arguments"].(string)), &arguments); err != nil {
		t.Fatalf("arguments must stay valid JSON, got %v", functionCall["arguments"])
	}
	if arguments["tz"] != "utc" {
		t.Fatalf("arguments = %v", arguments)
	}
	if !strings.Contains(response["output_text"].(string), "你好，世界") {
		t.Fatalf("output_text must keep multibyte content, got %v", response["output_text"])
	}
}
