package server

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

// newModelLabelStore returns a store seeded with the gpt-4.1-mini catalog entry
// and metrics attached, which is all knownModelLabel reads. No server is built,
// so nothing else is querying the database while these tests break it.
func newModelLabelStore(t *testing.T) *GormStore {
	t.Helper()
	store, _, _ := newResourceRoutedStore(t, ProviderMock)
	store.SetGatewayMetrics(NewGatewayMetrics(false))
	return store
}

// closeStoreDB makes every later query fail, standing in for a database that has
// become unreachable.
func closeStoreDB(t *testing.T, store *GormStore) {
	t.Helper()
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

// dropModelDirectly removes a model without going through the store, so the label
// snapshot is left pointing at a catalog that no longer contains it. That is how
// these tests tell a cached answer apart from a fresh query.
func dropModelDirectly(t *testing.T, store *GormStore, name string) {
	t.Helper()
	if err := store.db.Where("name = ?", name).Delete(&Model{}).Error; err != nil {
		t.Fatal(err)
	}
}

func expireModelLabels(store *GormStore) {
	store.modelLabels.mu.Lock()
	store.modelLabels.fetchedAt = time.Now().Add(-2 * modelLabelTTL)
	store.modelLabels.mu.Unlock()
}

// The label bound is what keeps a rejected request from minting a series per
// invented model name, so caching must not change which names survive.
func TestKnownModelLabelKeepsCatalogNamesAndCollapsesTheRest(t *testing.T) {
	store := newModelLabelStore(t)

	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("a catalog model must keep its name, got %q", got)
	}
	if got := store.knownModelLabel("definitely-not-a-model"); got != "unknown" {
		t.Fatalf("an unknown model must collapse, got %q", got)
	}
	if got := store.knownModelLabel("   "); got != "" {
		t.Fatalf("a blank model name must stay blank, got %q", got)
	}
}

// The point of the cache: a warm store answers without touching the database.
func TestKnownModelLabelAnswersFromSnapshotWithoutQuerying(t *testing.T) {
	store := newModelLabelStore(t)
	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("warm-up lookup = %q", got)
	}

	dropModelDirectly(t, store, "gpt-4.1-mini")

	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("a fresh snapshot must be reused, got %q from a re-read of the catalog", got)
	}
}

func TestKnownModelLabelRefreshesAfterTTL(t *testing.T) {
	store := newModelLabelStore(t)
	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("warm-up lookup = %q", got)
	}
	dropModelDirectly(t, store, "gpt-4.1-mini")

	expireModelLabels(store)

	if got := store.knownModelLabel("gpt-4.1-mini"); got != "unknown" {
		t.Fatalf("an expired snapshot must be reloaded, got %q", got)
	}
}

func TestKnownModelLabelFollowsModelMutations(t *testing.T) {
	store := newModelLabelStore(t)
	if got := store.knownModelLabel("added-model"); got != "unknown" {
		t.Fatalf("warm-up lookup = %q", got)
	}

	store.AddModel(Model{Name: "added-model", Modality: "chat", Status: StatusActive})
	if got := store.knownModelLabel("added-model"); got != "added-model" {
		t.Fatalf("AddModel must be visible at once, got %q", got)
	}

	if _, err := store.UpdateModel("added-model", Model{Name: "renamed-model"}); err != nil {
		t.Fatal(err)
	}
	if got := store.knownModelLabel("renamed-model"); got != "renamed-model" {
		t.Fatalf("the new name must be visible at once, got %q", got)
	}
	if got := store.knownModelLabel("added-model"); got != "unknown" {
		t.Fatalf("the old name must stop being a label, got %q", got)
	}

	if err := store.DeleteModel("renamed-model"); err != nil {
		t.Fatal(err)
	}
	if got := store.knownModelLabel("renamed-model"); got != "unknown" {
		t.Fatalf("DeleteModel must be visible at once, got %q", got)
	}
}

func TestKnownModelLabelFollowsCreateModelWithRoutes(t *testing.T) {
	store := newModelLabelStore(t)
	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("warm-up lookup = %q", got)
	}

	if _, err := store.CreateModelWithRoutes(Model{Name: "bundled-model", Modality: "chat", Status: StatusActive}, nil); err != nil {
		t.Fatal(err)
	}
	if got := store.knownModelLabel("bundled-model"); got != "bundled-model" {
		t.Fatalf("CreateModelWithRoutes must be visible at once, got %q", got)
	}
}

