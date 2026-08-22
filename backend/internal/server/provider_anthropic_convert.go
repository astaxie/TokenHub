package server

import (
	"fmt"
	"strings"
	"time"
)

// Conversion between OpenAI Chat Completions and the Anthropic Messages wire
// format. Both the streaming and non-streaming adapter paths share these
// functions so the two cannot drift apart.

const anthropicDefaultMaxTokens = 1024

// buildAnthropicRequest renders an OpenAI chat request as an Anthropic Messages
// request body.
func buildAnthropicRequest(providerModel string, req ChatCompletionRequest, reasoningEffort *string) (map[string]any, error) {
	system, messages, err := anthropicMessagesFromChat(req.Messages)
	if err != nil {
		return nil, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		// Anthropic requires an explicit positive max_tokens; 0 carries special
		// prompt-cache semantics and must not be used as a default.
		maxTokens = anthropicDefaultMaxTokens
	}

	payload := map[string]any{
		"model":      providerModel,
		"max_tokens": maxTokens,
		"messages":   messages,
	}
	if len(system) > 0 {
		payload["system"] = system
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	if stop := anthropicStopSequences(req.Stop); len(stop) > 0 {
		payload["stop_sequences"] = stop
	}
	if reasoningEffort != nil {
		payload["output_config"] = map[string]any{"effort": *reasoningEffort}
	}

	tools, err := parseChatTools(req.Tools)
	if err != nil {
		return nil, err
	}
	choice, err := parseChatToolChoice(req.ToolChoice)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		if choice.Mode == "function" && !toolNameDeclared(tools, choice.Name) {
			return nil, unsupportedContentError(fmt.Sprintf("tool_choice names %q which is not present in tools", choice.Name))
		}
		payload["tools"] = anthropicToolDefinitions(tools)
		// tool_choice "none" keeps the tool definitions in place. Dropping them
		// would change the tool system prompt, token usage and prompt-cache
		// prefix, so it is not an equivalent rewrite.
		payload["tool_choice"] = anthropicToolChoice(choice, req.ParallelToolCalls)
	}
	return payload, nil
}

func toolNameDeclared(tools []chatToolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func anthropicToolDefinitions(tools []chatToolDefinition) []map[string]any {
	converted := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]any{
			"name":         tool.Name,
			"input_schema": tool.Parameters,
		}
		if tool.Description != "" {
			entry["description"] = tool.Description
		}
		if tool.Strict {
			// strict is a top-level tool field in Anthropic, not part of the schema.
			entry["strict"] = true
		}
		converted = append(converted, entry)
	}
	return converted
}

func anthropicToolChoice(choice chatToolChoice, parallelToolCalls *bool) map[string]any {
	converted := map[string]any{}
	switch choice.Mode {
	case "required":
		converted["type"] = "any"
	case "none":
		converted["type"] = "none"
	case "function":
		converted["type"] = "tool"
		converted["name"] = choice.Name
	default:
		converted["type"] = "auto"
	}
	if parallelToolCalls != nil && !*parallelToolCalls {
		// Anthropic expects this inside tool_choice, not at the request root.
		converted["disable_parallel_tool_use"] = true
	}
	return converted
}

func anthropicStopSequences(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []any:
		sequences := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				sequences = append(sequences, text)
			}
		}
		return sequences
	default:
		return nil
	}
}

// anthropicMessagesFromChat splits OpenAI messages into the Anthropic top-level
// system prompt and the user/assistant turn list.
func anthropicMessagesFromChat(messages []ChatMessage) ([]map[string]any, []map[string]any, error) {
	var (
		system         []map[string]any
		converted      []map[string]any
		pendingResults []map[string]any
	)

	// Tool results must arrive in a single user turn that immediately follows
	// the assistant turn holding the matching tool_use blocks.
	flushResults := func() {
		if len(pendingResults) == 0 {
			return
		}
		converted = append(converted, map[string]any{"role": "user", "content": pendingResults})
		pendingResults = nil
	}

	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			flushResults()
			parts, err := parseChatContent(message.Content)
			if err != nil {
				return nil, nil, err
			}
			if text := contentPartsText(parts); text != "" {
				system = append(system, map[string]any{"type": "text", "text": text})
			}
		case "tool":
			result, err := anthropicToolResultBlock(message)
			if err != nil {
				return nil, nil, err
			}
			pendingResults = append(pendingResults, result)
		case "assistant":
			flushResults()
			blocks, err := anthropicAssistantBlocks(message)
			if err != nil {
				return nil, nil, err
			}
			if len(blocks) == 0 {
				continue
			}
			converted = append(converted, map[string]any{"role": "assistant", "content": blocks})
		default:
			flushResults()
			blocks, err := anthropicUserBlocks(message)
			if err != nil {
				return nil, nil, err
			}
			if len(blocks) == 0 {
				continue
			}
			converted = append(converted, map[string]any{"role": "user", "content": blocks})
		}
	}
	flushResults()
	return system, converted, nil
}

