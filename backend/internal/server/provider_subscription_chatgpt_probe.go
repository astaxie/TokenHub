package server

import (
	"context"
	"strings"
	"time"
)

func (a *ChatGPTSubscriptionAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{Prompt: "Reply with OK."}
}

func (a *ChatGPTSubscriptionAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
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
		if len(catalog.Models) > 0 {
			model = catalog.Models[0].ID
		}
	}
	if model == "" {
		return ProviderProbeResult{}, NewHTTPError(400, "subscription_models_empty", "ChatGPT account returned no available models")
	}
	output, usage, err := a.Responses(ctx, provider, model, ResponsesRequest{Input: firstNonEmpty(strings.TrimSpace(request.Prompt), a.DefaultProbeRequest().Prompt)})
	if err != nil {
		return ProviderProbeResult{}, err
	}
	response, _ := output.(map[string]any)
	return ProviderProbeResult{ResourceID: resource.ID, Model: model, OutputText: codexResponseOutputText(response), Usage: usage, LatencyMS: time.Since(started).Milliseconds(), Response: response}, nil
}
