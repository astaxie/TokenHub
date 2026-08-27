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
	if !s.hasGatewayHookStage(pluginmeta.StageProviderCall) {
		return nil
	}
	hooks := []pluginmeta.GatewayHookDescriptor{}
	for _, hook := range s.gatewayChain.Hooks(pluginmeta.StageProviderCall) {
		if hook.PluginID == tokenHubCoreGatewayChainPluginID {
			continue
		}
		if gatewayHookMatchesProviderRoute(hook, route, protocol) {
			hooks = append(hooks, hook)
		}
	}
	return hooks
}

func gatewayHookMatchesProviderRoute(hook pluginmeta.GatewayHookDescriptor, route RouteSelection, protocol string) bool {
	providerType := strings.TrimSpace(route.Provider.Type)
	if hook.Subject != "" && hook.Subject != providerType {
		return false
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	for _, key := range []string{"protocol", "route_protocol"} {
		expected := strings.TrimSpace(hook.Metadata[key])
		if expected == "" {
			continue
		}
		if protocol == "" || !gatewayHookMetadataListContains(expected, protocol) {
			return false
		}
	}
	return true
}

func gatewayHookMetadataListContains(raw string, value string) bool {
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';'
	}) {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}
