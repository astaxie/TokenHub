package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	codexSignatureProvider = "codex"
	codexIdentifierLimit   = 64
)

// anthropicToCodexResponsesRequest translates the Anthropic Messages wire
// format directly into Responses input items. Codex subscription traffic must
// not pass through Chat Completions: doing so loses ordered function-call
// outputs and opaque reasoning continuation data.
func anthropicToCodexResponsesRequest(req anthropicMessagesRequest) (ResponsesRequest, error) {
	body := map[string]any{
		"model":   req.Model,
		"input":   []any{},
		"stream":  req.Stream,
		"store":   false,
		"include": []any{"reasoning.encrypted_content"},
	}
	if system, exists := req.Raw["system"]; exists {
		text, err := anthropicSystemText(system)
		if err != nil {
			return ResponsesRequest{}, err
		}
		if text != "" {
			body["instructions"] = text
		}
	}

	toolNames, tools, err := anthropicToolsToCodex(req.Raw["tools"])
	if err != nil {
		return ResponsesRequest{}, err
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["parallel_tool_calls"] = anthropicParallelTools(req.Raw["tool_choice"])
		choice, err := anthropicToolChoiceToCodex(req.Raw["tool_choice"], toolNames)
		if err != nil {
			return ResponsesRequest{}, err
		}
		body["tool_choice"] = choice
	}

	input := make([]any, 0, len(req.Messages)*2)
	for _, rawMessage := range req.Messages {
		message := rawMessage.(map[string]any)
		items, err := anthropicMessageToCodex(message, toolNames)
		if err != nil {
			return ResponsesRequest{}, err
		}
		input = append(input, items...)
	}
	body["input"] = input

	body["reasoning"] = anthropicCodexReasoning(req.Raw)
	if tier := anthropicCodexServiceTier(req.Raw); tier != "" {
		body["service_tier"] = tier
	}
	return responsesRequestFromMap(body)
}

func anthropicMessageToCodex(message map[string]any, toolNames map[string]string) ([]any, error) {
	role, _ := message["role"].(string)
	blocks, err := anthropicContentBlocks(message["content"])
	if err != nil {
		return nil, err
	}
	responseRole := role
	if role == "system" {
		responseRole = "developer"
	}
	partType := "input_text"
	if responseRole == "assistant" {
		partType = "output_text"
	}

	items := make([]any, 0, len(blocks)+1)
	parts := make([]any, 0, len(blocks))
	flushMessage := func() {
		if len(parts) == 0 {
			return
		}
		items = append(items, map[string]any{
			"type":    "message",
			"role":    responseRole,
			"content": parts,
		})
		parts = make([]any, 0, len(blocks))
	}

	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			text, ok := block["text"].(string)
			if !ok {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_content_block", "text block text must be a string")
			}
			parts = append(parts, map[string]any{"type": partType, "text": text})
		case "image":
			if responseRole == "assistant" {
				return nil, NewHTTPError(http.StatusBadRequest, "unsupported_content_block", "Codex routes do not support assistant image blocks")
			}
			part, err := anthropicImageToCodex(block)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case "thinking":
			flushMessage()
			signature, _ := block["signature"].(string)
			encrypted, ok := decodeProviderSignature(codexSignatureProvider, signature)
			if !ok {
				continue
			}
			reasoning := map[string]any{
				"type":              "reasoning",
				"summary":           []any{},
				"encrypted_content": encrypted,
			}
			if text, _ := block["thinking"].(string); text != "" {
				reasoning["summary"] = []any{map[string]any{"type": "summary_text", "text": text}}
			}
			items = append(items, reasoning)
		case "redacted_thinking":
			// A redacted Anthropic block cannot be replayed to Codex.
		case "tool_use":
			flushMessage()
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			if id == "" || name == "" {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_use", "tool_use requires id and name")
			}
			input := block["input"]
			if input == nil {
				input = map[string]any{}
			}
			arguments, err := json.Marshal(input)
			if err != nil {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_use", "tool_use input must be JSON")
			}
			items = append(items, map[string]any{
				"type":      "function_call",
				"call_id":   codexShortIdentifier(id),
				"name":      codexMappedToolName(toolNames, name),
				"arguments": string(arguments),
			})
		case "tool_result":
			flushMessage()
			callID, _ := block["tool_use_id"].(string)
			if callID == "" {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_result", "tool_result requires tool_use_id")
			}
			output, err := anthropicToolResultToCodex(block["content"])
			if err != nil {
				return nil, err
			}
			if isError, _ := block["is_error"].(bool); isError {
				output = codexErrorToolOutput(output)
			}
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": codexShortIdentifier(callID),
				"output":  output,
			})
		default:
			return nil, NewHTTPError(
				http.StatusBadRequest,
				"unsupported_content_block",
				fmt.Sprintf("Codex routes do not support Anthropic content block type %q", blockType),
			)
		}
	}
	flushMessage()
	if len(items) == 0 {
		items = append(items, map[string]any{
			"type":    "message",
			"role":    responseRole,
			"content": []any{map[string]any{"type": partType, "text": ""}},
		})
	}
	return items, nil
}