// A database that stops answering must not widen the label set, so the last good
// snapshot keeps serving.
func TestKnownModelLabelKeepsSnapshotWhenRefreshFails(t *testing.T) {
	store := newModelLabelStore(t)
	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("warm-up lookup = %q", got)
	}

	expireModelLabels(store)
	closeStoreDB(t, store)

	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("a failed refresh must keep the last snapshot, got %q", got)
	}
	if got := store.knownModelLabel("definitely-not-a-model"); got != "unknown" {
		t.Fatalf("a failed refresh must not widen the label set, got %q", got)
	}
}

// With no snapshot and no database there is nothing to answer from: the label must
// collapse rather than pass client input through, and — the whole point of the
// cache — the rejection path must stop querying instead of letting a client
// hammering invented names amplify into a query per request.
func TestKnownModelLabelStopsQueryingWhenTheCatalogIsUnreachable(t *testing.T) {
	store := newModelLabelStore(t)
	var queries atomic.Int64
	if err := store.db.Callback().Query().Before("gorm:query").Register("test:count_queries", func(tx *gorm.DB) {
		queries.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	closeStoreDB(t, store)

	for i := 0; i < 10; i++ {
		if got := store.knownModelLabel(fmt.Sprintf("attacker-model-%d", i)); got != "unknown" {
			t.Fatalf("an unreachable catalog must collapse the label, got %q", got)
		}
	}
	if got := queries.Load(); got > 1 {
		t.Fatalf("a failed refresh must not put a query back on every rejected request, got %d queries across 10 requests", got)
	}
}

// While there is no cache object at all, the single row check still has to bound
// the label correctly.
func TestKnownModelLabelFallsBackToSingleRowCheck(t *testing.T) {
	store := newModelLabelStore(t)
	store.modelLabels = nil

	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("the fallback must keep a catalog model, got %q", got)
	}
	if got := store.knownModelLabel("definitely-not-a-model"); got != "unknown" {
		t.Fatalf("the fallback must collapse an unknown model, got %q", got)
	}
}

// fakeClock drives the cache's TTL and backoff without sleeping, and lets a load
// function bill its own duration to the clock.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func TestModelLabelCacheReloadsOnlyWhenStale(t *testing.T) {
	cache := newModelLabelCache()
	clock := &fakeClock{now: time.Now()}
	loads := 0
	load := func() ([]string, error) {
		loads++
		return []string{"alpha"}, nil
	}

	if known, resolved := cache.lookup("alpha", clock.Now, load); !known || !resolved {
		t.Fatalf("first lookup = (%v, %v)", known, resolved)
	}
	clock.advance(modelLabelTTL - time.Millisecond)
	if known, _ := cache.lookup("beta", clock.Now, load); known {
		t.Fatal("beta is not in the snapshot")
	}
	if loads != 1 {
		t.Fatalf("a fresh snapshot must be reused, loaded %d times", loads)
	}

	clock.advance(time.Millisecond)
	if _, resolved := cache.lookup("alpha", clock.Now, load); !resolved {
		t.Fatal("expected the expired snapshot to resolve after a reload")
	}
	if loads != 2 {
		t.Fatalf("an expired snapshot must reload, loaded %d times", loads)
	}

	cache.invalidate()
	if _, resolved := cache.lookup("alpha", clock.Now, load); !resolved {
		t.Fatal("expected an invalidated cache to resolve after a reload")
	}
	if loads != 3 {
		t.Fatalf("invalidate must force a reload, loaded %d times", loads)
	}
}

// A snapshot is fresh for a TTL counted from when the load returned, so a slow
// load does not come back already expired.
func TestModelLabelCacheTTLStartsWhenTheRefreshReturns(t *testing.T) {
	cache := newModelLabelCache()
	clock := &fakeClock{now: time.Now()}
	loads := 0
	slowLoad := func() ([]string, error) {
		loads++
		clock.advance(2 * modelLabelTTL)
		return []string{"alpha"}, nil
	}

	if known, resolved := cache.lookup("alpha", clock.Now, slowLoad); !known || !resolved {
		t.Fatalf("first lookup = (%v, %v)", known, resolved)
	}
	if _, resolved := cache.lookup("alpha", clock.Now, slowLoad); !resolved {
		t.Fatal("expected the snapshot to resolve")
	}
	if loads != 1 {
		t.Fatalf("a slow load must still produce a fresh snapshot, loaded %d times", loads)
	}
}

