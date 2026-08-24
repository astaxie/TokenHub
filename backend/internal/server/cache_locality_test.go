package server

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"strings"
	"testing"
)

func responsesRequestFromJSON(t *testing.T, payload string) ResponsesRequest {
	t.Helper()
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func newCacheLocalityStore(t *testing.T, providerModels []string) (*GormStore, []RouteSelection) {
	t.Helper()
	store := NewMemoryStore()
	store.AddModel(Model{Name: "locality-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID:      "prv_locality",
		Name:    "locality",
		Type:    ProviderOpenAICompatible,
		BaseURL: "https://upstream.invalid",
		Status:  StatusActive,
		Healthy: true,
	})
	for index, providerModel := range providerModels {
		route := ModelRoute{
			ID:            fmt.Sprintf("route_locality_%d", index),
			ModelName:     "locality-model",
			ProviderID:    provider.ID,
			ProviderModel: providerModel,
			Priority:      1,
			Weight:        100,
			Status:        StatusActive,
			Strategy:      RouteStrategyBalanced,
		}
		store.AddRoute(route)
	}
	routes, err := store.SelectRouteCandidates("locality-model")
	if err != nil {
		t.Fatal(err)
	}
	return store, routes
}

func localityAffinity(t *testing.T, session string) *RequestAffinity {
	t.Helper()
	affinity, err := resolveCacheLocalityAffinity("secret", "key_alpha", session, sessionScopeSession, false)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil {
		t.Fatal("expected affinity")
	}
	return affinity
}

func TestSameSessionPinsToSameCacheDomain(t *testing.T) {
	store, routes := newCacheLocalityStore(t, []string{"model-a", "model-b", "model-c"})
	server := New(store)
	affinity := localityAffinity(t, "session-stable")

	var first string
	for turn := 0; turn < 12; turn++ {
		call := CallContext{RequestID: fmt.Sprintf("req_%d", turn), Affinity: affinity}
		planned := server.planRouteOrder(call, routes)
		domain := cacheDomainID(planned[0])
		if turn == 0 {
			first = domain
			continue
		}
		if domain != first {
			t.Fatalf("turn %d drifted to a different cache domain: %q vs %q", turn, domain, first)
		}
	}
}

func TestDistinctSessionsSpreadAcrossDomains(t *testing.T) {
	store, routes := newCacheLocalityStore(t, []string{"model-a", "model-b", "model-c"})
	server := New(store)

	counts := map[string]int{}
	for index := 0; index < 300; index++ {
		affinity := localityAffinity(t, fmt.Sprintf("session-%d", index))
		call := CallContext{RequestID: fmt.Sprintf("req_%d", index), Affinity: affinity}
		planned := server.planRouteOrder(call, routes)
		counts[cacheDomainID(planned[0])]++
	}
	if len(counts) != 3 {
		t.Fatalf("expected all three domains to receive traffic, got %v", counts)
	}
	for domain, count := range counts {
		if count < 60 || count > 140 {
			t.Fatalf("domain %q got %d of 300 sessions, expected roughly even spread: %v", domain, count, counts)
		}
	}
}

// Without a session identifier the original random spread must be preserved:
// introducing affinity must not change behaviour here.
func TestNoAffinityKeepsRequestIDSpread(t *testing.T) {
	store, routes := newCacheLocalityStore(t, []string{"model-a", "model-b", "model-c"})
	server := New(store)

	domains := map[string]struct{}{}
	for index := 0; index < 60; index++ {
		call := CallContext{RequestID: fmt.Sprintf("req_%d", index)}
		planned := server.planRouteOrder(call, routes)
		domains[cacheDomainID(planned[0])] = struct{}{}
	}
	if len(domains) < 2 {
		t.Fatalf("expected request-id routing to still spread, got %v", domains)
	}
}

func TestCacheDomainFallsBackToProviderID(t *testing.T) {
	route := RouteSelection{
		Provider:      Provider{ID: "prv_x"},
		ProviderModel: "m1",
	}
	domain := cacheDomainID(route)
	if domain != "prov:prv_x|model:m1" {
		t.Fatalf("unexpected fallback domain: %q", domain)
	}
	// Different upstream models under one provider must be different domains:
	// measured, DeepSeek does not share prefix cache across models on one key.
	other := cacheDomainID(RouteSelection{Provider: Provider{ID: "prv_x"}, ProviderModel: "m2"})
	if domain == other {
		t.Fatal("different provider models must map to different cache domains")
	}
}

func TestCacheDomainStableAcrossRouteRecreate(t *testing.T) {
	base := RouteSelection{
		Provider:      Provider{ID: "prv_x"},
		ProviderModel: "m1",
		Route:         ModelRoute{ID: "route_original"},
	}
	recreated := base
	recreated.Route.ID = "route_recreated_with_new_id"
	if cacheDomainID(base) != cacheDomainID(recreated) {
		t.Fatal("cache domain must not depend on route ID; recreating a route would reshuffle every session")
	}
}

func TestCacheDomainPrefersAccountOverResource(t *testing.T) {
	withOrg := RouteSelection{
		Provider:      Provider{ID: "prv_x", Options: map[string]string{"organization_id": "org-1"}},
		ProviderModel: "m1",
	}
	if got := cacheDomainID(withOrg); got != "org:org-1|model:m1" {
		t.Fatalf("organization_id should win: %q", got)
	}
	withAccount := RouteSelection{
		Provider:      Provider{ID: "prv_x", Options: map[string]string{"account_id": "acct-1"}},
		ProviderModel: "m1",
	}
	if got := cacheDomainID(withAccount); got != "acct:acct-1|model:m1" {
		t.Fatalf("account_id should be used: %q", got)
	}
}

// -Inf boundary: when unit == 1, -math.Log returns negative zero and the division
// yields -Inf, which would sort that candidate last forever.
func TestRendezvousScoreHandlesUnitBoundary(t *testing.T) {
	for _, weight := range []int{1, 100} {
		score := weightedRendezvousScore("any-key", "any-identity", weight)
		if score <= 0 {
			t.Fatalf("expected a positive finite score, got %v", score)
		}
	}
}

func TestCacheAffinityRespectsModelAllowlist(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{
		SecretKey:            "secret",
		CacheAffinityEnabled: true,
		CacheAffinityModels:  []string{"allowed-model"},
	})
	headers := make(http.Header)
	headers.Set("x-tokenhub-session-id", "session-1")

	affinity, err := server.chatCacheLocalityAffinity("key_alpha", headers,
		ChatCompletionRequest{Model: "allowed-model"})
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil {
		t.Fatal("expected affinity for an allowlisted model")
	}

	affinity, err = server.chatCacheLocalityAffinity("key_alpha", headers,
		ChatCompletionRequest{Model: "other-model"})
	if err != nil {
		t.Fatal(err)
	}
	if affinity != nil {
		t.Fatal("expected no affinity for a model outside the allowlist")
	}
}

