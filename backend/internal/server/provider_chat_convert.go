package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Shared helpers for converting OpenAI Chat Completions payloads to and from
// provider-native wire formats. Anthropic and Gemini both need the same
// normalization of message content, tool definitions and reasoning metadata, so
// the logic lives here instead of being duplicated per adapter.

// chatContentKind enumerates the content part types TokenHub can forward to a
// non-OpenAI provider. Anything outside this set is rejected explicitly rather
// than silently dropped.
type chatContentKind string

const (
	chatContentText     chatContentKind = "text"
	chatContentImageURL chatContentKind = "image_url"
	chatContentImageB64 chatContentKind = "image_base64"
)

// chatContentPart is the normalized form of one OpenAI content array element.
type chatContentPart struct {
	Kind      chatContentKind
	Text      string
	URL       string
	MediaType string
	Data      string
}

// parseChatContent normalizes an OpenAI message content value. Content may be a
// plain string or an array of typed parts.
func parseChatContent(content any) ([]chatContentPart, error) {
	switch typed := content.(type) {
	case nil:
		return nil, nil
	case string:
		if typed == "" {
			return nil, nil
		}
		return []chatContentPart{{Kind: chatContentText, Text: typed}}, nil
	case []any:
		parts := make([]chatContentPart, 0, len(typed))
		for _, item := range typed {
			asMap, ok := item.(map[string]any)
			if !ok {
				return nil, unsupportedContentError("content array entries must be objects")
			}
			part, err := parseChatContentPart(asMap)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		return parts, nil
	default:
		return nil, unsupportedContentError("message content must be a string or an array of content parts")
	}
}

func parseChatContentPart(part map[string]any) (chatContentPart, error) {
	partType, _ := part["type"].(string)
	switch partType {
	case "text", "input_text":
		text, _ := part["text"].(string)
		return chatContentPart{Kind: chatContentText, Text: text}, nil
	case "image_url":
		return parseChatImagePart(part)
	case "":
		if text, ok := part["text"].(string); ok {
			return chatContentPart{Kind: chatContentText, Text: text}, nil
		}
		return chatContentPart{}, unsupportedContentError("content part is missing a type")
	default:
		return chatContentPart{}, unsupportedContentError(fmt.Sprintf("content part type %q is not supported by this provider", partType))
	}
}

func parseChatImagePart(part map[string]any) (chatContentPart, error) {
	var raw string
	switch typed := part["image_url"].(type) {
	case string:
		raw = typed
	case map[string]any:
		raw, _ = typed["url"].(string)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return chatContentPart{}, unsupportedContentError("image_url content part is missing a url")
	}
	if strings.HasPrefix(raw, "data:") {
		mediaType, data, err := parseDataURI(raw)
		if err != nil {
			return chatContentPart{}, err
		}
		return chatContentPart{Kind: chatContentImageB64, MediaType: mediaType, Data: data}, nil
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return chatContentPart{}, unsupportedContentError("image_url must be an http(s) URL or a data URI")
	}
	return chatContentPart{Kind: chatContentImageURL, URL: raw}, nil
}

// parseDataURI splits a base64 data URI into its media type and payload.
func parseDataURI(value string) (string, string, error) {
	remainder := strings.TrimPrefix(value, "data:")
	separator := strings.Index(remainder, ",")
	if separator < 0 {
		return "", "", unsupportedContentError("data URI is missing a comma separator")
	}
	meta := remainder[:separator]
	payload := remainder[separator+1:]
	if !strings.Contains(meta, ";base64") {
		return "", "", unsupportedContentError("only base64 data URIs are supported")
	}
	mediaType := strings.TrimSpace(strings.SplitN(meta, ";", 2)[0])
	if mediaType == "" {
		return "", "", unsupportedContentError("data URI is missing a media type")
	}
	if payload == "" {
		return "", "", unsupportedContentError("data URI payload is empty")
	}
	return mediaType, payload, nil
}

// contentPartsText joins the text parts of a normalized content value.
func contentPartsText(parts []chatContentPart) string {
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Kind == chatContentText && part.Text != "" {
			segments = append(segments, part.Text)
		}
	}
	return strings.Join(segments, "")
}

// unsupportedContentError is raised while converting the caller's request, before
// the provider is contacted. It is the caller's payload that cannot be expressed,
// so the resource is not at fault and no other candidate would fare better —
// without the disposition it counted as a provider failure, and one client
// looping on an unconvertible payload could trip a healthy resource's breaker.
func unsupportedContentError(detail string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadRequest, "unsupported_content_block", detail),
		Disposition: ProviderErrorClient,
	}
}

