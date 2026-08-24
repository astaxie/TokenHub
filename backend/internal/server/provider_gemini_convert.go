package server

import (
	"fmt"
	"strings"
	"time"
)

// Conversion between OpenAI Chat Completions and the Gemini generateContent
// wire format, shared by the streaming and non-streaming adapter paths.

// buildGeminiRequest renders an OpenAI chat request as a Gemini
// generateContent request body.
func buildGeminiRequest(providerModel string, req ChatCompletionRequest) (map[string]any, error) {
	if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		// Gemini has no generateContent equivalent for disabling parallel tool
		// calls. Silently ignoring it would let a client believe a constraint
		// was applied when it was not.
		return nil, unsupportedCapabilityError("Gemini does not support parallel_tool_calls=false")
	}

	systemParts, contents, err := geminiContentsFromChat(req.Messages)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{"contents": contents}
	if len(systemParts) > 0 {
		// System guidance belongs in the top-level systemInstruction. Folding it
		// into a user turn changes how the model weighs it.
		payload["systemInstruction"] = map[string]any{"parts": systemParts}
	}

	generationConfig := map[string]any{}
	if req.MaxTokens > 0 {
		generationConfig["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		generationConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		generationConfig["topP"] = *req.TopP
	}
	if stop := anthropicStopSequences(req.Stop); len(stop) > 0 {
		generationConfig["stopSequences"] = stop
	}
	if effort := normalizedReasoningEffort(req.ReasoningEffort); effort != nil {
		if thinkingConfig, supported := geminiThinkingConfig(providerModel, *effort); supported {
			generationConfig["thinkingConfig"] = thinkingConfig
		}
	}
	if len(generationConfig) > 0 {
		payload["generationConfig"] = generationConfig
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
		payload["tools"] = []map[string]any{{"functionDeclarations": geminiToolDeclarations(tools)}}
		payload["toolConfig"] = map[string]any{"functionCallingConfig": geminiFunctionCallingConfig(choice)}
	}
	return payload, nil
}

func geminiToolDeclarations(tools []chatToolDefinition) []map[string]any {
	declarations := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]any{"name": tool.Name}
		// "parameters" is a narrow OpenAPI Schema subset. Schemas using JSON
		// Schema constructs OpenAPI lacks must go through parametersJsonSchema
		// or the provider rejects an otherwise valid OpenAI tool definition.
		if schemaNeedsJSONSchema(tool.Parameters) {
			entry["parametersJsonSchema"] = tool.Parameters
		} else {
			entry["parameters"] = tool.Parameters
		}
		if tool.Description != "" {
			entry["description"] = tool.Description
		}
		declarations = append(declarations, entry)
	}
	return declarations
}

// openAPISchemaKeywords is the field set Gemini's "parameters" accepts. It is an
// allowlist on purpose: JSON Schema keeps growing, so a blacklist would silently
// route new constructs into a field that cannot express them.
var openAPISchemaKeywords = map[string]struct{}{
	"type":             {},
	"format":           {},
	"title":            {},
	"description":      {},
	"nullable":         {},
	"enum":             {},
	"properties":       {},
	"required":         {},
	"items":            {},
	"anyOf":            {},
	"default":          {},
	"example":          {},
	"minimum":          {},
	"maximum":          {},
	"minLength":        {},
	"maxLength":        {},
	"minItems":         {},
	"maxItems":         {},
	"minProperties":    {},
	"maxProperties":    {},
	"pattern":          {},
	"propertyOrdering": {},
}

// schemaNeedsJSONSchema reports whether a tool schema uses constructs the
// OpenAPI Schema subset cannot carry, in which case it must travel through
// parametersJsonSchema instead.
//
// Both the keyword names and their value shapes matter: OpenAPI models `type`
// as a single enum value and `enum` as a list of strings, so JSON Schema forms
// such as {"type": ["string", "null"]} need the richer field even though the
// keyword itself is recognized.
//
// Only fields that actually carry schemas are recursed into. Annotation fields
// such as `example` and `default` hold arbitrary JSON, and walking them would
// mistake ordinary data keys for unsupported schema keywords.
func schemaNeedsJSONSchema(value any) bool {
	schema, ok := value.(map[string]any)
	if !ok {
		return true
	}
	for key, item := range schema {
		if _, supported := openAPISchemaKeywords[key]; !supported {
			return true
		}
		switch key {
		case "type":
			if _, ok := item.(string); !ok {
				return true
			}
		case "enum":
			values, ok := item.([]any)
			if !ok {
				return true
			}
			for _, entry := range values {
				if _, ok := entry.(string); !ok {
					return true
				}
			}
		case "properties":
			members, ok := item.(map[string]any)
			if !ok {
				return true
			}
			for _, member := range members {
				if schemaNeedsJSONSchema(member) {
					return true
				}
			}
		case "items":
			if schemaNeedsJSONSchema(item) {
				return true
			}
		case "anyOf":
			entries, ok := item.([]any)
			if !ok {
				return true
			}
			for _, entry := range entries {
				if schemaNeedsJSONSchema(entry) {
					return true
				}
			}
		}
	}
	return false
}