func TestCacheAffinityDisabledByDefault(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{SecretKey: "secret"})
	headers := make(http.Header)
	headers.Set("x-tokenhub-session-id", "session-1")
	affinity, err := server.chatCacheLocalityAffinity("key_alpha", headers,
		ChatCompletionRequest{Model: "any-model"})
	if err != nil {
		t.Fatal(err)
	}
	if affinity != nil {
		t.Fatal("cache affinity must be off unless explicitly enabled")
	}
}

func TestResponsesCacheAffinityUsesCodexSessionHints(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{
		SecretKey:            "secret",
		CacheAffinityEnabled: true,
		CacheAffinityModels:  []string{"deepseek-v4-flash"},
	})
	request := responsesRequestFromJSON(t, `{
		"model":"deepseek-v4-flash",
		"input":"hello",
		"prompt_cache_key":"prompt-session",
		"client_metadata":{"session_id":"metadata-session"},
		"user":"user-session"
	}`)
	headers := make(http.Header)
	headers.Set("session-id", "header-session")

	affinity, err := server.responsesCacheLocalityAffinity("key_alpha", headers, request)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil || affinity.Kind != AffinityKindCacheLocality {
		t.Fatalf("expected stateless Responses cache affinity, got %#v", affinity)
	}
	want, err := resolveCacheLocalityAffinity("secret", "key_alpha", "header-session", sessionScopeSession, false)
	if err != nil {
		t.Fatal(err)
	}
	if affinity.KeyHash != want.KeyHash {
		t.Fatalf("session-id must take precedence: got %q want %q", affinity.KeyHash, want.KeyHash)
	}

	headers.Del("session-id")
	affinity, err = server.responsesCacheLocalityAffinity("key_alpha", headers, request)
	if err != nil {
		t.Fatal(err)
	}
	want, err = resolveCacheLocalityAffinity("secret", "key_alpha", "metadata-session", sessionScopeSession, false)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil || affinity.KeyHash != want.KeyHash {
		t.Fatalf("client_metadata.session_id must precede prompt_cache_key: got %#v want %#v", affinity, want)
	}
}