func anthropicImageToCodex(block map[string]any) (map[string]any, error) {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_image", "image block source is required")
	}
	sourceType, _ := source["type"].(string)
	imageURL := ""
	switch sourceType {
	case "base64":
		mediaType, _ := source["media_type"].(string)
		data, _ := source["data"].(string)
		if mediaType == "" || data == "" {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_image", "base64 image source requires media_type and data")
		}
		imageURL = "data:" + mediaType + ";base64," + data
	case "url":
		imageURL, _ = source["url"].(string)
		if imageURL == "" {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_image", "URL image source requires url")
		}
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "unsupported_image_source", "Codex routes support base64 and URL image sources")
	}
	return map[string]any{"type": "input_image", "image_url": imageURL}, nil
}

func anthropicToolResultToCodex(content any) (any, error) {
	if content == nil {
		return "", nil
	}
	if text, ok := content.(string); ok {
		return text, nil
	}
	blocks, err := anthropicContentBlocks(content)
	if err != nil {
		return nil, err
	}
	parts := make([]any, 0, len(blocks))
	texts := make([]string, 0, len(blocks))
	textOnly := true
	for _, block := range blocks {
		switch block["type"] {
		case "text":
			text, ok := block["text"].(string)
			if !ok {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_result", "tool result text must be a string")
			}
			texts = append(texts, text)
			parts = append(parts, map[string]any{"type": "input_text", "text": text})
		case "image":
			textOnly = false
			part, err := anthropicImageToCodex(block)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		default:
			return nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool_result", "Codex routes support text and image tool results")
		}
	}
	if textOnly {
		return strings.Join(texts, "\n"), nil
	}
	return parts, nil
}

func codexErrorToolOutput(output any) any {
	if text, ok := output.(string); ok {
		return "Error: " + text
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "Error"
	}
	return "Error: " + string(encoded)
}

func anthropicToolsToCodex(value any) (map[string]string, []any, error) {
	if value == nil {
		return nil, nil, nil
	}
	rawTools, ok := value.([]any)
	if !ok {
		return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tools", "tools must be an array")
	}
	names := make([]string, 0, len(rawTools))
	for _, item := range rawTools {
		tool, ok := item.(map[string]any)
		if !ok {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "each tool must be an object")
		}
		toolType, _ := tool["type"].(string)
		if toolType != "" && toolType != "custom" && toolType != "function" {
			return nil, nil, NewHTTPError(
				http.StatusBadRequest,
				"unsupported_tool",
				fmt.Sprintf("Codex routes do not support Anthropic tool type %q", toolType),
			)
		}
		name, _ := tool["name"].(string)
		if name == "" {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "tool name is required")
		}
		names = append(names, name)
	}
	nameMap := codexToolNameMap(names)
	tools := make([]any, 0, len(rawTools))
	for _, item := range rawTools {
		tool := item.(map[string]any)
		name := tool["name"].(string)
		schema, ok := tool["input_schema"].(map[string]any)
		if !ok {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "tool input_schema must be an object")
		}
		parameters := cloneAnyMap(schema)
		delete(parameters, "$schema")
		converted := map[string]any{
			"type":       "function",
			"name":       nameMap[name],
			"parameters": parameters,
			"strict":     false,
		}
		if description, _ := tool["description"].(string); description != "" {
			converted["description"] = description
		}
		tools = append(tools, converted)
	}
	return nameMap, tools, nil
}