func anthropicToolResultBlock(message ChatMessage) (map[string]any, error) {
	if message.ToolCallID == "" {
		return nil, unsupportedContentError("tool messages must carry tool_call_id")
	}
	parts, err := parseChatContent(message.Content)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"type":        "tool_result",
		"tool_use_id": message.ToolCallID,
		"content":     contentPartsText(parts),
	}, nil
}

// anthropicAssistantBlocks rebuilds an assistant turn. Block order matters:
// thinking blocks must precede text, which must precede tool_use.
func anthropicAssistantBlocks(message ChatMessage) ([]map[string]any, error) {
	blocks := make([]map[string]any, 0, 3)

	if block := anthropicThinkingBlock(message); block != nil {
		blocks = append(blocks, block)
	}
	if data := strings.TrimSpace(message.RedactedReasoningContent); data != "" {
		blocks = append(blocks, map[string]any{"type": "redacted_thinking", "data": data})
	}

	parts, err := parseChatContent(message.Content)
	if err != nil {
		return nil, err
	}
	if text := contentPartsText(parts); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}

	calls, err := parseChatToolCalls(message.ToolCalls)
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		if call.ID == "" {
			return nil, unsupportedContentError("assistant tool calls must carry an id")
		}
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": call.Arguments,
		})
	}
	return blocks, nil
}

// anthropicThinkingBlock reconstructs a thinking block only when the client
// echoed back a signature this provider minted. A thinking block with a missing
// or foreign signature is rejected by Anthropic with HTTP 400, so the block is
// dropped instead: losing reasoning continuity is recoverable, a failed request
// is not.
//
// The signature alone gates reconstruction. Models that omit the thinking text
// still return a signed block that must be replayed verbatim, so an empty
// thinking string is valid as long as the signature checks out.
func anthropicThinkingBlock(message ChatMessage) map[string]any {
	signature, ok := decodeProviderSignature(anthropicSignatureProvider, message.ReasoningSignature)
	if !ok {
		return nil
	}
	return map[string]any{
		"type":      "thinking",
		"thinking":  message.ReasoningContent,
		"signature": signature,
	}
}

func anthropicUserBlocks(message ChatMessage) ([]map[string]any, error) {
	parts, err := parseChatContent(message.Content)
	if err != nil {
		return nil, err
	}
	blocks := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case chatContentText:
			if part.Text == "" {
				continue
			}
			blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
		case chatContentImageURL:
			blocks = append(blocks, map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "url", "url": part.URL},
			})
		case chatContentImageB64:
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": part.MediaType,
					"data":       part.Data,
				},
			})
		}
	}
	return blocks, nil
}

// anthropicChatResponse converts an Anthropic Messages response body into an
// OpenAI chat completion object.
func anthropicChatResponse(body map[string]any, model string, usage Usage) (map[string]any, error) {
	content, _ := body["content"].([]any)

	var (
		textParts []string
		reasoning []string
		signature string
		redacted  string
		toolCalls []any
	)

	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch blockType, _ := block["type"].(string); blockType {
		case "text":
			if text, ok := block["text"].(string); ok {
				textParts = append(textParts, text)
			}
		case "thinking":
			if text, ok := block["thinking"].(string); ok {
				reasoning = append(reasoning, text)
			}
			if value, ok := block["signature"].(string); ok && value != "" {
				signature = value
			}
		case "redacted_thinking":
			if data, ok := block["data"].(string); ok {
				redacted = data
			}
		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": encodeToolCallArguments(block["input"]),
				},
			})
		}
	}

	message := map[string]any{"role": "assistant"}
	if len(textParts) > 0 {
		message["content"] = strings.Join(textParts, "")
	} else {
		// A tool-only turn has no text; null distinguishes it from an empty reply.
		message["content"] = nil
	}
	if len(reasoning) > 0 {
		message["reasoning_content"] = strings.Join(reasoning, "")
	}
	if signature != "" {
		message["reasoning_signature"] = encodeProviderSignature(anthropicSignatureProvider, signature)
	}
	if redacted != "" {
		message["redacted_reasoning_content"] = redacted
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	stopReason, _ := body["stop_reason"].(string)
	finishReason, err := anthropicFinishReason(stopReason, len(toolCalls) > 0)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"id":      NewID("chatcmpl"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": openAIChatUsageObject(usage),
	}, nil
}
