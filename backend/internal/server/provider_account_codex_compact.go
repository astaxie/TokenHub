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

func (a CodexSubscriptionAdapter) CompactWithHeaders(ctx context.Context, provider Provider, providerModel string, body map[string]json.RawMessage, incoming http.Header) (any, Usage, error) {
	resourceID := strings.TrimSpace(provider.Options["resource_id"])
	if resourceID == "" {
		return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "provider_resource_missing", "Codex Subscription resource is missing")
	}
	if a.RefreshCredentials == nil {
		return nil, Usage{}, NewHTTPError(http.StatusServiceUnavailable, "provider_credentials_unavailable", "Codex Subscription credentials are unavailable")
	}
	endpoint, err := codexCompactEndpoint(provider)
	if err != nil {
		return nil, Usage{}, err
	}
	encodedModel, _ := json.Marshal(strings.TrimSpace(providerModel))
	body["model"] = encodedModel
	clientSessionID, _ := codexSessionIdentifier(incoming, ResponsesRequest{raw: body})
	fingerprintIDs := resolveCodexFingerprintIDs(provider, clientSessionID)
	// client_metadata is a TokenHub affinity hint. The Codex compact endpoint
	// rejects it as an unknown upstream parameter, so consume it locally only.
	delete(body, "client_metadata")
	applyCodexCompactEnvelope(body, incoming)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	credentials, err := a.RefreshCredentials(ctx, resourceID, false)
	if err != nil {
		return nil, Usage{}, err
	}
	response, usage, err := a.compactWithRetry(ctx, endpoint, credentials, payload, incoming, fingerprintIDs)
	if err == nil || providerErrorDisposition(err) != ProviderErrorAuthBroken {
		return response, usage, err
	}
	credentials, refreshErr := a.RefreshCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return nil, Usage{}, refreshErr
	}
	return a.compactWithRetry(ctx, endpoint, credentials, payload, incoming, fingerprintIDs)
}

func (a CodexSubscriptionAdapter) compactWithRetry(ctx context.Context, endpoint string, credentials ProviderResourceCredentials, payload []byte, incoming http.Header, fingerprintIDs *codexFingerprintIDs) (any, Usage, error) {
	maxRetries := a.MaxRequestRetries
	if maxRetries <= 0 {
		maxRetries = openAICodexMaxRequestRetries
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		response, usage, err := a.compactOnce(ctx, endpoint, credentials, payload, incoming, fingerprintIDs)
		if err == nil {
			return response, usage, nil
		}
		lastErr = err
		if providerErrorDisposition(err) != ProviderErrorTransientSame || attempt == maxRetries {
			return nil, usage, err
		}
		delay := providerErrorRetryAfter(err)
		if delay <= 0 {
			delay = time.Duration(100*(1<<attempt)) * time.Millisecond
		}
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, usage, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, Usage{}, lastErr
}

func (a CodexSubscriptionAdapter) compactOnce(ctx context.Context, endpoint string, credentials ProviderResourceCredentials, payload []byte, incoming http.Header, fingerprintIDs *codexFingerprintIDs) (any, Usage, error) {
	accessToken := strings.TrimSpace(credentials.AccessToken)
	accountID := strings.TrimSpace(credentials.AccountID)
	if accessToken == "" {
		return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "openai_account_token_missing", "OpenAI account access token is missing")
	}
	if accountID == "" {
		return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "openai_account_id_missing", "OpenAI ChatGPT account ID is missing")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "codex_compact_request_failed", err.Error())
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("ChatGPT-Account-ID", accountID)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	applyCodexRequestHeaders(request.Header, incoming)
	applyCodexFingerprintHeaders(request.Header, fingerprintIDs)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, Usage{}, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "codex_compact_request_failed", fmt.Sprintf("Codex compact request failed: %v", err)),
			Disposition: ProviderErrorTransientSame,
		}
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 256<<10))
		return nil, Usage{}, classifyCodexHTTPError(response.StatusCode, response.Header, data)
	}
	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&result); err != nil {
		return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "codex_compact_invalid_response", "Codex compact endpoint returned an invalid response")
	}
	usage := usageFromMap(result)
	applyCodexResponseMetadata(&usage, response.Header)
	usage.Transport = "http"
	return result, usage, nil
}

func codexCompactEndpoint(provider Provider) (string, error) {
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" {
		return openAICodexBaseURL + "/responses/compact", nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", NewHTTPError(http.StatusBadRequest, "codex_base_url_invalid", "Codex base URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && !strings.EqualFold(parsed.Hostname(), "localhost") {
		return "", NewHTTPError(http.StatusBadRequest, "codex_base_url_invalid", "Codex base URL must use HTTPS")
	}
	if err := validateCodexEndpointHost(provider, parsed); err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/responses/compact"):
	case strings.HasSuffix(path, "/responses"):
		path += "/compact"
	default:
		path += "/responses/compact"
	}
	parsed.Path = path
	return parsed.String(), nil
}
