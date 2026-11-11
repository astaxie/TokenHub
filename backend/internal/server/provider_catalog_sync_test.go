package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// pausingSnapshotStore wraps the real store and pauses a specific snapshot save so a
// test can interleave another writer at the exact point the reviewer described: after
// the fresh reload has read the snapshot and before it writes its merge.
type pausingSnapshotStore struct {
	Store
	armed       atomic.Bool
	reached     chan struct{}
	release     chan struct{}
	reachedOnce sync.Once
}

func (s *pausingSnapshotStore) SaveProviderCatalogSnapshot(entries []ProviderCatalogEntry, source string, fetchedAt time.Time) error {
	// The fresh reload saves once, as the final step of its merge. Pausing the first
	// save keeps the interleaving deterministic without depending on which source the
	// merge happened to inherit from the stored snapshot.
	if s.armed.CompareAndSwap(true, false) {
		s.reachedOnce.Do(func() { close(s.reached) })
		<-s.release
	}
	return s.Store.SaveProviderCatalogSnapshot(entries, source, fetchedAt)
}

// TestProviderCatalogFreshReloadDoesNotOverwriteConcurrentUpstreamSync reproduces the
// lost-update the reviewer described. The interleaving has to be: the fresh reload reads
// the snapshot, a forced sync imports a provider and saves it, and only then does the
// fresh reload save its merge. Its merge was built from the pre-sync snapshot, so without
// shared coordination the imported provider is silently deleted.
func TestProviderCatalogFreshReloadDoesNotOverwriteConcurrentUpstreamSync(t *testing.T) {
	base := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)

	inner := newProviderCatalogService(base, catalogFile)
	inner.SetUpstreamURL("")
	if _, _, err := inner.reloadWithOptions(context.Background(), true); err != nil {
		t.Fatalf("prime snapshot: %v", err)
	}

	upstreamOnly := ProviderCatalogEntry{
		ID:          "concurrent-upstream-only",
		Name:        "Concurrent Upstream Only",
		DisplayName: "Concurrent Upstream Only",
		BaseURL:     "https://concurrent.example.test/v1",
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(upstreamCatalogWith(t, upstreamOnly))
	}))
	defer upstream.Close()

	// The fresh reload runs through the pausing store so the test can stop it between
	// its read and its write.
	pausing := &pausingSnapshotStore{
		Store:   base,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	pausing.armed.Store(true)
	fresh := newProviderCatalogService(pausing, catalogFile)
	fresh.SetUpstreamURL("")

	// The forced sync writes through the same underlying store, so it shares the same
	// cluster lease as the fresh reload.
	concurrent := newProviderCatalogService(base, catalogFile)
	concurrent.SetUpstreamURL(upstream.URL)

	freshDone := make(chan error, 1)
	go func() {
		_, _, err := fresh.reloadWithOptions(context.Background(), false)
		freshDone <- err
	}()
	select {
	case <-pausing.reached:
		// The fresh reload has now read the pre-sync snapshot and is about to save.
	case <-time.After(30 * time.Second):
		t.Fatal("fresh reload never reached its snapshot save")
	}

	syncDone := make(chan error, 1)
	go func() {
		_, _, err := concurrent.reloadWithOptions(context.Background(), true)
		syncDone <- err
	}()
	// While the fresh reload is paused mid-merge, a coordinated forced sync cannot
	// complete: it is waiting on the same cluster lease. An uncoordinated one runs to
	// completion here, which is exactly the write the fresh save is about to clobber.
	syncFinishedEarly := false
	select {
	case err := <-syncDone:
		if err != nil {
			t.Fatalf("concurrent forced sync: %v", err)
		}
		syncFinishedEarly = true
	case <-time.After(5 * time.Second):
	}

	close(pausing.release)
	if err := <-freshDone; err != nil {
		t.Fatalf("fresh reload: %v", err)
	}
	if !syncFinishedEarly {
		if err := <-syncDone; err != nil {
			t.Fatalf("concurrent forced sync: %v", err)
		}
	}

	stored, _, _, err := inner.loadStored(false)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if !providerCatalogHasEntry(stored, upstreamOnly.ID) {
		t.Fatal("fresh reload dropped a provider imported by a concurrent forced sync")
	}
}

