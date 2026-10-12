package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
)

func rerankPatch(r *RerankRequest) func(json.RawMessage) error {
	return func(data json.RawMessage) error { return applyRerankPatch(r, data) }
}

// Route transforms run after cache binding and must preserve the ranking input.
func rerankRoutePatch(r *RerankRequest) func(json.RawMessage) error {
	return func(data json.RawMessage) error {
		next := *r
		if err := applyRerankPatch(&next, data); err != nil {
			return err
		}
		if !reflect.DeepEqual(next, *r) {
			return NewHTTPError(502, "gateway_hook_patch_invalid", "Route plugins cannot change cache-bound rerank input")
		}
		return nil
	}
}
func (s *Server) runGatewayRerankDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, r *RerankRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *r, rerankPatch(r))
}
func (s *Server) runGatewayRerankPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, r *RerankRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *r, rerankPatch(r))
}
func (s *Server) runGatewayRerankGuardrailPreHooks(ctx context.Context, call CallContext, r *RerankRequest) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, *r, rerankGuardrailTargets(r), rerankPatch(r))
}
func (s *Server) runGatewayRerankContextOptimizeHooks(ctx context.Context, call CallContext, r *RerankRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *r, rerankPatch(r))
}
func (s *Server) executeRoutedRerank(r *http.Request, routed RoutedCall, req RerankRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstream := req
		upstream.Documents = append([]string(nil), req.Documents...)
		if err = s.runGatewayRequestTransformHooks(ctx, routed.Call, route, upstream, providerRouteProtocolRerank, rerankRoutePatch(&upstream)); err != nil {
			return nil, Usage{}, err
		}
		if resp, usage, handled, err := s.runGatewayProviderCallHooks(ctx, routed.Call, route, upstream, providerRouteProtocolRerank); err != nil || handled {
			if err == nil {
				usage, err = pluginRetrievalUsage(resp, usage, providerRerankProtocol(route.Provider) == "cohere")
			}
			return resp, usage, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		reranker, ok := adapter.(ProviderReranker)
		if !ok {
			return nil, Usage{}, NewHTTPError(501, "provider_capability_not_supported", "Provider does not support rerank")
		}
		resp, usage, err := reranker.Rerank(ctx, route.Provider, route.ProviderModel, upstream)
		if err == nil {
			usage, err = pluginRetrievalUsage(resp, usage, providerRerankProtocol(route.Provider) == "cohere")
		}
		return resp, usage, err
	})
}
