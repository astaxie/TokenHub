package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

type copilotSubscriptionAutoKey struct{}

var copilotInternalModel = regexp.MustCompile(`^(copilot-search|exec-agent|trajectory)|-(secondary|tertiary|4th|free-auto)$`)

type copilotSubscriptionAutoSession struct {
	Token, Model, Endpoint string
	ExpiresAt              time.Time
	SupportsResponses      bool
}

func copilotSubscriptionPrompt(input any) string {
	if raw, ok := input.(json.RawMessage); ok {
		var decoded any
		if json.Unmarshal(raw, &decoded) == nil {
			return copilotSubscriptionPrompt(decoded)
		}
	}
	if messages, ok := input.([]ChatMessage); ok {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				return copilotSubscriptionPrompt(messages[i].Content)
			}
		}
	}
	if messages, ok := input.([]any); ok {
		for i := len(messages) - 1; i >= 0; i-- {
			if message, ok := messages[i].(map[string]any); ok && message["role"] == "user" {
				return copilotSubscriptionPrompt(message["content"])
			}
		}
	}
	if text := openAIMessageText(input); strings.TrimSpace(text) != "" {
		if len(text) > 16000 {
			text = text[:16000]
		}
		return text
	}
	return "hi"
}

func (a *CopilotSubscriptionAdapter) resolveAuto(ctx context.Context, provider Provider, model, prompt string, refused map[string]bool) (context.Context, string, error) {
	if model != "auto" {
		return ctx, model, nil
	}
	credentials, err := subscriptionCredentials(ctx, provider, a.RefreshCredentials, false)
	if err != nil {
		return ctx, model, err
	}
	key := sha256.Sum256([]byte(credentials.AccessToken + "\x00" + credentials.TokenType + "\x00" + credentials.SessionJSON))
	a.mu.Lock()
	selected, cached := a.autoSessions[key]
	a.mu.Unlock()
	if cached && time.Until(selected.ExpiresAt) > 2*time.Minute && !refused[selected.Model] {
		return context.WithValue(ctx, copilotSubscriptionAutoKey{}, selected), selected.Model, nil
	}
	catalog, _, err := a.ResourceModels(ctx, provider, ProviderResource{ID: provider.Options["resource_id"]}, "")
	if err != nil {
		return ctx, model, err
	}
	selected, err = a.selectAuto(ctx, provider, prompt, catalog.Models, refused)
	if err != nil {
		return ctx, model, err
	}
	a.mu.Lock()
	if a.autoSessions == nil || len(a.autoSessions) >= 256 {
		a.autoSessions = make(map[[32]byte]copilotSubscriptionAutoSession)
	}
	a.autoSessions[key] = selected
	a.mu.Unlock()
	return context.WithValue(ctx, copilotSubscriptionAutoKey{}, selected), selected.Model, nil
}

func (a *CopilotSubscriptionAdapter) selectAuto(ctx context.Context, provider Provider, prompt string, models []ProviderCatalogModel, refused map[string]bool) (copilotSubscriptionAutoSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, path := range []string{"/auto", "/models/session"} {
		var payload any = map[string]string{"prompt": prompt, "tier": "balance"}
		if path == "/models/session" {
			payload = map[string]any{"auto_mode": map[string]any{"model_hints": []string{"auto"}}}
		}
		response, err := a.request(ctx, provider, path, payload, false)
		if err != nil {
			if ctx.Err() != nil {
				return copilotSubscriptionAutoSession{}, ctx.Err()
			}
			failure := AsHTTPError(err)
			if failure != nil && (failure.UpstreamStatus == 401 || failure.Status == 401) {
				return copilotSubscriptionAutoSession{}, err
			}
			continue
		}
		var result struct {
			Token     string          `json:"session_token"`
			Selected  json.RawMessage `json:"selected_model"`
			Available []string        `json:"available_models"`
			ExpiresAt int64           `json:"expires_at"`
		}
		if subscriptionDecode(response, &result) != nil || result.Token == "" {
			continue
		}
		var pick struct {
			ID        string   `json:"id"`
			Endpoints []string `json:"supported_endpoints"`
		}
		if json.Unmarshal(result.Selected, &pick.ID) != nil {
			_ = json.Unmarshal(result.Selected, &pick)
		}
		if refused[pick.ID] {
			break
		}
		if pick.ID == "" {
			for _, id := range result.Available {
				if !refused[id] {
					pick.ID = id
					break
				}
			}
		}
		if pick.ID == "" || pick.ID == "auto" {
			continue
		}
		endpoints := pick.Endpoints
		for _, item := range models {
			if item.ID == pick.ID && len(endpoints) == 0 {
				endpoints = strings.Split(item.Metadata["supported_endpoints"], ",")
				break
			}
		}
		expires := time.Now().Add(time.Hour)
		if result.ExpiresAt > 0 && time.Unix(result.ExpiresAt, 0).Before(expires) {
			expires = time.Unix(result.ExpiresAt, 0)
		}
		if !expires.After(time.Now()) {
			continue
		}
		return copilotSubscriptionAutoSession{Token: result.Token, Model: pick.ID, Endpoint: copilotAutoEndpoint(endpoints), ExpiresAt: expires, SupportsResponses: slices.Contains(endpoints, "/responses")}, nil
	}
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].Metadata["auto_fallback_priority"] < models[j].Metadata["auto_fallback_priority"]
	})
	for _, item := range models {
		if item.ID != "auto" && !refused[item.ID] {
			endpoints := strings.Split(item.Metadata["supported_endpoints"], ",")
			return copilotSubscriptionAutoSession{Model: item.ID, Endpoint: copilotAutoEndpoint(endpoints), ExpiresAt: time.Now().Add(10 * time.Minute), SupportsResponses: slices.Contains(endpoints, "/responses")}, nil
		}
	}
	return copilotSubscriptionAutoSession{}, NewHTTPError(400, "copilot_auto_unavailable", "Copilot did not offer an Auto selection or an available model for this account")
}

