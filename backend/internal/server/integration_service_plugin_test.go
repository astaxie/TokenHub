package server

import (
	"context"
	"testing"
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
