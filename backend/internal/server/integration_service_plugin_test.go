package server

import (
	"context"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type pluginHealthProbeAdapter struct {
	calls int
}

func (a *pluginHealthProbeAdapter) ProbeProvider(context.Context, Provider) (any, error) {
	a.calls++
	return map[string]any{"ok": true}, nil
}

func TestIntegrationServiceUsesProviderHealthProbeCapability(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_plugin_health", Name: "Plugin Health", Type: "plugin_health",
		Status: StatusActive, Healthy: false,
	})
	adapter := &pluginHealthProbeAdapter{}
	registry := NewAdapterRegistry()
	registry.Register("plugin_health", adapter, AdapterCapabilityProbe)

	result, err := NewIntegrationService(store, registry).TestProvider(context.Background(), provider.ID)
	if err != nil {
		t.Fatalf("test provider: %v", err)
	}
	if adapter.calls != 1 {
		t.Fatalf("health probe calls = %d, want 1", adapter.calls)
	}
	if payload, ok := result.(map[string]any); !ok || payload["ok"] != true {
		t.Fatalf("result = %#v, want health probe payload", result)
	}
	updated, _ := store.GetProvider(provider.ID)
	if !updated.Healthy {
		t.Fatal("successful plugin health probe should mark the provider healthy")
	}
}

func TestIntegrationServiceUsesRegistryProbeFallbackPolicy(t *testing.T) {
	store := NewMemoryStore()
	providerType := "store_probe_provider"
	provider := store.AddProvider(Provider{
		ID: "prv_store_probe", Name: "Store Probe", Type: providerType,
		Status: StatusActive, Healthy: false,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_store_probe", ProviderID: provider.ID, Name: "Store Probe Account",
		Status: StatusActive, Healthy: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := NewAdapterRegistry()
	descriptor := pluginmeta.BuiltInProvider(
		"tokenhub.provider.store-probe",
		"Store Probe",
		[]string{providerType},
		[]string{string(AdapterCapabilityChat)},
	)
	descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
		Kind:    pluginmeta.CapabilityKindProviderPolicy,
		Name:    pluginmeta.ProviderPolicyStoreProbeFallback,
		Subject: providerType,
		Value:   "true",
	})
	if err := registry.RegisterPlugin(descriptor, AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityChat},
	}); err != nil {
		t.Fatalf("register plugin provider: %v", err)
	}

	result, err := NewIntegrationService(store, registry).TestProviderResource(context.Background(), resource.ID, nil)
	if err != nil {
		t.Fatalf("test provider resource: %v", err)
	}
	if resourceResult, ok := result.(ProviderResource); !ok || resourceResult.ID != resource.ID || !resourceResult.Healthy {
		t.Fatalf("resource probe result = %#v, want recovered ProviderResource", result)
	}

	providerResult, err := NewIntegrationService(store, registry).TestProvider(context.Background(), provider.ID)
	if err != nil {
		t.Fatalf("test provider: %v", err)
	}
	if tested, ok := providerResult.(Provider); !ok || tested.ID != provider.ID || !tested.Healthy {
		t.Fatalf("provider probe result = %#v, want store-tested Provider", providerResult)
	}
}

func TestIntegrationServiceDoesNotUseStoreFallbackWithoutCapability(t *testing.T) {
	store := NewMemoryStore()
	providerType := "catalog_probe_provider"
	provider := store.AddProvider(Provider{
		ID: "prv_catalog_probe", Name: "Catalog Probe", Type: providerType,
		BaseURL: "://bad-url", Status: StatusActive, Healthy: false,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_catalog_probe", ProviderID: provider.ID, Name: "Catalog Probe Account",
		Status: StatusActive, Healthy: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := NewAdapterRegistry()
	if err := registry.RegisterPlugin(pluginmeta.BuiltInProvider(
		"tokenhub.provider.catalog-probe",
		"Catalog Probe",
		[]string{providerType},
		[]string{string(AdapterCapabilityChat)},
	), AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityChat},
	}); err != nil {
		t.Fatalf("register plugin provider: %v", err)
	}

	_, err = NewIntegrationService(store, registry).TestProviderResource(context.Background(), resource.ID, nil)
	if err == nil {
		t.Fatal("test provider resource succeeded without store probe fallback policy")
	}
	if strings.Contains(err.Error(), "Provider resource not found") {
		t.Fatalf("unexpected store fallback error: %v", err)
	}
}
