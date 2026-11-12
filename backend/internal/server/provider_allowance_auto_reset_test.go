package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"tokenhub/backend/internal/allowance"
)

func TestAutomaticAllowanceResetUsesDurableWeeklyBound(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown_outcome_%v", unknown), func(t *testing.T) {
			upstream := &quotaResetUpstream{availableCount: 1, creditID: "synthetic-auto-card"}
			if unknown {
				upstream.consumeStatus = http.StatusBadGateway
				upstream.consumeBody = `{"error":"synthetic transport failure"}`
			}
			server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			cfg, err := store.saveProviderAllowanceConfig(t.Context(), quotaResetTestResourceID, providerAllowanceConfig{Policy: allowance.Policy{Reset: allowance.ResetPolicy{OnExhausted: true, WeeklyLimit: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			resource, _ := store.GetProviderResource(quotaResetTestResourceID)
			provider, _ := store.GetProvider(resource.ProviderID)
			route := RouteSelection{Provider: provider, Resource: &resource, ProviderModel: "synthetic-model"}
			call := automaticResetCallFixture(t, store, provider, resource)
			seed := func() {
				t.Helper()
				now := time.Now().UTC()
				raw := fmt.Sprintf(`{"rate_limit":{"limit_reached":true,"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":%d}},"rate_limit_reset_credits":{"available_count":1}}`, now.Add(7*24*time.Hour).Unix())
				if err := store.SaveProviderResourceQuota(resource.ID, provider.Type, raw, now); err != nil {
					t.Fatal(err)
				}
			}
			seed()
			recordAllowanceResetIntent(t.Context(), store, call, route, []RouteSelection{route})
			if !server.allowanceResetIntentCurrent(t.Context(), resource.ID, time.Now().UTC()) {
				t.Fatal("authorized exhausted intent is not current")
			}

			server.refreshAllowanceEntitlements(t.Context(), resource, cfg)
			if upstream.consumeCalls != 1 {
				t.Fatalf("eligible weekly reset not consumed once: %d", upstream.consumeCalls)
			}
			if !unknown {
				upstream.creditID = "synthetic-next-card"
			}
			seed()
			recordAllowanceResetIntent(t.Context(), store, call, route, []RouteSelection{route})
			server.refreshAllowanceEntitlements(t.Context(), resource, cfg)
			if upstream.consumeCalls != 1 {
				t.Fatal("weekly bound or unknown outcome permitted another automatic spend")
			}
		})
	}
}

func TestAutomaticAllowanceResetDoesNotBypassUserCap(t *testing.T) {
	upstream := &quotaResetUpstream{availableCount: 1, creditID: "synthetic-held-card"}
	server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	cfg, err := store.saveProviderAllowanceConfig(t.Context(), quotaResetTestResourceID, providerAllowanceConfig{Policy: allowance.Policy{Cap: allowance.Cap{Percent: 70}, Reset: allowance.ResetPolicy{OnExhausted: true}}})
	if err != nil {
		t.Fatal(err)
	}
	resource, _ := store.GetProviderResource(quotaResetTestResourceID)
	provider, _ := store.GetProvider(resource.ProviderID)
	route := RouteSelection{Provider: provider, Resource: &resource, ProviderModel: "synthetic-model"}
	now := time.Now().UTC()
	raw := fmt.Sprintf(`{"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":%d}}}`, now.Add(7*24*time.Hour).Unix())
	if err := store.SaveProviderResourceQuota(resource.ID, provider.Type, raw, now); err != nil {
		t.Fatal(err)
	}
	recordAllowanceResetIntent(t.Context(), store, automaticResetCallFixture(t, store, provider, resource), route, []RouteSelection{route})
	server.refreshAllowanceEntitlements(t.Context(), resource, cfg)
	if upstream.consumeCalls != 0 {
		t.Fatal("user cap consumed a reset")
	}
}

func automaticResetCallFixture(t *testing.T, store *GormStore, provider Provider, resource ProviderResource) CallContext {
	t.Helper()
	project := store.CreateProject(Project{Name: "Synthetic reset project", Status: StatusActive})
	model := store.AddModel(Model{Name: "synthetic-model", Modality: "chat", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Synthetic reset key", Allowed: []string{model.Name}, Status: StatusActive}, "synthetic-reset-project-key")
	if err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{ID: "synthetic-reset-route", ModelName: model.Name, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: model.Name, Status: StatusActive, Priority: 1, Weight: 100})
	return CallContext{Project: project, Key: key, Model: model}
}

func TestAutomaticAllowanceResetRechecksRevokedAPIKey(t *testing.T) {
	upstream := &quotaResetUpstream{availableCount: 1, creditID: "synthetic-revoked-card"}
	server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	cfg, err := store.saveProviderAllowanceConfig(t.Context(), quotaResetTestResourceID, providerAllowanceConfig{Policy: allowance.Policy{Reset: allowance.ResetPolicy{OnExhausted: true}}})
	if err != nil {
		t.Fatal(err)
	}
	resource, _ := store.GetProviderResource(quotaResetTestResourceID)
	provider, _ := store.GetProvider(resource.ProviderID)
	call := automaticResetCallFixture(t, store, provider, resource)
	route := RouteSelection{Provider: provider, Resource: &resource, ProviderModel: call.Model.Name}
	now := time.Now().UTC()
	raw := fmt.Sprintf(`{"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":%d}}}`, now.Add(7*24*time.Hour).Unix())
	if err := store.SaveProviderResourceQuota(resource.ID, provider.Type, raw, now); err != nil {
		t.Fatal(err)
	}
	recordAllowanceResetIntent(t.Context(), store, call, route, []RouteSelection{route})
	if !server.allowanceResetIntentCurrent(t.Context(), resource.ID, now) {
		t.Fatal("valid intent was not established")
	}
	if _, err := store.UpdateAPIKey(call.Key.ID, APIKey{Status: StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	server.refreshAllowanceEntitlements(t.Context(), resource, cfg)
	if upstream.consumeCalls != 0 {
		t.Fatal("revoked credential authorized an automatic reset")
	}
}