// TestProviderCatalogScheduledSyncFetchesDespiteFreshSnapshot pins the scheduler
// boundary: RunClusterTask marks the UTC day complete as soon as the run returns, so a
// scheduled run that reuses the interactive freshness window can consume a day without
// contacting the upstream. A forced fetch is what keeps one download per day.
func TestProviderCatalogScheduledSyncFetchesDespiteFreshSnapshot(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	service := newProviderCatalogService(store, catalogFile)

	var downloads atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = w.Write(upstreamCatalogWith(t, ProviderCatalogEntry{
			ID:      "scheduled-download",
			Name:    "Scheduled Download",
			BaseURL: "https://scheduled.example.test/v1",
		}))
	}))
	defer upstream.Close()
	service.SetUpstreamURL(upstream.URL)

	server := &Server{providerCatalog: service}
	// A snapshot written a moment ago is inside the interactive freshness window.
	if _, _, err := service.reloadWithOptions(context.Background(), true); err != nil {
		t.Fatalf("prime snapshot: %v", err)
	}
	if got := downloads.Load(); got != 1 {
		t.Fatalf("prime downloads = %d, want 1", got)
	}
	if err := server.runProviderCatalogSync(context.Background()); err != nil {
		t.Fatalf("scheduled sync: %v", err)
	}
	if got := downloads.Load(); got != 2 {
		t.Fatalf("downloads after scheduled sync = %d, want 2 (the daily task must not reuse the freshness window)", got)
	}
}

// TestProviderCatalogSchedulerRunDueDoesNotBurnTheDayAtTheFreshnessBoundary covers the
// exact boundary the reviewer reproduced. The scheduler's cluster task records the UTC
// day as complete as soon as the run returns, and the freshness window is measured from
// the previous download's completion. A download that finished just after its own tick
// therefore makes the next day's tick still look fresh; if the scheduled run honoured
// that window it would return without fetching and the day's sync would be consumed.
func TestProviderCatalogSchedulerRunDueDoesNotBurnTheDayAtTheFreshnessBoundary(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	service := newProviderCatalogService(store, catalogFile)

	var downloads atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = w.Write(upstreamCatalogWith(t, ProviderCatalogEntry{
			ID:      "boundary-download",
			Name:    "Boundary Download",
			BaseURL: "https://boundary.example.test/v1",
		}))
	}))
	defer upstream.Close()
	service.SetUpstreamURL(upstream.URL)

	// Seed a snapshot aged one second inside the freshness window, which is the state a
	// download finishing just after its tick leaves behind.
	if _, _, err := service.reloadWithOptions(context.Background(), true); err != nil {
		t.Fatalf("prime snapshot: %v", err)
	}
	if got := downloads.Load(); got != 1 {
		t.Fatalf("prime downloads = %d, want 1", got)
	}
	entries, _, _, err := service.loadStored(true)
	if err != nil {
		t.Fatalf("load primed snapshot: %v", err)
	}
	aged := time.Now().UTC().Add(-(providerCatalogFreshInterval - time.Second))
	if err := store.SaveProviderCatalogSnapshot(entries, providerCatalogUpstreamSource, aged); err != nil {
		t.Fatalf("age snapshot: %v", err)
	}

	server := &Server{providerCatalog: service}
	scheduler := newProviderCatalogScheduler(store, server.runProviderCatalogSync)
	// RunDue for the next UTC day: the cluster revision advances, so the task runs and
	// must actually fetch rather than treating the aged snapshot as fresh.
	nextDay := time.Now().UTC().Add(24 * time.Hour)
	if err := scheduler.RunDue(context.Background(), nextDay); err != nil {
		t.Fatalf("RunDue next day: %v", err)
	}
	if got := downloads.Load(); got != 2 {
		t.Fatalf("downloads after next-day RunDue = %d, want 2 (a skipped fetch must not consume the day's sync)", got)
	}
}