// unsupportedCapabilityError says this adapter cannot serve the request at all.
// Another candidate may still be able to, but the resource itself is healthy.
func unsupportedCapabilityError(detail string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", detail),
		Disposition: ProviderErrorModelUnsupported,
	}
}

func invalidProviderResponseError(detail string) error {
	return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", detail)
}

// chatToolDefinition is the normalized form of one OpenAI tool entry.
type chatToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
	Strict      bool
}

// parseChatTools normalizes request tools. OpenAI nests the schema under
// "function"; both Anthropic and Gemini expect a flattened shape.
func parseChatTools(value any) ([]chatToolDefinition, error) {
	if value == nil {
		return nil, nil
	}
	entries, ok := value.([]any)
	if !ok {
		return nil, unsupportedContentError("tools must be an array")
	}
	tools := make([]chatToolDefinition, 0, len(entries))
	for _, entry := range entries {
		asMap, ok := entry.(map[string]any)
		if !ok {
			return nil, unsupportedContentError("tool entries must be objects")
		}
		if toolType, _ := asMap["type"].(string); toolType != "" && toolType != "function" {
			return nil, unsupportedCapabilityError(fmt.Sprintf("tool type %q is not supported by this provider", toolType))
		}
		function, ok := asMap["function"].(map[string]any)
		if !ok {
			return nil, unsupportedContentError("tool entries must contain a function object")
		}
		name, _ := function["name"].(string)
		if err := validateToolName(name); err != nil {
			return nil, err
		}
		description, _ := function["description"].(string)
		parameters, _ := function["parameters"].(map[string]any)
		if parameters == nil {
			// Both providers require an object schema; null is rejected upstream.
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		strict, _ := function["strict"].(bool)
		tools = append(tools, chatToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  parameters,
			Strict:      strict,
		})
	}
	return tools, nil
}

// validateToolName enforces the character set both providers accept.
func validateToolName(name string) error {
	if name == "" {
		return unsupportedContentError("tool function name is required")
	}
	if len(name) > 64 {
		return unsupportedContentError(fmt.Sprintf("tool function name %q exceeds 64 characters", name))
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return unsupportedContentError(fmt.Sprintf("tool function name %q contains unsupported characters", name))
		}
	}
	return nil
}

// chatToolChoice is the normalized form of the OpenAI tool_choice field.
type chatToolChoice struct {
	Mode string // "auto", "required", "none" or "function"
	Name string // set when Mode is "function"
}

func parseChatToolChoice(value any) (chatToolChoice, error) {
	switch typed := value.(type) {
	case nil:
		return chatToolChoice{Mode: "auto"}, nil
	case string:
		switch typed {
		case "auto", "required", "none":
			return chatToolChoice{Mode: typed}, nil
		default:
			return chatToolChoice{}, unsupportedContentError(fmt.Sprintf("tool_choice %q is not supported", typed))
		}
	case map[string]any:
		choiceType, _ := typed["type"].(string)
		if choiceType != "function" {
			return chatToolChoice{}, unsupportedCapabilityError(fmt.Sprintf("tool_choice type %q is not supported by this provider", choiceType))
		}
		function, _ := typed["function"].(map[string]any)
		name, _ := function["name"].(string)
		if name == "" {
			return chatToolChoice{}, unsupportedContentError("tool_choice function name is required")
		}
		return chatToolChoice{Mode: "function", Name: name}, nil
	default:
		return chatToolChoice{}, unsupportedContentError("tool_choice must be a string or an object")
	}
}

// chatToolCall is the normalized form of one assistant tool call.
type chatToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
	// Signature carries provider-scoped continuation data (Gemini thought
	// signatures). Empty when the client did not echo it back.
	Signature string
}

// parseChatToolCalls normalizes assistant tool_calls. OpenAI encodes arguments
// as a JSON string; providers expect a decoded object.
func parseChatToolCalls(value any) ([]chatToolCall, error) {
	if value == nil {
		return nil, nil
	}
	entries, ok := anySlice(value)
	if !ok {
		return nil, unsupportedContentError("tool_calls must be an array")
	}
	calls := make([]chatToolCall, 0, len(entries))
	for _, entry := range entries {
		asMap, ok := entry.(map[string]any)
		if !ok {
			return nil, unsupportedContentError("tool_calls entries must be objects")
		}
		id, _ := asMap["id"].(string)
		function, ok := asMap["function"].(map[string]any)
		if !ok {
			return nil, unsupportedContentError("tool_calls entries must contain a function object")
		}
		name, _ := function["name"].(string)
		if name == "" {
			return nil, unsupportedContentError("tool_calls entries must contain a function name")
		}
		arguments := map[string]any{}
		if raw, _ := function["arguments"].(string); strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
				return nil, unsupportedContentError(fmt.Sprintf("tool call %q has invalid JSON arguments", name))
			}
		}
		signature, _ := asMap["thought_signature"].(string)
		calls = append(calls, chatToolCall{ID: id, Name: name, Arguments: arguments, Signature: signature})
	}
	return calls, nil
}

