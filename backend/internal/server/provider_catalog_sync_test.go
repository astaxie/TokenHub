package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
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

	service.lastSyncAttemptNanos.Store(time.Now().Add(-2 * time.Hour).UnixNano())
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

// TestProviderCatalogConcurrentReloadsAreRaceFree exercises the freshness read on
// the fast path against the writes performed by forced and stale reloads. Those
// paths run concurrently when the admin refresh endpoint, the force-sync endpoint
// and the daily scheduler overlap, so the test asserts the timestamp is published
// safely rather than only guarded on the writer side.
func TestProviderCatalogConcurrentReloadsAreRaceFree(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	service := catalogSyncTestService(t, nil, &fetches)
	freshenStoredSnapshot(t, service)

	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			for attempt := 0; attempt < 4; attempt++ {
				if index%2 == 0 {
					if _, _, err := service.ForceReload(context.Background()); err != nil {
						t.Errorf("force reload: %v", err)
						return
					}
					continue
				}
				if _, _, _, err := service.Get(context.Background(), "openai", true); err != nil {
					t.Errorf("ordinary refresh: %v", err)
					return
				}
			}
		}(index)
	}
	wait.Wait()
}

// TestProviderCatalogFreshReloadKeepsUpstreamOnlyProviders covers a sync that imports
// a provider the bundled file does not contain. The following non-forced refresh must
// keep it instead of rewriting the snapshot with the bundled catalog, which also means
// no additional upstream request is needed to recover it.
func TestProviderCatalogFreshReloadKeepsUpstreamOnlyProviders(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	upstreamOnly := ProviderCatalogEntry{ID: "upstream-only", Name: "Upstream Only", Type: "openai_compatible", BaseURL: "https://upstream-only.example/v1", ModelsCount: 4, Models: []ProviderCatalogModel{{ID: "upstream-only-model"}}}
	service := catalogSyncTestService(t, upstreamCatalogWith(t, upstreamOnly), &fetches)
	if _, source, err := service.ForceReload(context.Background()); err != nil || source != providerCatalogUpstreamSource {
		t.Fatalf("force reload: source=%q err=%v", source, err)
	}
	if _, _, found, err := service.Get(context.Background(), "upstream-only", false); err != nil || !found {
		t.Fatalf("synced provider missing right after sync: found=%v err=%v", found, err)
	}

	// Non-forced refresh inside the freshness window: no upstream request, bundled
	// file re-read, merged with the stored snapshot.
	fetchesBefore := fetches.Load()
	entries, source, err := service.reload(context.Background())
	if err != nil {
		t.Fatalf("fresh reload: %v", err)
	}
	if source != providerCatalogUpstreamSource {
		t.Fatalf("fresh reload source = %q, want upstream", source)
	}
	if fetches.Load() != fetchesBefore {
		t.Fatalf("fresh reload fetched upstream %d times, want 0", fetches.Load()-fetchesBefore)
	}
	if !providerCatalogHasEntry(entries, "upstream-only") {
		t.Fatal("fresh reload dropped the upstream-only provider")
	}
	if _, _, found, err := service.Get(context.Background(), "upstream-only", false); err != nil || !found {
		t.Fatalf("upstream-only provider missing after fresh reload: found=%v err=%v", found, err)
	}
}

// TestProviderCatalogFreshReloadAppliesLocalEdits keeps the other direction: a changed
// bundled file still overrides the stored entry with the same ID.
func TestProviderCatalogFreshReloadAppliesLocalEdits(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	service := catalogSyncTestService(t, nil, &fetches)
	if _, _, err := service.ForceReload(context.Background()); err != nil {
		t.Fatalf("force reload: %v", err)
	}

	writeProviderCatalogFixtureWithBaseURL(t, service.catalogFile, "openai", "https://edited.example/v1")
	entries, _, err := service.reload(context.Background())
	if err != nil {
		t.Fatalf("fresh reload: %v", err)
	}
	var edited *ProviderCatalogEntry
	for index := range entries {
		if entries[index].ID == "openai" {
			edited = &entries[index]
			break
		}
	}
	if edited == nil {
		t.Fatal("openai entry missing after fresh reload")
	}
	if edited.BaseURL != "https://edited.example/v1" {
		t.Fatalf("openai base URL = %q, want the edited local value", edited.BaseURL)
	}
}

