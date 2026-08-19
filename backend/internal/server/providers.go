package server

import (
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
		"usage": openAIChatUsageObject(usage),
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
		"usage": openAIResponsesUsageObject(usage),
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

// preservesReasoningContent reports whether an OpenAI-compatible upstream
// understands the reasoning_content it produced being handed back on the next
// turn. DeepSeek does and needs it for multi-turn reasoning; for everyone else
// the field is a TokenHub-local extension and is stripped.
func preservesReasoningContent(provider Provider) bool {
	return providerPreservesReasoningContent(provider)
}

// dropsReasoningContent is the Azure policy: reasoning_content never reaches a
// deployment. It is stated as the adapter's own rule rather than inferred from
// the provider type, because nothing enforces which types an adapter is
// registered under and Azure's answer does not depend on that.
func dropsReasoningContent(Provider) bool {
	return false
}

// providerRequestBuilder builds the upstream request for one OpenAI-shaped call.
// The URL and the auth headers are the whole wire-format difference between the
// OpenAI-compatible and Azure OpenAI adapters; everything around it — sending,
// failure checking, decoding, streaming — is shared by openAICompatibleCore.
type providerRequestBuilder func(ctx context.Context, provider Provider, method string, model string, endpoint string, body []byte) (*http.Request, error)

// openAICompatibleCore is the request machinery the OpenAI-compatible and Azure
// OpenAI adapters share. The adapters hold it through a core() method rather
// than embedding it, and it stays unexported, because embedding would also hand
// AzureOpenAIAdapter the OpenAI adapter's Responses and OpenResponses methods:
// /v1/responses dispatches on those interfaces without consulting the
// capability registry, so Azure would stop answering 501 and start calling an
// endpoint Azure deployments do not serve.
type openAICompatibleCore struct {
	client *http.Client
	// streamClient carries no total deadline; see provider_stream_timeout.go.
	// Streaming calls fall back to client when it is unset.
	streamClient      *http.Client
	streamIdleTimeout time.Duration
	// baseURLRequired is the misconfiguration reported when the provider has no
	// base URL. Both adapters require one; only the wording differs.
	baseURLRequired string
	// preserveReasoningContent is the adapter's rule for whether TokenHub's
	// reasoning_content extension may be forwarded to this provider.
	preserveReasoningContent func(provider Provider) bool
	build                    providerRequestBuilder
}

func (c openAICompatibleCore) chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	req = withoutGatewayExtensions(req, c.preserveReasoningContent(provider))
	req.Model = providerModel
	req.ReasoningEffort = normalizedReasoningEffort(req.ReasoningEffort)
	var body map[string]any
	if err := c.doJSON(ctx, provider, http.MethodPost, providerModel, "/chat/completions", req, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (c openAICompatibleCore) chatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	req = withoutGatewayExtensions(req, c.preserveReasoningContent(provider))
	req.Model = providerModel
	req.Stream = true
	req.ReasoningEffort = normalizedReasoningEffort(req.ReasoningEffort)
	req = includeOpenAIStreamUsage(req)
	resp, err := c.doRaw(ctx, provider, http.MethodPost, providerModel, "/chat/completions", req, true)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	return copyOpenAIStreamAndUsageForProvider(w, resp.Body, provider)
}

func (c openAICompatibleCore) embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	req.Model = providerModel
	var body map[string]any
	if err := c.doJSON(ctx, provider, http.MethodPost, providerModel, "/embeddings", req, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (c openAICompatibleCore) doJSON(ctx context.Context, provider Provider, method string, model string, endpoint string, payload any, target any) error {
	resp, err := c.doRaw(ctx, provider, method, model, endpoint, payload, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func (c openAICompatibleCore) doRaw(ctx context.Context, provider Provider, method string, model string, endpoint string, payload any, stream bool) (*http.Response, error) {
	if provider.BaseURL == "" {
		return nil, newProviderMisconfigured(c.baseURLRequired)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := c.build(ctx, provider, method, model, endpoint, body)
	if err != nil {
		return nil, err
	}
	resp, err := sendUpstream(c.client, c.streamClient, c.streamIdleTimeout, req, stream)
	if err != nil {
		return nil, err
	}
	if err := checkProviderResponseForProvider(resp, provider); err != nil {
		return nil, err
	}
	return resp, nil
}

// openAICompatibleRequest addresses an OpenAI-compatible base URL directly: the
// model travels in the body, so the model argument is unused here.
func openAICompatibleRequest(ctx context.Context, provider Provider, method string, _ string, endpoint string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, joinURL(provider.BaseURL, endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if provider.APIKey != "" {
		req.Header.Set("authorization", "Bearer "+provider.APIKey)
	}
	applyOpenAICompatibleAccountHeaders(req, provider)
	applyProviderHeaders(req.Header, provider.Headers)
	return req, nil
}

type OpenAICompatibleAdapter struct {
	Client *http.Client
	// StreamClient carries no total deadline; see provider_stream_timeout.go.
	// Streaming calls fall back to Client when it is unset.
	StreamClient      *http.Client
	StreamIdleTimeout time.Duration
}

func (a OpenAICompatibleAdapter) core() openAICompatibleCore {
	return openAICompatibleCore{
		client:                   a.Client,
		streamClient:             a.StreamClient,
		streamIdleTimeout:        a.StreamIdleTimeout,
		baseURLRequired:          "Provider base_url is required",
		preserveReasoningContent: preservesReasoningContent,
		build:                    openAICompatibleRequest,
	}
}

func (a OpenAICompatibleAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	return a.core().chat(ctx, provider, providerModel, req)
}

func (a OpenAICompatibleAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	return a.core().chatStream(ctx, provider, providerModel, req, w)
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

func (a OpenAICompatibleAdapter) OpenResponses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest, _ http.Header) (*http.Response, error) {
	req.Model = providerModel
	req.Stream = true
	req = normalizedResponsesReasoning(req)
	return a.doRaw(ctx, provider, http.MethodPost, "/responses", req, true)
}

func (a OpenAICompatibleAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	return a.core().embeddings(ctx, provider, providerModel, req)
}

func (a OpenAICompatibleAdapter) doJSON(ctx context.Context, provider Provider, method, endpoint string, payload any, target any) error {
	return a.core().doJSON(ctx, provider, method, "", endpoint, payload, target)
}

func (a OpenAICompatibleAdapter) doRaw(ctx context.Context, provider Provider, method, endpoint string, payload any, stream bool) (*http.Response, error) {
	return a.core().doRaw(ctx, provider, method, "", endpoint, payload, stream)
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

// azureOpenAIDefaultAPIVersion is used when the provider does not pin one.
const azureOpenAIDefaultAPIVersion = "2024-02-15-preview"

// azureOpenAIRequest addresses one Azure OpenAI deployment: the model names a
// deployment in the path, the API version is a query parameter, and the key is
// an api-key header rather than a bearer token. Provider.Headers are not applied
// here — Azure has never forwarded them, and adding that now would change which
// headers reach every existing deployment.
func azureOpenAIRequest(ctx context.Context, provider Provider, method string, deployment string, endpoint string, body []byte) (*http.Request, error) {
	apiVersion := provider.Options["api_version"]
	if apiVersion == "" {
		apiVersion = azureOpenAIDefaultAPIVersion
	}
	u := fmt.Sprintf("%s/openai/deployments/%s%s?api-version=%s", strings.TrimRight(provider.BaseURL, "/"), url.PathEscape(deployment), endpoint, url.QueryEscape(apiVersion))
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("api-key", provider.APIKey)
	return req, nil
}

// AzureOpenAIAdapter speaks the OpenAI wire format against Azure deployments. It
// shares openAICompatibleCore with OpenAICompatibleAdapter but stays a separate
// type on purpose: it must not acquire that adapter's Responses or
// OpenResponses, which the /v1/responses paths dispatch on directly. See
// openAICompatibleCore.
type AzureOpenAIAdapter struct {
	Client *http.Client
	// StreamClient carries no total deadline; see provider_stream_timeout.go.
	// Streaming calls fall back to Client when it is unset.
	StreamClient      *http.Client
	StreamIdleTimeout time.Duration
}

func (a AzureOpenAIAdapter) core() openAICompatibleCore {
	return openAICompatibleCore{
		client:                   a.Client,
		streamClient:             a.StreamClient,
		streamIdleTimeout:        a.StreamIdleTimeout,
		baseURLRequired:          "Azure OpenAI base_url is required",
		preserveReasoningContent: dropsReasoningContent,
		build:                    azureOpenAIRequest,
	}
}

func (a AzureOpenAIAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	return a.core().chat(ctx, provider, providerModel, req)
}

func (a AzureOpenAIAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	return a.core().chatStream(ctx, provider, providerModel, req, w)
}

func (a AzureOpenAIAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(501, "provider_capability_not_supported", "Azure responses adapter is not implemented in MVP")
}

func (a AzureOpenAIAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	return a.core().embeddings(ctx, provider, providerModel, req)
}

type AnthropicAdapter struct {
	Client *http.Client
	// StreamClient carries no total deadline; see provider_stream_timeout.go.
	// Streaming calls fall back to Client when it is unset.
	StreamClient      *http.Client
	StreamIdleTimeout time.Duration
}

const (
	anthropicAuthTypeOption = "anthropic_auth_type"
	anthropicAuthTypeAPIKey = "x-api-key"
	anthropicAuthTypeBearer = "bearer"
)

func configureAnthropicProviderAuth(provider *Provider, requested string) error {
	if provider == nil || provider.Type != ProviderAnthropic {
		return nil
	}
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	authType := strings.ToLower(strings.TrimSpace(requested))
	if authType == "" {
		authType = strings.ToLower(strings.TrimSpace(provider.Options[anthropicAuthTypeOption]))
	}
	if authType == "" {
		return nil
	}
	if authType != anthropicAuthTypeAPIKey && authType != anthropicAuthTypeBearer {
		return NewHTTPError(
			http.StatusBadRequest,
			"provider_anthropic_auth_type_invalid",
			"Anthropic authentication type must be x-api-key or bearer",
		)
	}
	provider.Options[anthropicAuthTypeOption] = authType
	return nil
}

func applyAnthropicProviderAuth(req *http.Request, provider Provider) {
	if req == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(provider.Options[anthropicAuthTypeOption]), anthropicAuthTypeBearer) {
		req.Header.Del("x-api-key")
		req.Header.Set("authorization", "Bearer "+provider.APIKey)
		return
	}
	// Preserve the existing Anthropic behavior when no option is configured.
	req.Header.Del("authorization")
	req.Header.Set("x-api-key", provider.APIKey)
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
	resp, err := a.doRaw(ctx, provider, "/v1/messages", payload, true)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	encoder := newOpenAIChatStreamEncoder(w, req.Model, streamUsageRequested(req))
	return streamAnthropicChatForProvider(resp.Body, encoder, provider)
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
	resp, err := a.doRaw(ctx, provider, endpoint, payload, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func (a AnthropicAdapter) doRaw(ctx context.Context, provider Provider, endpoint string, payload any, stream bool) (*http.Response, error) {
	if provider.BaseURL == "" {
		provider.BaseURL = "https://api.anthropic.com"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpointURL(provider.BaseURL, endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	version := provider.Options["anthropic_version"]
	if version == "" {
		version = "2023-06-01"
	}
	req.Header.Set("content-type", "application/json")
	applyAnthropicProviderAuth(req, provider)
	req.Header.Set("anthropic-version", version)
	applyProviderHeaders(req.Header, provider.Headers)
	resp, err := sendUpstream(a.Client, a.StreamClient, a.StreamIdleTimeout, req, stream)
	if err != nil {
		return nil, err
	}
	if err := checkProviderResponseForProvider(resp, provider); err != nil {
		return nil, err
	}
	return resp, nil
}

type GeminiAdapter struct {
	Client *http.Client
	// StreamClient carries no total deadline; see provider_stream_timeout.go.
	// Streaming calls fall back to Client when it is unset.
	StreamClient      *http.Client
	StreamIdleTimeout time.Duration
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
	resp, err := a.doRaw(ctx, provider, providerModel, ":streamGenerateContent?alt=sse", payload, true)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	encoder := newOpenAIChatStreamEncoder(w, req.Model, streamUsageRequested(req))
	return streamGeminiChatForProvider(resp.Body, encoder, provider)
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
	resp, err := a.doRaw(ctx, provider, model, action, payload, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

// doRaw issues a Gemini request. The action may already carry a query string
// (streaming uses ":streamGenerateContent?alt=sse"), so the API key separator is
// chosen accordingly.
func (a GeminiAdapter) doRaw(ctx context.Context, provider Provider, model string, action string, payload any, stream bool) (*http.Response, error) {
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
	applyProviderHeaders(req.Header, provider.Headers)
	resp, err := sendUpstream(a.Client, a.StreamClient, a.StreamIdleTimeout, req, stream)
	if err != nil {
		return nil, err
	}
	if err := checkProviderResponseForProvider(resp, provider); err != nil {
		return nil, err
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
	// Both codes: an upstream 400 or 422 is classified as provider_invalid_request,
	// while anything unrecognised still arrives as provider_error.
	if httpErr == nil ||
		(httpErr.Code != "provider_error" && httpErr.Code != "provider_invalid_request") ||
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

func geminiUsage(body map[string]any) Usage {
	usageMap, _ := body["usageMetadata"].(map[string]any)
	reasoningTokens := int64FromAny(usageMap["thoughtsTokenCount"])
	usage := Usage{
		PromptTokens:          int64FromAny(usageMap["promptTokenCount"]),
		CachedInputTokens:     int64FromAny(firstNonNil(usageMap["cachedContentTokenCount"], usageMap["totalCachedTokens"])),
		CompletionTokens:      int64FromAny(usageMap["candidatesTokenCount"]) + reasoningTokens,
		ReasoningOutputTokens: reasoningTokens,
		TotalTokens:           int64FromAny(usageMap["totalTokenCount"]),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}

func usageFromMap(body map[string]any) Usage {
	usageMap, _ := body["usage"].(map[string]any)
	inputDetails, _ := firstNonNil(usageMap["prompt_tokens_details"], usageMap["input_tokens_details"]).(map[string]any)
	outputDetails, _ := firstNonNil(usageMap["completion_tokens_details"], usageMap["output_tokens_details"]).(map[string]any)
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
		InputAudioTokens:      int64FromAny(firstNonNil(inputDetails["audio_tokens"], usageMap["input_audio_tokens"])),
		CompletionTokens:      int64FromAny(firstNonNil(usageMap["completion_tokens"], usageMap["output_tokens"])),
		ReasoningOutputTokens: int64FromAny(firstNonNil(usageMap["reasoning_output_tokens"], outputDetails["reasoning_tokens"])),
		OutputAudioTokens:     int64FromAny(firstNonNil(outputDetails["audio_tokens"], usageMap["output_audio_tokens"])),
		AcceptedPredictionTokens: int64FromAny(firstNonNil(
			outputDetails["accepted_prediction_tokens"],
			usageMap["accepted_prediction_tokens"],
			usageMap["output_accepted_prediction_tokens"],
		)),
		RejectedPredictionTokens: int64FromAny(firstNonNil(
			outputDetails["rejected_prediction_tokens"],
			usageMap["rejected_prediction_tokens"],
			usageMap["output_rejected_prediction_tokens"],
		)),
		TotalTokens: int64FromAny(usageMap["total_tokens"]),
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

// copyOpenAIStreamAndUsage forwards an upstream OpenAI-compatible event stream
// to the client verbatim while reading usage out of it. Framing is delegated to
// sseDecoder, which bounds a single event; the frame's raw bytes are what reach
// the client, so the provider's framing survives the round trip untouched.
func copyOpenAIStreamAndUsage(w io.Writer, body io.Reader) (Usage, error) {
	return copyOpenAIStreamAndUsageForProvider(w, body, Provider{})
}

func copyOpenAIStreamAndUsageForProvider(w io.Writer, body io.Reader, provider Provider) (Usage, error) {
	events := newSSEDecoder(body)
	var usage Usage
	for {
		event, err := events.Next()
		if err == io.EOF {
			return usage, nil
		}
		if err != nil {
			// A stream that fails mid-frame has already put those bytes on the
			// wire; the client is owed them before the failure surfaces.
			if pending := events.PendingEvent(); len(pending.Raw) > 0 {
				if _, writeErr := w.Write(redactProviderStreamEventSecrets(pending, provider)); writeErr != nil {
					return usage, writeErr
				}
			}
			return usage, err
		}
		// A token stream is mostly frames that are neither an error nor a usage
		// report, so both questions are answered from a single decode of the
		// payload rather than one full map decode each.
		probe := probeProviderStreamEvent(event.Data)
		output := event.Raw
		if sseEventNameIsError(event.Event) || probe.isError() {
			output = redactProviderStreamEventSecrets(event, provider)
		}
		if _, err := w.Write(output); err != nil {
			return usage, err
		}
		if flusher, ok := w.(streamFlusher); ok {
			flusher.Flush()
		}
		if parsed, ok := usageFromProbedFrame(event, probe); ok {
			usage = parsed
		}
	}
}

// providerStreamEventProbe is one SSE payload decoded down to its top-level
// keys and no further, so a single decode answers both the error check and the
// usage read.
//
// Leaving the values raw is what makes that safe: a field carrying an
// unexpected JSON type cannot fail the decode and take the other field's answer
// with it, which is the same independence the old per-key type assertions on
// map[string]any had. A map rather than a struct is deliberate too —
// encoding/json matches struct fields case-insensitively, so a frame could hide
// its "error" behind a later "ERROR" key and be forwarded to the client without
// redaction. Map keys match exactly, the way the old lookups did.
type providerStreamEventProbe map[string]json.RawMessage

// probeProviderStreamEvent decodes one SSE payload's top-level keys. A payload
// that carries no JSON object — empty, [DONE], malformed, or a non-object —
// probes as nil, which reports neither an error nor usage.
func probeProviderStreamEvent(data string) providerStreamEventProbe {
	payload := strings.TrimSpace(data)
	if payload == "" || payload == "[DONE]" {
		return nil
	}
	var probe providerStreamEventProbe
	if json.Unmarshal([]byte(payload), &probe) != nil {
		return nil
	}
	return probe
}

func (probe providerStreamEventProbe) isError() bool {
	if sseEventTypeIsError(probe["type"]) {
		return true
	}
	if jsonRawIsPresent(probe["error"]) {
		return true
	}
	// Only an object can carry a nested error; anything else failed the old
	// map[string]any assertion and was read as no error.
	response := jsonRawValue(probe["response"])
	if len(response) == 0 || response[0] != '{' {
		return false
	}
	var nested providerStreamEventProbe
	return json.Unmarshal(response, &nested) == nil && jsonRawIsPresent(nested["error"])
}

// usage decodes the frame's usage object. Only an object counts, and an empty
// one counts as no usage, matching what the old map assertion accepted.
func (probe providerStreamEventProbe) usage() (Usage, bool) {
	value := jsonRawValue(probe["usage"])
	if len(value) == 0 || value[0] != '{' {
		return Usage{}, false
	}
	var usageMap map[string]any
	if json.Unmarshal(value, &usageMap) != nil || len(usageMap) == 0 {
		return Usage{}, false
	}
	// usageFromMap only ever reads body["usage"], so handing it the decoded
	// usage object under that one key is the same call the whole-payload map
	// used to make.
	return usageFromMap(map[string]any{"usage": usageMap}), true
}

func sseEventNameIsError(name string) bool {
	eventName := strings.ToLower(strings.TrimSpace(name))
	return eventName == "error" || strings.HasSuffix(eventName, ".failed")
}

// sseEventTypeIsError reads the payload's "type" field. The value is only read
// when it is a JSON string, because the old code reached it through a string
// type assertion that failed for every other JSON type.
func sseEventTypeIsError(raw json.RawMessage) bool {
	value := jsonRawValue(raw)
	if len(value) < 2 || value[0] != '"' {
		return false
	}
	quoted := value[1 : len(value)-1]
	eventType := string(quoted)
	if bytes.IndexByte(quoted, '\\') >= 0 {
		// An escaped name has to be unquoted before it can be compared, but
		// every frame a stream actually carries names a plain type, so the
		// decode stays off the common path.
		if json.Unmarshal(value, &eventType) != nil {
			return false
		}
	}
	trimmed := strings.TrimSpace(eventType)
	return strings.EqualFold(trimmed, "error") || strings.HasSuffix(strings.ToLower(trimmed), ".failed")
}

// jsonRawValue strips the JSON whitespace that can surround a raw value so its
// first byte identifies its type.
func jsonRawValue(raw json.RawMessage) []byte {
	return bytes.Trim(raw, " \t\r\n")
}

// jsonRawIsPresent reports whether a field was present and is not JSON null,
// which is what "the key exists and its value is not nil" meant when these
// payloads were decoded into a map[string]any.
func jsonRawIsPresent(raw json.RawMessage) bool {
	value := jsonRawValue(raw)
	return len(value) > 0 && !bytes.Equal(value, []byte("null"))
}

func providerStreamEventIsError(event serverSentEvent) bool {
	return sseEventNameIsError(event.Event) || probeProviderStreamEvent(event.Data).isError()
}

// usageFromServerSentEvent reads usage out of one frame. A frame it cannot parse
// is reported as carrying no usage rather than as an error: this is a
// pass-through, and the client is owed the frame either way.
func usageFromServerSentEvent(frame serverSentEvent) (Usage, bool) {
	return usageFromProbedFrame(frame, probeProviderStreamEvent(frame.Data))
}

// usageFromProbedFrame reads usage from a frame whose payload has already been
// probed, so the streaming path pays for one decode instead of two.
//
// A payload spanning more than one line is handed back to the parsing the probe
// replaced. Some OpenAI-compatible servers separate chunks with a single
// newline rather than the blank line SSE requires, which leaves every payload
// joined into one frame; scanning the joined segments keeps usage — and so
// billing — working for those upstreams instead of silently recording zero. But
// which segment wins depends on which segments parse at all, so that whole
// decision is left with the parser that has always made it.
func usageFromProbedFrame(frame serverSentEvent, probe providerStreamEventProbe) (Usage, bool) {
	if !strings.Contains(frame.Data, "\n") {
		return probe.usage()
	}
	if usage, ok := usageFromSSEPayload(frame.Data); ok {
		return usage, true
	}
	var (
		usage Usage
		found bool
	)
	for _, segment := range strings.Split(frame.Data, "\n") {
		if parsed, ok := usageFromSSEPayload(segment); ok {
			usage, found = parsed, true
		}
	}
	return usage, found
}

// usageFromSSEPayload reads usage out of a multi-line payload or one of its
// newline-separated segments. It keeps the whole-payload decode the probe
// replaced, on purpose: a segment carrying a number no float64 can hold has
// never parsed here, and that is part of which segment wins. Only a frame whose
// data spans more than one line reaches it, so the frames a token stream is
// actually made of never pay for it.
func usageFromSSEPayload(data string) (Usage, bool) {
	payload := strings.TrimSpace(data)
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
		"usage": openAIResponsesUsageObject(usage),
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

// statusForProvider is the flat mapping the routed paths no longer use; see
// provider_error_classification.go. It remains for the catalog probe, which
// reports on a provider the operator is configuring rather than routing to.
func statusForProvider(status int) int {
	if status == http.StatusTooManyRequests {
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}