func TestResponsesCacheAffinityUserScopeRequiresOptIn(t *testing.T) {
	request := responsesRequestFromJSON(t, `{"model":"deepseek-v4-flash","input":"hello","user":"tenant-user"}`)
	server := NewWithConfig(NewMemoryStore(), Config{
		SecretKey:            "secret",
		CacheAffinityEnabled: true,
	})
	affinity, err := server.responsesCacheLocalityAffinity("key_alpha", nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if affinity != nil {
		t.Fatal("Responses user scope must remain opt-in")
	}

	server.config.CacheAffinityAllowUserScope = true
	affinity, err = server.responsesCacheLocalityAffinity("key_alpha", nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil {
		t.Fatal("expected Responses user scope affinity after opt-in")
	}
}

// User-scoped identifiers stay out of affinity by default: one user's concurrent
// sessions share the value and would create an account hotspot.
func TestUserScopeIdentifierRequiresOptIn(t *testing.T) {
	affinity, err := resolveCacheLocalityAffinity("secret", "key_alpha", "user-1", sessionScopeUser, false)
	if err != nil {
		t.Fatal(err)
	}
	if affinity != nil {
		t.Fatal("user-scope identifiers must be opt-in")
	}
	affinity, err = resolveCacheLocalityAffinity("secret", "key_alpha", "user-1", sessionScopeUser, true)
	if err != nil {
		t.Fatal(err)
	}
	if affinity == nil {
		t.Fatal("expected affinity once user scope is opted in")
	}
}

// Cache locality is stateless and must never touch the binding table.
func TestCacheLocalityDoesNotPersistBinding(t *testing.T) {
	locality := &RequestAffinity{Kind: AffinityKindCacheLocality, KeyHash: "abc"}
	if locality.persistsBinding() {
		t.Fatal("cache locality must not persist bindings")
	}
	codex := &RequestAffinity{Kind: AffinityKindCodexSession, KeyHash: "abc"}
	if !codex.persistsBinding() {
		t.Fatal("codex session affinity must keep persisting bindings")
	}
}

// Deterministic strategies already provide cache locality; the switch must not
// change their behaviour.
func TestDeterministicStrategiesIgnoreAffinity(t *testing.T) {
	for _, strategy := range []string{RouteStrategyPriorityOnly, RouteStrategyQuality, RouteStrategyCost} {
		t.Run(strategy, func(t *testing.T) {
			store := NewMemoryStore()
			store.AddModel(Model{Name: "locality-model", Modality: "chat", Status: StatusActive})
			provider := store.AddProvider(Provider{
				ID: "prv_locality", Name: "locality", Type: ProviderOpenAICompatible,
				BaseURL: "https://upstream.invalid", Status: StatusActive, Healthy: true,
			})
			for index, providerModel := range []string{"model-a", "model-b", "model-c"} {
				store.AddRoute(ModelRoute{
					ID:        fmt.Sprintf("route_%s_%d", strategy, index),
					ModelName: "locality-model", ProviderID: provider.ID,
					ProviderModel: providerModel, Priority: 1, Weight: 100,
					Status: StatusActive, Strategy: strategy,
				})
			}
			routes, err := store.SelectRouteCandidates("locality-model")
			if err != nil {
				t.Fatal(err)
			}
			server := New(store)

			baseline := server.planRouteOrder(CallContext{RequestID: "req_1"}, routes)
			withAffinity := server.planRouteOrder(
				CallContext{RequestID: "req_1", Affinity: localityAffinity(t, "session-x")}, routes)
			for index := range baseline {
				if routeSortID(baseline[index]) != routeSortID(withAffinity[index]) {
					t.Fatalf("%s ordering changed at %d: %q vs %q", strategy, index,
						routeSortID(baseline[index]), routeSortID(withAffinity[index]))
				}
			}
		})
	}
}

// The sticky bypass applies only to cache locality; Codex keeps its existing
// semantics.
func TestStickyBypassOnlyForCacheLocality(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: "locality-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_locality", Name: "locality", Type: ProviderOpenAICompatible,
		BaseURL: "https://upstream.invalid", Status: StatusActive, Healthy: true,
	})
	for index, providerModel := range []string{"model-a", "model-b", "model-c"} {
		store.AddRoute(ModelRoute{
			ID: fmt.Sprintf("route_sticky_%d", index), ModelName: "locality-model",
			ProviderID: provider.ID, ProviderModel: providerModel,
			Priority: 1, Weight: 100, Status: StatusActive,
			Strategy: RouteStrategyBalanced, StickySession: true,
		})
	}
	routes, err := store.SelectRouteCandidates("locality-model")
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	key := APIKey{ID: "key_alpha"}

	codexAffinity := &RequestAffinity{
		AdapterType: ProviderOpenAICodex,
		Kind:        AffinityKindCodexSession,
		KeyHash:     "codexhash",
	}
	stickyFirst := server.planRouteOrder(CallContext{RequestID: "r", Key: key}, routes)
	codexFirst := server.planRouteOrder(CallContext{RequestID: "r", Key: key, Affinity: codexAffinity}, routes)
	if routeSortID(stickyFirst[0]) != routeSortID(codexFirst[0]) {
		t.Fatalf("codex affinity must not bypass sticky: %q vs %q",
			routeSortID(codexFirst[0]), routeSortID(stickyFirst[0]))
	}
}

