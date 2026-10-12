package server

import (
	"context"
	"encoding/json"
	"net/http"
)

type systemOneRouteResult struct {
	response SystemOneResponse
	request  SystemOneRequest
}

func (s *Server) executeRoutedSystemOne(r *http.Request, routed RoutedCall, req SystemOneRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		// Clone the nested request before a route-specific transform or failover.
		body, err := json.Marshal(req)
		if err != nil {
			return nil, Usage{}, err
		}
		var upstream SystemOneRequest
		if err := json.Unmarshal(body, &upstream); err != nil {
			return nil, Usage{}, err
		}
		if err := s.runGatewayRequestTransformHooks(ctx, routed.Call, route, upstream, providerRouteProtocolSystemOne, func(data json.RawMessage) error {
			return applySystemOneRequestPatch(&upstream, data)
		}); err != nil {
			return nil, Usage{}, err
		}
		if response, usage, handled, err := s.runGatewayProviderCallHooks(ctx, routed.Call, route, upstream, providerRouteProtocolSystemOne); err != nil || handled {
			var result SystemOneResponse
			if err == nil {
				result, err = decodeSystemOneGatewayResponse(response)
				// Native usage is required even if the hook omits DataUsage. A malformed
				// answer envelope can still contain independently valid native usage.
				metered := result.meteredUsage()
				metered.UpstreamRequestID = usage.UpstreamRequestID
				metered.ResponseHeaders = usage.ResponseHeaders
				usage = metered
				if err == nil {
					err = result.validate(upstream)
				}
			}
			if err != nil {
				return nil, usage, err
			}
			return systemOneRouteResult{response: result, request: upstream}, usage, nil
		}
		adapter, ok := resolveTypedAdapter[SystemOneInvoker](s.adapterRegistry, route.Provider.Type)
		if !ok {
			return nil, Usage{}, NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", "System One is not supported")
		}
		response, usage, err := adapter.SystemOne(ctx, route.Provider, route.ProviderModel, upstream)
		return systemOneRouteResult{response: response, request: upstream}, usage, err
	})
}

func validateSystemOneGatewayResponse(response any, req, effectiveRequest SystemOneRequest) error {
	result, err := decodeSystemOneGatewayResponse(response)
	if err != nil {
		return err
	}
	return result.validateWithLegendRequest(req, effectiveRequest)
}

func decodeSystemOneGatewayResponse(response any) (SystemOneResponse, error) {
	if result, ok := response.(SystemOneResponse); ok {
		return result, nil
	}
	data, err := json.Marshal(response)
	if err != nil {
		return SystemOneResponse{}, invalidSystemOneResponse()
	}
	return decodeSystemOneResponse(data)
}
