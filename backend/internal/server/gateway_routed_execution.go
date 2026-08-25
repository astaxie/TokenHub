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
		adapter, err := s.responsesAdapterForRoute(prepared)
		if err != nil {
			return nil, Usage{}, err
		}
		compactAdapter, ok := adapter.(ResponsesCompactAdapter)
		if !ok {
			return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support Responses compact")
		}
		body := make(map[string]json.RawMessage, len(request))
		for key, value := range request {
			body[key] = append(json.RawMessage(nil), value...)
		}
		body, err = s.runGatewayCompactRequestTransformHooks(ctx, routed.Call, prepared, body)
		if err != nil {
			return nil, Usage{}, err
		}
		return compactAdapter.CompactWithHeaders(ctx, prepared.Provider, prepared.ProviderModel, body, r.Header)
	})
}

func (s *Server) executeRoutedEmbeddings(r *http.Request, routed RoutedCall, req EmbeddingsRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstreamReq := req
		if transformErr := s.runGatewayEmbeddingsRequestTransformHooks(ctx, routed.Call, route, &upstreamReq); transformErr != nil {
			return nil, Usage{}, transformErr
		}
		return adapter.Embeddings(ctx, route.Provider, route.ProviderModel, upstreamReq)
	})
}