// Codex scoring must keep using FNV-1a: this function is not guarded by
// CACHE_AFFINITY_ENABLED, so changing the hash would reassign every existing
// Codex session that has no binding yet.
func TestCodexRendezvousStillUsesFNV(t *testing.T) {
	expected := func(key, identity string, weight int) float64 {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(identity))
		unit := (float64(hash.Sum64()) + 1) / (float64(^uint64(0)) + 2)
		return float64(weight) / -math.Log(unit)
	}
	for _, testCase := range []struct{ key, identity string }{
		{"codexhash", "route_a:rsrc_1"},
		{"another-session", "route_b:rsrc_2"},
	} {
		got := weightedRendezvousScore(testCase.key, testCase.identity, 100)
		want := expected(testCase.key, testCase.identity, 100)
		if got != want {
			t.Fatalf("codex rendezvous score drifted for %q/%q: got %v want %v",
				testCase.key, testCase.identity, got, want)
		}
	}
	// The two must stay distinct, otherwise the cache-domain skew regresses.
	if weightedRendezvousScore("k", "id", 100) == weightedCacheDomainScore("k", "id", 100) {
		t.Fatal("cache domain scoring must not fall back to the codex hash")
	}
}

func TestRendezvousSortPrecomputesScores(t *testing.T) {
	group := []RouteSelection{
		{Route: ModelRoute{ID: "route_c", Weight: 30}},
		{Route: ModelRoute{ID: "route_a", Weight: 10}},
		{Route: ModelRoute{ID: "route_b", Weight: 20}},
	}
	scores := map[string]float64{
		"route_a": 10,
		"route_b": 20,
		"route_c": 20,
	}
	calls := map[string]int{}
	sortRouteGroupByRendezvous("affinity-key", group, routeSortID, func(key, identity string, weight int) float64 {
		if key != "affinity-key" {
			t.Fatalf("routing key = %q, want affinity-key", key)
		}
		calls[identity]++
		return scores[identity]
	})

	want := []string{"route_b", "route_c", "route_a"}
	for index := range group {
		identity := routeSortID(group[index])
		if identity != want[index] {
			t.Fatalf("route at %d = %q, want %q", index, identity, want[index])
		}
		if calls[identity] != 1 {
			t.Fatalf("score calls for %q = %d, want 1", identity, calls[identity])
		}
	}

	singletonCalls := 0
	singleton := []RouteSelection{{Route: ModelRoute{ID: "route_single", Weight: 100}}}
	sortRouteGroupByRendezvous("affinity-key", singleton, routeSortID, func(string, string, int) float64 {
		singletonCalls++
		return 1
	})
	if singletonCalls != 0 {
		t.Fatalf("singleton score calls = %d, want 0", singletonCalls)
	}
}

