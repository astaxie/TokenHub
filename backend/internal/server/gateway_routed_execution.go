package server

import (
	"context"
	"encoding/json"
	"net/http"
)

func (s *Server) executeRoutedCompact(r *http.Request, routed RoutedCall, request map[string]json.RawMessage) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
		prepared, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		body := make(map[string]json.RawMessage, len(request))
		for key, value := range request {
			body[key] = append(json.RawMessage(nil), value...)
		}
		body, err = s.runGatewayCompactRequestTransformHooks(ctx, routed.Call, prepared, body)
		if err != nil {
			return nil, Usage{}, err
		}
		if resp, usage, handled, err := s.runGatewayProviderCallHooks(ctx, routed.Call, prepared, body, providerRouteProtocolResponses); err != nil || handled {
			return resp, usage, err
		}
		adapter, err := s.responsesAdapterForRoute(prepared)
		if err != nil {
			return nil, Usage{}, err
		}
		compactAdapter, ok := adapter.(ResponsesCompactAdapter)
		if !ok {
			return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support Responses compact")
		}
		return compactAdapter.CompactWithHeaders(ctx, prepared.Provider, prepared.ProviderModel, body, r.Header)
	})
}

func (s *Server) executeRoutedPlaygroundChat(r *http.Request, routed RoutedCall, req ChatCompletionRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(req.ReasoningEffort) != nil
	return executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool, _ int) (any, Usage, error) {
		responsesReq, useResponses, err := playgroundResponsesRequestForRoute(s.adapterRegistry, route, req)
		if err != nil {
			return nil, Usage{}, err
		}
		route, err = s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		if useResponses {
			upstreamReq := responsesReq
			if omitReasoningEffort {
				upstreamReq = withoutResponsesReasoningEffort(upstreamReq)
			}
			if transformErr := s.runGatewayResponsesRequestTransformHooks(ctx, routed.Call, route, &upstreamReq); transformErr != nil {
				return nil, Usage{}, transformErr
			}
			if resp, usage, handled, err := s.runGatewayProviderCallHooks(ctx, routed.Call, route, upstreamReq, providerRouteProtocolResponses); err != nil || handled {
				return resp, usage, err
			}
			resp, usage, err := s.invokeResponsesAdapter(ctx, route, upstreamReq, r.Header)
			if providerResourceModelUnsupportedError(err) {
				s.removeProviderResourceModel(routeResourceID(route), route.ProviderModel)
			}
			return resp, usage, err
		}
		upstreamReq := req
		if omitReasoningEffort {
			upstreamReq.ReasoningEffort = nil
		}
		if transformErr := s.runGatewayChatRequestTransformHooks(ctx, routed.Call, route, &upstreamReq); transformErr != nil {
			return nil, Usage{}, transformErr
		}
		if resp, usage, handled, err := s.runGatewayProviderCallHooks(ctx, routed.Call, route, upstreamReq, providerRouteProtocolChatCompletions); err != nil || handled {
			return resp, usage, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		return adapter.Chat(ctx, route.Provider, route.ProviderModel, upstreamReq)
	})
}

func (s *Server) executeRoutedResponses(r *http.Request, routed RoutedCall, req ResponsesRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return s.executeRoutedResponsesContext(r.Context(), r.Header, routed, req)
}

func (s *Server) invokeResponsesAdapter(ctx context.Context, route RouteSelection, req ResponsesRequest, incoming http.Header) (any, Usage, error) {
	adapter, err := s.responsesAdapterForRoute(route)
	if err != nil {
		return nil, Usage{}, err
	}
	if envelopeAdapter, ok := adapter.(ResponsesEnvelopeAdapter); ok {
		return envelopeAdapter.ResponsesWithHeaders(ctx, route.Provider, route.ProviderModel, req, incoming)
	}
	if responsesAdapter, ok := adapter.(ResponsesInvoker); ok {
		return responsesAdapter.Responses(ctx, route.Provider, route.ProviderModel, req)
	}
	return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support Responses")
}

func (s *Server) executeRoutedEmbeddings(r *http.Request, routed RoutedCall, req EmbeddingsRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstreamReq := req
		if transformErr := s.runGatewayEmbeddingsRequestTransformHooks(ctx, routed.Call, route, &upstreamReq); transformErr != nil {
			return nil, Usage{}, transformErr
		}
		if resp, usage, handled, err := s.runGatewayProviderCallHooks(ctx, routed.Call, route, upstreamReq, providerRouteProtocolEmbeddings); err != nil || handled {
			return resp, usage, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		return adapter.Embeddings(ctx, route.Provider, route.ProviderModel, upstreamReq)
	})
}
