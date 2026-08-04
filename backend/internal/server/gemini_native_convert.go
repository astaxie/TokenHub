package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const codexBridgeProtocolGemini = "gemini_generate_content"

func geminiToResponsesRequest(model string, payload map[string]any, stream bool) (ResponsesRequest, map[string]string, error) {
	if cached, _ := payload["cachedContent"].(string); strings.TrimSpace(cached) != "" {
		return ResponsesRequest{}, nil, unsupportedCapabilityError("Codex subscription routes do not support Gemini cachedContent")
	}

	nameMap, tools, err := geminiToolsToCodex(payload["tools"])
	if err != nil {
		return ResponsesRequest{}, nil, err
	}
	input, err := geminiContentsToCodex(payload["contents"], nameMap)
	if err != nil {
		return ResponsesRequest{}, nil, err
	}
	body := map[string]any{
		"model":   model,
		"input":   input,
		"stream":  stream,
		"store":   false,
		"include": []any{"reasoning.encrypted_content"},
		"reasoning": map[string]any{
			"effort":  geminiCodexReasoningEffort(payload),
			"summary": "auto",
		},
	}
	if instruction := geminiTextParts(payload["systemInstruction"]); instruction != "" {
		body["instructions"] = instruction
	}
	if len(tools) > 0 {
		body["tools"] = tools
		// Coding CLIs are prepared for repeated tool turns. Serializing calls avoids
		// exhausting a shared Codex subscription account with one assistant turn.
		body["parallel_tool_calls"] = false
		choice, err := geminiToolChoiceToCodex(payload["toolConfig"], nameMap)
		if err != nil {
			return ResponsesRequest{}, nil, err
		}
		body["tool_choice"] = choice
	}
	if key := geminiConversationFingerprint(payload); key != "" {
		body["prompt_cache_key"] = codexShortIdentifier(key)
	}
	request, err := responsesRequestFromMap(body)
	if err != nil {
		return ResponsesRequest{}, nil, err
	}
	return request, reverseCodexToolNames(nameMap), nil
}

