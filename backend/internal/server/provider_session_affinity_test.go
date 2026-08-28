package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdapterSessionBindingExpiresAfterOneHour(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_expiring_session",
		Name:    "Expiring Session",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_expiring_session",
		ProviderID:   provider.ID,
		Name:         "Expiring Session Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	binding, changed, err := store.CommitAdapterSessionBinding(ctx, AdapterSessionBinding{
		AdapterType:     ProviderOpenAICodex,
		AffinityKind:    AffinityKindCodexSession,
		ProviderID:      provider.ID,
		AffinityKeyHash: "expired-affinity",
		ResourceID:      resource.ID,
	}, noBindingGeneration)
	if err != nil || !changed {
		t.Fatalf("create binding: changed=%v err=%v", changed, err)
	}

	expiredAt := time.Now().UTC().Add(-codexSessionAffinityTTL - time.Minute)
	if err := store.db.Model(&AdapterSessionBinding{}).
		Where("id = ?", binding.ID).
		Update("last_used_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}

	if _, ok, err := store.GetAdapterSessionBinding(ctx, ProviderOpenAICodex, provider.ID, "expired-affinity"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("binding should expire after one hour of inactivity")
	}

	var count int64
	if err := store.db.Model(&AdapterSessionBinding{}).Where("id = ?", binding.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired binding was not removed: count=%d", count)
	}
}

func TestAdapterSessionBindingSuccessfulUseRefreshesExpiration(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_refreshing_session",
		Name:    "Refreshing Session",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_refreshing_session",
		ProviderID:   provider.ID,
		Name:         "Refreshing Session Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	binding, changed, err := store.CommitAdapterSessionBinding(ctx, AdapterSessionBinding{
		AdapterType:     ProviderOpenAICodex,
		AffinityKind:    AffinityKindCodexSession,
		ProviderID:      provider.ID,
		AffinityKeyHash: "refreshing-affinity",
		ResourceID:      resource.ID,
	}, noBindingGeneration)
	if err != nil || !changed {
		t.Fatalf("create binding: changed=%v err=%v", changed, err)
	}

	previousUse := time.Now().UTC().Add(-codexSessionAffinityTTL + time.Minute)
	if err := store.db.Model(&AdapterSessionBinding{}).
		Where("id = ?", binding.ID).
		Update("last_used_at", previousUse).Error; err != nil {
		t.Fatal(err)
	}

	current, ok, err := store.GetAdapterSessionBinding(ctx, ProviderOpenAICodex, provider.ID, "refreshing-affinity")
	if err != nil || !ok {
		t.Fatalf("get active binding: ok=%v err=%v", ok, err)
	}
	refreshed, changed, err := store.CommitAdapterSessionBinding(ctx, current, current.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("touching the same resource should not count as a rebind")
	}
	if !refreshed.LastUsedAt.After(previousUse) {
		t.Fatalf("successful use did not refresh expiration: previous=%s current=%s", previousUse, refreshed.LastUsedAt)
	}
}

func TestProviderSessionAffinityPersistsBinding(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_provider_session", Name: "Provider Session", Type: "sticky_provider",
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_provider_session", ProviderID: provider.ID, Name: "Provider Session Account",
		ResourceType: "sticky_account", Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	routed := RoutedCall{
		Affinity: &RequestAffinity{
			AdapterType: "sticky_provider",
			Kind:        AffinityKindProviderSession,
			KeyHash:     "provider-session-affinity",
		},
		Routes: []RouteSelection{{
			Provider: provider,
			Resource: &resource,
			Route: ModelRoute{
				ProviderID: provider.ID, ProviderResourceID: resource.ID,
			},
		}},
	}
	ctx := context.Background()
	_, bindings, err := applyAdapterSessionAffinity(ctx, store, routed)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitAdapterSessionAffinity(ctx, store, routed, bindings, routed.Routes[0], "initial"); err != nil {
		t.Fatal(err)
	}
	keyHash := providerScopedAffinityKeyHash("provider-session-affinity", provider.ID)
	binding, ok, err := store.GetAdapterSessionBinding(ctx, "sticky_provider", provider.ID, keyHash)
	if err != nil || !ok {
		t.Fatalf("provider session binding missing: ok=%v err=%v", ok, err)
	}
	if binding.AffinityKind != AffinityKindProviderSession || binding.ResourceID != resource.ID {
		t.Fatalf("provider session binding = %+v", binding)
	}
}

