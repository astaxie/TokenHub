package server

import pluginmeta "tokenhub/backend/internal/plugin"

func (s *Server) hasGatewayHookStage(stage pluginmeta.GatewayHookStage) bool {
	return s != nil && s.gatewayHooks != nil && s.gatewayChain != nil && len(s.gatewayChain.Hooks(stage)) > 0
}

func (s *Server) routesWithAdapterCapabilityOrProviderCall(routes []RouteSelection, capability AdapterCapability) []RouteSelection {
	filtered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if s.routeSupportsAdapterCapability(route, capability) || s.hasGatewayProviderCallHookForRoute(route) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

func (s *Server) hasGatewayProviderCallHookForRoute(route RouteSelection) bool {
	if !s.hasGatewayHookStage(pluginmeta.StageProviderCall) {
		return false
	}
	providerType := route.Provider.Type
	for _, hook := range s.gatewayChain.Hooks(pluginmeta.StageProviderCall) {
		if hook.PluginID == tokenHubCoreGatewayChainPluginID {
			continue
		}
		if hook.Subject == "" || hook.Subject == providerType {
			return true
		}
	}
	return false
}