func geminiFunctionCallingConfig(choice chatToolChoice) map[string]any {
	switch choice.Mode {
	case "required":
		return map[string]any{"mode": "ANY"}
	case "none":
		return map[string]any{"mode": "NONE"}
	case "function":
		return map[string]any{"mode": "ANY", "allowedFunctionNames": []string{choice.Name}}
	default:
		return map[string]any{"mode": "AUTO"}
	}
}

// geminiContentsFromChat splits OpenAI messages into systemInstruction parts and
// the contents turn list.
func geminiContentsFromChat(messages []ChatMessage) ([]map[string]any, []map[string]any, error) {
	var (
		systemParts    []map[string]any
		contents       []map[string]any
		pendingResults []map[string]any
	)

	flushResults := func() {
		if len(pendingResults) == 0 {
			return
		}
		contents = append(contents, map[string]any{"role": "user", "parts": pendingResults})
		pendingResults = nil
	}

	// Function responses reference the call by name, which OpenAI only carries
	// on the assistant turn, so remember the id to name mapping as we go.
	toolNames := map[string]string{}

	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			flushResults()
			parts, err := parseChatContent(message.Content)
			if err != nil {
				return nil, nil, err
			}
			if text := contentPartsText(parts); text != "" {
				systemParts = append(systemParts, map[string]any{"text": text})
			}
		case "tool":
			part, err := geminiFunctionResponsePart(message, toolNames)
			if err != nil {
				return nil, nil, err
			}
			pendingResults = append(pendingResults, part)
		case "assistant":
			flushResults()
			parts, err := geminiAssistantParts(message, toolNames)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, map[string]any{"role": "model", "parts": parts})
		default:
			flushResults()
			parts, err := geminiUserParts(message)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, map[string]any{"role": "user", "parts": parts})
		}
	}
	flushResults()
	return systemParts, contents, nil
}

func geminiFunctionResponsePart(message ChatMessage, toolNames map[string]string) (map[string]any, error) {
	if message.ToolCallID == "" {
		return nil, unsupportedContentError("tool messages must carry tool_call_id")
	}
	name := toolNames[message.ToolCallID]
	if name == "" {
		name = message.Name
	}
	if name == "" {
		return nil, unsupportedContentError(fmt.Sprintf("cannot resolve the function name for tool_call_id %q", message.ToolCallID))
	}
	parts, err := parseChatContent(message.Content)
	if err != nil {
		return nil, err
	}
	response := map[string]any{
		"id":   message.ToolCallID,
		"name": name,
		// Gemini requires an object; tool output is opaque text to the gateway.
		"response": map[string]any{"result": contentPartsText(parts)},
	}
	return map[string]any{"functionResponse": response}, nil
}

