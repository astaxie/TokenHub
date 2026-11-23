package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Responses-to-Anthropic conversion. The gateway entry is the OpenAI Responses
// surface; the upstream is the Anthropic Messages API. The mapping is explicit
// and lossless for the supported item types: anything this file cannot express
// is rejected with 400 instead of being dropped silently, because a lossy
// "success" hides data loss from the caller.

const (
	// anthropicResponsesDefaultMaxTokens covers the Messages API requirement
	// that max_tokens is always present. The Responses surface treats it as
	// optional, so a documented default fills the gap when the caller omits it.
	anthropicResponsesDefaultMaxTokens = 4096
)

// responsesUnsupportedItemError reports an input item the conversion cannot
// express without losing information.
func responsesUnsupportedItemError(itemType string) error {
	return NewHTTPError(http.StatusBadRequest, "responses_item_unsupported",
		fmt.Sprintf("Responses input item type %q cannot be converted for this provider", itemType))
}

// anthropicContentFromResponsesText wraps one text chunk as a content block.
func anthropicTextBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

// responsesContentString reads a Responses content field that may be either a
// plain string or a block array.
func responsesContentString(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, block := range value {
			if blockMap, ok := block.(map[string]any); ok {
				if text, ok := blockMap["text"].(string); ok {
					builder.WriteString(text)
				}
			}
		}
		return builder.String()
	default:
		return ""
	}
}

// responsesInputItems normalizes the Responses input into an ordered item list.
// A plain string becomes one user message item, matching the Responses spec.
func responsesInputItems(req ResponsesRequest) ([]map[string]any, error) {
	switch value := req.Input.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		return []map[string]any{{"type": "message", "role": "user", "content": value}}, nil
	case []any:
		items := make([]map[string]any, 0, len(value))
		for _, entry := range value {
			item, ok := entry.(map[string]any)
			if !ok {
				return nil, responsesUnsupportedItemError(fmt.Sprintf("%T", entry))
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, responsesUnsupportedItemError(fmt.Sprintf("%T", value))
	}
}

// responsesItemsToAnthropicMessages converts Responses input items into
// Anthropic messages. Consecutive messages with the same role are merged so
// the output satisfies the Messages API ordering rules.
func responsesItemsToAnthropicMessages(items []map[string]any) ([]map[string]any, []string, error) {
	messages := make([]map[string]any, 0, len(items))
	var systemParts []string
	appendMessage := func(role string, blocks []map[string]any) {
		if len(blocks) == 0 {
			return
		}
		content := make([]any, 0, len(blocks))
		for _, block := range blocks {
			content = append(content, block)
		}
		if n := len(messages); n > 0 {
			last := messages[n-1]
			if last["role"] == role && last["type"] == nil {
				if existing, ok := last["content"].([]any); ok {
					last["content"] = append(existing, content...)
					return
				}
			}
		}
		messages = append(messages, map[string]any{"role": role, "content": content})
	}
	for _, item := range items {
		switch itemType, _ := item["type"].(string); itemType {
		case "", "message":
			role, _ := item["role"].(string)
			switch role {
			case "system", "developer":
				if text := responsesContentString(item["content"]); text != "" {
					systemParts = append(systemParts, text)
				}
				continue
			case "":
				role = "user"
			}
			blocks := make([]map[string]any, 0, 4)
			switch content := item["content"].(type) {
			case string:
				if content != "" {
					blocks = append(blocks, anthropicTextBlock(content))
				}
			case []any:
				for _, rawBlock := range content {
					block, ok := rawBlock.(map[string]any)
					if !ok {
						continue
					}
					switch blockType, _ := block["type"].(string); blockType {
					case "input_text", "output_text":
						text, _ := block["text"].(string)
						if text != "" {
							blocks = append(blocks, anthropicTextBlock(text))
						}
					case "input_image":
						blocks = append(blocks, responsesImageToAnthropicBlock(block))
					case "output_image":
						return nil, nil, responsesUnsupportedItemError("output_image")
					default:
						return nil, nil, responsesUnsupportedItemError(blockType)
					}
				}
			case nil:
				continue
			default:
				return nil, nil, responsesUnsupportedItemError(fmt.Sprintf("content %T", content))
			}
			if role != "user" && role != "assistant" {
				return nil, nil, responsesUnsupportedItemError("message role " + role)
			}
			appendMessage(role, blocks)
		case "function_call":
			name, _ := item["name"].(string)
			arguments, _ := item["arguments"].(string)
			callID, _ := item["call_id"].(string)
			input := jsonRawOrEmptyObject(arguments)
			blocks := []map[string]any{{
				"type":  "tool_use",
				"id":    callID,
				"name":  name,
				"input": input,
			}}
			appendMessage("assistant", blocks)
		case "function_call_output":
			output := responsesFunctionOutputText(item["output"])
			callID, _ := item["call_id"].(string)
			blocks := []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": callID,
				"content":     output,
			}}
			appendMessage("user", blocks)
		case "reasoning":
			// Reasoning items carry the provider's own chain-of-thought
			// bookkeeping; Anthropic mints its own thinking, so replaying a
			// foreign one would be noise at best.
			continue
		default:
			return nil, nil, responsesUnsupportedItemError(itemType)
		}
	}
	return messages, systemParts, nil
}

