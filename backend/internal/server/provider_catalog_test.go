package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeProviderCatalogModelUsesExplicitCanonicalName(t *testing.T) {
	explicit := normalizeProviderCatalogModel(map[string]any{
		"id":             "k3",
		"display_name":   "Kimi K3",
		"canonical_name": "KIMI_K3",
	})
	if explicit.CanonicalName != "kimi-k3" {
		t.Fatalf("expected explicit canonical name kimi-k3, got %q", explicit.CanonicalName)
	}

	fallback := normalizeProviderCatalogModel(map[string]any{
		"id":           "k3",
		"display_name": "Kimi K3",
	})
	if fallback.CanonicalName != "k3" {
		t.Fatalf("expected ID-derived canonical name k3, got %q", fallback.CanonicalName)
	}
}

func TestNormalizeProviderCatalogEntryInfersAnthropicProtocolFromBaseURL(t *testing.T) {
	entry := normalizeProviderCatalogEntry("minimax-cn", map[string]any{
		"name": "MiniMax China",
		"api":  "https://api.minimaxi.com/anthropic/v1",
	})
	if entry.Type != ProviderAnthropic {
		t.Fatalf("expected Anthropic provider type, got %q", entry.Type)
	}
}

func TestBuiltinDeepSeekCatalogDescribesNativeV4Capabilities(t *testing.T) {
	var deepSeek ProviderCatalogEntry
	for _, entry := range builtinProviderCatalog(true) {
		if entry.ID == "deepseek" {
			deepSeek = entry
			break
		}
	}
	if deepSeek.ID == "" {
		t.Fatal("expected builtin DeepSeek provider")
	}
	models := map[string]ProviderCatalogModel{}
	for _, model := range deepSeek.Models {
		models[model.ID] = model
	}
	for _, legacyID := range []string{"deepseek-chat", "deepseek-reasoner"} {
		if _, ok := models[legacyID]; ok {
			t.Fatalf("discontinued DeepSeek model %q must not be advertised", legacyID)
		}
	}
	flash, ok := models["deepseek-v4-flash"]
	if !ok {
		t.Fatal("expected native deepseek-v4-flash model")
	}
	if flash.ContextWindow != 1048576 || flash.MaxOutputTokens != 393216 ||
		flash.InputPriceUSDPer1M != 0.14 || flash.CacheReadPriceUSDPer1M != 0.0028 || flash.OutputPriceUSDPer1M != 0.28 {
		t.Fatalf("unexpected V4 Flash limits or pricing: %+v", flash)
	}
	if flash.Metadata["endpoints"] != "responses,chat/completions,anthropic" || flash.Metadata["reasoning_effort_options"] != "low,high,max" {
		t.Fatalf("unexpected V4 Flash protocol metadata: %+v", flash.Metadata)
	}
	pro, ok := models["deepseek-v4-pro"]
	if !ok {
		t.Fatal("expected native deepseek-v4-pro model")
	}
	if pro.Metadata["endpoints"] != "responses,chat/completions,anthropic" {
		t.Fatalf("unexpected V4 Pro protocol metadata: %+v", pro.Metadata)
	}
	for _, model := range []ProviderCatalogModel{flash, pro} {
		if !slices.Contains(model.SupportedParameters, "top_logprobs") ||
			model.Metadata["features"] != "function-calling,structured-outputs,reasoning,apply-patch,web-search" ||
			model.Metadata["top_logprobs_range"] != "0,20" || model.Metadata["responses_stateful"] != "false" ||
			model.Metadata["prompt_cache_mode"] != "automatic" || model.Metadata["custom_tool_names"] != "apply_patch" {
			t.Fatalf("incomplete builtin DeepSeek Responses metadata for %s: %+v", model.ID, model)
		}
	}
}

