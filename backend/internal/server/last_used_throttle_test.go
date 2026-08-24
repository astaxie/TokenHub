package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

// newTestLastUsedThrottle returns a throttle reading the caller's clock, so
// tests can move time without sleeping.
func newTestLastUsedThrottle(clock *time.Time) *lastUsedThrottle {
	throttle := newLastUsedThrottle()
	throttle.now = func() time.Time { return *clock }
	return throttle
}

func lastUsedThrottleKeys(throttle *lastUsedThrottle) map[string]bool {
	keys := map[string]bool{}
	throttle.entries.Range(func(key, _ any) bool {
		keys[key.(string)] = true
		return true
	})
	return keys
}

func TestLastUsedThrottleWritesOncePerWindow(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)
	writes := 0
	write := func() error {
		writes++
		return nil
	}

	if err := throttle.mark("route:a", write); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("first mark should write, got %d writes", writes)
	}

	clock = clock.Add(lastUsedThrottleWindow - time.Second)
	if err := throttle.mark("route:a", write); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("a mark inside the window should be suppressed, got %d writes", writes)
	}

	clock = clock.Add(2 * time.Second)
	if err := throttle.mark("route:a", write); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Fatalf("a mark past the window should write, got %d writes", writes)
	}
}

func TestLastUsedThrottleClosesWindowAtWriteCompletion(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)

	// A write taking most of the window must not let the next one fire right
	// after it: the window runs from completion, not from the claim.
	if err := throttle.mark("route:slow", func() error {
		clock = clock.Add(lastUsedThrottleWindow - time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	writes := 0
	if err := throttle.mark("route:slow", func() error {
		writes++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("window should be measured from write completion, got %d writes", writes)
	}
}

func TestLastUsedThrottleNamespacesAreIndependent(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)
	writes := map[string]int{}
	mark := func(key string) {
		if err := throttle.mark(key, func() error {
			writes[key]++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	const id = "shared_id"
	mark(lastUsedAPIKeyKey(id))
	mark(lastUsedRouteKey(id))
	mark(lastUsedResourceKey(id))

	for _, key := range []string{lastUsedAPIKeyKey(id), lastUsedRouteKey(id), lastUsedResourceKey(id)} {
		if writes[key] != 1 {
			t.Fatalf("object kinds sharing an id must throttle independently: %s wrote %d times", key, writes[key])
		}
	}
}

func TestLastUsedThrottleFailedWriteBacksOffAndRetries(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)
	failure := errors.New("write failed")
	attempts := 0

	if err := throttle.mark("key:a", func() error {
		attempts++
		return failure
	}); !errors.Is(err, failure) {
		t.Fatalf("mark should surface the write error, got %v", err)
	}

	if err := throttle.mark("key:a", func() error {
		attempts++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("a failed write should suppress attempts inside the backoff, got %d", attempts)
	}

	clock = clock.Add(lastUsedThrottleFailureBackoff)
	if err := throttle.mark("key:a", func() error {
		attempts++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("a failed write should retry after the backoff, got %d attempts", attempts)
	}
}

func TestLastUsedThrottleFailedWriteReplacesCommittedTimestamp(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)

	if err := throttle.mark("key:a", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	// Fail a write past the window, then confirm the in-flight placeholder was
	// replaced by an explicit failed-at state rather than the stale success.
	clock = clock.Add(2 * lastUsedThrottleWindow)
	failure := errors.New("write failed")
	if err := throttle.mark("key:a", func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("mark should surface the write error, got %v", err)
	}
	value, ok := throttle.entries.Load("key:a")
	if !ok {
		t.Fatal("a failed write should remain tracked during its backoff")
	}
	failed, ok := value.(lastUsedFailedAt)
	if !ok || !failed.at.Equal(clock) {
		t.Fatalf("expected a failed-at state for %v, got %#v", clock, value)
	}
}

func TestLastUsedThrottleBacksOffAfterPanic(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("mark should propagate a panic from the write")
			}
		}()
		_ = throttle.mark("route:panic", func() error { panic("write exploded") })
	}()

	writes := 0
	if err := throttle.mark("route:panic", func() error {
		writes++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("a panicking write should start a failure backoff, got %d writes", writes)
	}

	clock = clock.Add(lastUsedThrottleFailureBackoff)
	if err := throttle.mark("route:panic", func() error {
		writes++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("a panicking write must become retryable after the backoff, got %d writes", writes)
	}
}

func TestLastUsedThrottleConcurrentMarksWriteOnce(t *testing.T) {
	throttle := newLastUsedThrottle()
	var writes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := throttle.mark("route:hot", func() error {
				writes.Add(1)
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := writes.Load(); got != 1 {
		t.Fatalf("a burst of concurrent marks should produce one write, got %d", got)
	}
}

func TestLastUsedThrottleSuppressesMarksWhileAWriteIsInFlight(t *testing.T) {
	throttle := newLastUsedThrottle()
	var writes atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})

	var winner sync.WaitGroup
	winner.Add(1)
	go func() {
		defer winner.Done()
		if err := throttle.mark("route:hot", func() error {
			writes.Add(1)
			close(entered)
			<-release
			return nil
		}); err != nil {
			t.Error(err)
		}
	}()
	<-entered

	var others sync.WaitGroup
	for i := 0; i < 32; i++ {
		others.Add(1)
		go func() {
			defer others.Done()
			if err := throttle.mark("route:hot", func() error {
				writes.Add(1)
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	others.Wait()
	if got := writes.Load(); got != 1 {
		t.Fatalf("an in-flight write should suppress concurrent marks, got %d writes", got)
	}

	close(release)
	winner.Wait()
	if got := writes.Load(); got != 1 {
		t.Fatalf("expected exactly one write, got %d", got)
	}
}

func TestLastUsedThrottleEntryCountTracksTheMap(t *testing.T) {
	throttle := newLastUsedThrottle()
	throttle.window = 0 // successful marks can claim again immediately
	throttle.failureBackoff = 0
	failure := errors.New("write failed")

	var wg sync.WaitGroup
	for key := 0; key < 8; key++ {
		for attempt := 0; attempt < 16; attempt++ {
			wg.Add(1)
			go func(key int, fail bool) {
				defer wg.Done()
				_ = throttle.mark(fmt.Sprintf("route:%d", key), func() error {
					if fail {
						return failure
					}
					return nil
				})
			}(key, attempt%2 == 0)
		}
	}
	wg.Wait()

	if got, want := throttle.count.Load(), int64(len(lastUsedThrottleKeys(throttle))); got != want {
		t.Fatalf("entry count drifted from the map: count=%d entries=%d", got, want)
	}
}

func TestLastUsedThrottleStopsTrackingAtCapacity(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)
	throttle.maxEntries = 8

	writes := 0
	write := func() error {
		writes++
		return nil
	}
	for i := 0; i < 64; i++ {
		if err := throttle.mark(fmt.Sprintf("route:%d", i), write); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 64 {
		t.Fatalf("every distinct object should be written once, got %d writes", writes)
	}
	if got := int64(len(lastUsedThrottleKeys(throttle))); got > throttle.maxEntries {
		t.Fatalf("the throttle must not track more than %d entries, got %d", throttle.maxEntries, got)
	}

	// A tracked key is still throttled, and a key the throttle refused to track
	// simply writes through.
	if err := throttle.mark("route:0", write); err != nil {
		t.Fatal(err)
	}
	if writes != 64 {
		t.Fatalf("a tracked key should stay throttled at capacity, got %d writes", writes)
	}
	if err := throttle.mark("route:63", write); err != nil {
		t.Fatal(err)
	}
	if writes != 65 {
		t.Fatalf("an untracked key should keep writing through, got %d writes", writes)
	}
}

func TestLastUsedThrottleCapacityHoldsUnderConcurrency(t *testing.T) {
	throttle := newLastUsedThrottle()
	throttle.maxEntries = 16

	var wg sync.WaitGroup
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = throttle.mark(fmt.Sprintf("route:%d", i), func() error { return nil })
		}(i)
	}
	wg.Wait()

	entries := int64(len(lastUsedThrottleKeys(throttle)))
	if entries > throttle.maxEntries {
		t.Fatalf("concurrent admissions overshot the ceiling: %d entries for max %d", entries, throttle.maxEntries)
	}
	if got := throttle.count.Load(); got != entries {
		t.Fatalf("entry count drifted from the map: count=%d entries=%d", got, entries)
	}
}

func TestLastUsedThrottleSweepDropsIdleEntries(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)
	throttle.entryTTL = time.Minute

	for _, key := range []string{"route:idle_a", "route:idle_b", "route:live"} {
		if err := throttle.mark(key, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	failure := errors.New("write failed")
	if err := throttle.mark("route:failed", func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("expected failed mark, got %v", err)
	}
	// An in-flight claim must survive the sweep, otherwise the writer would have
	// no place to record its outcome.
	if _, ok := throttle.shouldWrite("route:in_flight"); !ok {
		t.Fatal("a fresh key should be writable: route:in_flight")
	}

	clock = clock.Add(2 * time.Minute)
	if err := throttle.mark("route:live", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	throttle.sweep(clock)

	remaining := lastUsedThrottleKeys(throttle)
	if len(remaining) != 2 || !remaining["route:live"] || !remaining["route:in_flight"] {
		t.Fatalf("sweep should drop only entries idle past the TTL, kept %v", remaining)
	}
	if got := throttle.count.Load(); got != 2 {
		t.Fatalf("sweep should keep the entry count in step with the map, got %d", got)
	}
}

func TestLastUsedThrottleSweepsAtMostOncePerInterval(t *testing.T) {
	clock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	throttle := newTestLastUsedThrottle(&clock)
	throttle.entryTTL = time.Minute
	throttle.sweepEvery = time.Hour

	if err := throttle.mark("route:first", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(5 * time.Minute)
	throttle.maybeSweep(clock)
	if _, ok := throttle.entries.Load("route:first"); ok {
		t.Fatal("the first sweep should drop an entry idle past the TTL")
	}

	if err := throttle.mark("route:second", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(5 * time.Minute)
	throttle.maybeSweep(clock)
	if _, ok := throttle.entries.Load("route:second"); !ok {
		t.Fatal("sweeps should be rate limited, not run on every attempt")
	}

	clock = clock.Add(time.Hour)
	throttle.maybeSweep(clock)
	if _, ok := throttle.entries.Load("route:second"); ok {
		t.Fatal("the next eligible sweep should drop the stale entry")
	}
}

func TestWithContextSharesLastUsedThrottle(t *testing.T) {
	store := NewMemoryStore()
	scoped := store.WithContext(context.Background())
	if scoped.lastUsed != store.lastUsed {
		t.Fatal("a context-scoped store must share the last_used throttle, or every request would write")
	}
}

func TestValidateAPIKeyThrottlesLastUsedWrites(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Last Used Throttle"})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:   "throttled-key",
		Status: StatusActive,
	}, "thk_last_used_throttle")
	if err != nil {
		t.Fatal(err)
	}

	if _, validated, err := store.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
		t.Fatal(err)
	} else if validated.LastUsedAt == nil {
		t.Fatal("the first validation should report the key as used")
	}
	first := persistedAPIKeyLastUsed(t, store, key.ID)
	if first == nil {
		t.Fatal("the first validation should persist last_used_at")
	}

	if _, validated, err := store.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
		t.Fatal(err)
	} else if validated.LastUsedAt == nil || !validated.LastUsedAt.After(*first) {
		t.Fatalf("the returned copy should always report the current request, got %v", validated.LastUsedAt)
	}
	if second := persistedAPIKeyLastUsed(t, store, key.ID); second == nil || !second.Equal(*first) {
		t.Fatalf("a validation inside the window should not rewrite last_used_at: %v then %v", first, second)
	}

	expireLastUsedThrottle(store, lastUsedAPIKeyKey(key.ID))
	if _, _, err := store.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if third := persistedAPIKeyLastUsed(t, store, key.ID); third == nil || !third.After(*first) {
		t.Fatalf("a validation past the window should rewrite last_used_at: %v then %v", first, third)
	}
}

func TestValidateAPIKeySucceedsWhenLastUsedWriteFails(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Last Used Write Failure"})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:   "failing-key",
		Status: StatusActive,
	}, "thk_last_used_failure")
	if err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int64
	failure := errors.New("simulated last_used_at write failure")
	if err := store.db.Callback().Update().Before("gorm:update").
		Register("test:fail_api_key_last_used", func(tx *gorm.DB) {
			if tx.Statement.Table == "api_keys" {
				attempts.Add(1)
				_ = tx.AddError(failure)
			}
		}); err != nil {
		t.Fatal(err)
	}

	if _, validated, err := store.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
		t.Fatalf("a failed display-only write must not reject a valid key: %v", err)
	} else if validated.ID != key.ID {
		t.Fatalf("expected key %s, got %s", key.ID, validated.ID)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected one last_used_at write attempt, got %d", got)
	}
	if persisted := persistedAPIKeyLastUsed(t, store, key.ID); persisted != nil {
		t.Fatalf("the failed write should not have persisted last_used_at, got %v", persisted)
	}

	// The failed-at state suppresses both the retry and its caller-side log until
	// the backoff expires.
	if _, _, err := store.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("a failed write should not retry inside the backoff, got %d attempts", got)
	}

	if err := store.db.Callback().Update().Remove("test:fail_api_key_last_used"); err != nil {
		t.Fatal(err)
	}
	store.lastUsed.entries.Store(lastUsedAPIKeyKey(key.ID), lastUsedFailedAt{
		at: time.Now().Add(-2 * lastUsedThrottleFailureBackoff),
	})
	if _, _, err := store.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if persisted := persistedAPIKeyLastUsed(t, store, key.ID); persisted == nil {
		t.Fatal("a recovered write should persist last_used_at")
	}
}

func TestMarkRouteAndProviderResourceUsedAreThrottled(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_throttle", Name: "Throttle Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_throttle", ProviderID: provider.ID, Name: "Throttle Resource",
		ResourceType: "mock", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "throttle-model", Modality: "chat", Status: StatusActive})
	route := store.AddRoute(ModelRoute{
		ID: "route_throttle", ModelName: "throttle-model", ProviderID: provider.ID,
		ProviderResourceID: resource.ID, ProviderModel: "throttle-model",
		Priority: 1, Weight: 100, Status: StatusActive,
	})

	store.MarkRouteUsed(route.ID)
	store.MarkProviderResourceUsed(resource.ID)
	routeFirst := persistedRouteLastUsed(t, store, route.ID)
	resourceFirst := persistedResourceLastUsed(t, store, resource.ID)
	if routeFirst == nil || resourceFirst == nil {
		t.Fatalf("the first mark should persist last_used_at: route=%v resource=%v", routeFirst, resourceFirst)
	}

	store.MarkRouteUsed(route.ID)
	store.MarkProviderResourceUsed(resource.ID)
	if got := persistedRouteLastUsed(t, store, route.ID); got == nil || !got.Equal(*routeFirst) {
		t.Fatalf("a route mark inside the window should be suppressed: %v then %v", routeFirst, got)
	}
	if got := persistedResourceLastUsed(t, store, resource.ID); got == nil || !got.Equal(*resourceFirst) {
		t.Fatalf("a resource mark inside the window should be suppressed: %v then %v", resourceFirst, got)
	}

	expireLastUsedThrottle(store, lastUsedRouteKey(route.ID), lastUsedResourceKey(resource.ID))
	store.MarkRouteUsed(route.ID)
	store.MarkProviderResourceUsed(resource.ID)
	if got := persistedRouteLastUsed(t, store, route.ID); got == nil || !got.After(*routeFirst) {
		t.Fatalf("a route mark past the window should rewrite last_used_at: %v then %v", routeFirst, got)
	}
	if got := persistedResourceLastUsed(t, store, resource.ID); got == nil || !got.After(*resourceFirst) {
		t.Fatalf("a resource mark past the window should rewrite last_used_at: %v then %v", resourceFirst, got)
	}
}

func TestCompleteImageJobMarksRouteAndResourceUsed(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Last Used"})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "image-last-used-key", Allowed: []string{openAIImageModelName}, Status: StatusActive,
	}, "thk_image_last_used")
	if err != nil {
		t.Fatal(err)
	}
	model := store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_image_last_used", Name: "Image Provider", Type: ProviderOpenAI, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_image_last_used", ProviderID: provider.ID, Name: "Image Resource",
		ResourceType: "openai", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	route := store.AddRoute(ModelRoute{
		ModelName: model.Name, ProviderID: provider.ID, ProviderResourceID: resource.ID,
		ProviderModel: openAIImageModelName, Priority: 1, Weight: 100, Status: StatusActive,
	})
	selection := RouteSelection{Provider: provider, Route: route, ProviderModel: openAIImageModelName}

	complete := func(assetID string) error {
		t.Helper()
		call, err := store.StartCall(context.Background(), project, key, model.Name, 0)
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.CreateImageJob(ImageJob{
			ProjectID: project.ID, APIKeyID: key.ID, RequestID: call.RequestID,
			Status: imageJobStatusQueued, Model: model.Name, Action: "generate",
		}, "image last used prompt")
		if err != nil {
			t.Fatal(err)
		}
		job, claimed, err := store.ClaimImageJob(job.ID)
		if err != nil || !claimed {
			t.Fatalf("claim image job: claimed=%v err=%v", claimed, err)
		}
		return store.CompleteImageJob(call, job, "", ImageAsset{
			ID: assetID, JobID: job.ID, ProjectID: project.ID,
			Role: "output", RelativePath: assetID + ".png", ContentType: "image/png",
		}, selection, Usage{TotalTokens: 1}, "127.0.0.1", "image-last-used")
	}

	// A completion that rolls back must not mark anything used.
	if _, err := store.CreateImageAsset(ImageAsset{
		ID: "asset_image_collision", JobID: "other_job", ProjectID: project.ID,
		Role: "output", RelativePath: "other/output.png", ContentType: "image/png",
	}); err != nil {
		t.Fatal(err)
	}
	if err := complete("asset_image_collision"); err == nil {
		t.Fatal("a duplicate asset must fail the completion")
	}
	if got := persistedRouteLastUsed(t, store, route.ID); got != nil {
		t.Fatalf("a failed completion should not mark the route used, got %v", got)
	}
	if got := persistedResourceLastUsed(t, store, resource.ID); got != nil {
		t.Fatalf("a failed completion should not mark the resource used, got %v", got)
	}

	if err := complete("asset_image_last_used_1"); err != nil {
		t.Fatal(err)
	}
	routeFirst := persistedRouteLastUsed(t, store, route.ID)
	resourceFirst := persistedResourceLastUsed(t, store, resource.ID)
	if routeFirst == nil || resourceFirst == nil {
		t.Fatalf("completing an image job should mark the route and resource used: route=%v resource=%v", routeFirst, resourceFirst)
	}

	expireLastUsedThrottle(store, lastUsedRouteKey(route.ID), lastUsedResourceKey(resource.ID))
	if err := complete("asset_image_last_used_2"); err != nil {
		t.Fatal(err)
	}
	if got := persistedRouteLastUsed(t, store, route.ID); got == nil || !got.After(*routeFirst) {
		t.Fatalf("a completion past the window should refresh the route: %v then %v", routeFirst, got)
	}
	if got := persistedResourceLastUsed(t, store, resource.ID); got == nil || !got.After(*resourceFirst) {
		t.Fatalf("a completion past the window should refresh the resource: %v then %v", resourceFirst, got)
	}
}

// expireLastUsedThrottle rewinds entries past the window so a test can observe
// the next write without waiting a minute.
func expireLastUsedThrottle(store *GormStore, keys ...string) {
	expired := time.Now().Add(-2 * lastUsedThrottleWindow)
	for _, key := range keys {
		store.lastUsed.entries.Store(key, expired)
	}
}

func persistedAPIKeyLastUsed(t *testing.T, store *GormStore, id string) *time.Time {
	t.Helper()
	var key APIKey
	if err := store.db.First(&key, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return key.LastUsedAt
}

func persistedRouteLastUsed(t *testing.T, store *GormStore, id string) *time.Time {
	t.Helper()
	var route ModelRoute
	if err := store.db.First(&route, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return route.LastUsedAt
}

func persistedResourceLastUsed(t *testing.T, store *GormStore, id string) *time.Time {
	t.Helper()
	var resource ProviderResource
	if err := store.db.First(&resource, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return resource.LastUsedAt
}