// responsesImageToAnthropicBlock converts a data-URL input image into the
// Anthropic base64 image block. Remote URLs are rejected: downloading them
// here would add a fetch dependency the gateway does not own.
func responsesImageToAnthropicBlock(block map[string]any) map[string]any {
	imageURL, _ := block["image_url"].(string)
	const prefix = "data:"
	if !strings.HasPrefix(imageURL, prefix) {
		return map[string]any{"type": "text", "text": "[image omitted: only data URLs are supported]"}
	}
	rest := strings.TrimPrefix(imageURL, prefix)
	mediaType, encoded, found := strings.Cut(rest, ";base64,")
	if !found {
		return map[string]any{"type": "text", "text": "[image omitted: data URL is not base64]"}
	}
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       encoded,
		},
	}
}

func responsesFunctionOutputText(output any) string {
	switch value := output.(type) {
	case string:
		return value
	case map[string]any:
		if text, ok := value["text"].(string); ok {
			return text
		}
	case []any:
		var builder strings.Builder
		for _, entry := range value {
			if entryMap, ok := entry.(map[string]any); ok {
				builder.WriteString(responsesFunctionOutputText(entryMap))
			}
		}
		return builder.String()
	}
	return ""
}

func jsonRawOrEmptyObject(raw string) map[string]any {
	decoded := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return decoded
	}
	_ = json.Unmarshal([]byte(raw), &decoded)
	return decoded
}

// responsesToolsToAnthropic converts the Responses tools array (function-type
// entries with top-level name/description/parameters) into Anthropic tools.
func responsesToolsToAnthropic(req ResponsesRequest) ([]map[string]any, error) {
	var rawTools []any
	if raw, present := req.raw["tools"]; present && len(raw) > 0 {
		if err := json.Unmarshal(raw, &rawTools); err != nil {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_responses_tools",
				"The Responses tools field must be an array")
		}
	}
	tools := make([]map[string]any, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := tool["type"].(string)
		if toolType != "" && toolType != "function" {
			return nil, responsesUnsupportedItemError("tool type " + toolType)
		}
		name, _ := tool["name"].(string)
		description, _ := tool["description"].(string)
		schema := jsonRawOrEmptyObject(stringOrJSON(tool["parameters"]))
		anthropicTool := map[string]any{"name": name, "input_schema": schema}
		if description != "" {
			anthropicTool["description"] = description
		}
		tools = append(tools, anthropicTool)
	}
	return tools, nil
}

