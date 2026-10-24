package server

import (
	"context"
	"strings"
	"time"
)

func (a *CopilotSubscriptionAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{Prompt: "Reply with OK."}
}

func (a *CopilotSubscriptionAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
	started := time.Now()
	provider.Options = cloneStringMap(provider.Options)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options["resource_id"] = resource.ID
	catalog, _, err := a.ResourceModels(ctx, provider, resource, "")
	if err != nil {
		return ProviderProbeResult{}, err
	}
	model := strings.TrimSpace(request.Model)
	if model == "" && len(catalog.Models) > 0 {
		model = catalog.Models[0].ID
	}
	if model == "" {
		return ProviderProbeResult{}, NewHTTPError(400, "subscription_models_empty", "Copilot account returned no available models")
	}
	prompt := firstNonEmpty(strings.TrimSpace(request.Prompt), a.DefaultProbeRequest().Prompt)
	useResponses := false
	for _, candidate := range catalog.Models {
		if candidate.ID == model {
			endpoints := candidate.Metadata["supported_endpoints"]
			useResponses = !strings.Contains(endpoints, "/chat/completions") && strings.Contains(endpoints, "/responses")
			break
		}
	}
	var output any
	var usage Usage
	if useResponses {
		output, usage, err = a.Responses(ctx, provider, model, ResponsesRequest{Input: prompt})
	} else {
		output, usage, err = a.Chat(ctx, provider, model, ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: prompt}}})
	}
	if err != nil {
		return ProviderProbeResult{}, err
	}
	response, _ := output.(map[string]any)
	text := ""
	if useResponses {
		text = codexResponseOutputText(response)
	} else if choices, ok := response["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if message, ok := first["message"].(map[string]any); ok {
				text, _ = message["content"].(string)
			}
		}
	}
	return ProviderProbeResult{ResourceID: resource.ID, Model: model, OutputText: text, Usage: usage, LatencyMS: time.Since(started).Milliseconds(), Response: response}, nil
}
