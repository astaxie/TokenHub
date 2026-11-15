//go:build integration

package server

import (
	"encoding/json"
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

func TestProviderLocalSpendCapPostgresMultiInstance(t *testing.T) {
	first, second, _ := openSharedPostgresStores(t)
	provider := first.AddProvider(Provider{ID: NewID("local-cap-pg"), Name: "Synthetic local cap", Type: ProviderMock, Status: StatusActive})
	t.Cleanup(func() { _ = first.DeleteProvider(provider.ID) })
	resource, err := first.AddProviderResource(ProviderResource{ID: NewID("local-cap-pg"), ProviderID: provider.ID, Name: "Synthetic account", Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := first.saveProviderAllowanceConfig(t.Context(), resource.ID, providerAllowanceConfig{Policy: allowance.Policy{SpendCap: &allowance.SpendCap{USD: 10, Period: "total"}, CapStop: true}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := first.db.Create(&UsageRecord{ID: NewID("usage"), ProviderResourceID: resource.ID, ProviderCostUSD: 12, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := first.refreshManualAllowance(t.Context(), resource.ID, cfg, now); err != nil {
		t.Fatal(err)
	}
	for _, failed := range []bool{false, true} {
		vendor := allowance.Snapshot{Version: allowance.Version, FetchedAt: now.Add(-11 * time.Minute)}
		if failed {
			vendor.Status, vendor.ErrorCode = allowance.QueryError, "allowance_query_failed"
		}
		raw, _ := json.Marshal(map[string]any{"allowance": vendor})
		if err := first.SaveProviderResourceQuota(resource.ID, ProviderMock, string(raw), now); err != nil {
			t.Fatal(err)
		}
		data, _, err := second.loadAccountRoutingData(t.Context(), []string{resource.ID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		entry := data[resource.ID]
		if d := allowance.Evaluate(entry.Allowance, entry.Policy, "model", now, accountAllowanceMaxAge); !d.Held || d.Reason != "usage_cap" {
			t.Fatalf("shared local cap bypassed: failed=%v decision=%+v", failed, d)
		}
	}
}
