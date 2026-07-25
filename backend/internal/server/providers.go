package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ProviderAdapter interface {
	Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error)
	ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error)
	Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error)
	Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error)
}

type ResponsesEnvelopeAdapter interface {
	ResponsesWithHeaders(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest, incoming http.Header) (any, Usage, error)
}

type ResponsesInvoker interface {
	Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error)
}

type ResponsesStreamOpener interface {
	OpenResponses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest, incoming http.Header) (*http.Response, error)
}

type ProviderResourceProber interface {
	DefaultProbeRequest() ProviderProbeRequest
	Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error)
}

type ResponsesCompactAdapter interface {
	CompactWithHeaders(ctx context.Context, provider Provider, providerModel string, body map[string]json.RawMessage, incoming http.Header) (any, Usage, error)
}

type MockAdapter struct{}

func (a MockAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	text := "Echo: " + ChatPromptText(req.Messages)
	usage := Usage{
		PromptTokens:     EstimateTextTokens(ChatPromptText(req.Messages)),
		CompletionTokens: EstimateTextTokens(text),
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return map[string]any{
		"id":      NewID("chatcmpl"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": text,
				},
				"finish_reason": "stop",
			},
		},
		"usage": usage,
	}, usage, nil
}

func (a MockAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	prompt := ChatPromptText(req.Messages)
	text := "Echo: " + prompt
	parts := []string{text}
	if len([]rune(text)) > 24 {
		runes := []rune(text)
		parts = []string{string(runes[:24]), string(runes[24:])}
	}
	for _, part := range parts {
		chunk := map[string]any{
			"id":      NewID("chatcmpl"),
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{
						"content": part,
					},
					"finish_reason": nil,
				},
			},
		}
		payload, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return Usage{}, err
		}
	}
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return Usage{}, err
	}
	usage := Usage{
		PromptTokens:     EstimateTextTokens(prompt),
		CompletionTokens: EstimateTextTokens(text),
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage, nil
}

func (a MockAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	input := ResponsesInputText(req.Input)
	text := "Echo: " + input
	usage := Usage{
		PromptTokens:     EstimateTextTokens(input),
		CompletionTokens: EstimateTextTokens(text),
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return map[string]any{
		"id":          NewID("resp"),
		"object":      "response",
		"created_at":  time.Now().Unix(),
		"model":       req.Model,
		"output_text": text,
		"output": []map[string]any{
			{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": text},
				},
			},
		},
		"usage": usage,
	}, usage, nil
}

func (a MockAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	input := EmbeddingInputText(req.Input)
	vector := deterministicEmbedding(input, 8)
	usage := Usage{PromptTokens: EstimateTextTokens(input)}
	usage.TotalTokens = usage.PromptTokens
	return map[string]any{
		"object": "list",
		"model":  req.Model,
		"data": []map[string]any{
			{
				"object":    "embedding",
				"index":     0,
				"embedding": vector,
			},
		},
		"usage": map[string]any{
			"prompt_tokens": usage.PromptTokens,
			"total_tokens":  usage.TotalTokens,
		},
	}, usage, nil
}

type OpenAICompatibleAdapter struct {
	Client *http.Client
}