func TestDeepSeekResponsesCapabilityIsModelScoped(t *testing.T) {
	server := New(NewMemoryStore())
	flash := RouteSelection{Provider: Provider{Type: "deepseek"}, ProviderModel: "deepseek-v4-flash"}
	pro := RouteSelection{Provider: Provider{Type: "deepseek"}, ProviderModel: "deepseek-v4-pro"}
	legacy := RouteSelection{Provider: Provider{Type: "deepseek"}, ProviderModel: "deepseek-chat"}
	if !server.routeSupportsAdapterCapability(flash, AdapterCapabilityResponses) ||
		!server.routeSupportsAdapterCapability(flash, AdapterCapabilityResponseStream) {
		t.Fatal("V4 Flash must support Responses and streaming Responses")
	}
	if !server.routeSupportsAdapterCapability(pro, AdapterCapabilityResponses) ||
		!server.routeSupportsAdapterCapability(pro, AdapterCapabilityResponseStream) {
		t.Fatal("V4 Pro must support Responses and streaming Responses")
	}
	if server.routeSupportsAdapterCapability(legacy, AdapterCapabilityResponses) ||
		server.routeSupportsAdapterCapability(legacy, AdapterCapabilityResponseStream) {
		t.Fatal("unadvertised DeepSeek models must not inherit provider-level Responses support")
	}
	if !server.routeSupportsAdapterCapability(pro, AdapterCapabilityChat) {
		t.Fatal("V4 Pro must retain Chat Completions support")
	}
}

func TestProviderCatalogServiceReloadsTrackedLocalFile(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)

	service := newProviderCatalogService(store, catalogFile)
	service.upstreamURL = ""
	summaries, source, err := service.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if source != "local-provider-catalog" || len(summaries) != providerCatalogMinProviders+1 ||
		!providerCatalogContains(summaries, "fresh-provider") {
		t.Fatalf("unexpected local catalog summary: source=%q entries=%+v", source, summaries)
	}
	if len(summaries[0].Models) != 0 {
		t.Fatalf("list should return summaries without models: %+v", summaries[0])
	}

	entry, source, ok, err := service.Get(context.Background(), "fresh-provider", false)
	if err != nil || !ok {
		t.Fatalf("expected local provider entry, ok=%v err=%v", ok, err)
	}
	if source != "local-provider-catalog" || len(entry.Models) != 5 {
		t.Fatalf("unexpected local provider entry: source=%q entry=%+v", source, entry)
	}

	restarted := newProviderCatalogService(store, filepath.Join(t.TempDir(), "missing.json"))
	persisted, source, err := restarted.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if source != "local-provider-catalog" || !providerCatalogContains(persisted, "fresh-provider") {
		t.Fatalf("expected persisted local catalog, source=%q entries=%+v", source, persisted)
	}
}

func TestProviderCatalogServiceRefreshesFromUpstream(t *testing.T) {
	store := NewMemoryStore()
	localCatalogFile := filepath.Join(t.TempDir(), "local-provider-catalog.json")
	writeProviderCatalogFixture(t, localCatalogFile)

	upstreamCatalogFile := filepath.Join(t.TempDir(), "upstream-provider-catalog.json")
	writeProviderCatalogFixture(t, upstreamCatalogFile)
	replaceProviderCatalogFixtureURL(t, upstreamCatalogFile, "fresh-provider", "https://upstream-provider.example/v1")
	upstreamCatalog, err := os.ReadFile(upstreamCatalogFile)
	if err != nil {
		t.Fatal(err)
	}
	upstreamCatalog = append(upstreamCatalog, bytes.Repeat([]byte(" "), 6<<20-len(upstreamCatalog))...)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(upstreamCatalog)
	}))
	t.Cleanup(upstream.Close)

	service := newProviderCatalogService(store, localCatalogFile)
	service.upstreamURL = upstream.URL
	service.upstreamClient = upstream.Client()
	summaries, source, err := service.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || source != providerCatalogUpstreamSource || !providerCatalogContains(summaries, "fresh-provider") {
		t.Fatalf("unexpected upstream refresh: requests=%d source=%q entries=%+v", requests.Load(), source, summaries)
	}

	entry, source, ok, err := service.Get(context.Background(), "fresh-provider", false)
	if err != nil || !ok {
		t.Fatalf("expected upstream provider entry, ok=%v err=%v", ok, err)
	}
	if source != providerCatalogUpstreamSource || entry.Source != providerCatalogUpstreamSource ||
		entry.BaseURL != "https://upstream-provider.example/v1" || entry.Models[0].Metadata["source"] != providerCatalogUpstreamSource {
		t.Fatalf("unexpected upstream provider entry: source=%q entry=%+v", source, entry)
	}
}

