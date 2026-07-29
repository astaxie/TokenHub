package server

import (
	"context"
	"testing"
	"time"
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
