package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitMonitoringWaiters(t *testing.T, c *monitoringObservationCache, key string, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		flight := c.flights[key]
		ready := flight != nil && flight.waiters == count
		c.mu.Unlock()
		if ready {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("monitoring waiters did not join shared query")
}

func TestProviderMonitoringCacheCoalescesAndExpires(t *testing.T) {
	var cache monitoringObservationCache
	var calls atomic.Int32
	release := make(chan struct{})
	load := func(ctx context.Context) (map[string]ProviderObservationSummary, error) {
		calls.Add(1)
		select {
		case <-release:
			return map[string]ProviderObservationSummary{"p": {}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func() { _, err := cache.get(context.Background(), "p", load); done <- err }()
	}
	waitMonitoringWaiters(t, &cache, "p", 20)
	close(release)
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.get(context.Background(), "p", load); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent/cached refreshes ran %d queries", calls.Load())
	}
	cache.mu.Lock()
	cache.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	if _, err := cache.get(context.Background(), "p", load); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("expired cache was reused")
	}
	if _, err := cache.get(context.Background(), "new-provider", load); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("changed provider membership reused old cache")
	}
}

func TestProviderMonitoringCacheIndependentCancellation(t *testing.T) {
	var cache monitoringObservationCache
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	load := func(ctx context.Context) (map[string]ProviderObservationSummary, error) {
		started <- ctx
		select {
		case <-release:
			return map[string]ProviderObservationSummary{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { _, err := cache.get(first, "p", load); firstDone <- err }()
	queryCtx := <-started
	if deadline, ok := queryCtx.Deadline(); !ok || time.Until(deadline) > providerMonitoringQueryTimeout {
		t.Fatal("shared query has no bounded deadline")
	}
	go func() { _, err := cache.get(context.Background(), "p", load); secondDone <- err }()
	waitMonitoringWaiters(t, &cache, "p", 2)
	cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter returned %v", err)
	}
	if queryCtx.Err() != nil {
		t.Fatal("one waiter canceled another waiter's query")
	}
	close(release)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestProviderMonitoringCacheCancelsAbandonedQueryAndRetriesErrors(t *testing.T) {
	var cache monitoringObservationCache
	started := make(chan context.Context, 1)
	load := func(ctx context.Context) (map[string]ProviderObservationSummary, error) {
		started <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := cache.get(ctx, "p", load); done <- err }()
	queryCtx := <-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-queryCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("abandoned query was not canceled")
	}
	calls := 0
	fail := func(context.Context) (map[string]ProviderObservationSummary, error) {
		calls++
		return nil, errors.New("synthetic database failure")
	}
	for i := 0; i < 2; i++ {
		if _, err := cache.get(context.Background(), "p", fail); err == nil {
			t.Fatal("failed query succeeded")
		}
	}
	if calls != 2 {
		t.Fatal("query failures were cached")
	}
}

type monitoringFailureStore struct {
	Store
	calls int
}

func (s *monitoringFailureStore) ProviderMonitoringObservations(context.Context, time.Time, []string) (map[string]ProviderObservationSummary, error) {
	s.calls++
	return nil, errors.New("private database diagnostics")
}
func (s *monitoringFailureStore) ListProviderObservations(time.Time) []ProviderObservation {
	panic("monitoring must not load raw observations")
}

func TestProviderMonitoringHTTPErrorAndAuthorization(t *testing.T) {
	underlying := NewMemoryStore()
	t.Cleanup(func() {
		if err := underlying.Close(); err != nil {
			t.Error(err)
		}
	})
	store := &monitoringFailureStore{Store: underlying}
	server := New(store)
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/providers/monitoring", nil)
		if authorized {
			req.Header.Set("Authorization", "Bearer dev_admin_token")
		}
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if !authorized {
			if rec.Code != http.StatusUnauthorized || store.calls != 0 {
				t.Fatalf("unauthorized query: %d, calls=%d", rec.Code, store.calls)
			}
			continue
		}
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "provider_monitoring_unavailable") || strings.Contains(rec.Body.String(), "private database") {
			t.Fatalf("unexpected monitoring error: %d %s", rec.Code, rec.Body.String())
		}
	}
}