func copilotAutoEndpoint(endpoints []string) string {
	for _, candidate := range []string{"/chat/completions", "/responses", "/v1/messages"} {
		for _, endpoint := range endpoints {
			if candidate == endpoint {
				return candidate
			}
		}
	}
	return "/chat/completions"
}

func copilotAutoRetry(err error) bool {
	failure := AsHTTPError(err)
	if failure == nil || failure.UpstreamStatus == 200 {
		return false
	}
	status := failure.UpstreamStatus
	if status == 0 {
		status = failure.Status
	}
	return status == 403 || status == 404
}

func (a *CopilotSubscriptionAdapter) Chat(ctx context.Context, provider Provider, model string, request ChatCompletionRequest) (any, Usage, error) {
	refused := map[string]bool{}
	for attempt := 0; ; attempt++ {
		selectedCtx, selected, err := a.resolveAuto(ctx, provider, model, copilotSubscriptionPrompt(request.Messages), refused)
		if err != nil {
			return nil, Usage{}, err
		}
		body, usage, err := a.chatOnce(selectedCtx, provider, selected, request)
		if model == "auto" {
			usage.ServedModel = selected
		}
		if model != "auto" || attempt >= 2 || !copilotAutoRetry(err) {
			return body, usage, err
		}
		refused[selected] = true
	}
}

type copilotAutoWriter struct {
	io.Writer
	written int
}

func (w *copilotAutoWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	w.written += n
	return n, err
}

func (a *CopilotSubscriptionAdapter) ChatStream(ctx context.Context, provider Provider, model string, request ChatCompletionRequest, writer io.Writer) (Usage, error) {
	refused := map[string]bool{}
	out := &copilotAutoWriter{Writer: writer}
	for attempt := 0; ; attempt++ {
		selectedCtx, selected, err := a.resolveAuto(ctx, provider, model, copilotSubscriptionPrompt(request.Messages), refused)
		if err != nil {
			return Usage{}, err
		}
		usage, err := a.chatStreamOnce(selectedCtx, provider, selected, request, out)
		if model == "auto" {
			usage.ServedModel = selected
		}
		if model != "auto" || attempt >= 2 || out.written > 0 || !copilotAutoRetry(err) {
			return usage, err
		}
		refused[selected] = true
	}
}

func (a *CopilotSubscriptionAdapter) Responses(ctx context.Context, provider Provider, model string, request ResponsesRequest) (any, Usage, error) {
	refused := map[string]bool{}
	for attempt := 0; ; attempt++ {
		selectedCtx, selected, err := a.resolveAuto(ctx, provider, model, copilotSubscriptionPrompt(request.Input), refused)
		if err != nil {
			return nil, Usage{}, err
		}
		body, usage, err := a.responsesOnce(selectedCtx, provider, selected, request)
		if model == "auto" {
			usage.ServedModel = selected
		}
		if model != "auto" || attempt >= 2 || !copilotAutoRetry(err) {
			return body, usage, err
		}
		refused[selected] = true
	}
}

func (a *CopilotSubscriptionAdapter) OpenResponses(ctx context.Context, provider Provider, model string, request ResponsesRequest, _ http.Header) (*http.Response, error) {
	refused := map[string]bool{}
	for attempt := 0; ; attempt++ {
		selectedCtx, selected, err := a.resolveAuto(ctx, provider, model, copilotSubscriptionPrompt(request.Input), refused)
		if err != nil {
			return nil, err
		}
		opened, err := a.openResponsesOnce(selectedCtx, provider, selected, request)
		if model != "auto" || attempt >= 2 || !copilotAutoRetry(err) {
			return opened, err
		}
		refused[selected] = true
	}
}