func TestProviderCatalogServiceRefreshFallsBackToLocalCatalog(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	replaceProviderCatalogFixtureURL(t, catalogFile, "fresh-provider", "https://local-fallback.example/v1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(upstream.Close)

	service := newProviderCatalogService(store, catalogFile)
	service.upstreamURL = upstream.URL
	service.upstreamClient = upstream.Client()
	_, source, err := service.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	entry, storedSource, ok, err := service.Get(context.Background(), "fresh-provider", false)
	if err != nil || !ok || source != providerCatalogLocalSource || storedSource != providerCatalogLocalSource ||
		entry.BaseURL != "https://local-fallback.example/v1" {
		t.Fatalf("expected local fallback, refresh_source=%q stored_source=%q entry=%+v ok=%v err=%v", source, storedSource, entry, ok, err)
	}
}

func TestProviderCatalogServiceRefreshRejectsIncompleteUpstreamCatalog(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"providers": map[string]any{
				"openai": map[string]any{
					"name":   "OpenAI",
					"models": []map[string]any{{"id": "gpt-test"}},
				},
			},
		})
	}))
	t.Cleanup(upstream.Close)

	service := newProviderCatalogService(store, catalogFile)
	service.upstreamURL = upstream.URL
	service.upstreamClient = upstream.Client()
	entries, source, err := service.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if source != providerCatalogLocalSource || !providerCatalogContains(entries, "fresh-provider") {
		t.Fatalf("expected validated local fallback, source=%q entries=%+v", source, entries)
	}
}

func TestProviderCatalogServiceRefreshStopsWhenContextIsCanceledBeforeFallback(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	service := newProviderCatalogService(store, catalogFile)
	previous, originalSource, _, err := service.loadStored(false)
	if err != nil {
		t.Fatal(err)
	}

	leaseLost := errors.New("provider catalog lease lost")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(leaseLost)
	_, source, err := service.refreshLocked(ctx, previous)
	if !errors.Is(err, leaseLost) || source != providerCatalogUpstreamSource {
		t.Fatalf("expected canceled upstream refresh, source=%q err=%v", source, err)
	}
	_, storedSource, _, found, err := store.LoadProviderCatalogSnapshot(false)
	if err != nil || !found || storedSource != originalSource {
		t.Fatalf("canceled refresh changed snapshot: original=%q stored=%q found=%v err=%v", originalSource, storedSource, found, err)
	}
}

func TestProviderCatalogServiceRefreshRechecksContextBeforeUpstreamSave(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	content, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	service := newProviderCatalogService(store, catalogFile)
	previous, originalSource, _, err := service.loadStored(false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	service.upstreamURL = "https://catalog.example/provider-catalog.json"
	service.upstreamClient = providerCatalogHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(content)),
			Request:    req,
		}, nil
	})
	_, source, err := service.refreshLocked(ctx, previous)
	if !errors.Is(err, context.Canceled) || source != providerCatalogUpstreamSource {
		t.Fatalf("expected cancellation before upstream save, source=%q err=%v", source, err)
	}
	_, storedSource, _, found, err := store.LoadProviderCatalogSnapshot(false)
	if err != nil || !found || storedSource != originalSource {
		t.Fatalf("canceled refresh changed snapshot: original=%q stored=%q found=%v err=%v", originalSource, storedSource, found, err)
	}
}

