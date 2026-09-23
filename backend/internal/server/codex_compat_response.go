package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func codexResponsesToAnthropic(
	body map[string]any,
	req anthropicMessagesRequest,
	usage Usage,
) (map[string]any, error) {
	reverseNames, err := anthropicCodexToolNameReverse(req.Raw["tools"])
	if err != nil {
		return nil, err
	}
	content := make([]any, 0, 4)
	hasToolCalls := false
	outputs, _ := anySlice(body["output"])
	for _, rawItem := range outputs {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "reasoning":
			summary := codexReasoningText(item)
			encrypted, _ := item["encrypted_content"].(string)
			if encrypted != "" {
				block := map[string]any{"type": "thinking", "thinking": summary}
				block["signature"] = encodeProviderSignature(codexSignatureProvider, encrypted)
				content = append(content, block)
			}
		case "message":
			parts, _ := anySlice(item["content"])
			for _, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				switch part["type"] {
				case "output_text":
					if text, _ := part["text"].(string); text != "" {
						content = append(content, map[string]any{"type": "text", "text": text})
					}
				case "refusal":
					if text, _ := part["refusal"].(string); text != "" {
						content = append(content, map[string]any{"type": "text", "text": text})
					}
				}
			}
		case "function_call":
			callID, name, input, err := codexFunctionCall(item)
			if err != nil {
				return nil, err
			}
			hasToolCalls = true
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    codexShortIdentifier(callID),
				"name":  codexRestoreToolName(reverseNames, name),
				"input": input,
			})
		}
	}
	if len(content) == 0 {
		if text := codexResponseOutputText(body); text != "" {
			content = append(content, map[string]any{"type": "text", "text": text})
		} else {
			content = append(content, map[string]any{"type": "text", "text": ""})
		}
	}
	id, _ := body["id"].(string)
	if id == "" {
		id = NewID("msg")
	}
	return map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         req.Model,
		"content":       content,
		"stop_reason":   codexAnthropicStopReason(body, hasToolCalls),
		"stop_sequence": codexStopSequence(body),
		"usage":         anthropicUsageObject(usage),
	}, nil
}

func codexResponsesToChat(
	body map[string]any,
	req ChatCompletionRequest,
	usage Usage,
) (map[string]any, error) {
	reverseNames, err := chatCodexToolNameReverse(req.Tools)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	var reasoning strings.Builder
	reasoningSignature := ""
	toolCalls := make([]any, 0, 2)
	outputs, _ := anySlice(body["output"])
	for _, rawItem := range outputs {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "reasoning":
			reasoning.WriteString(codexReasoningText(item))
			if encrypted, _ := item["encrypted_content"].(string); encrypted != "" {
				reasoningSignature = encodeProviderSignature(codexSignatureProvider, encrypted)
			}
		case "message":
			parts, _ := anySlice(item["content"])
			for _, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				if part["type"] == "output_text" {
					value, _ := part["text"].(string)
					text.WriteString(value)
				}
				if part["type"] == "refusal" {
					value, _ := part["refusal"].(string)
					text.WriteString(value)
				}
			}
		case "function_call":
			callID, name, _, err := codexFunctionCall(item)
			if err != nil {
				return nil, err
			}
			arguments := codexFunctionArguments(item)
			toolCalls = append(toolCalls, map[string]any{
				"id":   codexShortIdentifier(callID),
				"type": "function",
				"function": map[string]any{
					"name":      codexRestoreToolName(reverseNames, name),
					"arguments": arguments,
				},
			})
		}
	}
	if text.Len() == 0 {
		text.WriteString(codexResponseOutputText(body))
	}
	message := map[string]any{
		"role":    "assistant",
		"content": text.String(),
	}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if reasoningSignature != "" {
		message["reasoning_signature"] = reasoningSignature
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		if reasoningSignature != "" {
			firstCall, _ := toolCalls[0].(map[string]any)
			callID, _ := firstCall["id"].(string)
			message["reasoning_details"] = []any{map[string]any{
				"type": "reasoning.encrypted",
				"id":   callID,
				"data": reasoningSignature,
			}}
		}
		if text.Len() == 0 {
			message["content"] = nil
		}
	}
	id, _ := body["id"].(string)
	if id == "" {
		id = NewID("chatcmpl")
	}
	created := int64FromAny(body["created_at"])
	if created == 0 {
		created = time.Now().Unix()
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	} else if codexResponseIncompleteReason(body) != "" {
		finishReason = "length"
	}
	return map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   req.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": openAIChatUsageObject(usage),
	}, nil
}

func codexFunctionCall(item map[string]any) (string, string, map[string]any, error) {
	callID, _ := item["call_id"].(string)
	if callID == "" {
		callID, _ = item["id"].(string)
	}
	name, _ := item["name"].(string)
	if callID == "" || name == "" {
		return "", "", nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Codex function call requires call_id and name")
	}
	input := map[string]any{}
	arguments := codexFunctionArguments(item)
	if strings.TrimSpace(arguments) != "" {
		decoder := json.NewDecoder(strings.NewReader(arguments))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			return "", "", nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Codex function call arguments are not valid JSON")
		}
	}
	return callID, name, input, nil
}

func codexFunctionArguments(item map[string]any) string {
	switch value := item["arguments"].(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return "{}"
		}
		return value
	case map[string]any:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	default:
		return "{}"
	}
}

func codexReasoningText(item map[string]any) string {
	var result strings.Builder
	for _, key := range []string{"summary", "content"} {
		value := item[key]
		if text, ok := value.(string); ok {
			result.WriteString(text)
			continue
		}
		parts, _ := anySlice(value)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			text, _ := part["text"].(string)
			result.WriteString(text)
		}
		if result.Len() > 0 {
			break
		}
	}
	return result.String()
}

func codexAnthropicStopReason(body map[string]any, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_use"
	}
	switch reason := codexResponseIncompleteReason(body); reason {
	case "max_output_tokens", "max_tokens":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	case "":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func codexResponseIncompleteReason(body map[string]any) string {
	if value, _ := body["stop_reason"].(string); value != "" && value != "stop" {
		return value
	}
	details, _ := body["incomplete_details"].(map[string]any)
	value, _ := details["reason"].(string)
	return value
}

func codexStopSequence(body map[string]any) any {
	if value, exists := body["stop_sequence"]; exists {
		return value
	}
	return nil
}

func anthropicCodexToolNameReverse(value any) (map[string]string, error) {
	names, _, err := anthropicToolsToCodex(value)
	if err != nil {
		return nil, err
	}
	return reverseCodexToolNames(names), nil
}

func chatCodexToolNameReverse(value any) (map[string]string, error) {
	names, _, err := chatToolsToCodex(value)
	if err != nil {
		return nil, err
	}
	return reverseCodexToolNames(names), nil
}

func reverseCodexToolNames(names map[string]string) map[string]string {
	result := make(map[string]string, len(names))
	for original, shortened := range names {
		result[shortened] = original
	}
	return result
}

func codexRestoreToolName(names map[string]string, name string) string {
	if original := names[name]; original != "" {
		return original
	}
	return name
}
