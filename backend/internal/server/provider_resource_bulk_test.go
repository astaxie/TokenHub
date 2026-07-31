package server

import (
	"testing"
	"time"
)

func TestProviderResourceBulkOperationsPreserveActiveLeases(t *testing.T) {
	store := NewMemoryStore()
	provider := mustAddProvider(t, store, Provider{
		Name: "Lease Provider", Type: ProviderMock, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		Name: "Lease Resource", ProviderID: provider.ID, ResourceType: "api_key",
		Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, action := range []string{"clear_error", "reset_usage"} {
		t.Run(action, func(t *testing.T) {
			now := time.Now().UTC()
			expired := InFlightLease{
				ID: action + "_expired", ScopeType: "provider_resource",
				ScopeID: resource.ID, ExpiresAt: now.Add(-time.Minute),
			}
			active := InFlightLease{
				ID: action + "_active", ScopeType: "provider_resource",
				ScopeID: resource.ID, ExpiresAt: now.Add(time.Minute),
			}
			if err := store.db.Create([]InFlightLease{expired, active}).Error; err != nil {
				t.Fatal(err)
			}

			result, err := store.BulkOperateProviderResources(action, []string{resource.ID})
			if err != nil {
				t.Fatal(err)
			}
			if result.Success != 1 || result.Failed != 0 {
				t.Fatalf("%s result = %+v", action, result)
			}
			assertInFlightLeaseExists(t, store, expired.ID, false)
			assertInFlightLeaseExists(t, store, active.ID, true)
			if err := store.db.Delete(&InFlightLease{}, "id = ?", active.ID).Error; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertInFlightLeaseExists(t testing.TB, store *GormStore, leaseID string, want bool) {
	t.Helper()
	var count int64
	if err := store.db.Model(&InFlightLease{}).Where("id = ?", leaseID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Fatalf("lease %s existence = %t, want %t", leaseID, got, want)
	}
}