func anthropicToolChoiceToCodex(value any, toolNames map[string]string) (any, error) {
	if value == nil {
		return "auto", nil
	}
	choice, ok := value.(map[string]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_choice", "tool_choice must be an object")
	}
	choiceType, _ := choice["type"].(string)
	switch choiceType {
	case "", "auto":
		return "auto", nil
	case "any":
		return "required", nil
	case "none":
		return "none", nil
	case "tool":
		name, _ := choice["name"].(string)
		if name == "" {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_choice", "tool_choice tool requires name")
		}
		mapped, exists := toolNames[name]
		if !exists {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_choice", "tool_choice refers to an undeclared tool")
		}
		return map[string]any{"type": "function", "name": mapped}, nil
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool_choice", "unsupported tool_choice type")
	}
}

func anthropicParallelTools(value any) bool {
	choice, _ := value.(map[string]any)
	disabled, _ := choice["disable_parallel_tool_use"].(bool)
	return !disabled
}

func anthropicCodexReasoning(raw map[string]any) map[string]any {
	effort := "medium"
	for _, value := range []any{
		raw["effort"],
		nestedAny(raw, "output_config", "effort"),
	} {
		if candidate, ok := value.(string); ok && codexReasoningEffort(candidate) != "" {
			effort = codexReasoningEffort(candidate)
		}
	}
	if thinking, ok := raw["thinking"].(map[string]any); ok {
		switch thinkingType, _ := thinking["type"].(string); thinkingType {
		case "disabled":
			effort = "none"
		case "adaptive", "auto":
			if nested := codexReasoningEffort(anyString(nestedAny(raw, "output_config", "effort"))); nested != "" {
				effort = nested
			} else {
				effort = "high"
			}
		case "enabled":
			effort = codexEffortForBudget(int(int64FromAny(thinking["budget_tokens"])))
		}
	}
	return map[string]any{"effort": effort, "summary": "auto"}
}

func codexEffortForBudget(budget int) string {
	switch {
	case budget <= 0:
		return "none"
	case budget <= 2048:
		return "low"
	case budget <= 8192:
		return "medium"
	case budget <= 16384:
		return "high"
	default:
		return "xhigh"
	}
}

func codexReasoningEffort(value string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return normalized
	default:
		return ""
	}
}

func anthropicCodexServiceTier(raw map[string]any) string {
	for _, value := range []any{raw["service_tier"], raw["speed"]} {
		switch strings.ToLower(strings.TrimSpace(anyString(value))) {
		case "fast", "priority":
			return "priority"
		}
	}
	return ""
}

func chatToCodexResponsesRequest(req ChatCompletionRequest) (ResponsesRequest, error) {
	toolNames, tools, err := chatToolsToCodex(req.Tools)
	if err != nil {
		return ResponsesRequest{}, err
	}
	input := make([]any, 0, len(req.Messages)*2)
	for _, message := range req.Messages {
		items, err := chatMessageToCodex(message, toolNames)
		if err != nil {
			return ResponsesRequest{}, err
		}
		input = append(input, items...)
	}
	body := map[string]any{
		"model":   req.Model,
		"input":   input,
		"stream":  req.Stream,
		"store":   false,
		"include": []any{"reasoning.encrypted_content"},
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["parallel_tool_calls"] = req.ParallelToolCalls == nil || *req.ParallelToolCalls
		choice, err := chatToolChoiceToCodex(req.ToolChoice, toolNames)
		if err != nil {
			return ResponsesRequest{}, err
		}
		body["tool_choice"] = choice
	}
	if effort := normalizedReasoningEffort(req.ReasoningEffort); effort != nil {
		body["reasoning"] = map[string]any{"effort": *effort, "summary": "auto"}
	} else {
		body["reasoning"] = map[string]any{"effort": "medium", "summary": "auto"}
	}
	if req.Metadata != nil {
		body["metadata"] = req.Metadata
	}
	if key, ok := req.PromptCacheKey.(string); ok && strings.TrimSpace(key) != "" {
		body["prompt_cache_key"] = codexShortIdentifier(strings.TrimSpace(key))
	}
	if text, err := chatResponseFormatToCodex(req.ResponseFormat); err != nil {
		return ResponsesRequest{}, err
	} else if text != nil {
		body["text"] = text
	}
	return responsesRequestFromMap(body)
}