func TestChatGatewayAffinityUsesAdapterCapability(t *testing.T) {
	server := New(NewMemoryStore())
	server.adapterRegistry.Register("sticky_provider", MockAdapter{}, AdapterCapabilityChat, AdapterCapabilityAffinity)
	headers := make(http.Header)
	headers.Set("x-tokenhub-session-id", "sticky-session")
	routes := []RouteSelection{{
		Provider: Provider{ID: "prv_sticky", Type: "sticky_provider"},
	}}

	affinity, err := server.chatGatewayAffinity("key_sticky", headers, ChatCompletionRequest{Model: "sticky-model"}, routes)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil {
		t.Fatal("expected provider session affinity")
	}
	if affinity.AdapterType != "sticky_provider" || affinity.Kind != AffinityKindProviderSession {
		t.Fatalf("provider affinity metadata = %+v", affinity)
	}
	expected := deriveSessionAffinityKey(server.config.SecretKey, "key_sticky", codexBridgeProtocolChat+"\x00sticky-session")
	if affinity.KeyHash != expected {
		t.Fatalf("provider affinity key = %q, want %q", affinity.KeyHash, expected)
	}
}

func TestChatGatewayAffinityUsesPluginPolicyKind(t *testing.T) {
	server := New(NewMemoryStore())
	providerType := "legacy_sticky_provider"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.legacy-sticky",
		Name:    "Legacy Sticky Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider_policy", Name: "session_affinity_kind", Subject: providerType, Value: AffinityKindCodexSession},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityAffinity},
	}); err != nil {
		t.Fatalf("register plugin adapter: %v", err)
	}
	headers := make(http.Header)
	headers.Set("x-tokenhub-session-id", "sticky-session")
	routes := []RouteSelection{{
		Provider: Provider{ID: "prv_legacy_sticky", Type: providerType},
	}}

	affinity, err := server.chatGatewayAffinity("key_sticky", headers, ChatCompletionRequest{Model: "sticky-model"}, routes)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil || affinity.AdapterType != providerType || affinity.Kind != AffinityKindCodexSession {
		t.Fatalf("provider affinity metadata = %+v", affinity)
	}
}

func TestResponsesGatewayAffinityUsesDescriptorSelectedCodexPolicy(t *testing.T) {
	registry := NewAdapterRegistry()
	providerType := "plugin_codex_responses"
	if err := registry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.plugin-codex-responses",
		Name:    "Plugin Codex Responses",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider_policy", Name: "session_affinity_kind", Subject: providerType, Value: AffinityKindCodexSession},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityResponses, AdapterCapabilityAffinity},
	}); err != nil {
		t.Fatalf("register plugin adapter: %v", err)
	}
	descriptor, ok := registry.Describe(providerType)
	if !ok {
		t.Fatal("plugin adapter descriptor is missing")
	}

	var request ResponsesRequest
	if err := request.UnmarshalJSON([]byte(`{
		"model":"gpt-test",
		"input":[],
		"client_metadata":{"session_id":"codex-metadata-session"}
	}`)); err != nil {
		t.Fatal(err)
	}
	affinity, err := resolveProviderSessionAffinity(
		"secret",
		"key_alpha",
		providerType,
		descriptor.ProviderPolicy.SessionAffinityKind,
		make(http.Header),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil {
		t.Fatal("expected descriptor-selected Codex policy to consume client_metadata.session_id")
	}
	if affinity.AdapterType != providerType || affinity.Kind != AffinityKindCodexSession {
		t.Fatalf("descriptor-selected affinity metadata = %+v", affinity)
	}
}
