package server

func chatToSubscriptionResponsesRequest(request ChatCompletionRequest) (ResponsesRequest, error) {
	upstream, err := chatToCodexResponsesRequest(request)
	if err != nil {
		return ResponsesRequest{}, err
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
