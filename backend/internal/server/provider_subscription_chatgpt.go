package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	ProviderChatGPTSubscription         = "chatgpt-subscription"
	ProviderResourceChatGPTSubscription = "chatgpt_subscription"
	chatGPTSubscriptionBaseURL          = "https://api.openai.com/v1"
	chatGPTSubscriptionTokenURL         = "https://auth.openai.com/api/accounts/oauth/token"
	chatGPTSubscriptionRefreshProfile   = "chatgpt_subscription_oauth"
	chatGPTSubscriptionDirectScope      = "chatgpt.tokens.use.direct"
)

// ChatGPTSubscriptionAdapter uses Sign in with ChatGPT's dynamic client and
// direct API token. It does not impersonate Codex or call the Codex backend.
type ChatGPTSubscriptionAdapter struct {
	Client                  *http.Client
	StreamClient            *http.Client
	CredentialRefreshClient *http.Client
	StreamIdleTimeout       time.Duration
	RefreshCredentials      func(context.Context, string, bool) (ProviderResourceCredentials, error)
	APIBaseURL              string
	TokenURL                string
}

func (a *ChatGPTSubscriptionAdapter) ProviderResourceCredentialRefreshHandlers() []providerResourceCredentialRefreshRegistration {
	return []providerResourceCredentialRefreshRegistration{{ProviderType: ProviderChatGPTSubscription, Profile: chatGPTSubscriptionRefreshProfile,
		RefreshLead: 3 * time.Minute, AuthenticationEqual: providerResourceAuthenticationEqual, Refresh: a.Refresh}}
}

func (a *ChatGPTSubscriptionAdapter) request(ctx context.Context, provider Provider, path string, body []byte, etag string) (*http.Response, error) {
	endpoint, err := subscriptionEndpoint(provider.BaseURL, chatGPTSubscriptionBaseURL, path, func(host string) bool { return host == "api.openai.com" })
	if err != nil {
		return nil, err
	}
	if a.APIBaseURL != "" {
		endpoint = strings.TrimRight(a.APIBaseURL, "/") + path
	}
	for attempt := 0; attempt < 2; attempt++ {
		credentials, err := subscriptionCredentials(ctx, provider, a.RefreshCredentials, attempt > 0)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(credentials.AccessToken) == "" {
			return nil, NewHTTPError(400, "subscription_token_missing", "ChatGPT API access token is required")
		}
		method := http.MethodPost
		if body == nil {
			method = http.MethodGet
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		client := a.Client
		if body != nil {
			req.Header.Set("Accept", "text/event-stream")
			client = a.StreamClient
			if client == nil {
				client = a.Client
			}
		}
		resp, err := subscriptionHTTP(ctx, client, req)
		if err != nil {
			if attempt == 0 && providerErrorDisposition(err) == ProviderErrorAuthBroken {
				continue
			}
			return nil, err
		}
		if body != nil {
			resp.Body = newIdleTimeoutReadCloser(resp.Body, subscriptionIdleTimeout(a.StreamIdleTimeout), NewHTTPError(504, "subscription_stream_idle", "Subscription stream was idle for too long"))
			redactionProvider := provider
			redactionProvider.Headers = cloneStringMap(provider.Headers)
			if redactionProvider.Headers == nil {
				redactionProvider.Headers = make(map[string]string)
			}
			redactionProvider.Headers["TokenHub-Subscription-Access"] = credentials.AccessToken
			redactionProvider.SensitiveHeaders = append(append([]string{}, provider.SensitiveHeaders...), "TokenHub-Subscription-Access")
			resp.Body = newSubscriptionErrorStream(resp.Body, func() Provider { return redactionProvider }, 0)
		}
		return resp, nil
	}
	return nil, NewHTTPError(401, "subscription_token_refused", "ChatGPT API access token was refused")
}

func (a *ChatGPTSubscriptionAdapter) OpenResponses(ctx context.Context, provider Provider, model string, request ResponsesRequest, _ http.Header) (*http.Response, error) {
	request.Model = model
	data, err := chatGPTSubscriptionBody(request)
	if err != nil {
		return nil, err
	}
	return a.request(ctx, provider, "/responses", data, "")
}

func (a *ChatGPTSubscriptionAdapter) Responses(ctx context.Context, provider Provider, model string, request ResponsesRequest) (any, Usage, error) {
	resp, err := a.OpenResponses(ctx, provider, model, request, nil)
	if err != nil {
		return nil, Usage{}, err
	}
	defer resp.Body.Close()
	body, text, usage, err := consumeSubscriptionResponsesStream(resp.Body, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, usage, ctx.Err()
		}
		return nil, usage, NewHTTPError(502, "subscription_stream_failed", "ChatGPT API stream did not complete")
	}
	if text != "" {
		body["output_text"] = text
	}
	return body, usage, nil
}

