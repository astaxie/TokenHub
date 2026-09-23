package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
)

type providerRouteBridge struct {
	Protocol string

	ChatCompatible       func(ChatCompletionRequest) error
	ExecuteChat          func(*Server, context.Context, RouteSelection, ChatCompletionRequest, http.Header) (any, Usage, error)
	StreamChat           func(*Server, context.Context, RouteSelection, ChatCompletionRequest, http.Header, io.Writer) (Usage, error)
	ValidateAnthropic    func(anthropicMessagesRequest) error
	ExecuteAnthropic     func(*Server, context.Context, RouteSelection, anthropicMessagesRequest, http.Header) (map[string]any, Usage, error)
	StreamAnthropic      func(*Server, context.Context, RouteSelection, anthropicMessagesRequest, http.Header, io.Writer) (Usage, error)
	GeminiHeaders        func(http.Header, map[string]any) http.Header
	WriteResponseHeaders func(http.Header, http.Header)
}

var (
	providerRouteBridgeMu sync.RWMutex
	providerRouteBridges  []providerRouteBridge
)

func registerProviderRouteBridge(bridge providerRouteBridge) {
	if strings.TrimSpace(bridge.Protocol) == "" {
		return
	}
	providerRouteBridgeMu.Lock()
	defer providerRouteBridgeMu.Unlock()
	providerRouteBridges = append(providerRouteBridges, bridge)
}

func registeredProviderRouteBridges() []providerRouteBridge {
	providerRouteBridgeMu.RLock()
	defer providerRouteBridgeMu.RUnlock()
	return append([]providerRouteBridge(nil), providerRouteBridges...)
}

func providerRouteBridgeByProtocol(protocol string, supports func(providerRouteBridge) bool) (providerRouteBridge, bool) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return providerRouteBridge{}, false
	}
	for _, bridge := range registeredProviderRouteBridges() {
		if strings.EqualFold(bridge.Protocol, protocol) && (supports == nil || supports(bridge)) {
			return bridge, true
		}
	}
	return providerRouteBridge{}, false
}

func providerRouteBridgeForRoute(registry *AdapterRegistry, route RouteSelection, supports func(providerRouteBridge) bool) (providerRouteBridge, bool) {
	descriptor, ok := registry.Describe(route.Provider.Type)
	if !ok {
		return providerRouteBridge{}, false
	}
	for _, protocol := range routeProtocolSetList(adapterDescriptorRouteProtocolSet(descriptor)) {
		if !routeSupportsProviderProtocol(registry, route, protocol) {
			continue
		}
		if bridge, ok := providerRouteBridgeByProtocol(protocol, supports); ok {
			return bridge, true
		}
	}
	return providerRouteBridge{}, false
}

func chatRouteBridgeSupported(bridge providerRouteBridge) bool {
	return bridge.ExecuteChat != nil
}

func streamChatRouteBridgeSupported(bridge providerRouteBridge) bool {
	return bridge.StreamChat != nil
}

func anthropicRouteBridgeSupported(bridge providerRouteBridge) bool {
	return bridge.ExecuteAnthropic != nil && bridge.ValidateAnthropic != nil
}

func streamAnthropicRouteBridgeSupported(bridge providerRouteBridge) bool {
	return bridge.StreamAnthropic != nil
}

func geminiRouteBridgeSupported(bridge providerRouteBridge) bool {
	return bridge.GeminiHeaders != nil
}

func responseHeaderRouteBridgeSupported(bridge providerRouteBridge) bool {
	return bridge.WriteResponseHeaders != nil
}

func (s *Server) chatRouteBridge(route RouteSelection) (providerRouteBridge, bool) {
	return providerRouteBridgeForRoute(s.adapterRegistry, route, chatRouteBridgeSupported)
}

func (s *Server) streamChatRouteBridge(route RouteSelection) (providerRouteBridge, bool) {
	return providerRouteBridgeForRoute(s.adapterRegistry, route, streamChatRouteBridgeSupported)
}

func (s *Server) anthropicRouteBridge(route RouteSelection) (providerRouteBridge, bool) {
	return providerRouteBridgeForRoute(s.adapterRegistry, route, anthropicRouteBridgeSupported)
}

func (s *Server) geminiRouteBridge(route RouteSelection) (providerRouteBridge, bool) {
	return providerRouteBridgeForRoute(s.adapterRegistry, route, geminiRouteBridgeSupported)
}

func (s *Server) writeProviderResponseHeaders(target http.Header, route RouteSelection, source http.Header) {
	if bridge, ok := providerRouteBridgeForRoute(s.adapterRegistry, route, responseHeaderRouteBridgeSupported); ok {
		bridge.WriteResponseHeaders(target, source)
	}
}
