package server

func chatToSubscriptionResponsesRequest(request ChatCompletionRequest) (ResponsesRequest, error) {
	upstream, err := chatToCodexResponsesRequest(request)
	if err != nil {
		return ResponsesRequest{}, err
	}
	// Subscription Responses endpoints accept output limits that the Codex
	// backend omits. Preserve the gateway's existing Chat limit semantics.
	if limit := request.requestedMaxTokens(); limit > 0 {
		upstream.MaxTokens = int(limit)
	}
	return upstream, nil
}
