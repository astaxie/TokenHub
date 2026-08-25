package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	xaiCLIChatProxyBaseURL      = "https://cli-chat-proxy.grok.com/v1"
	xaiCLIChatProxyResponsesURL = xaiCLIChatProxyBaseURL + "/responses"
	xaiGrokClientVersion        = "0.2.120"
	xaiGrokUserAgent            = "xai-grok-workspace/" + xaiGrokClientVersion
	xaiGrokDefaultProbeModel    = "grok-4.5"
	xaiGrokComposerPrefix       = "grok-composer-"
	xaiGrokStreamIdleTimeout    = 5 * time.Minute
)

var errXAIGrokStreamIdle = NewHTTPError(
	http.StatusGatewayTimeout,
	"xai_grok_stream_idle_timeout",
	"Super Grok stream was idle for too long",
)

type XAIGrokSubscriptionAdapter struct {
	Client *http.Client
	// Client has no total deadline: streaming is bounded by silence, not by
	// wall-clock duration. StreamIdleTimeout is that budget; zero keeps five minutes.
	StreamIdleTimeout  time.Duration
	RefreshCredentials func(context.Context, string, bool) (ProviderResourceCredentials, error)
	MaxRequestRetries  int
}

func (a *XAIGrokSubscriptionAdapter) streamIdleTimeout() time.Duration {
	if a == nil || a.StreamIdleTimeout <= 0 {
		return xaiGrokStreamIdleTimeout
	}
	return a.StreamIdleTimeout
}

func (a XAIGrokSubscriptionAdapter) Responses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest) (any, Usage, error) {
	return a.ResponsesWithHeaders(ctx, provider, providerModel, request, nil)
}

func (a XAIGrokSubscriptionAdapter) ResponsesWithHeaders(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest, incoming http.Header) (any, Usage, error) {
	resp, err := a.OpenResponses(ctx, provider, providerModel, request, incoming)
	if err != nil {
		return nil, Usage{}, err
	}
	defer resp.Body.Close()
	response, outputText, usage, err := consumeCodexResponsesStream(resp.Body, nil)
	if err != nil {
		return nil, usage, err
	}
	if outputText != "" {
		response["output_text"] = outputText
		if output, _ := response["output"].([]any); len(output) == 0 {
			response["output"] = []map[string]any{{
				"type":    "message",
				"role":    "assistant",
				"status":  "completed",
				"content": []map[string]any{{"type": "output_text", "text": outputText, "annotations": []any{}}},
			}}
		}
	}
	return response, usage, nil
}

func (a XAIGrokSubscriptionAdapter) OpenResponses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest, incoming http.Header) (*http.Response, error) {
	resourceID := strings.TrimSpace(provider.Options["resource_id"])
	if resourceID == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_resource_missing", "Super Grok subscription resource is missing")
	}
	if a.RefreshCredentials == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "provider_credentials_unavailable", "Super Grok credentials are unavailable")
	}
	creds, err := a.RefreshCredentials(ctx, resourceID, false)
	if err != nil {
		return nil, err
	}
	endpoint, err := xaiGrokResponsesEndpoint(provider)
	if err != nil {
		return nil, err
	}
	resp, err := a.openResponsesWithRetry(ctx, endpoint, creds, providerModel, request, incoming)
	if err == nil || providerErrorDisposition(err) != ProviderErrorAuthBroken {
		return resp, err
	}
	creds, refreshErr := a.RefreshCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return nil, refreshErr
	}
	return a.openResponsesWithRetry(ctx, endpoint, creds, providerModel, request, incoming)
}

func (a XAIGrokSubscriptionAdapter) openResponsesWithRetry(ctx context.Context, endpoint string, creds ProviderResourceCredentials, providerModel string, request ResponsesRequest, incoming http.Header) (*http.Response, error) {
	attempts := a.MaxRequestRetries
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for range attempts {
		resp, err := a.openResponsesWithCredentials(ctx, endpoint, creds, providerModel, request, incoming)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if providerErrorDisposition(err) != ProviderErrorTransientSame {
			return nil, err
		}
	}
	return nil, lastErr
}

func (a XAIGrokSubscriptionAdapter) openResponsesWithCredentials(ctx context.Context, endpoint string, creds ProviderResourceCredentials, providerModel string, request ResponsesRequest, incoming http.Header) (*http.Response, error) {
	accessToken := strings.TrimSpace(creds.AccessToken)
	if accessToken == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "xai_account_token_missing", "Super Grok access token is missing")
	}
	request.Model = strings.TrimSpace(providerModel)
	if inputText, ok := request.Input.(string); ok {
		request.Input = []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": inputText}},
		}}
	}
	request.Stream = true
	if request.Store == nil {
		store := false
		request.Store = &store
	}
	request = sanitizeXAIGrokResponsesRequest(request, incoming)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "xai_grok_request_failed", err.Error())
	}
	applyXAIGrokChatHeaders(req, accessToken, request, incoming, endpoint)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if egressErr := providerEgressFailure(err); egressErr != nil {
			return nil, egressErr
		}
		return nil, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "xai_grok_request_failed", fmt.Sprintf("Super Grok request failed: %v", err)),
			Disposition: ProviderErrorTransientSame,
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		return nil, newProviderHTTPError(resp.StatusCode, resp.Header, data)
	}
	resp.Body = newIdleTimeoutReadCloser(resp.Body, a.streamIdleTimeout(), errXAIGrokStreamIdle)
	return resp, nil
}

