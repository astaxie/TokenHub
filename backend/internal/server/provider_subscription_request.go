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
	// Preserve the gateway's existing Chat output-limit semantics.
	if limit := request.requestedMaxTokens(); limit > 0 {
		upstream.MaxTokens = int(limit)
	}
	return upstream, nil
}