// With the switch off the whole routing path must stay byte-identical to the
// pre-change behaviour.
func TestSwitchOffIsByteIdenticalToLegacyRouting(t *testing.T) {
	store, routes := newCacheLocalityStore(t, []string{"model-a", "model-b", "model-c"})
	server := NewWithConfig(store, Config{SecretKey: "secret"}) // CacheAffinityEnabled defaults to false
	headers := make(http.Header)
	headers.Set("x-tokenhub-session-id", "session-should-be-ignored")

	for index := 0; index < 40; index++ {
		request := ChatCompletionRequest{Model: "locality-model"}
		affinity, err := server.chatCacheLocalityAffinity("key_alpha", headers, request)
		if err != nil {
			t.Fatal(err)
		}
		if affinity != nil {
			t.Fatal("switch is off; no affinity may be produced")
		}
		call := CallContext{RequestID: fmt.Sprintf("req_%d", index)}
		withSwitch := server.planRouteOrder(call, routes)
		legacy := server.planRouteOrder(CallContext{RequestID: call.RequestID}, routes)
		for position := range legacy {
			if routeSortID(withSwitch[position]) != routeSortID(legacy[position]) {
				t.Fatalf("route order changed while the switch is off at %d", position)
			}
		}
	}
}

// Routes backed by real provider resources must map to per-resource cache domains:
// this is the multi-account shape the whole feature exists for.
func TestResourceBackedRoutesFormDistinctCacheDomains(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: "locality-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_multi_account", Name: "multi-account", Type: ProviderOpenAICompatible,
		BaseURL: "https://upstream.invalid", Status: StatusActive, Healthy: true,
	})
	for index := 0; index < 3; index++ {
		if _, err := store.AddProviderResource(ProviderResource{
			ProviderID:   provider.ID,
			Name:         fmt.Sprintf("account-%d", index),
			ResourceType: ProviderResourceAPIKey,
			APIKey:       fmt.Sprintf("key-%d", index),
			Status:       StatusActive,
			Healthy:      true,
			Weight:       100,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One route, no explicit resource: SelectRouteCandidates fans it out per account.
	store.AddRoute(ModelRoute{
		ID: "route_multi_account", ModelName: "locality-model", ProviderID: provider.ID,
		ProviderModel: "shared-upstream-model", Priority: 1, Weight: 100,
		Status: StatusActive, Strategy: RouteStrategyBalanced,
	})
	routes, err := store.SelectRouteCandidates("locality-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected one candidate per account, got %d", len(routes))
	}
	domains := map[string]struct{}{}
	for _, route := range routes {
		domain := cacheDomainID(route)
		if !strings.HasPrefix(domain, "rsrc:") {
			t.Fatalf("expected a resource-scoped domain, got %q", domain)
		}
		domains[domain] = struct{}{}
	}
	if len(domains) != 3 {
		t.Fatalf("each account must be its own cache domain, got %v", domains)
	}

	server := New(store)
	// One session must stay on a single account across turns...
	affinity := localityAffinity(t, "resource-session")
	var pinned string
	for turn := 0; turn < 10; turn++ {
		planned := server.planRouteOrder(
			CallContext{RequestID: fmt.Sprintf("req_%d", turn), Affinity: affinity}, routes)
		domain := cacheDomainID(planned[0])
		if turn == 0 {
			pinned = domain
		} else if domain != pinned {
			t.Fatalf("turn %d drifted from %q to %q", turn, pinned, domain)
		}
	}
	// ...while distinct sessions still use every account.
	spread := map[string]int{}
	for index := 0; index < 200; index++ {
		sessionAffinity := localityAffinity(t, fmt.Sprintf("resource-session-%d", index))
		planned := server.planRouteOrder(
			CallContext{RequestID: fmt.Sprintf("req_%d", index), Affinity: sessionAffinity}, routes)
		spread[cacheDomainID(planned[0])]++
	}
	if len(spread) != 3 {
		t.Fatalf("expected traffic across all accounts, got %v", spread)
	}
}