// A refresh that fails must not be retried on every call, or a database outage
// puts the query back on the rejection path the cache exists to protect.
func TestModelLabelCacheBacksOffAfterFailedRefresh(t *testing.T) {
	cache := newModelLabelCache()
	clock := &fakeClock{now: time.Now()}
	loads := 0
	failing := func() ([]string, error) {
		loads++
		return nil, errors.New("database unavailable")
	}

	if known, _ := cache.lookup("alpha", clock.Now, failing); known {
		t.Fatal("a cache that never loaded must not vouch for a model")
	}
	clock.advance(modelLabelRetryBackoff - time.Millisecond)
	if known, _ := cache.lookup("alpha", clock.Now, failing); known {
		t.Fatal("a cache that never loaded must not vouch for a model")
	}
	if loads != 1 {
		t.Fatalf("a failed refresh must back off, loaded %d times", loads)
	}

	clock.advance(time.Millisecond)
	if known, _ := cache.lookup("alpha", clock.Now, failing); known {
		t.Fatal("a cache that never loaded must not vouch for a model")
	}
	if loads != 2 {
		t.Fatalf("the backoff must expire, loaded %d times", loads)
	}
}

// A failing database usually fails slowly, which is exactly when the backoff has
// to hold: timed from before the query it would come back already elapsed, and
// every rejected request would go back to waiting on a doomed query.
func TestModelLabelCacheBackoffStartsWhenTheFailedRefreshReturns(t *testing.T) {
	cache := newModelLabelCache()
	clock := &fakeClock{now: time.Now()}
	loads := 0
	slowFailing := func() ([]string, error) {
		loads++
		clock.advance(2 * modelLabelRetryBackoff)
		return nil, errors.New("database unavailable")
	}

	cache.lookup("alpha", clock.Now, slowFailing)
	cache.lookup("alpha", clock.Now, slowFailing)
	if loads != 1 {
		t.Fatalf("the backoff must start when the failed refresh returns, loaded %d times", loads)
	}

	clock.advance(modelLabelRetryBackoff)
	cache.lookup("alpha", clock.Now, slowFailing)
	if loads != 2 {
		t.Fatalf("the backoff must still expire, loaded %d times", loads)
	}
}

// A store that came up against a dead database must not stay collapsed: the first
// lookup after the backoff rebuilds the snapshot and the labels come back.
func TestModelLabelCacheRecoversWhenTheDatabaseReturns(t *testing.T) {
	cache := newModelLabelCache()
	clock := &fakeClock{now: time.Now()}
	healthy := false
	load := func() ([]string, error) {
		if !healthy {
			return nil, errors.New("database unavailable")
		}
		return []string{"alpha"}, nil
	}

	if known, _ := cache.lookup("alpha", clock.Now, load); known {
		t.Fatal("a cache that never loaded must not vouch for a model")
	}

	healthy = true
	if known, _ := cache.lookup("alpha", clock.Now, load); known {
		t.Fatal("the backoff must hold even once the database is back")
	}

	clock.advance(modelLabelRetryBackoff)
	known, resolved := cache.lookup("alpha", clock.Now, load)
	if !known || !resolved {
		t.Fatalf("a recovered database must restore the label, got (%v, %v)", known, resolved)
	}
}

func TestModelLabelCacheServesStaleSnapshotThroughFailure(t *testing.T) {
	cache := newModelLabelCache()
	clock := &fakeClock{now: time.Now()}
	if known, resolved := cache.lookup("alpha", clock.Now, func() ([]string, error) {
		return []string{"alpha"}, nil
	}); !known || !resolved {
		t.Fatalf("first lookup = (%v, %v)", known, resolved)
	}

	failing := func() ([]string, error) { return nil, errors.New("database unavailable") }
	clock.advance(modelLabelTTL)
	known, resolved := cache.lookup("alpha", clock.Now, failing)
	if !known || !resolved {
		t.Fatalf("a failed refresh must serve the stale snapshot, got (%v, %v)", known, resolved)
	}
	if known, _ := cache.lookup("beta", clock.Now, failing); known {
		t.Fatal("a failed refresh must not widen the snapshot")
	}
}

func TestKnownModelLabelIsRaceFreeUnderConcurrentMutation(t *testing.T) {
	store := newModelLabelStore(t)

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				store.knownModelLabel("gpt-4.1-mini")
				store.knownModelLabel("definitely-not-a-model")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			store.AddModel(Model{Name: "churn-model", Modality: "chat", Status: StatusActive})
			_ = store.DeleteModel("churn-model")
		}
	}()
	wg.Wait()

	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("the seeded model must survive the churn, got %q", got)
	}
}