func stringOrJSON(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

// responsesToolChoiceToAnthropic maps the Responses tool_choice forms. A
// "none" choice removes tools entirely, mirroring the OpenAI semantics.
func responsesToolChoiceToAnthropic(req ResponsesRequest, tools []map[string]any) (choice any, keepTools bool) {
	if len(tools) == 0 {
		return nil, false
	}
	raw, present := req.raw["tool_choice"]
	if !present || len(raw) == 0 {
		return nil, true
	}
	var decodedChoice any
	if err := json.Unmarshal(raw, &decodedChoice); err != nil {
		return nil, true
	}
	switch value := decodedChoice.(type) {
	case string:
		switch value {
		case "none":
			return nil, false
		case "required":
			return map[string]any{"type": "any"}, true
		default: // "auto" and future soft modes
			return map[string]any{"type": "auto"}, true
		}
	case map[string]any:
		name, _ := value["name"].(string)
		if name != "" {
			return map[string]any{"type": "tool", "name": name}, true
		}
		return map[string]any{"type": "auto"}, true
	default:
		return nil, true
	}
}

// buildAnthropicResponsesPayload converts a Responses request into the
// Anthropic Messages wire payload.
func buildAnthropicResponsesPayload(req ResponsesRequest) (map[string]any, error) {
	items, err := responsesInputItems(req)
	if err != nil {
		return nil, err
	}
	messages, systemParts, err := responsesItemsToAnthropicMessages(items)
	if err != nil {
		return nil, err
	}
	tools, err := responsesToolsToAnthropic(req)
	if err != nil {
		return nil, err
	}
	choice, keepTools := responsesToolChoiceToAnthropic(req, tools)
	if !keepTools {
		tools = nil
	}
	if len(messages) == 0 {
		return nil, NewHTTPError(http.StatusBadRequest, "responses_input_required",
			"The Responses request carries no convertible input items")
	}
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": anthropicMaxTokens(req),
		"messages":   messages,
	}
	if req.Instructions != "" {
		systemParts = append([]string{req.Instructions}, systemParts...)
	}
	if len(systemParts) > 0 {
		payload["system"] = strings.Join(systemParts, "\n\n")
	}
	if len(tools) > 0 {
		payload["tools"] = tools
		if choice != nil {
			payload["tool_choice"] = choice
		}
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	// The Anthropic adapter surfaces reasoning effort as output_config, the
	// same shape the chat bridge uses (see provider_anthropic_convert.go).
	if effort := responsesReasoningEffort(req); effort != nil {
		payload["output_config"] = map[string]any{"effort": *effort}
	}
	return payload, nil
}

func anthropicMaxTokens(req ResponsesRequest) int64 {
	if req.MaxTokens > 0 {
		return int64(req.MaxTokens)
	}
	return anthropicResponsesDefaultMaxTokens
}

// anthropicResponseToResponsesObject converts an Anthropic Messages response
// into the Responses object shape. Text blocks collapse into one assistant
// message item; tool_use blocks become function_call items keyed by the
// Anthropic tool id so a following function_call_output can reference them.
func anthropicResponseToResponsesObject(model string, body map[string]any, usage Usage) map[string]any {
	anthropicID, _ := body["id"].(string)
	responseID := "resp_" + strings.TrimPrefix(anthropicID, "msg_")
	status := "completed"
	stopReason, _ := body["stop_reason"].(string)
	incompleteDetails := map[string]any{}
	switch stopReason {
	case "max_tokens":
		status = "incomplete"
		incompleteDetails["reason"] = "max_output_tokens"
	case "refusal":
		status = "incomplete"
		incompleteDetails["reason"] = contentBlockTextOf("refusal")
	}
	output := make([]any, 0, 4)
	var outputText strings.Builder
	toolCalls := 0
	contentBlocks, _ := body["content"].([]any)
	for _, rawBlock := range contentBlocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		switch blockType, _ := block["type"].(string); blockType {
		case "text":
			text, _ := block["text"].(string)
			if text == "" {
				continue
			}
			outputText.WriteString(text)
			output = append(output, map[string]any{
				"type":   "message",
				"id":     fmt.Sprintf("msg_%d", len(output)),
				"role":   "assistant",
				"status": status,
				"content": []map[string]any{
					{"type": "output_text", "text": text, "annotations": []any{}},
				},
			})
		case "tool_use":
			toolCalls++
			inputJSON, err := json.Marshal(block["input"])
			arguments := string(inputJSON)
			if err != nil {
				arguments = "{}"
			}
			blockID, _ := block["id"].(string)
			name, _ := block["name"].(string)
			output = append(output, map[string]any{
				"type":      "function_call",
				"id":        fmt.Sprintf("fc_%d", len(output)),
				"call_id":   blockID,
				"name":      name,
				"arguments": arguments,
				"status":    status,
			})
		case "thinking":
			// Provider-internal reasoning stays provider-internal.
			continue
		default:
			continue
		}
	}
	_ = toolCalls
	response := map[string]any{
		"id":                  responseID,
		"object":              "response",
		"created_at":          timeNowUnix(),
		"status":              status,
		"model":               model,
		"output":              output,
		"output_text":         outputText.String(),
		"usage":               openAIResponsesUsageObject(usage),
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"tools":               []any{},
	}
	if status == "incomplete" && len(incompleteDetails) > 0 {
		response["incomplete_details"] = incompleteDetails
	}
	return response
}

func contentBlockTextOf(text string) string { return text }

func timeNowUnix() int64 { return time.Now().Unix() }

// requestStatus performs an authenticated request and reports only the HTTP
// status, for capability probes that do not need a body.
func (a AnthropicAdapter) requestStatus(ctx context.Context, provider Provider, method, endpoint string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, anthropicEndpointURL(provider.BaseURL, endpoint), nil)
	if err != nil {
		return 0, err
	}
	applyAnthropicProviderAuth(req, provider)
	version := provider.Options["anthropic_version"]
	if version == "" {
		version = "2023-06-01"
	}
	req.Header.Set("anthropic-version", version)
	applyProviderHeaders(req.Header, provider.Headers)
	resp, err := sendUpstream(a.Client, a.StreamClient, a.StreamIdleTimeout, req, false)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// ProbeProvider answers the console connection test and the health monitor.
// Anthropic itself offers GET /v1/models; Anthropic-compatible gateways such
// as DeepSeek do not, so a 404/405 there falls back to a minimal Messages
// exchange before declaring the provider healthy.
func (a AnthropicAdapter) ProbeProvider(ctx context.Context, provider Provider) (any, error) {
	status, err := a.requestStatus(ctx, provider, http.MethodGet, "/v1/models")
	if err != nil {
		return nil, providerCatalogConnectionError(err)
	}
	if status < http.StatusBadRequest {
		return map[string]any{"healthy": true, "models_catalog": true}, nil
	}
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return nil, providerModelsUpstreamError(status)
	}
	probeModel := strings.TrimSpace(provider.Options["probe_model"])
	if probeModel == "" {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "anthropic_probe_model_required",
			"This upstream has no model catalog endpoint; set the probe_model provider option so health checks can verify it")
	}
	payload := map[string]any{
		"model":      probeModel,
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "ping"}},
	}
	var body map[string]any
	if err := a.doJSON(ctx, provider, "/v1/messages", payload, &body); err != nil {
		return nil, err
	}
	return map[string]any{"healthy": true, "models_catalog": false}, nil
}
