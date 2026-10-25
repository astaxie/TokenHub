package server

import (
	"context"
	"io"
	"strings"
	"time"
)

type copilotSubscriptionModelRoutes struct {
	expiresAt time.Time
	endpoints map[string]string
}

func (a *CopilotSubscriptionAdapter) cacheModelRoutes(resourceID string, models []ProviderCatalogModel) {
	routes := copilotSubscriptionModelRoutes{expiresAt: time.Now().Add(5 * time.Minute), endpoints: make(map[string]string, len(models))}
	for _, model := range models {
		supported := strings.Split(model.Metadata["supported_endpoints"], ",")
		selected := ""
		for _, endpoint := range supported {
			if endpoint == "/chat/completions" {
				selected = endpoint
				break
			}
			if endpoint == "/responses" {
				selected = endpoint
			} else if endpoint == "/v1/messages" && selected == "" {
				selected = endpoint
			}
		}
		routes.endpoints[model.ID] = firstNonEmpty(selected, "/chat/completions")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.modelRoutes == nil || len(a.modelRoutes) >= 256 {
		a.modelRoutes = make(map[string]copilotSubscriptionModelRoutes)
	}
	a.modelRoutes[resourceID] = routes
}

func (a *CopilotSubscriptionAdapter) modelEndpoint(ctx context.Context, provider Provider, model string) (string, error) {
	if selected, ok := ctx.Value(copilotSubscriptionAutoKey{}).(copilotSubscriptionAutoSession); ok && selected.Model == model {
		return selected.Endpoint, nil
	}
	resourceID := provider.Options["resource_id"]
	a.mu.Lock()
	cached, ok := a.modelRoutes[resourceID]
	a.mu.Unlock()
	if !ok || !cached.expiresAt.After(time.Now()) {
		if _, _, err := a.ResourceModels(ctx, provider, ProviderResource{ID: resourceID}, ""); err != nil {
			return "", err
		}
		a.mu.Lock()
		cached = a.modelRoutes[resourceID]
		a.mu.Unlock()
	}
	return firstNonEmpty(cached.endpoints[model], "/chat/completions"), nil
}

func (a *CopilotSubscriptionAdapter) chatResponses(ctx context.Context, provider Provider, model string, request ChatCompletionRequest) (any, Usage, error) {
	upstream, err := chatToSubscriptionResponsesRequest(request)
	if err != nil {
		return nil, Usage{}, err
	}
	output, usage, err := a.Responses(ctx, provider, model, upstream)
	if err != nil {
		return nil, usage, err
	}
	body, _ := output.(map[string]any)
	converted, err := codexResponsesToChat(body, request, usage)
	return converted, usage, err
}

func (a *CopilotSubscriptionAdapter) streamChatResponses(ctx context.Context, provider Provider, model string, request ChatCompletionRequest, w io.Writer) (Usage, error) {
	upstream, err := chatToSubscriptionResponsesRequest(request)
	if err != nil {
		return Usage{}, err
	}
	reverseNames, err := chatCodexToolNameReverse(request.Tools)
	if err != nil {
		return Usage{}, err
	}
	opened, err := a.OpenResponses(ctx, provider, model, upstream, nil)
	if err != nil {
		return subscriptionJSONFailureUsage(err), err
	}
	defer opened.Body.Close()
	sink := &codexChatStreamSink{encoder: newOpenAIChatStreamEncoder(w, request.Model, streamUsageRequested(request)), reverseNames: reverseNames, toolSlots: map[string]int{}}
	return consumeCodexCompatibilityStream(opened.Body, sink)
}

func (a *CopilotSubscriptionAdapter) chatAnthropic(ctx context.Context, provider Provider, model string, request ChatCompletionRequest) (any, Usage, error) {
	body, err := buildAnthropicRequest(model, request, request.ReasoningEffort)
	if err != nil {
		return nil, Usage{}, err
	}
	opened, err := a.request(ctx, provider, "/v1/messages", body, false)
	if err != nil {
		return nil, subscriptionJSONFailureUsage(err), err
	}
	var response map[string]any
	if err := subscriptionDecode(opened, &response); err != nil {
		return nil, Usage{}, err
	}
	usage := anthropicUsage(response)
	converted, err := anthropicChatResponse(response, request.Model, usage)
	return converted, usage, err
}

func (a *CopilotSubscriptionAdapter) streamChatAnthropic(ctx context.Context, provider Provider, model string, request ChatCompletionRequest, w io.Writer) (Usage, error) {
	body, err := buildAnthropicRequest(model, request, request.ReasoningEffort)
	if err != nil {
		return Usage{}, err
	}
	body["stream"] = true
	opened, err := a.request(ctx, provider, "/v1/messages", body, true)
	if err != nil {
		return subscriptionJSONFailureUsage(err), err
	}
	defer opened.Body.Close()
	return streamAnthropicChatForProvider(opened.Body, newOpenAIChatStreamEncoder(w, request.Model, streamUsageRequested(request)), provider)
}