func (a OpenAICompatibleAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	req = withoutGatewayExtensions(req)
	req.Model = providerModel
	req.ReasoningEffort = normalizedReasoningEffort(req.ReasoningEffort)
	var body map[string]any
	if err := a.doJSON(ctx, provider, http.MethodPost, "/chat/completions", req, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (a OpenAICompatibleAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	req = withoutGatewayExtensions(req)
	req.Model = providerModel
	req.Stream = true
	req.ReasoningEffort = normalizedReasoningEffort(req.ReasoningEffort)
	req = includeOpenAIStreamUsage(req)
	resp, err := a.doRaw(ctx, provider, http.MethodPost, "/chat/completions", req)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	return copyOpenAIStreamAndUsage(w, resp.Body)
}

func (a OpenAICompatibleAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	req.Model = providerModel
	req = normalizedResponsesReasoning(req)
	var body map[string]any
	if err := a.doJSON(ctx, provider, http.MethodPost, "/responses", req, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (a OpenAICompatibleAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	req.Model = providerModel
	var body map[string]any
	if err := a.doJSON(ctx, provider, http.MethodPost, "/embeddings", req, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (a OpenAICompatibleAdapter) doJSON(ctx context.Context, provider Provider, method, endpoint string, payload any, target any) error {
	resp, err := a.doRaw(ctx, provider, method, endpoint, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func (a OpenAICompatibleAdapter) doRaw(ctx context.Context, provider Provider, method, endpoint string, payload any) (*http.Response, error) {
	if provider.BaseURL == "" {
		return nil, NewHTTPError(503, "provider_not_configured", "Provider base_url is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, joinURL(provider.BaseURL, endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if provider.APIKey != "" {
		req.Header.Set("authorization", "Bearer "+provider.APIKey)
	}
	applyOpenAICompatibleAccountHeaders(req, provider)
	for key, value := range provider.Headers {
		req.Header.Set(key, value)
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, newProviderHTTPError(resp.StatusCode, data)
	}
	return resp, nil
}

func applyOpenAICompatibleAccountHeaders(req *http.Request, provider Provider) {
	if req == nil || provider.Options == nil {
		return
	}
	if value := strings.TrimSpace(provider.Options["organization_id"]); value != "" && req.Header.Get("OpenAI-Organization") == "" {
		req.Header.Set("OpenAI-Organization", value)
	}
	if value := strings.TrimSpace(provider.Options["openai_project_id"]); value != "" && req.Header.Get("OpenAI-Project") == "" {
		req.Header.Set("OpenAI-Project", value)
	}
	if value := strings.TrimSpace(provider.Options["account_id"]); value != "" && req.Header.Get("X-TokenHub-Upstream-Account") == "" {
		req.Header.Set("X-TokenHub-Upstream-Account", value)
	}
}

type AzureOpenAIAdapter struct {
	Client *http.Client
}

func (a AzureOpenAIAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	req = withoutGatewayExtensions(req)
	req.Model = providerModel
	req.ReasoningEffort = normalizedReasoningEffort(req.ReasoningEffort)
	var body map[string]any
	if err := a.doJSON(ctx, provider, providerModel, "/chat/completions", req, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (a AzureOpenAIAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	req = withoutGatewayExtensions(req)
	req.Model = providerModel
	req.Stream = true
	req.ReasoningEffort = normalizedReasoningEffort(req.ReasoningEffort)
	req = includeOpenAIStreamUsage(req)
	resp, err := a.doRaw(ctx, provider, providerModel, "/chat/completions", req)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	return copyOpenAIStreamAndUsage(w, resp.Body)
}

func (a AzureOpenAIAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(501, "provider_capability_not_supported", "Azure responses adapter is not implemented in MVP")
}

func (a AzureOpenAIAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	req.Model = providerModel
	var body map[string]any
	if err := a.doJSON(ctx, provider, providerModel, "/embeddings", req, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (a AzureOpenAIAdapter) doJSON(ctx context.Context, provider Provider, deployment string, endpoint string, payload any, target any) error {
	resp, err := a.doRaw(ctx, provider, deployment, endpoint, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func (a AzureOpenAIAdapter) doRaw(ctx context.Context, provider Provider, deployment string, endpoint string, payload any) (*http.Response, error) {
	apiVersion := provider.Options["api_version"]
	if apiVersion == "" {
		apiVersion = "2024-02-15-preview"
	}
	if provider.BaseURL == "" {
		return nil, NewHTTPError(503, "provider_not_configured", "Azure OpenAI base_url is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/openai/deployments/%s%s?api-version=%s", strings.TrimRight(provider.BaseURL, "/"), url.PathEscape(deployment), endpoint, url.QueryEscape(apiVersion))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("api-key", provider.APIKey)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, newProviderHTTPError(resp.StatusCode, data)
	}
	return resp, nil
}

type AnthropicAdapter struct {
	Client *http.Client
}

func (a AnthropicAdapter) buildRequest(providerModel string, req ChatCompletionRequest) (map[string]any, error) {
	reasoningEffort := normalizedReasoningEffort(req.ReasoningEffort)
	if reasoningEffort != nil && !anthropicReasoningEffortSupported(providerModel, *reasoningEffort) {
		reasoningEffort = nil
	}
	return buildAnthropicRequest(providerModel, req, reasoningEffort)
}

func (a AnthropicAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	payload, err := a.buildRequest(providerModel, req)
	if err != nil {
		return nil, Usage{}, err
	}
	var body map[string]any
	if err := a.doJSON(ctx, provider, "/v1/messages", payload, &body); err != nil {
		return nil, Usage{}, err
	}
	usage := anthropicUsage(body)
	converted, err := anthropicChatResponse(body, req.Model, usage)
	if err != nil {
		return nil, usage, err
	}
	return converted, usage, nil
}

func (a AnthropicAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	payload, err := a.buildRequest(providerModel, req)
	if err != nil {
		return Usage{}, err
	}
	payload["stream"] = true
	resp, err := a.doRaw(ctx, provider, "/v1/messages", payload)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	encoder := newOpenAIChatStreamEncoder(w, req.Model, streamUsageRequested(req))
	return streamAnthropicChat(resp.Body, encoder)
}

func (a AnthropicAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	chatReq := ChatCompletionRequest{
		Model:           req.Model,
		Messages:        []ChatMessage{{Role: "user", Content: req.Input}},
		MaxTokens:       req.MaxTokens,
		ReasoningEffort: responsesReasoningEffort(req),
	}
	resp, usage, err := a.Chat(ctx, provider, providerModel, chatReq)
	if err != nil {
		return nil, Usage{}, err
	}
	text := ""
	if asMap, ok := resp.(map[string]any); ok {
		text = choiceText(asMap)
	}
	return responseObject(req.Model, text, usage), usage, nil
}

func (a AnthropicAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(501, "provider_capability_not_supported", "Anthropic embeddings are not supported")
}

func (a AnthropicAdapter) doJSON(ctx context.Context, provider Provider, endpoint string, payload any, target any) error {
	resp, err := a.doRaw(ctx, provider, endpoint, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func (a AnthropicAdapter) doRaw(ctx context.Context, provider Provider, endpoint string, payload any) (*http.Response, error) {
	if provider.BaseURL == "" {
		provider.BaseURL = "https://api.anthropic.com"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.BaseURL, "/")+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	version := provider.Options["anthropic_version"]
	if version == "" {
		version = "2023-06-01"
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", provider.APIKey)
	req.Header.Set("anthropic-version", version)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, newProviderHTTPError(resp.StatusCode, data)
	}
	return resp, nil
}

type GeminiAdapter struct {
	Client *http.Client
}

func (a GeminiAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	payload, err := buildGeminiRequest(providerModel, req)
	if err != nil {
		return nil, Usage{}, err
	}
	var body map[string]any
	if err := a.doJSON(ctx, provider, providerModel, ":generateContent", payload, &body); err != nil {
		return nil, Usage{}, err
	}
	usage := geminiUsage(body)
	converted, err := geminiChatResponse(body, req.Model, usage)
	if err != nil {
		return nil, usage, err
	}
	return converted, usage, nil
}

func (a GeminiAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	payload, err := buildGeminiRequest(providerModel, req)
	if err != nil {
		return Usage{}, err
	}
	resp, err := a.doRaw(ctx, provider, providerModel, ":streamGenerateContent?alt=sse", payload)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	encoder := newOpenAIChatStreamEncoder(w, req.Model, streamUsageRequested(req))
	return streamGeminiChat(resp.Body, encoder)
}

func (a GeminiAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	chatReq := ChatCompletionRequest{
		Model:           req.Model,
		Messages:        []ChatMessage{{Role: "user", Content: req.Input}},
		MaxTokens:       req.MaxTokens,
		ReasoningEffort: responsesReasoningEffort(req),
	}
	resp, usage, err := a.Chat(ctx, provider, providerModel, chatReq)
	if err != nil {
		return nil, Usage{}, err
	}
	text := ""
	if asMap, ok := resp.(map[string]any); ok {
		text = choiceText(asMap)
	}
	return responseObject(req.Model, text, usage), usage, nil
}

func (a GeminiAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	payload := map[string]any{
		"content": map[string]any{
			"parts": []map[string]any{{"text": EmbeddingInputText(req.Input)}},
		},
	}
	var body map[string]any
	if err := a.doJSON(ctx, provider, providerModel, ":embedContent", payload, &body); err != nil {
		return nil, Usage{}, err
	}
	values := []any{}
	if embedding, ok := body["embedding"].(map[string]any); ok {
		if raw, ok := embedding["values"].([]any); ok {
			values = raw
		}
	}
	usage := Usage{PromptTokens: EstimateTextTokens(EmbeddingInputText(req.Input))}
	usage.TotalTokens = usage.PromptTokens
	return map[string]any{
		"object": "list",
		"model":  req.Model,
		"data": []map[string]any{
			{"object": "embedding", "index": 0, "embedding": values},
		},
		"usage": map[string]any{"prompt_tokens": usage.PromptTokens, "total_tokens": usage.TotalTokens},
	}, usage, nil
}

func (a GeminiAdapter) doJSON(ctx context.Context, provider Provider, model string, action string, payload any, target any) error {
	resp, err := a.doRaw(ctx, provider, model, action, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

// doRaw issues a Gemini request. The action may already carry a query string
// (streaming uses ":streamGenerateContent?alt=sse"), so the API key separator is
// chosen accordingly.
func (a GeminiAdapter) doRaw(ctx context.Context, provider Provider, model string, action string, payload any) (*http.Response, error) {
	if provider.BaseURL == "" {
		provider.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	separator := "?"
	if strings.Contains(action, "?") {
		separator = "&"
	}
	u := fmt.Sprintf("%s/models/%s%s%skey=%s",
		strings.TrimRight(provider.BaseURL, "/"),
		url.PathEscape(model),
		action,
		separator,
		url.QueryEscape(provider.APIKey),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, newProviderHTTPError(resp.StatusCode, data)
	}
	return resp, nil
}

func responsesReasoningEffort(req ResponsesRequest) *string {
	if req.Reasoning == nil {
		return nil
	}
	return req.Reasoning.Effort
}

func normalizedReasoningEffort(effort *string) *string {
	if effort == nil {
		return nil
	}
	value := strings.TrimSpace(*effort)
	if value == "" {
		return nil
	}
	return &value
}

func normalizedResponsesReasoning(req ResponsesRequest) ResponsesRequest {
	if req.Reasoning == nil {
		return req
	}
	reasoning := *req.Reasoning
	reasoning.Effort = normalizedReasoningEffort(reasoning.Effort)
	req.Reasoning = &reasoning
	return req
}

func withoutResponsesReasoningEffort(req ResponsesRequest) ResponsesRequest {
	if req.Reasoning == nil {
		return req
	}
	reasoning := *req.Reasoning
	reasoning.Effort = nil
	req.Reasoning = &reasoning
	return req
}

func isReasoningEffortRejection(err error) bool {
	httpErr := AsHTTPError(err)
	if httpErr == nil ||
		httpErr.Code != "provider_error" ||
		(httpErr.UpstreamStatus != http.StatusBadRequest && httpErr.UpstreamStatus != http.StatusUnprocessableEntity) {
		return false
	}
	message := strings.ToLower(httpErr.Message)
	effortField := false
	for _, marker := range []string{
		"reasoning_effort",
		"reasoning.effort",
		"output_config.effort",
		"output_config",
		"thinkingconfig",
		"thinking_config",
		"thinkinglevel",
		"thinkingbudget",
	} {
		if strings.Contains(message, marker) {
			effortField = true
			break
		}
	}
	if !effortField {
		return false
	}
	for _, marker := range []string{
		"invalid",
		"unsupported",
		"not supported",
		"unknown",
		"unrecognized",
		"unexpected",
		"not permitted",
		"does not support",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func anthropicReasoningEffortSupported(model string, effort string) bool {
	effort = strings.TrimSpace(effort)
	switch anthropicReasoningEffortModelFamily(model) {
	case "claude-fable-5", "claude-mythos-5", "claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-5":
		return effort == "low" || effort == "medium" || effort == "high" || effort == "xhigh" || effort == "max"
	case "claude-mythos-preview", "claude-opus-4-6", "claude-sonnet-4-6":
		return effort == "low" || effort == "medium" || effort == "high" || effort == "max"
	case "claude-opus-4-5":
		return effort == "low" || effort == "medium" || effort == "high"
	default:
		return false
	}
}

func anthropicReasoningEffortModelFamily(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	for _, family := range []string{
		"claude-mythos-preview",
		"claude-fable-5",
		"claude-mythos-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
	} {
		index := strings.LastIndex(normalized, family)
		if index < 0 || !anthropicModelBoundary(normalized, index-1) || !anthropicModelBoundary(normalized, index+len(family)) {
			continue
		}
		return family
	}
	return ""
}

func anthropicModelBoundary(model string, index int) bool {
	if index < 0 || index >= len(model) {
		return true
	}
	switch model[index] {
	case '-', '_', '.', '/', ':', '@':
		return true
	default:
		return false
	}
}

func geminiThinkingConfig(model string, effort string) (map[string]any, bool) {
	effort = strings.TrimSpace(effort)
	modelVersion := geminiModelVersion(model)
	modelVariant := geminiModelVariant(model)
	if strings.HasPrefix(modelVersion, "2.5") {
		switch effort {
		case "none":
			if geminiVariantIs(modelVariant, "pro") {
				return nil, false
			}
			return map[string]any{"thinkingBudget": 0}, true
		case "minimal", "low":
			return map[string]any{"thinkingBudget": 1024}, true
		case "medium":
			return map[string]any{"thinkingBudget": 8192}, true
		case "high":
			return map[string]any{"thinkingBudget": 24576}, true
		default:
			return nil, false
		}
	}
	majorText := strings.SplitN(modelVersion, ".", 2)[0]
	major, err := strconv.Atoi(majorText)
	if err != nil || major < 3 {
		return nil, false
	}
	if geminiVariantIs(modelVariant, "flash-lite-image") {
		switch effort {
		case "minimal", "high":
			return map[string]any{"thinkingLevel": effort}, true
		default:
			return nil, false
		}
	}
	if effort == "minimal" && geminiVariantIs(modelVariant, "pro") {
		return map[string]any{"thinkingLevel": "low"}, true
	}
	switch effort {
	case "minimal", "low", "medium", "high":
		return map[string]any{"thinkingLevel": effort}, true
	default:
		return nil, false
	}
}

func geminiModelVersion(model string) string {
	modelID := geminiModelID(model)
	if modelID == "" {
		return ""
	}
	version := strings.TrimPrefix(modelID, "gemini-")
	for i, r := range version {
		if (r < '0' || r > '9') && r != '.' {
			return version[:i]
		}
	}
	return version
}

func geminiModelVariant(model string) string {
	modelID := geminiModelID(model)
	version := geminiModelVersion(modelID)
	if modelID == "" || version == "" {
		return ""
	}
	return strings.TrimPrefix(modelID, "gemini-"+version+"-")
}

func geminiModelID(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	index := strings.LastIndex(normalized, "gemini-")
	if index < 0 {
		return ""
	}
	return normalized[index:]
}

func geminiVariantIs(variant string, family string) bool {
	return variant == family || strings.HasPrefix(variant, family+"-")
}

func anthropicUsage(body map[string]any) Usage {
	usageMap, _ := body["usage"].(map[string]any)
	uncachedInputTokens := int64FromAny(usageMap["input_tokens"])
	cacheCreationInputTokens := int64FromAny(usageMap["cache_creation_input_tokens"])
	cachedInputTokens := int64FromAny(usageMap["cache_read_input_tokens"])
	usage := Usage{
		PromptTokens:      uncachedInputTokens + cacheCreationInputTokens + cachedInputTokens,
		CachedInputTokens: cachedInputTokens,
		CompletionTokens:  int64FromAny(usageMap["output_tokens"]),
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

func geminiUsage(body map[string]any) Usage {
	usageMap, _ := body["usageMetadata"].(map[string]any)
	usage := Usage{
		PromptTokens:      int64FromAny(usageMap["promptTokenCount"]),
		CachedInputTokens: int64FromAny(firstNonNil(usageMap["cachedContentTokenCount"], usageMap["totalCachedTokens"])),
		CompletionTokens:  int64FromAny(usageMap["candidatesTokenCount"]),
		TotalTokens:       int64FromAny(usageMap["totalTokenCount"]),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}

func usageFromMap(body map[string]any) Usage {
	usageMap, _ := body["usage"].(map[string]any)
	inputDetails, _ := firstNonNil(usageMap["prompt_tokens_details"], usageMap["input_tokens_details"]).(map[string]any)
	outputDetails, _ := usageMap["output_tokens_details"].(map[string]any)
	usage := Usage{
		PromptTokens: int64FromAny(firstNonNil(usageMap["prompt_tokens"], usageMap["input_tokens"])),
		CachedInputTokens: int64FromAny(firstNonNil(
			inputDetails["cached_tokens"],
			usageMap["prompt_cache_hit_tokens"],
			usageMap["cached_input_tokens"],
			usageMap["cached_tokens"],
			usageMap["total_cached_tokens"],
		)),
		CacheWriteInputTokens: int64FromAny(firstNonNil(usageMap["cache_write_input_tokens"], inputDetails["cache_write_tokens"])),
		CompletionTokens:      int64FromAny(firstNonNil(usageMap["completion_tokens"], usageMap["output_tokens"])),
		ReasoningOutputTokens: int64FromAny(firstNonNil(usageMap["reasoning_output_tokens"], outputDetails["reasoning_tokens"])),
		TotalTokens:           int64FromAny(usageMap["total_tokens"]),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}

func includeOpenAIStreamUsage(req ChatCompletionRequest) ChatCompletionRequest {
	options := make(map[string]any, len(req.StreamOptions)+1)
	for key, value := range req.StreamOptions {
		options[key] = value
	}
	options["include_usage"] = true
	req.StreamOptions = options
	return req
}

func copyOpenAIStreamAndUsage(w io.Writer, body io.Reader) (Usage, error) {
	reader := bufio.NewReader(body)
	var usage Usage
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			if _, err := io.WriteString(w, line); err != nil {
				return usage, err
			}
			if parsed, ok := usageFromServerSentEvent(line); ok {
				usage = parsed
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

func usageFromServerSentEvent(line string) (Usage, bool) {
	payload := strings.TrimSpace(line)
	if !strings.HasPrefix(payload, "data:") {
		return Usage{}, false
	}
	payload = strings.TrimSpace(strings.TrimPrefix(payload, "data:"))
	if payload == "" || payload == "[DONE]" {
		return Usage{}, false
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return Usage{}, false
	}
	usageMap, ok := event["usage"].(map[string]any)
	if !ok || len(usageMap) == 0 {
		return Usage{}, false
	}
	return usageFromMap(event), true
}

func responseObject(model string, text string, usage Usage) map[string]any {
	return map[string]any{
		"id":          NewID("resp"),
		"object":      "response",
		"created_at":  time.Now().Unix(),
		"model":       model,
		"output_text": text,
		"output": []map[string]any{
			{
				"type":    "message",
				"role":    "assistant",
				"content": []map[string]any{{"type": "output_text", "text": text}},
			},
		},
		"usage": usage,
	}
}

func choiceText(resp map[string]any) string {
	choices, _ := resp["choices"].([]map[string]any)
	if len(choices) == 0 {
		rawChoices, _ := resp["choices"].([]any)
		if len(rawChoices) == 0 {
			return ""
		}
		first, _ := rawChoices[0].(map[string]any)
		message, _ := first["message"].(map[string]any)
		text, _ := message["content"].(string)
		return text
	}
	message, _ := choices[0]["message"].(map[string]any)
	text, _ := message["content"].(string)
	return text
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		v, _ := typed.Int64()
		return v
	default:
		return 0
	}
}

func deterministicEmbedding(text string, dims int) []float64 {
	if dims <= 0 {
		dims = 8
	}
	vector := make([]float64, dims)
	if text == "" {
		return vector
	}
	for idx, r := range []rune(text) {
		slot := idx % dims
		vector[slot] += float64((int(r)%97)+1) / 100
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return vector
	}
	for idx := range vector {
		vector[idx] = math.Round((vector[idx]/norm)*1_000_000) / 1_000_000
	}
	return vector
}

func joinURL(base string, endpoint string) string {
	base = strings.TrimRight(base, "/")
	endpoint = "/" + strings.TrimLeft(endpoint, "/")
	return base + endpoint
}

func newProviderHTTPError(upstreamStatus int, data []byte) *HTTPError {
	err := NewHTTPError(
		statusForProvider(upstreamStatus),
		"provider_error",
		strings.TrimSpace(string(data)),
	)
	err.UpstreamStatus = upstreamStatus
	return err
}

func statusForProvider(status int) int {
	if status == http.StatusTooManyRequests {
		return http.StatusTooManyRequests
	}
	if status >= 500 {
		return http.StatusBadGateway
	}
	return http.StatusBadGateway
}