func chatGPTSubscriptionBody(request ResponsesRequest) ([]byte, error) {
	raw, err := json.Marshal(normalizedResponsesReasoning(request))
	if err != nil {
		return nil, NewHTTPError(400, "invalid_request", "ChatGPT API request is invalid")
	}
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	for _, key := range []string{"background", "conversation", "max_output_tokens", "max_tool_calls", "metadata", "moderation", "multi_agent", "prompt", "prompt_cache_retention", "safety_identifier", "temperature", "top_logprobs", "top_p", "truncation", "user", "previous_response_id"} {
		delete(body, key)
	}
	body["store"], body["stream"] = false, true
	if input, ok := body["input"].(string); ok {
		body["input"] = []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": input}}}}
	}
	if items, ok := body["input"].([]any); ok {
		for _, item := range items {
			if message, ok := item.(map[string]any); ok {
				if message["role"] == "system" {
					message["role"] = "developer"
				}
				if message["type"] == "additional_tools" {
					if err := validateChatGPTSubscriptionTools(message["tools"]); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if err := validateChatGPTSubscriptionTools(body["tools"]); err != nil {
		return nil, err
	}
	return json.Marshal(body)
}

func validateChatGPTSubscriptionTools(value any) error {
	tools, _ := value.([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		switch tool["type"] {
		case "image_generation", "file_search", "code_interpreter", "computer_use", "computer_use_preview", "computer", "mcp", "tool_search":
			return NewHTTPError(400, "subscription_tool_unsupported", "ChatGPT API subscriptions do not support this hosted tool")
		}
	}
	return nil
}

func (a *ChatGPTSubscriptionAdapter) ResourceModels(ctx context.Context, provider Provider, resource ProviderResource, etag string) (ProviderCatalogEntry, int, error) {
	provider.Options = cloneStringMap(provider.Options)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options["resource_id"] = resource.ID
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := a.request(ctx, provider, "/models", nil, etag)
	if err != nil {
		return ProviderCatalogEntry{}, 0, err
	}
	if resp.StatusCode == http.StatusNotModified {
		resp.Body.Close()
		return subscriptionCatalog(ProviderChatGPTSubscription, "ChatGPT API", chatGPTSubscriptionBaseURL, "chatgpt-api-live", etag, nil), resp.StatusCode, nil
	}
	var payload struct {
		Models []struct {
			Slug       string   `json:"slug"`
			Name       string   `json:"display_name"`
			Visibility string   `json:"visibility"`
			Input      []string `json:"input_modalities"`
			Context    int64    `json:"context_window"`
			Levels     []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := subscriptionDecode(resp, &payload); err != nil {
		return ProviderCatalogEntry{}, resp.StatusCode, err
	}
	models := make([]ProviderCatalogModel, 0, len(payload.Models))
	for _, remote := range payload.Models {
		if remote.Slug == "" || remote.Visibility != "" && remote.Visibility != "list" {
			continue
		}
		levels := make([]string, 0, len(remote.Levels))
		for _, level := range remote.Levels {
			if level.Effort != "" {
				levels = append(levels, level.Effort)
			}
		}
		models = append(models, ProviderCatalogModel{ID: remote.Slug, Name: remote.Slug, DisplayName: firstNonEmpty(remote.Name, remote.Slug), Type: "chat", InputModalities: remote.Input, ContextWindow: remote.Context,
			OutputModalities: []string{"text"}, Capabilities: []string{"chat", "streaming", "tools"}, SupportedParameters: []string{"reasoning_effort"}, Metadata: map[string]string{"source": "chatgpt-api-live", "supported_reasoning_levels": strings.Join(levels, ","), "supported_endpoints": "/responses"}})
	}
	if len(models) == 0 {
		return ProviderCatalogEntry{}, resp.StatusCode, NewHTTPError(502, "subscription_models_empty", "ChatGPT API returned no available models")
	}
	return subscriptionCatalog(ProviderChatGPTSubscription, "ChatGPT API", chatGPTSubscriptionBaseURL, "chatgpt-api-live", resp.Header.Get("ETag"), models), resp.StatusCode, nil
}