// encodeToolCallArguments renders a provider tool input back to the JSON string
// OpenAI clients expect.
func encodeToolCallArguments(input any) string {
	if input == nil {
		return "{}"
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// Provider-scoped reasoning signatures.
//
// The OpenAI Chat Completions schema has no field for a provider's opaque
// reasoning continuation data, so TokenHub exposes it through extension fields.
// Values are prefixed with the originating provider so a signature minted by one
// provider is never replayed to another; a mismatch degrades to dropping the
// reasoning block rather than failing the request.
const (
	anthropicSignatureProvider = "anthropic"
	geminiSignatureProvider    = "gemini"
)

// withoutGatewayExtensions strips TokenHub-only reasoning fields before a
// request is forwarded to an OpenAI-shaped upstream.
//
// ReasoningContent is preserved only for DeepSeek, where it is an upstream
// protocol field used by tool-call continuations. Signature fields always stay
// gateway-local because they carry Anthropic or Gemini opaque state.
func withoutGatewayExtensions(req ChatCompletionRequest, preserveReasoningContent bool) ChatCompletionRequest {
	needsCopy := false
	for _, message := range req.Messages {
		_, hasReasoningContent := message.raw["reasoning_content"]
		_, hasReasoningSignature := message.raw["reasoning_signature"]
		_, hasRedactedReasoningContent := message.raw["redacted_reasoning_content"]
		_, hasCodexReasoningDetails := withoutCodexReasoningDetails(message.raw["reasoning_details"])
		if (!preserveReasoningContent && (message.ReasoningContent != "" || hasReasoningContent)) ||
			message.ReasoningSignature != "" || hasReasoningSignature ||
			message.RedactedReasoningContent != "" || hasRedactedReasoningContent ||
			hasCodexReasoningDetails {
			needsCopy = true
			break
		}
	}
	if !needsCopy {
		return req
	}
	messages := make([]ChatMessage, len(req.Messages))
	copy(messages, req.Messages)
	for index := range messages {
		if messages[index].raw != nil {
			messages[index].raw = cloneRawJSON(messages[index].raw, 0)
		}
		if !preserveReasoningContent {
			messages[index].ReasoningContent = ""
			delete(messages[index].raw, "reasoning_content")
		}
		messages[index].ReasoningSignature = ""
		messages[index].RedactedReasoningContent = ""
		delete(messages[index].raw, "reasoning_signature")
		delete(messages[index].raw, "redacted_reasoning_content")
		if details, changed := withoutCodexReasoningDetails(messages[index].raw["reasoning_details"]); changed {
			if len(details) == 0 {
				delete(messages[index].raw, "reasoning_details")
			} else {
				messages[index].raw["reasoning_details"] = details
			}
		}
	}
	req.Messages = messages
	return req
}

func withoutCodexReasoningDetails(value json.RawMessage) (json.RawMessage, bool) {
	var details []json.RawMessage
	if len(value) == 0 || json.Unmarshal(value, &details) != nil {
		return value, false
	}
	filtered := make([]json.RawMessage, 0, len(details))
	changed := false
	for _, detail := range details {
		var encoded struct {
			Data string `json:"data"`
		}
		if json.Unmarshal(detail, &encoded) == nil {
			if _, codex := decodeProviderSignature(codexSignatureProvider, encoded.Data); codex {
				changed = true
				continue
			}
		}
		filtered = append(filtered, detail)
	}
	if !changed {
		return value, false
	}
	if len(filtered) == 0 {
		return nil, true
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return value, false
	}
	return encoded, true
}

func encodeProviderSignature(provider string, raw string) string {
	if raw == "" {
		return ""
	}
	return provider + ":" + raw
}

// decodeProviderSignature returns the opaque payload when the value was minted
// by the same provider. The second result reports whether the value is usable.
func decodeProviderSignature(provider string, value string) (string, bool) {
	if value == "" {
		return "", false
	}
	prefix := provider + ":"
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	payload := strings.TrimPrefix(value, prefix)
	if payload == "" {
		return "", false
	}
	return payload, true
}
