package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type anthropicMessagesRequest struct {
	Raw       map[string]any
	Model     string
	MaxTokens int
	Messages  []any
	Stream    bool
}

const anthropicMidConversationSystemBeta = "mid-conversation-system-2026-04-07"

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAnthropicError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	project, key, err := s.authenticate(r)
	if err != nil {
		writeAnthropicError(w, r, err)
		return
	}
	req, err := decodeAnthropicMessagesRequest(r, true)
	if err != nil {
		writeAnthropicError(w, r, err)
		return
	}

	routed, ok := s.startAnthropicRoutedCall(w, r, project, key, req)
	if !ok {
		return
	}
	if req.Stream {
		s.handleAnthropicMessagesStream(w, r, routed, req)
		return
	}

	resp, route, usage, attempts, err := s.executeRoutedAnthropicMessages(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, err, req.Raw)
		writeAnthropicError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, req.Raw, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAnthropicError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	_, key, err := s.authenticate(r)
	if err != nil {
		writeAnthropicError(w, r, err)
		return
	}
	req, err := decodeAnthropicMessagesRequest(r, false)
	if err != nil {
		writeAnthropicError(w, r, err)
		return
	}
	if !keyCanAccessModel(s.store, key, req.Model) {
		writeAnthropicError(w, r, ErrModelNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"input_tokens": estimateAnthropicInputTokens(req.Raw),
	})
}

func decodeAnthropicMessagesRequest(r *http.Request, requireMaxTokens bool) (anthropicMessagesRequest, error) {
	var raw map[string]any
	if err := decodeJSON(r, &raw); err != nil {
		return anthropicMessagesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	model, _ := raw["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return anthropicMessagesRequest{}, NewHTTPError(http.StatusBadRequest, "missing_model", "model is required")
	}
	messages, ok := raw["messages"].([]any)
	if !ok || len(messages) == 0 {
		return anthropicMessagesRequest{}, NewHTTPError(http.StatusBadRequest, "missing_messages", "messages are required")
	}
	allowSystemMessages := anthropicBetaEnabled(r.Header.Get("anthropic-beta"), anthropicMidConversationSystemBeta)
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			return anthropicMessagesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_message", "each message must be an object")
		}
		role, _ := message["role"].(string)
		if role != "user" && role != "assistant" && !(allowSystemMessages && role == "system") {
			return anthropicMessagesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_message", "message role must be user or assistant")
		}
		if _, exists := message["content"]; !exists {
			return anthropicMessagesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_message", "message content is required")
		}
	}
	maxTokens := int(int64FromAny(raw["max_tokens"]))
	if requireMaxTokens && maxTokens <= 0 {
		return anthropicMessagesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_max_tokens", "max_tokens must be greater than zero")
	}
	stream, _ := raw["stream"].(bool)
	return anthropicMessagesRequest{
		Raw:       raw,
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  messages,
		Stream:    stream,
	}, nil
}

func anthropicBetaEnabled(header string, target string) bool {
	for _, beta := range strings.Split(header, ",") {
		if strings.TrimSpace(beta) == target {
			return true
		}
	}
	return false
}

func keyCanAccessModel(store Store, key APIKey, model string) bool {
	for _, candidate := range store.AccessibleModels(key) {
		if candidate.Name == model || candidate.ID == model {
			return true
		}
	}
	return false
}

func (s *Server) startAnthropicRoutedCall(
	w http.ResponseWriter,
	r *http.Request,
	project Project,
	key APIKey,
	req anthropicMessagesRequest,
) (RoutedCall, bool) {
	admittedAt := time.Now().UTC()
	call, err := s.store.StartCall(r.Context(), project, key, req.Model)
	call.Stream = req.Stream
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, req.Model, req.Stream, err, req.Raw)
		w.Header().Set("x-request-id", requestID)
		writeAnthropicError(w, r, err)
		return RoutedCall{}, false
	}
	w.Header().Set("x-request-id", call.RequestID)
	if call.requestContext != nil {
		*r = *r.WithContext(call.requestContext)
	}
	routes, err := s.store.SelectRouteCandidates(req.Model)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, err, req.Raw)
		writeAnthropicError(w, r, err)
		return RoutedCall{}, false
	}
	affinity, err := s.anthropicCacheLocalityAffinity(key.ID, req.Model, r.Header, req.Raw)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, err, req.Raw)
		writeAnthropicError(w, r, err)
		return RoutedCall{}, false
	}
	call.Affinity = affinity
	return RoutedCall{Call: call, Routes: s.planRouteOrder(call, routes), Affinity: affinity}, true
}

