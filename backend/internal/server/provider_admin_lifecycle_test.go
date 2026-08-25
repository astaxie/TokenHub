package server

import (
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type lifecycleTestAdapter struct {
	MockAdapter
}

func (lifecycleTestAdapter) ProviderOperationKey(provider Provider, operation ProviderAdminOperation) (string, bool) {
	if operation != ProviderAdminOperationDeleteProvider {
		return "", false
	}
	return "plugin-provider-delete:" + provider.ID, true
}

func (lifecycleTestAdapter) ProviderResourceOperationKey(provider Provider, resource ProviderResource, operation ProviderAdminOperation) (string, bool) {
	if operation != ProviderAdminOperationUpdateResource {
		return "", false
	}
	return "plugin-resource-update:" + firstNonEmpty(resource.ProviderID, provider.ID) + ":" + resource.ID, true
}

func TestProviderAdminOperationKeyUsesAdapterLifecycle(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "admin"})
	const providerType = "lifecycle_provider"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(
		"tokenhub.provider.lifecycle",
		"Lifecycle Provider",
		[]string{providerType},
		[]string{string(AdapterCapabilityResponses)},
	), AdapterRegistration{
		Type:         providerType,
		Adapter:      lifecycleTestAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityResponses},
	}); err != nil {
		t.Fatalf("register lifecycle provider: %v", err)
	}

	key, ok := server.providerAdminOperationKey(Provider{ID: "prv_lifecycle", Type: providerType}, ProviderAdminOperationDeleteProvider)
	if !ok || key != "plugin-provider-delete:prv_lifecycle" {
		t.Fatalf("provider operation key = %q, %v", key, ok)
	}
	key, ok = server.providerResourceAdminOperationKey(
		Provider{ID: "prv_lifecycle", Type: providerType},
		ProviderResource{ID: "rsrc_lifecycle", ProviderID: "prv_lifecycle"},
		ProviderAdminOperationUpdateResource,
	)
	if !ok || key != "plugin-resource-update:prv_lifecycle:rsrc_lifecycle" {
		t.Fatalf("resource operation key = %q, %v", key, ok)
	}
}

func TestCodexAdminLifecyclePreservesImageCapabilityLockKeys(t *testing.T) {
	adapter := CodexSubscriptionAdapter{}
	provider := Provider{ID: "prv_codex", Type: ProviderOpenAICodex}

	key, ok := adapter.ProviderOperationKey(provider, ProviderAdminOperationDeleteProvider)
	if !ok || key != "codex-image-capability:prv_codex" {
		t.Fatalf("provider delete key = %q, %v", key, ok)
	}
	key, ok = adapter.ProviderResourceOperationKey(provider, ProviderResource{
		ID:           "rsrc_codex",
		ProviderID:   "prv_codex",
		ResourceType: ProviderResourceOpenAISubscription,
	}, ProviderAdminOperationDeleteResource)
	if !ok || key != "codex-image-capability:prv_codex" {
		t.Fatalf("resource delete key = %q, %v", key, ok)
	}
	if key, ok := adapter.ProviderResourceOperationKey(provider, ProviderResource{
		ID:           "rsrc_api_key",
		ProviderID:   "prv_codex",
		ResourceType: ProviderResourceAPIKey,
	}, ProviderAdminOperationDeleteResource); ok || key != "" {
		t.Fatalf("non-subscription resource key = %q, %v", key, ok)
	}
}
