package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	ProviderGitHubCopilotSubscription         = "github-copilot-subscription"
	ProviderResourceGitHubCopilotSubscription = "github_copilot_subscription"
	copilotSubscriptionBaseURL                = "https://api.githubcopilot.com"
	copilotSubscriptionTokenURL               = "https://api.github.com/copilot_internal/v2/token"
)

// CopilotSubscriptionAdapter keeps GitHub credentials in the existing encrypted
// resource store. Short-lived Copilot sessions are cached only in memory.
type CopilotSubscriptionAdapter struct {
	Client                  *http.Client
	StreamClient            *http.Client
	CredentialRefreshClient *http.Client
	StreamIdleTimeout       time.Duration
	RefreshCredentials      func(context.Context, string, bool) (ProviderResourceCredentials, error)
	// Endpoint overrides are host-injected, never read from provider options.
	APIBaseURL     string
	TokenURL       string
	CLIUserURL     string
	DeviceURL      string
	DeviceTokenURL string
	mu             sync.Mutex
	sessions       map[[32]byte]copilotSubscriptionSession
	modelRoutes    map[string]copilotSubscriptionModelRoutes
	inflight       map[[32]byte]chan struct{}
}

type copilotSubscriptionSession struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Endpoints struct {
		API string `json:"api"`
	} `json:"endpoints"`
	CLI bool `json:"-"`
}

func copilotSubscriptionHost(host string) bool {
	return host == "api.githubcopilot.com" || strings.HasSuffix(host, ".githubcopilot.com")
}

func (a *CopilotSubscriptionAdapter) session(ctx context.Context, creds ProviderResourceCredentials, force bool) (copilotSubscriptionSession, error) {
	key := sha256.Sum256([]byte(creds.TokenType + "\x00" + creds.AccessToken))
	for {
		a.mu.Lock()
		if !force {
			if cached, ok := a.sessions[key]; ok && time.Until(time.Unix(cached.ExpiresAt, 0)) > 2*time.Minute {
				a.mu.Unlock()
				return cached, nil
			}
		}
		if waiting := a.inflight[key]; waiting != nil {
			a.mu.Unlock()
			select {
			case <-ctx.Done():
				return copilotSubscriptionSession{}, ctx.Err()
			case <-waiting:
			}
			force = false
			continue
		}
		if a.inflight == nil {
			a.inflight = make(map[[32]byte]chan struct{})
		}
		ready := make(chan struct{})
		a.inflight[key] = ready
		a.mu.Unlock()
		session, err := a.exchangeSession(ctx, creds)
		a.mu.Lock()
		if err == nil {
			if a.sessions == nil {
				a.sessions = make(map[[32]byte]copilotSubscriptionSession)
			}
			for k, old := range a.sessions {
				if old.ExpiresAt <= time.Now().Unix() {
					delete(a.sessions, k)
				}
			}
			// The cache is an optimization; never retain unbounded account secrets.
			if len(a.sessions) >= 256 {
				clear(a.sessions)
			}
			a.sessions[key] = session
		} else {
			delete(a.sessions, key)
		}
		delete(a.inflight, key)
		close(ready)
		a.mu.Unlock()
		return session, err
	}
}

