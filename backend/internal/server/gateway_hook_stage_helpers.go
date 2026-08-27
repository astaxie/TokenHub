package server

import pluginmeta "tokenhub/backend/internal/plugin"

func (s *Server) hasGatewayHookStage(stage pluginmeta.GatewayHookStage) bool {
	return s != nil && s.gatewayHooks != nil && s.gatewayChain != nil && len(s.gatewayChain.Hooks(stage)) > 0
}

func (s *Server) routesWithAdapterCapabilityOrProviderCall(routes []RouteSelection, capability AdapterCapability) []RouteSelection {
	filtered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if s.routeSupportsAdapterCapability(route, capability) || s.hasGatewayHookStage(pluginmeta.StageProviderCall) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}
