//go:build integration

package server

import (
	"context"
	"sync"
	"testing"

	"tokenhub/backend/internal/accountrouting"
)

func TestAccountDrainPostgresSharesAtomicState(t *testing.T) {
	storeA, storeB, _ := openSharedPostgresStores(t)
	poolID := NewID("account-pool")
	t.Cleanup(func() {
		if err := storeA.db.Where("kind = ? AND id = ?", accountRoutingStateKind, poolID).Delete(&AdminResource{}).Error; err != nil {
			t.Error(err)
		}
	})
	initial := accountRoutingAttempt{PoolID: poolID, Initial: "a"}
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	for _, store := range []*GormStore{storeA, storeB} {
		workers.Add(1)
		go func(store *GormStore) {
			defer workers.Done()
			_, err := store.recordAccountRoutingAttempt(context.Background(), initial, "a", false)
			errors <- err
		}(store)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	_, states, err := storeB.loadAccountRoutingData(context.Background(), []string{"synthetic-account"}, []string{poolID})
	if err != nil {
		t.Fatal(err)
	}
	current := states[poolID]
	if current.Current != "a" || current.Revision == "" {
		t.Fatalf("state not shared: %+v", current)
	}
	initial.Expected = current
	spent, err := storeA.recordAccountRoutingAttempt(context.Background(), initial, "a", true)
	if err != nil {
		t.Fatal(err)
	}
	if !spent.Past {
		t.Fatal("exhaustion not recorded")
	}
	next := accountRoutingAttempt{PoolID: poolID, Initial: "b", Expected: spent}
	chosen, err := storeB.recordAccountRoutingAttempt(context.Background(), next, "b", false)
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Current != "b" || chosen.Past {
		t.Fatalf("new account not selected: %+v", chosen)
	}
	// Even after a full cycle back to a, an old request cannot exhaust the
	// renewed account: the revision, not just the resource ID, must match.
	back := accountRoutingAttempt{PoolID: poolID, Initial: "a", Expected: chosen}
	renewed, err := storeB.recordAccountRoutingAttempt(context.Background(), back, "a", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeA.recordAccountRoutingAttempt(context.Background(), initial, "a", true); err != nil {
		t.Fatal(err)
	}
	_, states, err = storeA.loadAccountRoutingData(context.Background(), []string{"synthetic-account"}, []string{poolID})
	if err != nil {
		t.Fatal(err)
	}
	if states[poolID] != renewed || states[poolID].Past {
		t.Fatalf("late request changed a renewed pool: %+v", states[poolID])
	}
	if states[poolID] == (accountrouting.State{}) {
		t.Fatal("state disappeared")
	}
}
