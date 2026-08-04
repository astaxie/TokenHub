package server

import (
	"context"
	"io"
	"net/http"
	"strings"
)

const (
	codexBridgeProtocolAnthropic = "anthropic_messages"
	codexBridgeProtocolChat      = "chat_completions"
)

func (s *Server) executeCodexAnthropicMessages(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	headers http.Header,
) (map[string]any, Usage, error) {
	upstream, err := anthropicToCodexResponsesRequest(req)
	if err != nil {
		return nil, Usage{}, err
	}
	applyClaudeCodeCodexToolConstraints(&upstream, headers)
	resp, usage, err := s.invokeResponsesAdapter(ctx, route, upstream, codexAnthropicCompatibilityHeaders(headers, req.Raw))
	if isCodexModelUnsupportedError(err) {
		s.removeCodexResourceModel(routeResourceID(route), route.ProviderModel)
	}
	if err != nil {
		return nil, usage, err
	}
	body, ok := resp.(map[string]any)
	if !ok {
		return nil, usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Codex returned an invalid Responses payload")
	}
	converted, err := codexResponsesToAnthropic(body, req, usage)
	return converted, usage, err
}

// Claude Code can execute many Bash calls from one assistant turn at once. A
// Codex subscription account usually has a much smaller concurrency allowance,
// so that fan-out immediately exhausts the shared resource and causes every
// child request to retry. Keep API clients' normal Anthropic semantics, but ask
// the Codex model to emit one tool call per turn for the Claude Code CLI.
func applyClaudeCodeCodexToolConstraints(request *ResponsesRequest, headers http.Header) {
	if request == nil || request.raw == nil {
		return
	}
	userAgent := strings.ToLower(strings.TrimSpace(headers.Get("user-agent")))
	if !strings.HasPrefix(userAgent, "claude-cli/") {
		return
	}
	if _, hasTools := request.raw["parallel_tool_calls"]; !hasTools {
		return
	}
	request.raw = cloneRawJSON(request.raw, 0)
	setRawJSONField(request.raw, "parallel_tool_calls", false, true)
}

func (s *Server) executeRoutedChat(
	r *http.Request,
	routed RoutedCall,
	req ChatCompletionRequest,
) (any, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(req.ReasoningEffort) != nil
	return executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool, _ int) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstreamReq := req
		if omitReasoningEffort {
			upstreamReq.ReasoningEffort = nil
		}
		return s.executeChatRoute(ctx, route, upstreamReq, r.Header)
	})
}

func (s *Server) executeChatRoute(
	ctx context.Context,
	route RouteSelection,
	req ChatCompletionRequest,
	headers http.Header,
) (any, Usage, error) {
	if route.Provider.Type != ProviderOpenAICodex {
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		return adapter.Chat(ctx, route.Provider, route.ProviderModel, req)
	}
	upstream, err := chatToCodexResponsesRequest(req)
	if err != nil {
		return nil, Usage{}, err
	}
	resp, usage, err := s.invokeResponsesAdapter(ctx, route, upstream, codexChatCompatibilityHeaders(headers, req))
	if isCodexModelUnsupportedError(err) {
		s.removeCodexResourceModel(routeResourceID(route), route.ProviderModel)
	}
	if err != nil {
		return nil, usage, err
	}
	body, ok := resp.(map[string]any)
	if !ok {
		return nil, usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Codex returned an invalid Responses payload")
	}
	converted, err := codexResponsesToChat(body, req, usage)
	return converted, usage, err
}

func (s *Server) streamChatRoute(
	ctx context.Context,
	route RouteSelection,
	req ChatCompletionRequest,
	headers http.Header,
	writer io.Writer,
) (Usage, error) {
	if route.Provider.Type == ProviderOpenAICodex {
		return s.streamCodexAsChat(ctx, route, req, headers, writer)
	}
	adapter, err := s.adapterForRoute(route)
	if err != nil {
		return Usage{}, err
	}
	return adapter.ChatStream(ctx, route.Provider, route.ProviderModel, req, writer)
}