func chatMessageToCodex(message ChatMessage, toolNames map[string]string) ([]any, error) {
	role := strings.ToLower(strings.TrimSpace(message.Role))
	switch role {
	case "tool":
		if message.ToolCallID == "" {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_result", "tool message requires tool_call_id")
		}
		return []any{map[string]any{
			"type":    "function_call_output",
			"call_id": codexShortIdentifier(message.ToolCallID),
			"output":  contentToText(message.Content),
		}}, nil
	case "assistant":
		items := make([]any, 0, 3)
		if encrypted, ok := chatCodexReasoningContinuation(message); ok {
			reasoning := map[string]any{
				"type":              "reasoning",
				"summary":           []any{},
				"encrypted_content": encrypted,
			}
			if message.ReasoningContent != "" {
				reasoning["summary"] = []any{map[string]any{"type": "summary_text", "text": message.ReasoningContent}}
			}
			items = append(items, reasoning)
		}
		parts, err := chatContentToCodex(message.Content, true)
		if err != nil {
			return nil, err
		}
		if len(parts) > 0 {
			items = append(items, map[string]any{"type": "message", "role": "assistant", "content": parts})
		}
		calls, ok := anySlice(message.ToolCalls)
		if message.ToolCalls != nil && !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_call", "tool_calls must be an array")
		}
		for _, rawCall := range calls {
			call, ok := rawCall.(map[string]any)
			if !ok {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_call", "tool call must be an object")
			}
			id, _ := call["id"].(string)
			function, _ := call["function"].(map[string]any)
			name, _ := function["name"].(string)
			arguments, _ := function["arguments"].(string)
			if id == "" || name == "" {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_call", "tool call requires id and function name")
			}
			if arguments == "" {
				arguments = "{}"
			}
			items = append(items, map[string]any{
				"type":      "function_call",
				"call_id":   codexShortIdentifier(id),
				"name":      codexMappedToolName(toolNames, name),
				"arguments": arguments,
			})
		}
		if len(items) == 0 {
			items = append(items, map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": ""}},
			})
		}
		return items, nil
	case "system", "developer", "user":
		if role == "system" {
			role = "developer"
		}
		parts, err := chatContentToCodex(message.Content, false)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			parts = []any{map[string]any{"type": "input_text", "text": ""}}
		}
		return []any{map[string]any{"type": "message", "role": role, "content": parts}}, nil
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_message", fmt.Sprintf("unsupported message role %q", message.Role))
	}
}

func chatCodexReasoningContinuation(message ChatMessage) (string, bool) {
	if encrypted, ok := decodeProviderSignature(codexSignatureProvider, message.ReasoningSignature); ok {
		return encrypted, true
	}
	if message.raw == nil {
		return "", false
	}
	var details []map[string]any
	if err := json.Unmarshal(message.raw["reasoning_details"], &details); err != nil {
		return "", false
	}
	toolCallIDs := map[string]struct{}{}
	if calls, ok := anySlice(message.ToolCalls); ok {
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]any)
			if id, _ := call["id"].(string); id != "" {
				toolCallIDs[id] = struct{}{}
			}
		}
	}
	for _, detail := range details {
		id, _ := detail["id"].(string)
		data, _ := detail["data"].(string)
		if detail["type"] != "reasoning.encrypted" || id == "" {
			continue
		}
		if _, exists := toolCallIDs[id]; !exists {
			continue
		}
		if encrypted, ok := decodeProviderSignature(codexSignatureProvider, data); ok {
			return encrypted, true
		}
	}
	return "", false
}

func chatContentToCodex(content any, assistant bool) ([]any, error) {
	textType := "input_text"
	if assistant {
		textType = "output_text"
	}
	if content == nil {
		return nil, nil
	}
	if text, ok := content.(string); ok {
		return []any{map[string]any{"type": textType, "text": text}}, nil
	}
	rawParts, ok := anySlice(content)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_message", "message content must be a string or an array")
	}
	parts := make([]any, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_content_block", "message content blocks must be objects")
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "text", "input_text", "output_text":
			text, _ := part["text"].(string)
			parts = append(parts, map[string]any{"type": textType, "text": text})
		case "image_url":
			if assistant {
				return nil, NewHTTPError(http.StatusBadRequest, "unsupported_content_block", "Codex routes do not support assistant image blocks")
			}
			imageURL := ""
			switch value := part["image_url"].(type) {
			case string:
				imageURL = value
			case map[string]any:
				imageURL, _ = value["url"].(string)
			}
			if imageURL == "" {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_image", "image_url content requires a URL")
			}
			parts = append(parts, map[string]any{"type": "input_image", "image_url": imageURL})
		case "input_image":
			if assistant {
				return nil, NewHTTPError(http.StatusBadRequest, "unsupported_content_block", "Codex routes do not support assistant image blocks")
			}
			parts = append(parts, cloneAnyMap(part))
		default:
			return nil, NewHTTPError(http.StatusBadRequest, "unsupported_content_block", fmt.Sprintf("Codex routes do not support content block type %q", partType))
		}
	}
	return parts, nil
}