func geminiContentsToCodex(value any, names map[string]string) ([]any, error) {
	contents, ok := anySlice(value)
	if !ok || len(contents) == 0 {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_contents", "contents must be a non-empty array")
	}
	result := make([]any, 0, len(contents)*2)
	callIDs := map[string]string{}
	for contentIndex, rawContent := range contents {
		content, ok := rawContent.(map[string]any)
		if !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_content", "each content entry must be an object")
		}
		role, _ := content["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "model" {
			role = "assistant"
		}
		if role == "" {
			role = "user"
		}
		if role != "user" && role != "assistant" {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_content_role", fmt.Sprintf("unsupported Gemini content role %q", role))
		}
		parts, ok := anySlice(content["parts"])
		if !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_content", "content parts must be an array")
		}
		messageParts := make([]any, 0, len(parts))
		flushMessage := func() {
			if len(messageParts) == 0 {
				return
			}
			result = append(result, map[string]any{"type": "message", "role": role, "content": messageParts})
			messageParts = nil
		}
		for partIndex, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_part", "content parts must be objects")
			}
			if text, exists := part["text"]; exists {
				textValue, ok := text.(string)
				if !ok {
					return nil, NewHTTPError(http.StatusBadRequest, "invalid_part", "text must be a string")
				}
				if thought, _ := part["thought"].(bool); thought {
					flushMessage()
					if encrypted, valid := geminiCodexThoughtSignature(part); valid {
						result = append(result, geminiCodexReasoningItem(textValue, encrypted))
					}
					continue
				}
				partType := "input_text"
				if role == "assistant" {
					partType = "output_text"
				}
				messageParts = append(messageParts, map[string]any{"type": partType, "text": textValue})
				continue
			}
			if inline, ok := part["inlineData"].(map[string]any); ok {
				if role == "assistant" {
					return nil, unsupportedContentError("Codex routes do not support model image parts")
				}
				mime, _ := inline["mimeType"].(string)
				data, _ := inline["data"].(string)
				if mime == "" || data == "" {
					return nil, NewHTTPError(http.StatusBadRequest, "invalid_image", "inlineData requires mimeType and data")
				}
				messageParts = append(messageParts, map[string]any{"type": "input_image", "image_url": "data:" + mime + ";base64," + data})
				continue
			}
			if file, ok := part["fileData"].(map[string]any); ok {
				if role == "assistant" {
					return nil, unsupportedContentError("Codex routes do not support model file parts")
				}
				uri, _ := file["fileUri"].(string)
				if uri == "" {
					return nil, NewHTTPError(http.StatusBadRequest, "invalid_image", "fileData requires fileUri")
				}
				messageParts = append(messageParts, map[string]any{"type": "input_image", "image_url": uri})
				continue
			}
			if call, ok := part["functionCall"].(map[string]any); ok {
				flushMessage()
				name, _ := call["name"].(string)
				if name == "" {
					return nil, NewHTTPError(http.StatusBadRequest, "invalid_function_call", "functionCall requires name")
				}
				callID, _ := call["id"].(string)
				if callID == "" {
					callID = geminiStableCallID(contentIndex, partIndex, call)
				}
				callID = codexShortIdentifier(callID)
				callIDs[name] = callID
				if encrypted, valid := geminiCodexThoughtSignature(part); valid {
					result = append(result, geminiCodexReasoningItem("", encrypted))
				}
				arguments, err := json.Marshal(firstNonNil(call["args"], map[string]any{}))
				if err != nil {
					return nil, NewHTTPError(http.StatusBadRequest, "invalid_function_call", "functionCall args must be JSON")
				}
				result = append(result, map[string]any{
					"type": "function_call", "call_id": callID,
					"name": codexMappedToolName(names, name), "arguments": string(arguments),
				})
				continue
			}
			if response, ok := part["functionResponse"].(map[string]any); ok {
				flushMessage()
				name, _ := response["name"].(string)
				callID, _ := response["id"].(string)
				if callID == "" {
					callID = callIDs[name]
				}
				if callID == "" {
					return nil, NewHTTPError(http.StatusBadRequest, "invalid_function_response", "functionResponse must match an earlier functionCall")
				}
				output, err := json.Marshal(firstNonNil(response["response"], map[string]any{}))
				if err != nil {
					return nil, NewHTTPError(http.StatusBadRequest, "invalid_function_response", "functionResponse response must be JSON")
				}
				result = append(result, map[string]any{"type": "function_call_output", "call_id": codexShortIdentifier(callID), "output": string(output)})
				continue
			}
			return nil, NewHTTPError(http.StatusBadRequest, "unsupported_part", "Gemini content part is not supported by Codex routes")
		}
		flushMessage()
	}
	return result, nil
}

func geminiToolsToCodex(value any) (map[string]string, []any, error) {
	if value == nil {
		return nil, nil, nil
	}
	groups, ok := anySlice(value)
	if !ok {
		return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tools", "tools must be an array")
	}
	declarations := make([]map[string]any, 0)
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tools", "each tool group must be an object")
		}
		if _, present := group["codeExecution"]; present {
			return nil, nil, unsupportedCapabilityError("Gemini server-side codeExecution is not supported; Gemini CLI client tools remain available")
		}
		if _, present := group["googleSearch"]; present {
			return nil, nil, unsupportedCapabilityError("Gemini googleSearch is not supported on Codex subscription routes")
		}
		rawDeclarations, _ := anySlice(group["functionDeclarations"])
		for _, rawDeclaration := range rawDeclarations {
			declaration, ok := rawDeclaration.(map[string]any)
			if !ok {
				return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "function declarations must be objects")
			}
			declarations = append(declarations, declaration)
		}
	}
	names := make([]string, 0, len(declarations))
	for _, declaration := range declarations {
		name, _ := declaration["name"].(string)
		if name == "" {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "function declaration requires name")
		}
		names = append(names, name)
	}
	nameMap := codexToolNameMap(names)
	tools := make([]any, 0, len(declarations))
	for _, declaration := range declarations {
		name := declaration["name"].(string)
		parameters, _ := declaration["parametersJsonSchema"].(map[string]any)
		if parameters == nil {
			parameters, _ = declaration["parameters"].(map[string]any)
		}
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		} else {
			parameters = normalizeGeminiJSONSchema(parameters).(map[string]any)
		}
		tool := map[string]any{"type": "function", "name": nameMap[name], "parameters": parameters, "strict": false}
		if description, _ := declaration["description"].(string); description != "" {
			tool["description"] = description
		}
		tools = append(tools, tool)
	}
	return nameMap, tools, nil
}