func compatibleChatRoutes(routed RoutedCall, req ChatCompletionRequest) (RoutedCall, error) {
	if !routesContainAdapterType(routed.Routes, ProviderOpenAICodex) {
		return routed, nil
	}
	if _, err := chatToCodexResponsesRequest(req); err != nil {
		compatible := routed
		compatible.Routes = make([]RouteSelection, 0, len(routed.Routes))
		for _, route := range routed.Routes {
			if route.Provider.Type != ProviderOpenAICodex {
				compatible.Routes = append(compatible.Routes, route)
			}
		}
		if len(compatible.Routes) == 0 {
			return compatible, err
		}
		return compatible, nil
	}
	return routed, nil
}

func (s *Server) anthropicGatewayAffinity(
	apiKeyID string,
	model string,
	headers http.Header,
	raw map[string]any,
	routes []RouteSelection,
) (*RequestAffinity, error) {
	identifier, scope := anthropicSessionIdentifier(headers, raw)
	if scope == sessionScopeSession && routesContainAdapterType(routes, ProviderOpenAICodex) {
		return resolveCodexBridgeAffinity(s.config.SecretKey, apiKeyID, codexBridgeProtocolAnthropic, identifier)
	}
	return s.anthropicCacheLocalityAffinity(apiKeyID, model, headers, raw)
}

func (s *Server) chatGatewayAffinity(
	apiKeyID string,
	headers http.Header,
	request ChatCompletionRequest,
	routes []RouteSelection,
) (*RequestAffinity, error) {
	identifier, scope := chatCompletionSessionIdentifier(headers, request)
	if scope == sessionScopeSession && routesContainAdapterType(routes, ProviderOpenAICodex) {
		return resolveCodexBridgeAffinity(s.config.SecretKey, apiKeyID, codexBridgeProtocolChat, identifier)
	}
	return s.chatCacheLocalityAffinity(apiKeyID, headers, request)
}

func resolveCodexBridgeAffinity(
	secret string,
	apiKeyID string,
	protocol string,
	identifier string,
) (*RequestAffinity, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, nil
	}
	if err := validateSessionIdentifier(identifier, "session_id_invalid", "Session identifier"); err != nil {
		return nil, err
	}
	return &RequestAffinity{
		AdapterType: ProviderOpenAICodex,
		Kind:        AffinityKindCodexSession,
		KeyHash:     deriveSessionAffinityKey(secret, apiKeyID, protocol+"\x00"+identifier),
	}, nil
}

func codexCompatibilityHeaders(source http.Header) http.Header {
	headers := source.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	if value := boundedHeaderValue(headers.Get("session-id")); value != "" {
		headers.Set("session-id", codexShortIdentifier(value))
	} else {
		for _, key := range []string{"x-tokenhub-session-id", "x-claude-code-session-id"} {
			if value := boundedHeaderValue(headers.Get(key)); value != "" {
				headers.Set("session-id", codexShortIdentifier(value))
				break
			}
		}
	}
	return headers
}

func codexAnthropicCompatibilityHeaders(source http.Header, raw map[string]any) http.Header {
	headers := codexCompatibilityHeaders(source)
	if identifier, scope := anthropicSessionIdentifier(source, raw); scope == sessionScopeSession {
		if identifier = boundedHeaderValue(identifier); identifier != "" {
			headers.Set("session-id", codexShortIdentifier(identifier))
		}
	}
	return headers
}

func codexChatCompatibilityHeaders(source http.Header, request ChatCompletionRequest) http.Header {
	headers := codexCompatibilityHeaders(source)
	if identifier, scope := chatCompletionSessionIdentifier(source, request); scope == sessionScopeSession {
		if identifier = boundedHeaderValue(identifier); identifier != "" {
			headers.Set("session-id", codexShortIdentifier(identifier))
		}
	}
	return headers
}