func (s *Server) executeRoutedAnthropicMessages(
	r *http.Request,
	routed RoutedCall,
	req anthropicMessagesRequest,
) (map[string]any, RouteSelection, Usage, []RouteAttempt, error) {
	compatible, compatibilityErr := compatibleAnthropicRoutes(routed, req)
	if compatibilityErr != nil {
		return nil, RouteSelection{}, Usage{}, nil, compatibilityErr
	}
	return executeRoutedWithStore(r.Context(), s.store, compatible, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (map[string]any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		return s.executeAnthropicMessagesRoute(ctx, route, req, r.Header)
	})
}

func (s *Server) executeAnthropicMessagesRoute(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	headers http.Header,
) (map[string]any, Usage, error) {
	if route.Provider.Type == ProviderAnthropic {
		return s.executeNativeAnthropicMessages(ctx, route, req, headers)
	}
	if !openAIMessageProvider(route.Provider.Type) {
		return nil, Usage{}, NewHTTPError(
			http.StatusNotImplemented,
			"provider_capability_not_supported",
			"Provider does not support the Anthropic Messages gateway",
		)
	}
	chatReq, err := anthropicToOpenAIChatRequest(req)
	if err != nil {
		return nil, Usage{}, err
	}
	adapter, err := s.adapterForRoute(route)
	if err != nil {
		return nil, Usage{}, err
	}
	resp, usage, err := adapter.Chat(ctx, route.Provider, route.ProviderModel, chatReq)
	if err != nil {
		return nil, usage, err
	}
	body, ok := resp.(map[string]any)
	if !ok {
		return nil, usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider returned an invalid chat response")
	}
	converted, err := openAIResponseToAnthropic(body, req.Model, usage)
	if err != nil {
		return nil, usage, err
	}
	return converted, usage, nil
}

func openAIMessageProvider(providerType string) bool {
	switch providerType {
	case ProviderMock, ProviderOpenAI, ProviderOpenAICompatible, ProviderAzureOpenAI, "deepseek", "qwen", "local":
		return true
	default:
		return false
	}
}

