package server

import (
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type routeProtocolTestAdapter struct {
	MockAdapter
	protocols []string
}

func (adapter routeProtocolTestAdapter) RouteProtocols() []string {
	return adapter.protocols
}

func TestRouteModelProtocolUsesAdapterDeclaredProtocols(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "admin"})
	const providerType = "native_plugin_protocol"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(
		"tokenhub.provider.native-protocol",
		"Native Protocol Provider",
		[]string{providerType},
		[]string{string(AdapterCapabilityChat)},
	), AdapterRegistration{
		Type:         providerType,
		Adapter:      routeProtocolTestAdapter{protocols: []string{"native/messages"}},
		Capabilities: []AdapterCapability{AdapterCapabilityChat},
	}); err != nil {
		t.Fatalf("register native protocol provider: %v", err)
	}
	model := &Model{Name: "native-model", Metadata: map[string]string{"endpoints": "native/messages"}}

	descriptor, ok := server.adapterRegistry.Describe(providerType)
	if !ok {
		t.Fatal("provider descriptor was not registered")
	}
	if err := server.validateRouteModelProtocol(model.Name, model, providerType, descriptor); err != nil {
		t.Fatalf("adapter-declared protocol was rejected: %v", err)
	}

	model.Metadata["endpoints"] = "responses"
	if err := server.validateRouteModelProtocol(model.Name, model, providerType, descriptor); AsHTTPError(err).Code != "route_protocol_mismatch" {
		t.Fatalf("unexpected incompatible protocol error: %v", err)
	}
}
