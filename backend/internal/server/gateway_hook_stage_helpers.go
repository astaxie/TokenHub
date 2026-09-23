package server

import (
	"slices"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) hasGatewayHookStage(stage pluginmeta.GatewayHookStage) bool {
	return s != nil && s.gatewayHooks != nil && s.gatewayChain != nil && len(s.gatewayChain.Hooks(stage)) > 0
}

func (s *Server) routesWithAdapterCapabilityOrProviderCall(call CallContext, routes []RouteSelection, capability AdapterCapability, protocol string) []RouteSelection {
	filtered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if s.routeSupportsAdapterCapabilityOrProviderCall(call, route, capability, protocol) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

func (s *Server) routeSupportsAdapterCapabilityOrProviderCall(call CallContext, route RouteSelection, capability AdapterCapability, protocol string) bool {
	call.Stream = call.Stream || capability == AdapterCapabilityChatStream || capability == AdapterCapabilityResponseStream
	return s.routeSupportsAdapterCapability(route, capability) || s.hasGatewayProviderCallHookForRoute(call, route, protocol)
}

func (s *Server) hasGatewayProviderCallHookForRoute(call CallContext, route RouteSelection, protocol string) bool {
	output := pluginmeta.DataProviderResponse
	if call.Stream {
		output = pluginmeta.DataStreamEvents
	}
	for _, hook := range s.gatewayProviderCallHooksForRoute(call, route, protocol, call.Stream) {
		if slices.Contains(hook.Writes, output) {
			return true
		}
	}
	return false
}

func (s *Server) gatewayProviderCallHooksForRoute(call CallContext, route RouteSelection, protocol string, stream bool) []pluginmeta.GatewayHookDescriptor {
	if !s.hasGatewayHookStage(pluginmeta.StageProviderCall) {
		return nil
	}
	target := pluginmeta.GatewayHookScopeTarget{
		ProjectID: call.Project.ID, APIKeyID: call.Key.ID,
		ProviderType: route.Provider.Type, ProviderID: route.Provider.ID,
		ResourceID: routeResourceID(route), ResourceType: routeResourceType(route),
		RouteProtocol: protocol, Operation: "provider_call",
	}
	hooks := []pluginmeta.GatewayHookDescriptor{}
	for _, hook := range s.gatewayChain.Hooks(pluginmeta.StageProviderCall) {
		responseOutput := slices.Contains(hook.Writes, pluginmeta.DataProviderResponse)
		streamOutput := slices.Contains(hook.Writes, pluginmeta.DataStreamEvents)
		// Non-response hooks still participate, but cannot establish route capability.
		if (responseOutput || streamOutput) && !(stream && streamOutput || !stream && responseOutput) {
			continue
		}
		if hook.PluginID != tokenHubCoreGatewayChainPluginID && pluginmeta.GatewayHookScopeMatches(hook, target) {
			hooks = append(hooks, hook)
		}
	}
	return hooks
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
