package server

import (
	"context"
	"net/http"
)

// executeRoutedResponsesContext is shared by the synchronous HTTP handler and
// persistent background workers. Incoming contains only the caller headers that
// are safe and relevant to the selected protocol path.
func (s *Server) executeRoutedResponsesContext(ctx context.Context, incoming http.Header, routed RoutedCall, req ResponsesRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(responsesReasoningEffort(req)) != nil
	return executeRoutedWithStore(ctx, s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool, _ int) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstreamReq := req
		if omitReasoningEffort {
			upstreamReq = withoutResponsesReasoningEffort(upstreamReq)
		}
		resp, usage, err := s.invokeResponsesAdapter(ctx, route, upstreamReq, incoming)
		if isCodexModelUnsupportedError(err) {
			s.removeCodexResourceModel(routeResourceID(route), route.ProviderModel)
		}
		return resp, usage, err
	})
}