func compatibleAnthropicRoutes(
	routed RoutedCall,
	req anthropicMessagesRequest,
) (RoutedCall, error) {
	compatible := routed
	compatible.Routes = make([]RouteSelection, 0, len(routed.Routes))
	var firstErr error
	for _, route := range routed.Routes {
		err := validateAnthropicRouteCompatibility(route, req)
		if err == nil {
			compatible.Routes = append(compatible.Routes, route)
			continue
		}
		if !isAnthropicRouteIncompatibility(err) {
			return compatible, err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if len(compatible.Routes) == 0 {
		if firstErr == nil {
			firstErr = ErrProviderMissing
		}
		return compatible, firstErr
	}
	return compatible, nil
}

func validateAnthropicRouteCompatibility(route RouteSelection, req anthropicMessagesRequest) error {
	if route.Provider.Type == ProviderAnthropic {
		return nil
	}
	if !openAIMessageProvider(route.Provider.Type) {
		return NewHTTPError(
			http.StatusNotImplemented,
			"provider_capability_not_supported",
			"Provider does not support the Anthropic Messages gateway",
		)
	}
	_, err := anthropicToOpenAIChatRequest(req)
	return err
}

func isAnthropicRouteIncompatibility(err error) bool {
	switch AsHTTPError(err).Code {
	case "provider_capability_not_supported",
		"unsupported_content_block",
		"unsupported_image_source",
		"unsupported_tool",
		"unsupported_tool_choice",
		"unsupported_tool_result":
		return true
	default:
		return false
	}
}

func anthropicToOpenAIChatRequest(req anthropicMessagesRequest) (ChatCompletionRequest, error) {
	messages := make([]ChatMessage, 0, len(req.Messages)+1)
	if system, exists := req.Raw["system"]; exists {
		text, err := anthropicSystemText(system)
		if err != nil {
			return ChatCompletionRequest{}, err
		}
		if text != "" {
			messages = append(messages, ChatMessage{Role: "system", Content: text})
		}
	}
	for _, rawMessage := range req.Messages {
		message := rawMessage.(map[string]any)
		converted, err := anthropicMessageToOpenAI(message)
		if err != nil {
			return ChatCompletionRequest{}, err
		}
		messages = append(messages, converted...)
	}
	tools, err := anthropicToolsToOpenAI(req.Raw["tools"])
	if err != nil {
		return ChatCompletionRequest{}, err
	}
	toolChoice, parallelToolCalls, err := anthropicToolChoiceToOpenAI(req.Raw["tool_choice"])
	if err != nil {
		return ChatCompletionRequest{}, err
	}
	temperature, err := optionalFloat(req.Raw["temperature"], "temperature")
	if err != nil {
		return ChatCompletionRequest{}, err
	}
	topP, err := optionalFloat(req.Raw["top_p"], "top_p")
	if err != nil {
		return ChatCompletionRequest{}, err
	}
	var reasoningEffort *string
	if value, ok := req.Raw["effort"].(string); ok && strings.TrimSpace(value) != "" {
		effort := strings.TrimSpace(value)
		reasoningEffort = &effort
	}
	if outputConfig, ok := req.Raw["output_config"].(map[string]any); ok {
		if value, ok := outputConfig["effort"].(string); ok && strings.TrimSpace(value) != "" {
			effort := strings.TrimSpace(value)
			reasoningEffort = &effort
		}
	}
	chatReq := ChatCompletionRequest{
		Model:             req.Model,
		Messages:          messages,
		Stream:            req.Stream,
		MaxTokens:         req.MaxTokens,
		Temperature:       temperature,
		TopP:              topP,
		Tools:             tools,
		ToolChoice:        toolChoice,
		ParallelToolCalls: parallelToolCalls,
		ReasoningEffort:   reasoningEffort,
	}
	if stop, ok := req.Raw["stop_sequences"].([]any); ok {
		values := make([]string, 0, len(stop))
		for _, item := range stop {
			value, ok := item.(string)
			if !ok {
				return ChatCompletionRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_stop_sequences", "stop_sequences must contain strings")
			}
			values = append(values, value)
		}
		chatReq.Stop = values
	}
	return chatReq, nil
}

func optionalFloat(value any, field string) (*float64, error) {
	if value == nil {
		return nil, nil
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_"+field, field+" must be a number")
		}
		return &parsed, nil
	case float64:
		return &typed, nil
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_"+field, field+" must be a number")
	}
}

func anthropicSystemText(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if !ok || block["type"] != "text" {
				return "", NewHTTPError(http.StatusBadRequest, "unsupported_content_block", "OpenAI-compatible routes support only text system blocks")
			}
			text, ok := block["text"].(string)
			if !ok {
				return "", NewHTTPError(http.StatusBadRequest, "invalid_content_block", "text block text must be a string")
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", NewHTTPError(http.StatusBadRequest, "invalid_system", "system must be a string or an array of text blocks")
	}
}

func anthropicMessageToOpenAI(message map[string]any) ([]ChatMessage, error) {
	role, _ := message["role"].(string)
	if role == "system" {
		content, err := anthropicSystemText(message["content"])
		if err != nil {
			return nil, err
		}
		return []ChatMessage{{Role: "system", Content: content}}, nil
	}
	blocks, err := anthropicContentBlocks(message["content"])
	if err != nil {
		return nil, err
	}
	if role == "assistant" {
		return anthropicAssistantMessageToOpenAI(blocks)
	}
	return anthropicUserMessageToOpenAI(blocks)
}

func anthropicContentBlocks(content any) ([]map[string]any, error) {
	switch typed := content.(type) {
	case string:
		return []map[string]any{{"type": "text", "text": typed}}, nil
	case []any:
		blocks := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if !ok {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_content_block", "content blocks must be objects")
			}
			blocks = append(blocks, block)
		}
		return blocks, nil
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_message", "message content must be a string or an array")
	}
}

func anthropicAssistantMessageToOpenAI(blocks []map[string]any) ([]ChatMessage, error) {
	contentBlocks := make([]map[string]any, 0, len(blocks))
	toolCalls := make([]map[string]any, 0)
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text", "image":
			part, err := anthropicBlockToOpenAIContentPart(block)
			if err != nil {
				return nil, err
			}
			contentBlocks = append(contentBlocks, part)
		case "tool_use":
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
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": string(arguments),
				},
			})
		case "thinking", "redacted_thinking":
			// OpenAI-compatible providers do not accept Anthropic thinking blocks.
		default:
			return nil, NewHTTPError(
				http.StatusBadRequest,
				"unsupported_content_block",
				fmt.Sprintf("OpenAI-compatible routes do not support assistant content block type %q", blockType),
			)
		}
	}
	content := openAIContentFromParts(contentBlocks)
	if len(toolCalls) > 0 && content == nil {
		content = ""
	}
	return []ChatMessage{{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	}}, nil
}

