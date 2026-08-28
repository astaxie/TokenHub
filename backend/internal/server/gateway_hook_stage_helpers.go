package server

import (
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) hasGatewayHookStage(stage pluginmeta.GatewayHookStage) bool {
	return s != nil && s.gatewayHooks != nil && s.gatewayChain != nil && len(s.gatewayChain.Hooks(stage)) > 0
}

func (s *Server) routesWithAdapterCapabilityOrProviderCall(routes []RouteSelection, capability AdapterCapability, protocol string) []RouteSelection {
	filtered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if s.routeSupportsAdapterCapability(route, capability) || s.hasGatewayProviderCallHookForRoute(route, protocol) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

func (s *Server) hasGatewayProviderCallHookForRoute(route RouteSelection, protocol string) bool {
	return len(s.gatewayProviderCallHooksForRoute(route, protocol)) > 0
}

func (s *Server) gatewayProviderCallHooksForRoute(route RouteSelection, protocol string) []pluginmeta.GatewayHookDescriptor {
	return s.gatewayRouteHooksForRoute(pluginmeta.StageProviderCall, route, protocol, false)
}

func (s *Server) gatewayRequestTransformHooksForRoute(route RouteSelection, protocol string) []pluginmeta.GatewayHookDescriptor {
	return s.gatewayRouteHooksForRoute(pluginmeta.StageRequestTransform, route, protocol, true)
}

func (s *Server) gatewayRouteHooksForRoute(stage pluginmeta.GatewayHookStage, route RouteSelection, protocol string, includeCore bool) []pluginmeta.GatewayHookDescriptor {
	if !s.hasGatewayHookStage(stage) {
		return nil
	}
	hooks := []pluginmeta.GatewayHookDescriptor{}
	for _, hook := range s.gatewayChain.Hooks(stage) {
		if !includeCore && hook.PluginID == tokenHubCoreGatewayChainPluginID {
			continue
		}
		if gatewayHookMatchesProviderRoute(hook, route, protocol) {
			hooks = append(hooks, hook)
		}
	}
	return hooks
}

func gatewayHookMatchesProviderRoute(hook pluginmeta.GatewayHookDescriptor, route RouteSelection, protocol string) bool {
	hook = pluginmeta.NormalizeGatewayHookDescriptor(hook)
	scope := hook.Scope
	if !gatewayHookRouteScopeListMatches(scope.ProviderTypes, route.Provider.Type, true) {
		return false
	}
	if !gatewayHookRouteScopeListMatches(scope.ProviderIDs, route.Provider.ID, false) {
		return false
	}
	if !gatewayHookRouteScopeListMatches(scope.ResourceIDs, routeResourceID(route), false) {
		return false
	}
	if !gatewayHookRouteScopeListMatches(scope.ResourceTypes, routeResourceType(route), true) {
		return false
	}
	return gatewayHookRouteScopeListMatches(scope.RouteProtocols, protocol, true)
}

func gatewayHookRouteScopeListMatches(allowed []string, value string, caseInsensitive bool) bool {
	if len(allowed) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	if caseInsensitive {
		value = strings.ToLower(value)
	}
	if value == "" {
		return false
	}
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}