func geminiAssistantParts(message ChatMessage, toolNames map[string]string) ([]map[string]any, error) {
	parts := make([]map[string]any, 0, 2)

	contentParts, err := parseChatContent(message.Content)
	if err != nil {
		return nil, err
	}
	if text := contentPartsText(contentParts); text != "" {
		parts = append(parts, map[string]any{"text": text})
	}

	calls, err := parseChatToolCalls(message.ToolCalls)
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		if call.ID != "" {
			toolNames[call.ID] = call.Name
		}
		functionCall := map[string]any{
			"name": call.Name,
			"args": call.Arguments,
		}
		if call.ID != "" {
			// The matching functionResponse references this id. Replaying the
			// call without it leaves the result pointing at a call Gemini cannot
			// find, which it rejects.
			functionCall["id"] = call.ID
		}
		part := map[string]any{"functionCall": functionCall}
		// The thought signature is only usable if this provider minted it.
		// Replaying another provider's blob would be rejected upstream.
		if signature, ok := decodeProviderSignature(geminiSignatureProvider, call.Signature); ok {
			part["thoughtSignature"] = signature
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func geminiUserParts(message ChatMessage) ([]map[string]any, error) {
	contentParts, err := parseChatContent(message.Content)
	if err != nil {
		return nil, err
	}
	parts := make([]map[string]any, 0, len(contentParts))
	for _, part := range contentParts {
		switch part.Kind {
		case chatContentText:
			if part.Text == "" {
				continue
			}
			parts = append(parts, map[string]any{"text": part.Text})
		case chatContentImageB64:
			parts = append(parts, map[string]any{
				"inlineData": map[string]any{"mimeType": part.MediaType, "data": part.Data},
			})
		case chatContentImageURL:
			// Gemini's fileData only accepts URIs it already hosts, so an
			// arbitrary http(s) image URL cannot be forwarded.
			return nil, unsupportedCapabilityError("Gemini requires inline image data; pass the image as a base64 data URI instead of an http(s) URL")
		}
	}
	return parts, nil
}

// geminiChatResponse converts a Gemini generateContent response body into an
// OpenAI chat completion object.
func geminiChatResponse(body map[string]any, model string, usage Usage) (map[string]any, error) {
	candidates, _ := body["candidates"].([]any)
	if len(candidates) == 0 {
		if reason := geminiPromptBlockReason(body); reason != "" {
			return nil, newProviderPolicyRefusal(fmt.Sprintf("Gemini blocked the prompt: %s", reason))
		}
		return nil, invalidProviderResponseError("Gemini returned no candidates")
	}
	candidate, _ := candidates[0].(map[string]any)
	content, _ := candidate["content"].(map[string]any)
	parts, _ := content["parts"].([]any)

	var (
		textParts []string
		reasoning []string
		toolCalls []any
	)
	for index, item := range parts {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if call, ok := part["functionCall"].(map[string]any); ok {
			name, _ := call["name"].(string)
			id, _ := call["id"].(string)
			if id == "" {
				// Gemini does not always mint an id; clients still need a stable
				// handle to correlate the result with.
				id = fmt.Sprintf("call_%s_%d", NewID("gemini"), index)
			}
			entry := map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": encodeToolCallArguments(call["args"]),
				},
			}
			if signature, ok := part["thoughtSignature"].(string); ok && signature != "" {
				entry["thought_signature"] = encodeProviderSignature(geminiSignatureProvider, signature)
			}
			toolCalls = append(toolCalls, entry)
			continue
		}
		text, _ := part["text"].(string)
		if text == "" {
			continue
		}
		if thought, _ := part["thought"].(bool); thought {
			reasoning = append(reasoning, text)
			continue
		}
		textParts = append(textParts, text)
	}

	message := map[string]any{"role": "assistant"}
	if len(textParts) > 0 {
		message["content"] = strings.Join(textParts, "")
	} else {
		message["content"] = nil
	}
	if len(reasoning) > 0 {
		message["reasoning_content"] = strings.Join(reasoning, "")
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	finishReason, _ := candidate["finishReason"].(string)
	mapped, err := geminiFinishReason(finishReason, len(toolCalls) > 0)
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
			"finish_reason": mapped,
		}},
		"usage": openAIChatUsageObject(usage),
	}, nil
}

func geminiPromptBlockReason(body map[string]any) string {
	feedback, _ := body["promptFeedback"].(map[string]any)
	reason, _ := feedback["blockReason"].(string)
	return reason
}

// geminiFinishReason maps a Gemini finish reason. Protocol-level failures are
// reported as upstream errors instead of being flattened into a normal stop.
func geminiFinishReason(reason string, hasToolCalls bool) (string, error) {
	switch reason {
	case "STOP", "":
		if hasToolCalls {
			return openAIFinishReasonToolCalls, nil
		}
		return openAIFinishReasonStop, nil
	case "MAX_TOKENS":
		return openAIFinishReasonLength, nil
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
		return openAIFinishReasonContentFilter, nil
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "MISSING_THOUGHT_SIGNATURE", "MALFORMED_RESPONSE":
		return "", invalidProviderResponseError(fmt.Sprintf("Gemini reported a protocol failure: %s", reason))
	default:
		return "", invalidProviderResponseError(fmt.Sprintf("Gemini returned an unrecognized finishReason %q", reason))
	}
}