func TestProviderCatalogServiceRetainsBuiltinSnapshotWhenLocalFileIsMissing(t *testing.T) {
	store := NewMemoryStore()
	service := newProviderCatalogService(store, filepath.Join(t.TempDir(), "missing.json"))

	entries, source, err := service.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if source != "builtin" || len(entries) == 0 {
		t.Fatalf("expected builtin catalog, source=%q entries=%d", source, len(entries))
	}
	if initialized, err := service.Initialize(context.Background()); err == nil || initialized {
		t.Fatalf("expected missing local catalog to keep builtin snapshot, initialized=%v err=%v", initialized, err)
	}

	entries, source, err = service.List(context.Background(), false)
	if err != nil || source != "builtin" || len(entries) == 0 {
		t.Fatalf("expected builtin snapshot to remain available, source=%q entries=%d err=%v", source, len(entries), err)
	}
}

func TestProviderCatalogServiceInitializeReloadsLocalCatalogOnEveryStart(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)

	service := newProviderCatalogService(store, catalogFile)
	initialized, err := service.Initialize(context.Background())
	if err != nil || !initialized {
		t.Fatalf("expected first local catalog refresh, initialized=%v err=%v", initialized, err)
	}
	replaceProviderCatalogFixtureURL(t, catalogFile, "fresh-provider", "https://refreshed-provider.example/v1")

	initialized, err = service.Initialize(context.Background())
	if err != nil || !initialized {
		t.Fatalf("expected second local catalog refresh, initialized=%v err=%v", initialized, err)
	}
	entry, source, ok, err := service.Get(context.Background(), "fresh-provider", false)
	if err != nil || !ok || source != "local-provider-catalog" || entry.BaseURL != "https://refreshed-provider.example/v1" {
		t.Fatalf("expected refreshed provider entry, source=%q entry=%+v ok=%v err=%v", source, entry, ok, err)
	}
}

func TestProviderCatalogServiceInitializeRetainsLocalSnapshotWhenFileIsMissing(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)
	seedService := newProviderCatalogService(store, catalogFile)
	seedService.upstreamURL = ""
	if _, _, err := seedService.List(context.Background(), true); err != nil {
		t.Fatal(err)
	}

	service := newProviderCatalogService(store, filepath.Join(t.TempDir(), "missing.json"))
	if initialized, err := service.Initialize(context.Background()); err == nil || initialized {
		t.Fatalf("expected failed initialization, initialized=%v err=%v", initialized, err)
	}
	entries, source, err := service.List(context.Background(), false)
	if err != nil || source != "local-provider-catalog" || !providerCatalogContains(entries, "fresh-provider") {
		t.Fatalf("expected retained local snapshot, source=%q entries=%+v err=%v", source, entries, err)
	}
}

func TestProviderCatalogServiceInitializeSerializesConcurrentRefreshes(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join(t.TempDir(), "provider-catalog.json")
	writeProviderCatalogFixture(t, catalogFile)

	probe := newProviderCatalogConcurrentInitializeProbe()
	services := []*providerCatalogService{
		newProviderCatalogService(&providerCatalogConcurrentInitializeStore{Store: store, probe: probe}, catalogFile),
		newProviderCatalogService(&providerCatalogConcurrentInitializeStore{Store: store, probe: probe}, catalogFile),
	}
	errors := make(chan error, len(services))
	for _, service := range services {
		go func(service *providerCatalogService) {
			_, err := service.Initialize(context.Background())
			errors <- err
		}(service)
	}

	for range services {
		select {
		case err := <-errors:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent catalog initialization did not complete")
		}
	}

	if writes := probe.localSnapshotWriteCount(); writes != len(services) {
		t.Fatalf("expected %d serialized local catalog refreshes, got %d", len(services), writes)
	}
	if max := probe.maxConcurrentLocalSnapshotWrites(); max != 1 {
		t.Fatalf("expected serialized snapshot writes, max concurrent writes=%d", max)
	}
	entries, source, err := services[0].List(context.Background(), false)
	if err != nil || source != "local-provider-catalog" || !providerCatalogContains(entries, "fresh-provider") {
		t.Fatalf("unexpected concurrently upgraded catalog: source=%q entries=%+v err=%v", source, entries, err)
	}
}

