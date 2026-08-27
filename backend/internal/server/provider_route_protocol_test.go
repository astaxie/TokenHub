package server

import (
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type routeProtocolTestAdapter struct {
	MockAdapter
}

func providerPluginDescriptorWithRouteProtocol(pluginID string, name string, providerType string, capability AdapterCapability, protocol string) pluginmeta.Descriptor {
	descriptor := pluginmeta.BuiltInProvider(pluginID, name, []string{providerType}, []string{string(capability)})
	descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
		Kind:    "provider_policy",
		Name:    "route_protocol",
		Subject: providerType,
		Value:   protocol,
	})
	return pluginmeta.NormalizeDescriptor(descriptor)
}

func TestRouteModelProtocolUsesPluginDeclaredProtocols(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "admin"})
	const providerType = "native_plugin_protocol"
	pluginDescriptor := providerPluginDescriptorWithRouteProtocol(
		"tokenhub.provider.native-protocol",
		"Native Protocol Provider",
		providerType,
		AdapterCapabilityChat,
		"native/messages",
	)
	if err := server.adapterRegistry.RegisterPlugin(pluginDescriptor, AdapterRegistration{
		Type:         providerType,
		Adapter:      routeProtocolTestAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityChat},
	}); err != nil {
		t.Fatalf("register native protocol provider: %v", err)
	}
	model := &Model{Name: "native-model", Metadata: map[string]string{"endpoints": "native/messages"}}

	descriptor, ok := server.adapterRegistry.Describe(providerType)
	if !ok {
		t.Fatal("provider descriptor was not registered")
	}
	if err := server.validateRouteModelProtocol(model.Name, model, descriptor); err != nil {
		t.Fatalf("plugin-declared protocol was rejected: %v", err)
	}

	model.Metadata["endpoints"] = "responses"
	if err := server.validateRouteModelProtocol(model.Name, model, descriptor); AsHTTPError(err).Code != "route_protocol_mismatch" {
		t.Fatalf("unexpected incompatible protocol error: %v", err)
	}
}

func TestRouteModelProtocolUsesDescriptorPolicy(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "admin"})
	descriptor := AdapterDescriptor{
		Type:         "descriptor_native_protocol",
		Capabilities: []AdapterCapability{AdapterCapabilityChat},
		ProviderPolicy: AdapterProviderPolicy{
			RouteProtocols: []string{"descriptor/messages"},
		},
	}
	model := &Model{Name: "descriptor-model", Metadata: map[string]string{"endpoints": "descriptor/messages"}}

	if err := server.validateRouteModelProtocol(model.Name, model, descriptor); err != nil {
		t.Fatalf("descriptor-declared protocol was rejected: %v", err)
	}

	model.Metadata["endpoints"] = "chat/completions"
	if err := server.validateRouteModelProtocol(model.Name, model, descriptor); AsHTTPError(err).Code != "route_protocol_mismatch" {
		t.Fatalf("unexpected incompatible descriptor protocol error: %v", err)
	}
}

func TestRouteProtocolRequiresAdapterDescriptor(t *testing.T) {
	route := RouteSelection{Provider: Provider{Type: ProviderOpenAICodex}}
	if routeSupportsProviderProtocol(NewAdapterRegistry(), route, providerRouteProtocolCodexResponses) {
		t.Fatal("unregistered provider type should not be matched by legacy protocol fallback")
	}
}