func anthropicUserMessageToOpenAI(blocks []map[string]any) ([]ChatMessage, error) {
	toolMessages := make([]ChatMessage, 0)
	contentBlocks := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text", "image":
			part, err := anthropicBlockToOpenAIContentPart(block)
			if err != nil {
				return nil, err
			}
			contentBlocks = append(contentBlocks, part)
		case "tool_result":
			toolUseID, _ := block["tool_use_id"].(string)
			if toolUseID == "" {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_result", "tool_result requires tool_use_id")
			}
			content, err := anthropicToolResultContent(block["content"])
			if err != nil {
				return nil, err
			}
			if isError, _ := block["is_error"].(bool); isError {
				if text, ok := content.(string); ok {
					content = "Error: " + text
				}
			}
			toolMessages = append(toolMessages, ChatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: toolUseID,
			})
		default:
			return nil, NewHTTPError(
				http.StatusBadRequest,
				"unsupported_content_block",
				fmt.Sprintf("OpenAI-compatible routes do not support user content block type %q", blockType),
			)
		}
	}
	if content := openAIContentFromParts(contentBlocks); content != nil {
		toolMessages = append(toolMessages, ChatMessage{Role: "user", Content: content})
	}
	if len(toolMessages) == 0 {
		toolMessages = append(toolMessages, ChatMessage{Role: "user", Content: ""})
	}
	return toolMessages, nil
}

func anthropicBlockToOpenAIContentPart(block map[string]any) (map[string]any, error) {
	blockType, _ := block["type"].(string)
	if blockType == "text" {
		text, ok := block["text"].(string)
		if !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_content_block", "text block text must be a string")
		}
		return map[string]any{"type": "text", "text": text}, nil
	}
	source, ok := block["source"].(map[string]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_image", "image block source is required")
	}
	sourceType, _ := source["type"].(string)
	var imageURL string
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
		return nil, NewHTTPError(http.StatusBadRequest, "unsupported_image_source", "OpenAI-compatible routes support base64 and URL image sources")
	}
	return map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": imageURL},
	}, nil
}

func openAIContentFromParts(parts []map[string]any) any {
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 && parts[0]["type"] == "text" {
		return parts[0]["text"]
	}
	result := make([]any, 0, len(parts))
	for _, part := range parts {
		result = append(result, part)
	}
	return result
}

func anthropicToolResultContent(content any) (any, error) {
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
	parts := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		if blockType != "text" && blockType != "image" {
			return nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool_result", "OpenAI-compatible routes support text and image tool results")
		}
		part, err := anthropicBlockToOpenAIContentPart(block)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return openAIContentFromParts(parts), nil
}

func anthropicToolsToOpenAI(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	rawTools, ok := value.([]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_tools", "tools must be an array")
	}
	tools := make([]any, 0, len(rawTools))
	for _, item := range rawTools {
		tool, ok := item.(map[string]any)
		if !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "each tool must be an object")
		}
		if toolType, _ := tool["type"].(string); toolType != "" && toolType != "custom" {
			return nil, NewHTTPError(
				http.StatusBadRequest,
				"unsupported_tool",
				fmt.Sprintf("OpenAI-compatible routes do not support Anthropic tool type %q", toolType),
			)
		}
		name, _ := tool["name"].(string)
		if name == "" {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "tool name is required")
		}
		inputSchema, ok := tool["input_schema"].(map[string]any)
		if !ok {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_tool", "tool input_schema must be an object")
		}
		function := map[string]any{
			"name":       name,
			"parameters": inputSchema,
		}
		if description, ok := tool["description"].(string); ok && description != "" {
			function["description"] = description
		}
		if strict, ok := tool["strict"].(bool); ok {
			function["strict"] = strict
		}
		tools = append(tools, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return tools, nil
}

func anthropicToolChoiceToOpenAI(value any) (any, *bool, error) {
	if value == nil {
		return nil, nil, nil
	}
	choice, ok := value.(map[string]any)
	if !ok {
		return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_choice", "tool_choice must be an object")
	}
	choiceType, _ := choice["type"].(string)
	var converted any
	switch choiceType {
	case "auto", "":
		converted = "auto"
	case "any":
		converted = "required"
	case "none":
		converted = "none"
	case "tool":
		name, _ := choice["name"].(string)
		if name == "" {
			return nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_tool_choice", "tool_choice tool requires name")
		}
		converted = map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name},
		}
	default:
		return nil, nil, NewHTTPError(http.StatusBadRequest, "unsupported_tool_choice", "unsupported tool_choice type")
	}
	var parallel *bool
	if disabled, ok := choice["disable_parallel_tool_use"].(bool); ok {
		enabled := !disabled
		parallel = &enabled
	}
	return converted, parallel, nil
}

