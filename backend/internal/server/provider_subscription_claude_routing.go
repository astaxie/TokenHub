package server

// A parked CLI owns its tool calls even when the next request has no session
// header or weighted routing prefers another account. Narrow only the routes
// already authorized for this request; resume still validates credentials,
// conversation history, tool definitions, and controls before consuming results.
func (s *Server) bindClaudeSubscriptionContinuation(routed RoutedCall, ids []string) (RoutedCall, error) {
	adapter, ok := resolveTypedAdapter[*ClaudeSubscriptionAdapter](s.adapterRegistry, providerClaudeSubscription)
	if !ok || len(ids) == 0 {
		return routed, nil
	}
	adapter.mu.Lock()
	var run *claudeSubscriptionRun
	for _, id := range ids {
		if pending := adapter.calls[id]; pending != nil {
			run = pending
			break
		}
	}
	adapter.mu.Unlock()
	if run == nil {
		return routed, nil
	}
	invalid := NewHTTPError(400, "claude_subscription_continuation_invalid", "Claude tool continuation is expired or does not match this account, caller, or conversation")
	run.mu.Lock()
	defer run.mu.Unlock()
	if routed.Call.Project.ID == "" || routed.Call.Key.ID == "" || run.caller != routed.Call.Project.ID+"\x00"+routed.Call.Key.ID || run.closed {
		return routed, invalid
	}
	for _, id := range ids {
		if run.waiting[id] == nil {
			return routed, invalid
		}
	}
	bound := routed
	bound.Routes = nil
	for _, route := range routed.Routes {
		if route.Provider.Type == providerClaudeSubscription && route.Provider.ID == run.providerID && routeResourceID(route) == run.resourceID && route.ProviderModel == run.model {
			bound.Routes = append(bound.Routes, route)
		}
	}
	if len(bound.Routes) == 0 {
		return bound, invalid
	}
	return bound, nil
}

func claudeSubscriptionChatResultIDs(request ChatCompletionRequest) []string {
	var ids []string
	for _, result := range claudeSubscriptionResults(request.Messages) {
		ids = append(ids, result.ToolCallID)
	}
	return ids
}

func claudeSubscriptionAnthropicResultIDs(request anthropicMessagesRequest) []string {
	if len(request.Messages) == 0 {
		return nil
	}
	message, _ := request.Messages[len(request.Messages)-1].(map[string]any)
	if message["role"] != "user" {
		return nil
	}
	content, _ := anySlice(message["content"])
	var ids []string
	for _, value := range content {
		block, _ := value.(map[string]any)
		if block["type"] == "tool_result" {
			id, _ := block["tool_use_id"].(string)
			ids = append(ids, id)
		}
	}
	return ids
}