func TestBootstrapSeedsProviderCatalogSnapshot(t *testing.T) {
	store := NewMemoryStore()
	config := Config{
		BootstrapAdminPassword: "provider-catalog-bootstrap-password",
		ModelCatalogFile:       "../../../data/model-catalog.yaml",
	}
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}

	entries, source, _, found, err := store.LoadProviderCatalogSnapshot(false)
	if err != nil {
		t.Fatal(err)
	}
	if !found || source != "builtin" || len(entries) < 5 {
		t.Fatalf("expected builtin provider catalog, found=%v source=%q entries=%d", found, source, len(entries))
	}
}

func writeProviderCatalogFixture(t *testing.T, catalogFile string) {
	t.Helper()
	providers := map[string]any{}
	ids := []string{"openai", "anthropic", "google", "fresh-provider"}
	for index := len(ids); index < providerCatalogMinProviders; index++ {
		ids = append(ids, fmt.Sprintf("provider-%02d", index))
	}
	for _, id := range ids {
		models := make([]map[string]any, 0, 5)
		for modelIndex := 0; modelIndex < 5; modelIndex++ {
			modelID := fmt.Sprintf("%s-model-%d", id, modelIndex)
			models = append(models, map[string]any{"id": modelID, "name": modelID})
		}
		providers[id] = map[string]any{
			"name":   id,
			"api":    "https://" + id + ".example/v1",
			"models": models,
		}
	}
	content, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func replaceProviderCatalogFixtureURL(t *testing.T, catalogFile string, providerID string, baseURL string) {
	t.Helper()
	content, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatal(err)
	}
	provider, ok := payload.Providers[providerID]
	if !ok {
		t.Fatalf("missing fixture provider %q", providerID)
	}
	provider["api"] = baseURL
	content, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func providerCatalogContains(entries []ProviderCatalogEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

type providerCatalogHTTPClientFunc func(*http.Request) (*http.Response, error)

func (fn providerCatalogHTTPClientFunc) Do(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type providerCatalogConcurrentInitializeStore struct {
	Store
	probe *providerCatalogConcurrentInitializeProbe
}

func (s *providerCatalogConcurrentInitializeStore) SaveProviderCatalogSnapshot(entries []ProviderCatalogEntry, source string, fetchedAt time.Time) error {
	if source == "local-provider-catalog" {
		s.probe.beginLocalSnapshotWrite()
	}
	err := s.Store.SaveProviderCatalogSnapshot(entries, source, fetchedAt)
	if source == "local-provider-catalog" {
		s.probe.endLocalSnapshotWrite(err == nil)
	}
	return err
}

type providerCatalogConcurrentInitializeProbe struct {
	mu                        sync.Mutex
	localSnapshotWrites       int
	activeLocalSnapshotWrites int
	maxLocalSnapshotWrites    int
}

func newProviderCatalogConcurrentInitializeProbe() *providerCatalogConcurrentInitializeProbe {
	return &providerCatalogConcurrentInitializeProbe{}
}

func (p *providerCatalogConcurrentInitializeProbe) beginLocalSnapshotWrite() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeLocalSnapshotWrites++
	if p.activeLocalSnapshotWrites > p.maxLocalSnapshotWrites {
		p.maxLocalSnapshotWrites = p.activeLocalSnapshotWrites
	}
}

func (p *providerCatalogConcurrentInitializeProbe) endLocalSnapshotWrite(succeeded bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeLocalSnapshotWrites--
	if succeeded {
		p.localSnapshotWrites++
	}
}

func (p *providerCatalogConcurrentInitializeProbe) localSnapshotWriteCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.localSnapshotWrites
}

func (p *providerCatalogConcurrentInitializeProbe) maxConcurrentLocalSnapshotWrites() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxLocalSnapshotWrites
}
