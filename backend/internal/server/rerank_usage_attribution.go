package server

import "context"

// Attribution commits only valid usage. Hook failures and invalid overrides must
// leave the provider evidence available for settlement and quota reconciliation.
func (s *Server) attributeRerankUsage(ctx context.Context, call CallContext, route RouteSelection, response any, usage Usage) (Usage, error) {
	attributed, err := s.runGatewayUsageAttributionHooks(ctx, call, route, response, usage, providerRouteProtocolRerank)
	if err != nil {
		return usage, err
	}
	attributed, err = pluginRetrievalUsage(response, attributed, providerRerankProtocol(route.Provider) == "cohere")
	if err != nil {
		return usage, err
	}
	return attributed, nil
}
