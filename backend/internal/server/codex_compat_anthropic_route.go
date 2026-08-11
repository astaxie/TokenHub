package server

import (
	"context"
	"net/http"
)

func (s *Server) executeAnthropicMessagesRoute(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	headers http.Header,
) (map[string]any, Usage, error) {
	switch route.Provider.Type {
	case ProviderAnthropic:
		return s.executeNativeAnthropicMessages(ctx, route, req, headers)
	case ProviderOpenAICodex:
		return s.executeCodexAnthropicMessages(ctx, route, req, headers)
	}
	if !openAIMessageProvider(route.Provider.Type) {
		return nil, Usage{}, NewHTTPError(
			http.StatusNotImplemented,
			"provider_capability_not_supported",
			"Provider does not support the Anthropic Messages gateway",
		)
	}
	chatReq, err := anthropicToOpenAIChatRequest(req, route.Provider)
	if err != nil {
		return nil, Usage{}, err
	}
	adapter, err := s.adapterForRoute(route)
	if err != nil {
		return nil, Usage{}, err
	}
	resp, usage, err := adapter.Chat(ctx, route.Provider, route.ProviderModel, chatReq)
	if err != nil {
		return nil, usage, err
	}
	body, ok := resp.(map[string]any)
	if !ok {
		return nil, usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider returned an invalid chat response")
	}
	converted, err := openAIResponseToAnthropic(body, req.Model, usage, route.Provider)
	if err != nil {
		return nil, usage, err
	}
	return converted, usage, nil
}

func openAIMessageProvider(providerType string) bool {
	switch providerType {
	case ProviderMock, ProviderOpenAI, ProviderOpenAICompatible, ProviderAzureOpenAI, "deepseek", "qwen", "local":
		return true
	default:
		return false
	}
}

func compatibleAnthropicRoutes(routed RoutedCall, req anthropicMessagesRequest) (RoutedCall, error) {
	compatible := routed
	compatible.Routes = make([]RouteSelection, 0, len(routed.Routes))
	var firstErr error
	for _, route := range routed.Routes {
		err := validateAnthropicRouteCompatibility(route, req)
		if err == nil {
			compatible.Routes = append(compatible.Routes, route)
			continue
		}
		if !isAnthropicRouteIncompatibility(err) {
			return compatible, err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if len(compatible.Routes) == 0 {
		if firstErr == nil {
			firstErr = ErrProviderMissing
		}
		return compatible, firstErr
	}
	return compatible, nil
}

func validateAnthropicRouteCompatibility(route RouteSelection, req anthropicMessagesRequest) error {
	switch route.Provider.Type {
	case ProviderAnthropic:
		return nil
	case ProviderOpenAICodex:
		_, err := anthropicToCodexResponsesRequest(req)
		return err
	}
	if !openAIMessageProvider(route.Provider.Type) {
		return NewHTTPError(
			http.StatusNotImplemented,
			"provider_capability_not_supported",
			"Provider does not support the Anthropic Messages gateway",
		)
	}
	_, err := anthropicToOpenAIChatRequest(req, route.Provider)
	return err
}

func isAnthropicRouteIncompatibility(err error) bool {
	switch AsHTTPError(err).Code {
	case "provider_capability_not_supported",
		"unsupported_reasoning_effort",
		"unsupported_content_block",
		"unsupported_image_source",
		"unsupported_tool",
		"unsupported_tool_choice",
		"unsupported_tool_result":
		return true
	default:
		return false
	}
}