func chatToolsToCodex(value any) (map[string]string, []any, error) {
	if value == nil {
		return nil, nil, nil
	}
	rawTools, ok := anySlice(value)
	if !ok {
		return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tools", "tools must be an array")
	}
	names := make([]string, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "each tool must be an object")
		}
		function, _ := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		if tool["type"] != "function" || name == "" {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool", "Codex routes support function tools")
		}
		names = append(names, name)
	}
	nameMap := codexToolNameMap(names)
	tools := make([]any, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool := rawTool.(map[string]any)
		function := tool["function"].(map[string]any)
		name := function["name"].(string)
		parameters, _ := function["parameters"].(map[string]any)
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		} else {
			parameters = cloneAnyMap(parameters)
			delete(parameters, "$schema")
		}
		converted := map[string]any{
			"type":       "function",
			"name":       nameMap[name],
			"parameters": parameters,
			"strict":     false,
		}
		if description, _ := function["description"].(string); description != "" {
			converted["description"] = description
		}
		tools = append(tools, converted)
	}
	return nameMap, tools, nil
}

func chatToolChoiceToCodex(value any, names map[string]string) (any, error) {
	if value == nil {
		return "auto", nil
	}
	if text, ok := value.(string); ok {
		switch text {
		case "auto", "required", "none":
			return text, nil
		default:
			return nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool_choice", "unsupported tool_choice")
		}
	}
	choice, ok := value.(map[string]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_choice", "tool_choice must be a string or object")
	}
	function, _ := choice["function"].(map[string]any)
	name, _ := function["name"].(string)
	if choice["type"] != "function" || name == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool_choice", "Codex routes support function tool choices")
	}
	mapped, exists := names[name]
	if !exists {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_choice", "tool_choice refers to an undeclared tool")
	}
	return map[string]any{"type": "function", "name": mapped}, nil
}

func chatResponseFormatToCodex(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	format, ok := value.(map[string]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_response_format", "response_format must be an object")
	}
	switch formatType, _ := format["type"].(string); formatType {
	case "", "text":
		return nil, nil
	case "json_object":
		return map[string]any{"format": map[string]any{"type": "json_object"}}, nil
	case "json_schema":
		schema, ok := format["json_schema"].(map[string]any)
		if !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_response_format", "json_schema response format is invalid")
		}
		converted := cloneAnyMap(schema)
		converted["type"] = "json_schema"
		return map[string]any{"format": converted}, nil
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "unsupported_response_format", "unsupported response_format type")
	}
}

func responsesRequestFromMap(body map[string]any) (ResponsesRequest, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return ResponsesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	var request ResponsesRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return ResponsesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return request, nil
}

func codexToolNameMap(names []string) map[string]string {
	result := make(map[string]string, len(names))
	used := make(map[string]struct{}, len(names))
	for _, name := range names {
		candidate := codexShortIdentifier(name)
		if _, exists := used[candidate]; exists {
			sum := sha256.Sum256([]byte(name))
			suffix := "_" + hex.EncodeToString(sum[:12])
			candidate = truncateUTF8Bytes(name, codexIdentifierLimit-len(suffix)) + suffix
		}
		result[name] = candidate
		used[candidate] = struct{}{}
	}
	return result
}

func codexMappedToolName(names map[string]string, name string) string {
	if mapped := names[name]; mapped != "" {
		return mapped
	}
	return codexShortIdentifier(name)
}

func codexShortIdentifier(value string) string {
	if len(value) <= codexIdentifierLimit {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	suffix := "_" + hex.EncodeToString(sum[:8])
	return truncateUTF8Bytes(value, codexIdentifierLimit-len(suffix)) + suffix
}

func truncateUTF8Bytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && (value[limit]&0xc0) == 0x80 {
		limit--
	}
	return value[:limit]
}

func nestedAny(values map[string]any, first string, second string) any {
	nested, _ := values[first].(map[string]any)
	return nested[second]
}

func anyString(value any) string {
	text, _ := value.(string)
	return text
}
