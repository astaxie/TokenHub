package server

import "testing"

func TestProviderRouteBridgeUsesPluginDeclaredProtocol(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "admin"})
	const providerType = "plugin_bridge_protocol"
	descriptor := providerPluginDescriptorWithRouteProtocol(
		"tokenhub.provider.bridge-protocol",
		"Bridge Protocol Provider",
		providerType,
		AdapterCapabilityResponses,
		providerRouteProtocolCodexResponses,
	)
	if err := server.adapterRegistry.RegisterPlugin(descriptor, AdapterRegistration{
		Type:         providerType,
		Adapter:      routeProtocolTestAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityResponses},
	}); err != nil {
		t.Fatalf("register bridge protocol provider: %v", err)
	}

	bridge, ok := server.chatRouteBridge(RouteSelection{Provider: Provider{Type: providerType}})

	if !ok {
		t.Fatal("plugin-declared bridge protocol was not resolved")
	}
	if bridge.Protocol != providerRouteProtocolCodexResponses || bridge.ExecuteChat == nil || bridge.ChatCompatible == nil {
		t.Fatalf("unexpected bridge: %#v", bridge)
	}
}

func TestProviderRouteBridgeIgnoresUndeclaredProtocol(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "admin"})
	server.adapterRegistry.Register("plain_chat_provider", routeProtocolTestAdapter{}, AdapterCapabilityChat)

	if bridge, ok := server.chatRouteBridge(RouteSelection{Provider: Provider{Type: "plain_chat_provider"}}); ok {
		t.Fatalf("plain chat provider resolved bridge: %#v", bridge)
	}
}