func openAIResponseToAnthropic(body map[string]any, model string, usage Usage) (map[string]any, error) {
	choices, ok := anySlice(body["choices"])
	if !ok || len(choices) == 0 {
		return nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider response is missing choices")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider choice is invalid")
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		return nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider choice is missing message")
	}
	content := make([]any, 0)
	if text := openAIMessageText(message["content"]); text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	toolCalls, err := openAIToolCallsToAnthropic(message["tool_calls"])
	if err != nil {
		return nil, err
	}
	content = append(content, toolCalls...)
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	finishReason, _ := choice["finish_reason"].(string)
	stopReason := openAIFinishReasonToAnthropic(finishReason, len(toolCalls) > 0)
	id, _ := body["id"].(string)
	if id == "" {
		id = NewID("msg")
	}
	return map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         anthropicUsageObject(usage),
	}, nil
}

func openAIMessageText(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func openAIToolCallsToAnthropic(value any) ([]any, error) {
	if value == nil {
		return nil, nil
	}
	rawCalls, ok := anySlice(value)
	if !ok {
		return nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider tool_calls is invalid")
	}
	result := make([]any, 0, len(rawCalls))
	for _, item := range rawCalls {
		call, ok := item.(map[string]any)
		if !ok {
			return nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider tool call is invalid")
		}
		id, _ := call["id"].(string)
		function, _ := call["function"].(map[string]any)
		name, _ := function["name"].(string)
		arguments, _ := function["arguments"].(string)
		if id == "" || name == "" {
			return nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider tool call requires id and function name")
		}
		input := map[string]any{}
		if strings.TrimSpace(arguments) != "" {
			decoder := json.NewDecoder(strings.NewReader(arguments))
			decoder.UseNumber()
			if err := decoder.Decode(&input); err != nil {
				return nil, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider tool call arguments are not valid JSON")
			}
		}
		result = append(result, map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": input,
		})
	}
	return result, nil
}

func anySlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []map[string]any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result, true
	default:
		return nil, false
	}
}

func openAIFinishReasonToAnthropic(reason string, hasTools bool) string {
	if hasTools || reason == "tool_calls" || reason == "function_call" {
		return "tool_use"
	}
	switch reason {
	case "length":
		return "max_tokens"
	case "stop", "":
		return "end_turn"
	case "content_filter":
		return "refusal"
	default:
		return "end_turn"
	}
}

func anthropicUsageObject(usage Usage) map[string]any {
	cachedInputTokens := minInt64(maxInt64(usage.CachedInputTokens, 0), maxInt64(usage.PromptTokens, 0))
	return map[string]any{
		"input_tokens":                maxInt64(usage.PromptTokens-cachedInputTokens, 0),
		"cache_creation_input_tokens": int64(0),
		"cache_read_input_tokens":     cachedInputTokens,
		"output_tokens":               usage.CompletionTokens,
	}
}

func (s *Server) executeNativeAnthropicMessages(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	headers http.Header,
) (map[string]any, Usage, error) {
	payload := cloneAnyMap(req.Raw)
	payload["model"] = route.ProviderModel
	payload["stream"] = false
	resp, err := s.doNativeAnthropicRequest(ctx, route.Provider, "/v1/messages", payload, headers, false)
	if err != nil {
		return nil, Usage{}, err
	}
	defer resp.Body.Close()
	var body map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Anthropic provider returned invalid JSON")
	}
	body["model"] = req.Model
	return body, anthropicUsage(body), nil
}

func (s *Server) doNativeAnthropicRequest(
	ctx context.Context,
	provider Provider,
	endpoint string,
	payload map[string]any,
	downstreamHeaders http.Header,
	stream bool,
) (*http.Response, error) {
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(downstreamHeaders.Get("anthropic-version"))
	if version == "" {
		version = strings.TrimSpace(provider.Options["anthropic_version"])
	}
	if version == "" {
		version = "2023-06-01"
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", provider.APIKey)
	req.Header.Set("anthropic-version", version)
	if betas := strings.TrimSpace(downstreamHeaders.Get("anthropic-beta")); betas != "" {
		req.Header.Set("anthropic-beta", betas)
	}
	for key, value := range provider.Headers {
		req.Header.Set(key, value)
	}
	// The native path builds its own request but must follow the same streaming
	// policy as the adapter: a total deadline would truncate a live stream.
	adapter, _ := s.adapters[ProviderAnthropic].(AnthropicAdapter)
	resp, err := sendUpstream(adapter.Client, adapter.StreamClient, adapter.StreamIdleTimeout, req, stream)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, newProviderHTTPError(resp.StatusCode, resp.Header, data)
	}
	return resp, nil
}

func cloneAnyMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (s *Server) handleAnthropicMessagesStream(
	w http.ResponseWriter,
	r *http.Request,
	routed RoutedCall,
	req anthropicMessagesRequest,
) {
	compatible, compatibilityErr := compatibleAnthropicRoutes(routed, req)
	if compatibilityErr != nil {
		s.finishFailedRoutedCall(r, routed, nil, compatibilityErr, req.Raw)
		writeAnthropicError(w, r, compatibilityErr)
		return
	}
	routed = compatible

	tracker := &streamWriteTracker{writer: w}
	_, route, usage, attempts, err := executeRoutedWithStore(r.Context(), s.store, routed, false,
		func(ctx context.Context, candidate RouteSelection, _ bool, attempt int) (struct{}, Usage, error) {
			prepared, prepareErr := s.prepareRouteForUpstream(ctx, candidate)
			if prepareErr != nil {
				return struct{}{}, Usage{}, prepareErr
			}
			// Defer the response headers until the first byte is written, at which
			// point prepared is the route that actually served the request.
			tracker.onFirstWrite = func() {
				w.Header().Set("content-type", "text/event-stream")
				w.Header().Set("cache-control", "no-cache")
				w.Header().Set("x-request-id", routed.Call.RequestID)
				s.writeRouteHeaders(w, routed.Call, prepared, attempt)
			}

			var streamUsage Usage
			var streamErr error
			switch {
			case prepared.Provider.Type == ProviderAnthropic:
				streamUsage, streamErr = s.streamNativeAnthropicMessages(ctx, prepared, req, r.Header, tracker)
			case openAIMessageProvider(prepared.Provider.Type):
				streamUsage, streamErr = s.streamOpenAIAsAnthropic(ctx, prepared, req, tracker)
			default:
				streamErr = NewHTTPError(
					http.StatusNotImplemented,
					"provider_capability_not_supported",
					"Provider does not support the Anthropic Messages gateway",
				)
			}
			return struct{}{}, streamUsage, classifyStreamError(ctx, streamErr, tracker.Wrote())
		})

	status, code := statusAndCode(err)
	if err == nil {
		// An upstream may complete with 200 and an empty body, in which case
		// onFirstWrite never ran and the client would receive none of the
		// streaming headers.
		tracker.ensureStarted()
		s.store.MarkRouteUsed(route.Route.ID)
		s.store.MarkProviderResourceUsed(routeResourceID(route))
	}
	s.finishRoutedCall(r, GatewayCallCompletion{
		Call:            routed.Call,
		Route:           route,
		Usage:           usage,
		Attempts:        attempts,
		StatusCode:      status,
		ErrorCode:       code,
		ErrorMessage:    errorMessageOrEmpty(err),
		RequestPayload:  req.Raw,
		ResponsePayload: auditStreamPayload(status, code, err),
	})
	if err != nil {
		if tracker.Wrote() {
			_ = writeAnthropicStreamError(tracker, err)
		} else {
			// Nothing reached the client, so onFirstWrite never ran. Emit routing
			// headers alongside the JSON error so callers can still see the attempts.
			w.Header().Del("cache-control")
			s.writeRouteHeaders(w, routed.Call, lastAttemptRoute(attempts), len(attempts))
			writeAnthropicError(w, r, err)
		}
	}
}

func (s *Server) streamNativeAnthropicMessages(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	headers http.Header,
	writer io.Writer,
) (Usage, error) {
	payload := cloneAnyMap(req.Raw)
	payload["model"] = route.ProviderModel
	payload["stream"] = true
	resp, err := s.doNativeAnthropicRequest(ctx, route.Provider, "/v1/messages", payload, headers, true)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	return copyNativeAnthropicStream(writer, resp.Body, req.Model)
}

func copyNativeAnthropicStream(writer io.Writer, body io.Reader, model string) (Usage, error) {
	reader := bufio.NewReader(body)
	var usage Usage
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			output := line
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" && payload != "[DONE]" {
					var event map[string]any
					decoder := json.NewDecoder(strings.NewReader(payload))
					decoder.UseNumber()
					if err := decoder.Decode(&event); err != nil {
						return usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Anthropic provider returned invalid stream JSON")
					}
					if message, ok := event["message"].(map[string]any); ok {
						message["model"] = model
						if eventUsage, ok := message["usage"].(map[string]any); ok {
							usage = mergeAnthropicStreamUsage(usage, eventUsage)
						}
					}
					if eventUsage, ok := event["usage"].(map[string]any); ok {
						usage = mergeAnthropicStreamUsage(usage, eventUsage)
					}
					encoded, _ := json.Marshal(event)
					output = "data: " + string(encoded) + "\n"
				}
			}
			if _, err := io.WriteString(writer, output); err != nil {
				return usage, err
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return usage, nil
			}
			return usage, readErr
		}
	}
}

