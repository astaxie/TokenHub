package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOverviewCacheCoalescesAndBoundsStaleResults(t *testing.T) {
	var cache overviewSummaryCache
	var calls atomic.Int32
	release := make(chan struct{})
	load := func(ctx context.Context) (overviewSummaryEntry, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return overviewSummaryEntry{}, ctx.Err()
		}
		return overviewSummaryEntry{summary: UsageSummary{RequestCount: 7}, status: OverviewSummaryStatus{State: "ready", UpdatedAt: time.Now()}, expires: time.Now().Add(15 * time.Second)}, nil
	}
	var wait sync.WaitGroup
	for i := 0; i < 10; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			entry, err := cache.get(context.Background(), "scope", load)
			if err != nil || entry.summary.RequestCount != 7 {
				t.Errorf("entry=%+v err=%v", entry, err)
			}
		}()
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loads=%d", calls.Load())
	}
	failure := func(context.Context) (overviewSummaryEntry, error) {
		return overviewSummaryEntry{}, errors.New("database failure")
	}
	cache.mu.Lock()
	expired := cache.entries["scope"]
	expired.expires = time.Now().Add(-time.Second)
	cache.entries["scope"] = expired
	cache.mu.Unlock()
	entry, err := cache.get(context.Background(), "scope", failure)
	if err != nil || entry.status.State != "stale" || entry.summary.RequestCount != 7 {
		t.Fatalf("stale=%+v err=%v", entry, err)
	}
	cache.mu.Lock()
	expired.status.UpdatedAt = time.Now().Add(-61 * time.Second)
	cache.entries["scope"] = expired
	cache.mu.Unlock()
	if _, err := cache.get(context.Background(), "scope", failure); err == nil {
		t.Fatal("cache older than 60 seconds served")
	}
	if _, err := cache.get(context.Background(), "different-permissions", failure); err == nil {
		t.Fatal("stale cache leaked across scopes")
	}
}

func TestOverviewCacheCancelsLastWaiter(t *testing.T) {
	var cache overviewSummaryCache
	started, stopped := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cache.get(ctx, "scope", func(ctx context.Context) (overviewSummaryEntry, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return overviewSummaryEntry{}, ctx.Err()
		})
		done <- err
	}()
	<-started
	cancel()
	if !errors.Is(<-done, context.Canceled) {
		t.Fatal("waiter did not cancel")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shared work did not stop")
	}
}

func TestOverviewCacheRefreshTimeoutUsesOnlyRecentSameScopeData(t *testing.T) {
	now := time.Now()
	cached := overviewSummaryEntry{
		summary: UsageSummary{RequestCount: 42},
		status:  OverviewSummaryStatus{State: "ready", UpdatedAt: now.Add(-30 * time.Second)},
		expires: now.Add(-15 * time.Second),
	}
	old := cached
	old.status.UpdatedAt = now.Add(-61 * time.Second)
	cache := overviewSummaryCache{entries: map[string]overviewSummaryEntry{"mine": cached, "expired": old}}
	early, stop := context.WithDeadline(t.Context(), now.Add(-time.Second))
	defer stop()
	neverLoad := func(context.Context) (overviewSummaryEntry, error) {
		t.Fatal("expired request started work")
		return overviewSummaryEntry{}, nil
	}
	if hit, err := cache.get(early, "mine", neverLoad); err != nil || hit.status.State != "stale" {
		t.Fatalf("already expired page: %+v, %v", hit, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.get(canceled, "mine", neverLoad); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled page: %v", err)
	}
	for _, scope := range []string{"mine", "expired", "different"} {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		entry, err := cache.get(ctx, scope, func(query context.Context) (overviewSummaryEntry, error) {
			<-query.Done()
			return overviewSummaryEntry{}, query.Err()
		})
		cancel()
		if scope == "mine" {
			if err != nil || entry.status.State != "stale" || entry.summary.RequestCount != 42 || entry.status.UpdatedAt != cached.status.UpdatedAt {
				t.Fatalf("timed-out refresh: %+v, %v", entry, err)
			}
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s received stale data: %+v, %v", scope, entry, err)
		}
	}
}

func TestOverviewScopedProgressHidesGlobalCounts(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	server := &Server{store: preparingOverviewSummaryStore{Store: store}}
	_, status := server.overviewSummaryForUser(t.Context(), AdminUser{ID: "scoped", Role: "user"})
	if status.State != "preparing" || status.Processed != 0 {
		t.Fatalf("global progress leaked: %+v", status)
	}
	_, status = server.overviewSummaryForUser(t.Context(), AdminUser{ID: "global", Role: "admin"})
	if status.Processed != 999999 {
		t.Fatalf("global progress missing: %+v", status)
	}
}

type preparingOverviewSummaryStore struct{ Store }

func (preparingOverviewSummaryStore) QueryOverviewSummary(context.Context, UsageSummaryQuery) (UsageSummary, OverviewProgress, error) {
	return UsageSummary{}, OverviewProgress{Processed: 999999}, nil
}
