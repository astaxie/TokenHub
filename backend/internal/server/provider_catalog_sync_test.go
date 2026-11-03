package server

import (
	"bytes"
	"context"
	"os"
	"io"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func catalogSyncTestService(t *testing.T, upstreamBody []byte, fetches *atomic.Int64) *providerCatalogService {
	t.Helper()
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	upstreamContent, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	if upstreamBody == nil {
		upstreamBody = upstreamContent
	}
	service := newProviderCatalogService(store, catalogFile)
	if upstreamBody != nil {
		service.upstreamURL = "https://catalog.example/provider-catalog.json"
		service.upstreamClient = providerCatalogHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
			fetches.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
				Request:    req,
			}, nil
		})
	}
	return service
}

func freshenStoredSnapshot(t *testing.T, service *providerCatalogService) {
	t.Helper()
	entries, _, err := service.reload(context.Background())
	if err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected seeded snapshot entries")
	}
}

func TestProviderCatalogRefreshReusesFreshSnapshotWithoutNetwork(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	service := catalogSyncTestService(t, nil, &fetches)
	freshenStoredSnapshot(t, service)
	if got := fetches.Load(); got != 1 {
		t.Fatalf("seed fetches = %d, want 1", got)
	}

	entry, source, found, err := service.Get(context.Background(), "openai", true)
	if err != nil || !found {
		t.Fatalf("refresh within freshness window: found=%v err=%v", found, err)
	}
	if entry.ID != "openai" || source == "" {
		t.Fatalf("expected openai entry from stored snapshot, id=%q source=%q", entry.ID, source)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fresh snapshot triggered network fetches = %d, want 1", got)
	}
}

func TestProviderCatalogRefreshFetchesAgainAfterFreshnessWindow(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	service := catalogSyncTestService(t, nil, &fetches)
	freshenStoredSnapshot(t, service)

	service.lastSyncAttempt = time.Now().Add(-2 * time.Hour)
	entries, _, _, found, err := service.store.LoadProviderCatalogSnapshot(true)
	if err != nil || !found {
		t.Fatalf("load snapshot: found=%v err=%v", found, err)
	}
	if err := service.store.SaveProviderCatalogSnapshot(entries, providerCatalogUpstreamSource, time.Now().Add(-2*time.Hour).UTC()); err != nil {
		t.Fatalf("age snapshot: %v", err)
	}

	if _, _, found, err := service.Get(context.Background(), "openai", true); err != nil || !found {
		t.Fatalf("refresh after freshness window: found=%v err=%v", found, err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("stale snapshot fetches = %d, want 2", got)
	}
}

func TestProviderCatalogForceReloadBypassesFreshness(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	service := catalogSyncTestService(t, nil, &fetches)
	freshenStoredSnapshot(t, service)

	entries, source, err := service.ForceReload(context.Background())
	if err != nil {
		t.Fatalf("force reload: %v", err)
	}
	if len(entries) == 0 || source != providerCatalogUpstreamSource {
		t.Fatalf("force reload source=%q entries=%d, want upstream entries", source, len(entries))
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("force reload fetches = %d, want 2 (bypasses freshness)", got)
	}
}

func TestProviderCatalogRefreshFallsBackToLocalWhenUpstreamFails(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = 0
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	service := newProviderCatalogService(store, catalogFile)
	service.upstreamURL = "https://catalog.example/provider-catalog.json"
	service.upstreamClient = providerCatalogHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
	})

	entry, source, found, err := service.Get(context.Background(), "openai", true)
	if err != nil || !found {
		t.Fatalf("refresh should fall back to the bundled catalog: found=%v err=%v", found, err)
	}
	if entry.ID == "" {
		t.Fatal("expected fallback entry")
	}
	if source != providerCatalogLocalSource {
		t.Fatalf("fallback source = %q, want local", source)
	}
}

func TestProviderCatalogSchedulerRunDueRunsOncePerDay(t *testing.T) {
	store := NewMemoryStore()
	var runs atomic.Int64
	scheduler := newProviderCatalogScheduler(store, func(context.Context) error {
		runs.Add(1)
		return nil
	})
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	if err := scheduler.RunDue(context.Background(), now); err != nil {
		t.Fatalf("first RunDue: %v", err)
	}
	// Same day, different instance: the cluster task revision already ran.
	if err := scheduler.RunDue(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatalf("second RunDue same day: %v", err)
	}
	// Next day: revision advances, so the task runs again.
	if err := scheduler.RunDue(context.Background(), now.Add(24*time.Hour)); err != nil {
		t.Fatalf("next day RunDue: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("runs = %d, want 2 (once per day across instances)", got)
	}
}

func TestProviderCatalogSyncDisabledWithoutUpstreamURL(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	service := newProviderCatalogService(store, catalogFile)
	service.SetUpstreamURL("")

	entries, source, err := service.ForceReload(context.Background())
	if err != nil {
		t.Fatalf("force reload without upstream URL: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected local entries without upstream")
	}
	if source != providerCatalogLocalSource {
		t.Fatalf("source = %q, want local", source)
	}
}