func mergeAnthropicStreamUsage(current Usage, raw map[string]any) Usage {
	inputTokens := int64FromAny(raw["input_tokens"])
	cacheCreationInputTokens := int64FromAny(raw["cache_creation_input_tokens"])
	cacheReadInputTokens := int64FromAny(raw["cache_read_input_tokens"])
	if inputTokens > 0 || cacheCreationInputTokens > 0 || cacheReadInputTokens > 0 {
		current.PromptTokens = inputTokens + cacheCreationInputTokens + cacheReadInputTokens
		current.CachedInputTokens = cacheReadInputTokens
	}
	if value := int64FromAny(raw["output_tokens"]); value > 0 {
		current.CompletionTokens = value
	}
	current.TotalTokens = current.PromptTokens + current.CompletionTokens
	return current
}

func (s *Server) streamOpenAIAsAnthropic(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	writer io.Writer,
) (Usage, error) {
	chatReq, err := anthropicToOpenAIChatRequest(req)
	if err != nil {
		return Usage{}, err
	}
	adapter, err := s.adapterForRoute(route)
	if err != nil {
		return Usage{}, err
	}
	converter := newOpenAIAnthropicStreamConverter(writer, req.Model, estimateAnthropicInputTokens(req.Raw))
	usage, err := adapter.ChatStream(ctx, route.Provider, route.ProviderModel, chatReq, converter)
	if err != nil {
		return usage, err
	}
	if usage.TotalTokens == 0 {
		usage = converter.usage
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateAnthropicInputTokens(req.Raw)
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = EstimateTextTokens(converter.outputText.String() + converter.toolArgumentText())
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if err := converter.Finalize(usage); err != nil {
		return usage, err
	}
	return usage, nil
}

type openAIStreamToolCall struct {
	Index     int
	ID        string
	Name      string
	Arguments strings.Builder
}

type openAIAnthropicStreamConverter struct {
	writer      io.Writer
	model       string
	messageID   string
	inputTokens int64
	buffer      string
	started     bool
	textStarted bool
	textIndex   int
	nextIndex   int
	finish      string
	finalized   bool
	tools       map[int]*openAIStreamToolCall
	usage       Usage
	outputText  strings.Builder
}

func newOpenAIAnthropicStreamConverter(writer io.Writer, model string, inputTokens int64) *openAIAnthropicStreamConverter {
	return &openAIAnthropicStreamConverter{
		writer:      writer,
		model:       model,
		messageID:   NewID("msg"),
		inputTokens: inputTokens,
		tools:       map[int]*openAIStreamToolCall{},
	}
}

func (c *openAIAnthropicStreamConverter) Write(data []byte) (int, error) {
	c.buffer += string(data)
	for {
		index := strings.IndexByte(c.buffer, '\n')
		if index < 0 {
			break
		}
		line := c.buffer[:index]
		c.buffer = c.buffer[index+1:]
		if err := c.consumeLine(line); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (c *openAIAnthropicStreamConverter) consumeLine(line string) error {
	payload := strings.TrimSpace(line)
	if payload == "" || strings.HasPrefix(payload, ":") || strings.HasPrefix(payload, "event:") {
		return nil
	}
	if !strings.HasPrefix(payload, "data:") {
		return nil
	}
	payload = strings.TrimSpace(strings.TrimPrefix(payload, "data:"))
	if payload == "" || payload == "[DONE]" {
		return nil
	}
	var event map[string]any
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI provider returned invalid stream JSON")
	}
	if id, ok := event["id"].(string); ok && id != "" {
		c.messageID = id
	}
	if parsed := usageFromMap(event); parsed.TotalTokens > 0 {
		c.usage = parsed
	}
	choices, ok := anySlice(event["choices"])
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream choice is invalid")
	}
	if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
		c.finish = finish
	}
	delta, _ := choice["delta"].(map[string]any)
	if text, ok := delta["content"].(string); ok && text != "" {
		if err := c.startMessage(); err != nil {
			return err
		}
		if !c.textStarted {
			c.textIndex = c.nextIndex
			c.nextIndex++
			c.textStarted = true
			if err := c.emit("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": c.textIndex,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			}); err != nil {
				return err
			}
		}
		c.outputText.WriteString(text)
		if err := c.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": c.textIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": text,
			},
		}); err != nil {
			return err
		}
	}
	rawToolCalls, _ := anySlice(delta["tool_calls"])
	for _, item := range rawToolCalls {
		call, ok := item.(map[string]any)
		if !ok {
			return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream tool call is invalid")
		}
		index := int(int64FromAny(call["index"]))
		current := c.tools[index]
		if current == nil {
			current = &openAIStreamToolCall{Index: index}
			c.tools[index] = current
		}
		if id, ok := call["id"].(string); ok {
			current.ID += id
		}
		if function, ok := call["function"].(map[string]any); ok {
			if name, ok := function["name"].(string); ok {
				current.Name += name
			}
			if arguments, ok := function["arguments"].(string); ok {
				current.Arguments.WriteString(arguments)
			}
		}
	}
	return nil
}