// The Gemini wire Schema enum serializes primitive types as OBJECT, STRING,
// and so on. Codex expects ordinary JSON Schema, whose type values are lower
// case. Gemini CLI's built-in tools contain these values at several nested
// levels, including anyOf branches, so normalize recursively.
func normalizeGeminiJSONSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if key == "$schema" {
				continue
			}
			if key == "type" {
				switch schemaType := item.(type) {
				case string:
					result[key] = strings.ToLower(schemaType)
					continue
				case []any:
					normalized := make([]any, 0, len(schemaType))
					for _, rawType := range schemaType {
						if text, ok := rawType.(string); ok {
							normalized = append(normalized, strings.ToLower(text))
						} else {
							normalized = append(normalized, rawType)
						}
					}
					result[key] = normalized
					continue
				}
			}
			result[key] = normalizeGeminiJSONSchema(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizeGeminiJSONSchema(item)
		}
		return result
	default:
		return value
	}
}

func geminiToolChoiceToCodex(value any, names map[string]string) (any, error) {
	config, _ := value.(map[string]any)
	calling, _ := config["functionCallingConfig"].(map[string]any)
	mode, _ := calling["mode"].(string)
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "", "AUTO", "VALIDATED":
		return "auto", nil
	case "NONE":
		return "none", nil
	case "ANY":
		allowed, _ := anySlice(calling["allowedFunctionNames"])
		if len(allowed) == 0 {
			return "required", nil
		}
		name, _ := allowed[0].(string)
		mapped := names[name]
		if mapped == "" {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_config", "allowedFunctionNames refers to an undeclared function")
		}
		return map[string]any{"type": "function", "name": mapped}, nil
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool_config", fmt.Sprintf("unsupported function calling mode %q", mode))
	}
}

func geminiTextParts(value any) string {
	object, _ := value.(map[string]any)
	parts, _ := anySlice(object["parts"])
	text := make([]string, 0, len(parts))
	for _, rawPart := range parts {
		part, _ := rawPart.(map[string]any)
		if value, _ := part["text"].(string); strings.TrimSpace(value) != "" {
			text = append(text, value)
		}
	}
	return strings.Join(text, "\n")
}

func geminiCodexReasoningEffort(payload map[string]any) string {
	config, _ := payload["generationConfig"].(map[string]any)
	thinking, _ := config["thinkingConfig"].(map[string]any)
	if level, _ := thinking["thinkingLevel"].(string); level != "" {
		if effort := codexReasoningEffort(level); effort != "" {
			return effort
		}
	}
	if rawBudget, exists := thinking["thinkingBudget"]; exists {
		budget := int(int64FromAny(rawBudget))
		if budget >= 0 {
			return codexEffortForBudget(budget)
		}
	}
	return "medium"
}

func geminiCodexThoughtSignature(part map[string]any) (string, bool) {
	signature, _ := part["thoughtSignature"].(string)
	return decodeProviderSignature(codexSignatureProvider, signature)
}

func geminiCodexReasoningItem(summary string, encrypted string) map[string]any {
	item := map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": encrypted}
	if summary != "" {
		item["summary"] = []any{map[string]any{"type": "summary_text", "text": summary}}
	}
	return item
}

func geminiStableCallID(contentIndex int, partIndex int, call map[string]any) string {
	encoded, _ := json.Marshal(call)
	digest := sha256.Sum256(append([]byte(fmt.Sprintf("%d:%d:", contentIndex, partIndex)), encoded...))
	return "call_" + hex.EncodeToString(digest[:12])
}

func geminiConversationFingerprint(payload map[string]any) string {
	seed := map[string]any{"systemInstruction": payload["systemInstruction"]}
	if contents, ok := anySlice(payload["contents"]); ok && len(contents) > 0 {
		seed["firstContent"] = contents[0]
	}
	encoded, err := json.Marshal(seed)
	if err != nil || len(encoded) == 0 {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "gemini_" + hex.EncodeToString(digest[:16])
}
