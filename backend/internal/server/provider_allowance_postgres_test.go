//go:build integration

package server

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"tokenhub/backend/internal/allowance"
)

func TestProviderAllowancePostgresMultiInstance(t *testing.T) {
	first, second, config := openSharedPostgresStores(t)
	if err := VerifySchemaSemantics(t.Context(), config.DatabaseURL); err != nil {
		t.Fatal(err)
	}
	provider := first.AddProvider(Provider{ID: NewID("allowance-pg"), Name: "Synthetic allowance", Type: ProviderMock, Status: StatusActive})
	t.Cleanup(func() { _ = first.DeleteProvider(provider.ID) })
	resource, err := first.AddProviderResource(ProviderResource{ID: NewID("allowance-pg"), ProviderID: provider.ID, Name: "Synthetic account", Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := first.saveProviderAllowanceConfig(t.Context(), resource.ID, providerAllowanceConfig{Policy: allowance.Policy{Cap: allowance.Cap{Percent: 50}}})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	for i, store := range []*GormStore{first, second} {
		workers.Go(func() {
			_, err := store.saveProviderAllowanceConfig(t.Context(), resource.ID, providerAllowanceConfig{Revision: initial.Revision, Policy: allowance.Policy{Cap: allowance.Cap{Percent: float64(60 + i)}}})
			errors <- err
		})
	}
	workers.Wait()
	close(errors)
	succeeded, conflicted := 0, 0
	for err := range errors {
		if err == nil {
			succeeded++
		} else if AsHTTPError(err).Code == "allowance_config_changed" {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent updates: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	save := func(store *GormStore, balance float64, at time.Time) error {
		raw := fmt.Sprintf(`{"windows":[{"name":"Wallet","balance":%v,"unit":"USD"}]}`, balance)
		return store.SaveProviderResourceQuota(resource.ID, ProviderMock, raw, at)
	}
	if err := save(first, 100, base); err != nil {
		t.Fatal(err)
	}
	errors = make(chan error, 20)
	for i := range 20 {
		workers.Go(func() {
			store := []*GormStore{first, second}[i%2]
			errors <- save(store, 80, base.Add(2*time.Second))
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, reading := range []struct {
		balance float64
		offset  time.Duration
	}{{90, time.Second}, {200, 3 * time.Second}, {190, 4 * time.Second}} {
		if err := save(second, reading.balance, base.Add(reading.offset)); err != nil {
			t.Fatal(err)
		}
	}
	var counter providerAllowanceWalletCounter
	if err := first.db.First(&counter, "resource_id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if counter.Spent != 30 || counter.PreviousBalance != 190 {
		t.Fatalf("duplicate, stale, or top-up readings corrupted wallet spending: %+v", counter)
	}
}