func (c *openAIAnthropicStreamConverter) startMessage() error {
	if c.started {
		return nil
	}
	c.started = true
	return c.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            c.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         c.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  c.inputTokens,
				"output_tokens": int64(0),
			},
		},
	})
}

func (c *openAIAnthropicStreamConverter) Finalize(usage Usage) error {
	if c.finalized {
		return nil
	}
	c.finalized = true
	if c.buffer != "" {
		if err := c.consumeLine(c.buffer); err != nil {
			return err
		}
		c.buffer = ""
	}
	if err := c.startMessage(); err != nil {
		return err
	}
	if c.textStarted {
		if err := c.emit("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": c.textIndex,
		}); err != nil {
			return err
		}
	}
	toolIndexes := make([]int, 0, len(c.tools))
	for index := range c.tools {
		toolIndexes = append(toolIndexes, index)
	}
	sort.Ints(toolIndexes)
	for _, toolIndex := range toolIndexes {
		call := c.tools[toolIndex]
		if call.ID == "" || call.Name == "" {
			return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream tool call requires id and function name")
		}
		arguments := call.Arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		var input any
		decoder := json.NewDecoder(strings.NewReader(arguments))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream tool call arguments are not valid JSON")
		}
		index := c.nextIndex
		c.nextIndex++
		if err := c.emit("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Name,
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		if err := c.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": arguments,
			},
		}); err != nil {
			return err
		}
		if err := c.emit("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": index,
		}); err != nil {
			return err
		}
	}
	stopReason := openAIFinishReasonToAnthropic(c.finish, len(c.tools) > 0)
	if err := c.emit("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": usage.CompletionTokens,
		},
	}); err != nil {
		return err
	}
	return c.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (c *openAIAnthropicStreamConverter) emit(event string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	if flusher, ok := c.writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (c *openAIAnthropicStreamConverter) toolArgumentText() string {
	var output strings.Builder
	indexes := make([]int, 0, len(c.tools))
	for index := range c.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		output.WriteString(c.tools[index].Arguments.String())
	}
	return output.String()
}

func estimateAnthropicInputTokens(payload map[string]any) int64 {
	var tokens int64
	for _, key := range []string{"system", "messages", "tools", "tool_choice"} {
		tokens += estimateAnthropicValueTokens(payload[key])
	}
	if tokens <= 0 {
		return 1
	}
	return tokens
}

func estimateAnthropicValueTokens(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return EstimateTextTokens(typed)
	case json.Number:
		return 1
	case bool:
		return 1
	case []any:
		var total int64
		for _, item := range typed {
			total += estimateAnthropicValueTokens(item) + 1
		}
		return total
	case map[string]any:
		if blockType, _ := typed["type"].(string); blockType == "image" {
			return 1600
		}
		var total int64
		for key, item := range typed {
			if key == "cache_control" || key == "signature" || key == "data" {
				continue
			}
			total += EstimateTextTokens(key) + estimateAnthropicValueTokens(item) + 1
		}
		return total
	default:
		return EstimateTextTokens(fmt.Sprint(typed))
	}
}

func writeAnthropicError(w http.ResponseWriter, r *http.Request, err error) {
	httpErr := AsHTTPError(err)
	requestID := errorResponseHeaders(w, err)
	writeJSON(w, httpErr.Status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    anthropicErrorType(httpErr.Status),
			"message": httpErr.Message,
			"code":    httpErr.Code,
		},
		"request_id": requestID,
	})
}

func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

func writeAnthropicStreamError(writer io.Writer, err error) error {
	httpErr := AsHTTPError(err)
	payload, marshalErr := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    anthropicErrorType(httpErr.Status),
			"message": httpErr.Message,
			"code":    httpErr.Code,
		},
	})
	if marshalErr != nil {
		return marshalErr
	}
	_, writeErr := fmt.Fprintf(writer, "event: error\ndata: %s\n\n", payload)
	return writeErr
}