func (a *CopilotSubscriptionAdapter) exchangeSession(ctx context.Context, creds ProviderResourceCredentials) (copilotSubscriptionSession, error) {
	if strings.TrimSpace(creds.AccessToken) == "" {
		return copilotSubscriptionSession{}, NewHTTPError(400, "subscription_token_missing", "GitHub access token is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cli := creds.TokenType == "copilot_cli"
	endpoint := firstNonEmpty(a.TokenURL, copilotSubscriptionTokenURL)
	if cli {
		endpoint = firstNonEmpty(a.CLIUserURL, "https://api.github.com/copilot_internal/user")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return copilotSubscriptionSession{}, err
	}
	req.Header.Set("Authorization", "token "+creds.AccessToken)
	req.Header.Set("Accept", "application/json")
	applyCopilotSubscriptionHeaders(req.Header, cli)
	resp, err := subscriptionHTTP(ctx, a.CredentialRefreshClient, req)
	if err != nil {
		return copilotSubscriptionSession{}, err
	}
	var session copilotSubscriptionSession
	if err := subscriptionDecode(resp, &session); err != nil {
		return session, err
	}
	if cli {
		session.CLI, session.Token, session.ExpiresAt = true, creds.AccessToken, time.Now().Add(10*time.Minute).Unix()
	}
	if strings.TrimSpace(session.Token) == "" || session.ExpiresAt <= time.Now().Unix() {
		return session, NewHTTPError(502, "copilot_session_invalid", "GitHub did not return a usable Copilot session")
	}
	if _, err := subscriptionEndpoint(session.Endpoints.API, copilotSubscriptionBaseURL, "", copilotSubscriptionHost); err != nil {
		return session, err
	}
	return session, nil
}

func applyCopilotSubscriptionHeaders(headers http.Header, cli bool) {
	headers.Set("Editor-Version", "vscode/1.140.0")
	headers.Set("Editor-Plugin-Version", "copilot-chat/0.68.0")
	headers.Set("Copilot-Integration-Id", "vscode-chat")
	headers.Set("User-Agent", "GitHubCopilotChat/0.68.0")
	headers.Set("X-GitHub-Api-Version", "2026-01-09")
	if cli {
		headers.Del("Editor-Plugin-Version")
		headers.Set("Editor-Version", "copilot/1.0.88")
		headers.Set("Copilot-Integration-Id", "copilot-developer-cli")
		headers.Set("User-Agent", "copilot/1.0.88")
		headers.Set("X-GitHub-Api-Version", "2026-07-01")
	}
}

func (a *CopilotSubscriptionAdapter) request(ctx context.Context, provider Provider, path string, payload any, stream bool) (*http.Response, error) {
	if _, err := subscriptionEndpoint(provider.BaseURL, copilotSubscriptionBaseURL, "", copilotSubscriptionHost); err != nil {
		return nil, err
	}
	creds, err := subscriptionCredentials(ctx, provider, a.RefreshCredentials, false)
	if err != nil {
		return nil, err
	}
	var data []byte
	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, NewHTTPError(400, "invalid_request", "Subscription request is invalid")
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		session, err := a.session(ctx, creds, attempt > 0)
		if err != nil {
			return nil, err
		}
		base := firstNonEmpty(session.Endpoints.API, provider.BaseURL, copilotSubscriptionBaseURL)
		endpoint, err := subscriptionEndpoint(base, copilotSubscriptionBaseURL, path, copilotSubscriptionHost)
		if err != nil {
			return nil, err
		}
		if a.APIBaseURL != "" {
			endpoint = strings.TrimRight(a.APIBaseURL, "/") + path
		}
		method := http.MethodPost
		if payload == nil {
			method = http.MethodGet
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+session.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		applyCopilotSubscriptionHeaders(req.Header, session.CLI)
		req.Header.Set("Openai-Intent", "conversation-panel")
		if path == "/v1/messages" {
			req.Header.Set("Anthropic-Version", "2023-06-01")
		}
		req.Header.Set("X-Initiator", copilotSubscriptionInitiator(data))
		if bytes.Contains(data, []byte(`"image_url"`)) || bytes.Contains(data, []byte(`"input_image"`)) {
			req.Header.Set("Copilot-Vision-Request", "true")
		}
		client := a.Client
		if stream {
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
		if stream {
			resp.Body = newIdleTimeoutReadCloser(resp.Body, subscriptionIdleTimeout(a.StreamIdleTimeout), NewHTTPError(504, "subscription_stream_idle", "Subscription stream was idle for too long"))
		}
		return resp, nil
	}
	return nil, NewHTTPError(401, "copilot_session_refused", "Copilot session was refused")
}

func copilotSubscriptionInitiator(data []byte) string {
	var body struct {
		Messages []ChatMessage   `json:"messages"`
		Input    json.RawMessage `json:"input"`
	}
	if json.Unmarshal(data, &body) != nil {
		return "agent"
	}
	if len(body.Messages) > 0 && body.Messages[len(body.Messages)-1].Role == "user" {
		return "user"
	}
	var text string
	if json.Unmarshal(body.Input, &text) == nil && text != "" {
		return "user"
	}
	var items []struct {
		Role string `json:"role"`
		Type string `json:"type"`
	}
	if json.Unmarshal(body.Input, &items) == nil && len(items) > 0 && items[len(items)-1].Role == "user" {
		return "user"
	}
	return "agent"
}

func subscriptionIdleTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 5 * time.Minute
	}
	return timeout
}

func (a *CopilotSubscriptionAdapter) Chat(ctx context.Context, provider Provider, model string, request ChatCompletionRequest) (any, Usage, error) {
	endpoint, err := a.modelEndpoint(ctx, provider, model)
	if err != nil {
		return nil, Usage{}, err
	}
	if endpoint == "/responses" {
		return a.chatResponses(ctx, provider, model, request)
	}
	if endpoint == "/v1/messages" {
		return a.chatAnthropic(ctx, provider, model, request)
	}
	request = withoutGatewayExtensions(request, true)
	request.Model, request.Stream = model, false
	resp, err := a.request(ctx, provider, "/chat/completions", request, false)
	if err != nil {
		return nil, Usage{}, err
	}
	var body map[string]any
	if err := subscriptionDecode(resp, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (a *CopilotSubscriptionAdapter) ChatStream(ctx context.Context, provider Provider, model string, request ChatCompletionRequest, w io.Writer) (Usage, error) {
	endpoint, err := a.modelEndpoint(ctx, provider, model)
	if err != nil {
		return Usage{}, err
	}
	if endpoint == "/responses" {
		return a.streamChatResponses(ctx, provider, model, request, w)
	}
	if endpoint == "/v1/messages" {
		return a.streamChatAnthropic(ctx, provider, model, request, w)
	}
	request = includeOpenAIStreamUsage(withoutGatewayExtensions(request, true))
	request.Model, request.Stream = model, true
	resp, err := a.request(ctx, provider, "/chat/completions", request, true)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	return copyOpenAIStreamAndUsageWithLimit(w, resp.Body, provider, mediaChatStreamLimit(ctx))
}

func (a *CopilotSubscriptionAdapter) Responses(ctx context.Context, provider Provider, model string, request ResponsesRequest) (any, Usage, error) {
	request.Model, request.Stream = model, false
	resp, err := a.request(ctx, provider, "/responses", normalizedResponsesReasoning(request), false)
	if err != nil {
		return nil, Usage{}, err
	}
	var body map[string]any
	if err := subscriptionDecode(resp, &body); err != nil {
		return nil, Usage{}, err
	}
	return body, usageFromMap(body), nil
}

func (a *CopilotSubscriptionAdapter) OpenResponses(ctx context.Context, provider Provider, model string, request ResponsesRequest, _ http.Header) (*http.Response, error) {
	request.Model, request.Stream = model, true
	return a.request(ctx, provider, "/responses", normalizedResponsesReasoning(request), true)
}

func (a *CopilotSubscriptionAdapter) Embeddings(context.Context, Provider, string, EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(400, "adapter_capability_unsupported", "Copilot subscriptions do not support embeddings")
}

func (a *CopilotSubscriptionAdapter) ResourceModels(ctx context.Context, provider Provider, resource ProviderResource, _ string) (ProviderCatalogEntry, int, error) {
	provider.Options = cloneStringMap(provider.Options)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options["resource_id"] = resource.ID
	resp, err := a.request(ctx, provider, "/models", nil, false)
	if err != nil {
		return ProviderCatalogEntry{}, 0, err
	}
	var payload struct {
		Data []struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			Endpoints    []string `json:"supported_endpoints"`
			Capabilities struct {
				Type   string `json:"type"`
				Limits struct {
					Context int64 `json:"max_context_window_tokens"`
					Output  int64 `json:"max_output_tokens"`
				} `json:"limits"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := subscriptionDecode(resp, &payload); err != nil {
		return ProviderCatalogEntry{}, resp.StatusCode, err
	}
	models := make([]ProviderCatalogModel, 0, len(payload.Data))
	for _, remote := range payload.Data {
		if remote.ID == "" || remote.Capabilities.Type != "" && remote.Capabilities.Type != "chat" {
			continue
		}
		endpoints := remote.Endpoints
		if len(endpoints) == 0 {
			endpoints = []string{"/chat/completions"}
		}
		models = append(models, ProviderCatalogModel{ID: remote.ID, Name: remote.ID, DisplayName: firstNonEmpty(remote.Name, remote.ID), Type: "chat", ContextWindow: remote.Capabilities.Limits.Context,
			Capabilities: []string{"chat", "streaming", "tools"}, Metadata: map[string]string{"supported_endpoints": strings.Join(endpoints, ","), "source": "github-copilot-live"}})
	}
	a.cacheModelRoutes(resource.ID, models)
	return subscriptionCatalog(ProviderGitHubCopilotSubscription, "GitHub Copilot Subscription", copilotSubscriptionBaseURL, "github-copilot-live", "", models), resp.StatusCode, nil
}
