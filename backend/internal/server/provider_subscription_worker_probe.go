package server

import (
	"context"
	"strings"
	"time"
)

func (a *SubscriptionWorkerAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{Prompt: "Reply with OK."}
}
func (a *SubscriptionWorkerAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
	started := time.Now()
	provider.Options = cloneStringMap(provider.Options)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options["resource_id"] = resource.ID
	model := strings.TrimSpace(request.Model)
	if model == "" {
		catalog, _, err := a.ResourceModels(ctx, provider, resource, "")
		if err != nil {
			return ProviderProbeResult{}, err
		}
		if len(catalog.Models) == 0 {
			return ProviderProbeResult{}, NewHTTPError(400, "subscription_models_empty", "Subscription account returned no models")
		}
		model = catalog.Models[0].ID
	}
	response, usage, err := a.Chat(ctx, provider, model, ChatCompletionRequest{Model: model, Messages: []ChatMessage{{Role: "user", Content: firstNonEmpty(request.Prompt, a.DefaultProbeRequest().Prompt)}}})
	if err != nil {
		return ProviderProbeResult{}, err
	}
	body, _ := response.(map[string]any)
	text := ""
	if choices, ok := body["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				text, _ = message["content"].(string)
			}
		}
	}
	return ProviderProbeResult{ResourceID: resource.ID, Model: model, OutputText: text, Usage: usage, LatencyMS: time.Since(started).Milliseconds(), Response: body}, nil
}