// TestProviderCatalogFreshReloadRejectsIncompleteLocalFile covers the completeness
// safeguard: a truncated local file must not replace a healthy stored snapshot.
func TestProviderCatalogFreshReloadRejectsIncompleteLocalFile(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	service := catalogSyncTestService(t, nil, &fetches)
	if _, _, err := service.ForceReload(context.Background()); err != nil {
		t.Fatalf("force reload: %v", err)
	}
	before, _, _, err := service.loadStored(false)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	beforeProviders, _, _ := providerCatalogStats(before)

	// One provider with no models is a valid JSON file but fails the minimum counts.
	if err := os.WriteFile(service.catalogFile, []byte(`{"providers":{"only-one":{"name":"only-one","api":"https://only-one.example/v1","models":[]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, _, err := service.reload(context.Background())
	if err != nil {
		t.Fatalf("fresh reload: %v", err)
	}
	afterProviders, _, _ := providerCatalogStats(entries)
	if afterProviders != beforeProviders {
		t.Fatalf("incomplete local file changed the snapshot from %d to %d providers", beforeProviders, afterProviders)
	}
	if providerCatalogHasEntry(entries, "only-one") {
		t.Fatal("incomplete local file was accepted as the snapshot")
	}
}

// TestProviderCatalogFreshReloadDoesNotAdvanceUpstreamFreshness covers the timestamp
// rule: repeated local refreshes must not keep postponing the next upstream sync.
func TestProviderCatalogFreshReloadDoesNotAdvanceUpstreamFreshness(t *testing.T) {
	original := providerCatalogFreshInterval
	providerCatalogFreshInterval = time.Hour
	t.Cleanup(func() { providerCatalogFreshInterval = original })

	var fetches atomic.Int64
	service := catalogSyncTestService(t, nil, &fetches)
	if _, _, err := service.ForceReload(context.Background()); err != nil {
		t.Fatalf("force reload: %v", err)
	}
	attemptAfterSync, ok := service.lastSyncAttempt()
	if !ok {
		t.Fatal("forced sync did not record an attempt")
	}
	_, _, fetchedAfterSync, found, err := service.store.LoadProviderCatalogSnapshot(false)
	if err != nil || !found {
		t.Fatalf("load snapshot: found=%v err=%v", found, err)
	}

	// Two local refreshes. Neither reaches the network, so neither may move the
	// recorded attempt or the snapshot timestamp forward.
	for attempt := 0; attempt < 2; attempt++ {
		if _, _, err := service.reload(context.Background()); err != nil {
			t.Fatalf("fresh reload %d: %v", attempt, err)
		}
	}
	attemptAfterReloads, _ := service.lastSyncAttempt()
	if !attemptAfterReloads.Equal(attemptAfterSync) {
		t.Fatalf("local refresh moved the upstream attempt from %v to %v", attemptAfterSync, attemptAfterReloads)
	}
	_, _, fetchedAfterReloads, _, err := service.store.LoadProviderCatalogSnapshot(false)
	if err != nil {
		t.Fatalf("load snapshot after reloads: %v", err)
	}
	if !fetchedAfterReloads.Equal(fetchedAfterSync) {
		t.Fatalf("local refresh moved the snapshot timestamp from %v to %v", fetchedAfterSync, fetchedAfterReloads)
	}
}

// upstreamCatalogWith returns the bundled fixture catalog with one extra provider, as a
// stand-in for a newer upstream release.
func upstreamCatalogWith(t *testing.T, extra ProviderCatalogEntry) []byte {
	t.Helper()
	catalogFile := filepath.Join(t.TempDir(), "upstream-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	content, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	models := make([]map[string]any, 0, len(extra.Models))
	for _, model := range extra.Models {
		models = append(models, map[string]any{"id": model.ID, "name": model.ID})
	}
	document.Providers[extra.ID] = map[string]any{"name": extra.Name, "api": extra.BaseURL, "models": models}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// writeProviderCatalogFixtureWithBaseURL rewrites one provider's API base in a fixture,
// standing in for an operator editing the bundled file.
func writeProviderCatalogFixtureWithBaseURL(t *testing.T, catalogFile string, providerID string, baseURL string) {
	t.Helper()
	content, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	entry, ok := document.Providers[providerID]
	if !ok {
		t.Fatalf("fixture has no provider %q", providerID)
	}
	entry["api"] = baseURL
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogFile, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
