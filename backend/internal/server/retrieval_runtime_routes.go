package server

// Inventory can change independently of published routes. Check the execution
// snapshot on every request, including routes supplied by scoped provider hooks.
func (s *Server) pricedRetrievalRoutes(call CallContext, routes []RouteSelection, modality, protocol string) []RouteSelection {
	result, _ := s.retrievalRoutesWithDiagnostics(call, routes, modality, protocol)
	return result
}

func (s *Server) retrievalRoutesWithDiagnostics(call CallContext, routes []RouteSelection, modality, protocol string) ([]RouteSelection, map[string]int) {
	result := make([]RouteSelection, 0, len(routes))
	rejected := make(map[string]int)
	models := s.store.ListProviderModels()
	for _, route := range routes {
		if !s.providerRetrievalSupport(route.Provider, modality) && !s.hasGatewayProviderCallHookForRoute(call, route, protocol) {
			rejected["provider_capability_or_protocol_unsupported"]++
			continue
		}
		search := modality == "rerank" && providerRerankProtocol(route.Provider) == "cohere"
		if !retrievalPriceConfigured(call.Model, search, false) {
			rejected["tenant_price_not_configured"]++
			continue
		}
		// The explicit mock adapter has no external procurement contract.
		if route.Provider.Type == ProviderMock {
			result = append(result, route)
			continue
		}
		found := false
		for _, upstream := range models {
			if upstream.ProviderID != route.Provider.ID || upstream.UpstreamModel != route.ProviderModel {
				continue
			}
			found = true
			if canonicalProviderModality(upstream.Modality) != modality {
				rejected["upstream_model_modality_mismatch"]++
				break
			}
			if !retrievalTextInputSupported(upstream) {
				rejected["upstream_text_input_unsupported"]++
				break
			}
			// Procurement prices are accounting evidence, not execution capability.
			// Keep published routes callable after upgrades; missing costs remain
			// unknown in the metering snapshot. Publication still validates prices.
			result = append(result, route)
			break
		}
		if !found {
			rejected["upstream_model_inventory_missing"]++
		}
	}
	return result, rejected
}
