package server

func chatToBridgedResponsesRequest(providerType string, request ChatCompletionRequest) (ResponsesRequest, error) {
	upstream, err := chatToCodexResponsesRequest(request)
	if providerType == ProviderChatGPTSubscription && normalizedReasoningEffort(request.ReasoningEffort) == nil {
		upstream = withoutResponsesReasoningEffort(upstream)
	}
	return upstream, err
}

func anthropicToBridgedResponsesRequest(providerType string, request anthropicMessagesRequest) (ResponsesRequest, error) {
	upstream, err := anthropicToCodexResponsesRequest(request)
	if providerType != ProviderChatGPTSubscription || err != nil {
		return upstream, err
	}
	// Explicit thinking controls retain the existing budget/effort mapping.
	// Only the Codex-specific default differs for a direct ChatGPT account.
	for _, value := range []any{request.Raw["effort"], nestedAny(request.Raw, "output_config", "effort")} {
		if codexReasoningEffort(anyString(value)) != "" {
			return upstream, nil
		}
	}
	switch anyString(nestedAny(request.Raw, "thinking", "type")) {
	case "disabled", "adaptive", "auto", "enabled":
		return upstream, nil
	}
	return withoutResponsesReasoningEffort(upstream), nil
}

func chatToSubscriptionResponsesRequest(request ChatCompletionRequest) (ResponsesRequest, error) {
	upstream, err := chatToCodexResponsesRequest(request)
	if err != nil {
		return ResponsesRequest{}, err
	}
	// Codex supplies its own medium default, but other Responses providers may
	// not accept that effort. Keep the selected model's default when omitted.
	if normalizedReasoningEffort(request.ReasoningEffort) == nil {
		upstream = withoutResponsesReasoningEffort(upstream)
	}
	// Subscription Responses endpoints accept sampling and user parameters
	// that the Codex backend omits. Preserve their Chat equivalents.
	upstream.Temperature = request.Temperature
	setRawJSONField(upstream.raw, "top_p", request.TopP, request.TopP != nil)
	setRawJSONField(upstream.raw, "user", request.User, request.User != nil)
	// Native Responses adapters use the service tier to select speed and
	// billing behavior, including Factory's explicit Fast Mode model.
	if tier, ok := request.raw["service_tier"]; ok {
		upstream.raw["service_tier"] = tier
	}
	// Preserve the gateway's existing Chat output-limit semantics.
	if limit := request.requestedMaxTokens(); limit > 0 {
		upstream.MaxTokens = int(limit)
	}
	return upstream, nil
}