func sanitizeXAIGrokResponsesRequest(request ResponsesRequest, incoming http.Header) ResponsesRequest {
	if request.raw != nil {
		request.raw = cloneRawJSON(request.raw, 0)
		for _, key := range []string{"previous_response_id", "stream_options", "prompt_cache_retention", "safety_identifier"} {
			delete(request.raw, key)
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(request.Model)), xaiGrokComposerPrefix) {
		if request.raw == nil {
			request.raw = map[string]json.RawMessage{}
		}
		if strings.TrimSpace(codexRawStringField(request, "prompt_cache_key")) == "" {
			if sessionID := xaiGrokSessionIdentifier(incoming, request); sessionID != "" {
				setRawJSONField(request.raw, "prompt_cache_key", sessionID, true)
			}
		}
	}
	return request
}

func xaiGrokSessionIdentifier(incoming http.Header, request ResponsesRequest) string {
	if incoming != nil {
		for _, key := range []string{"session-id", "session_id", "x-grok-conv-id", "thread-id", "x-client-request-id"} {
			if value := strings.TrimSpace(incoming.Get(key)); value != "" {
				return value
			}
		}
	}
	if value := strings.TrimSpace(codexRawStringField(request, "prompt_cache_key")); value != "" {
		return value
	}
	return NewID("grok-session")
}

func applyXAIGrokChatHeaders(req *http.Request, accessToken string, request ResponsesRequest, incoming http.Header, endpoint string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "Keep-Alive")
	if parsed, err := url.Parse(endpoint); err == nil && xaiIsCLIChatProxyHost(parsed.Hostname()) {
		req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
		req.Header.Set("x-grok-client-version", xaiGrokClientVersion)
		req.Header.Set("User-Agent", xaiGrokUserAgent)
		req.Header.Set("x-grok-client-identifier", "grok-shell")
		req.Header.Set("x-authenticateresponse", "authenticate-response")
	}
	if sessionID := xaiGrokSessionIdentifier(incoming, request); sessionID != "" {
		req.Header.Set("x-grok-conv-id", sessionID)
	}
}

func xaiGrokResponsesEndpoint(provider Provider) (string, error) {
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" {
		return xaiCLIChatProxyResponsesURL, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", NewHTTPError(http.StatusBadRequest, "xai_grok_base_url_invalid", "Super Grok base URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && !isLocalProviderHostname(parsed.Hostname()) {
		return "", NewHTTPError(http.StatusBadRequest, "xai_grok_base_url_invalid", "Super Grok base URL must use HTTPS")
	}
	if err := validateXAIGrokEndpointHost(provider, parsed); err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/responses") {
		parsed.Path += "/responses"
	}
	return parsed.String(), nil
}

func validateXAIGrokEndpointHost(provider Provider, endpoint *url.URL) error {
	hostname := strings.ToLower(strings.TrimSpace(endpoint.Hostname()))
	if xaiIsCLIChatProxyHost(hostname) || isLocalProviderHostname(hostname) {
		return nil
	}
	for _, allowed := range strings.Split(provider.Options["allowed_xai_hosts"], ",") {
		if hostname == strings.ToLower(strings.TrimSpace(allowed)) {
			return nil
		}
	}
	return NewHTTPError(http.StatusBadRequest, "xai_grok_endpoint_host_not_allowed", "Super Grok credentials cannot be sent to this host")
}

func xaiIsCLIChatProxyHost(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	return hostname == "cli-chat-proxy.grok.com"
}

func (a XAIGrokSubscriptionAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{
		Model:           xaiGrokDefaultProbeModel,
		ReasoningEffort: "low",
		Prompt:          "Reply with exactly one short sentence confirming that the Super Grok connection works.",
	}
}

func (a XAIGrokSubscriptionAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
	defaults := a.DefaultProbeRequest()
	request.Model = strings.TrimSpace(request.Model)
	request.ReasoningEffort = strings.ToLower(strings.TrimSpace(request.ReasoningEffort))
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Model == "" {
		request.Model = defaults.Model
	}
	if request.ReasoningEffort == "" {
		request.ReasoningEffort = defaults.ReasoningEffort
	}
	if request.Prompt == "" {
		request.Prompt = defaults.Prompt
	}
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options = mergedStringMap(provider.Options, resource.Options)
	provider.Options["resource_id"] = resource.ID
	reasoningEffort := request.ReasoningEffort
	responsesRequest := ResponsesRequest{
		Model: request.Model,
		Input: []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": request.Prompt}},
		}},
		Reasoning: &ResponsesReasoning{Effort: &reasoningEffort},
	}
	startedAt := time.Now()
	resp, err := a.OpenResponses(ctx, provider, request.Model, responsesRequest, nil)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	defer resp.Body.Close()
	response, outputText, usage, err := consumeCodexResponsesStream(resp.Body, nil)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	return ProviderProbeResult{
		ResourceID:      resource.ID,
		Model:           request.Model,
		ReasoningEffort: request.ReasoningEffort,
		OutputText:      outputText,
		Usage:           usage,
		LatencyMS:       time.Since(startedAt).Milliseconds(),
		Response:        response,
	}, nil
}
