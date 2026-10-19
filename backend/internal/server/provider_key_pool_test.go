package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProviderAPIKeyPoolDistributesRequests(t *testing.T) {
	fixture := newProviderKeyPoolFixture(t, nil)
	for index := 0; index < 64; index++ {
		response := fixture.request(t)
		if response.Code != http.StatusOK {
			t.Fatalf("pooled request %d = %d: %s", index, response.Code, response.Body)
		}
	}
	counts := fixture.upstreamCounts()
	if counts["synthetic-pool-key-1"] == 0 || counts["synthetic-pool-key-2"] == 0 {
		t.Fatalf("both keys should receive requests, got %v", counts)
	}
	if len(counts) != 2 {
		t.Fatalf("unexpected upstream credentials used: %v", counts)
	}
	logs := fixture.store.ListRequestLogs()
	if len(logs) != 64 {
		t.Fatalf("request log count = %d, want 64", len(logs))
	}
	for _, log := range logs {
		if log.ProviderID != fixture.provider.ID || (log.ProviderResourceID != fixture.resources[0].ID && log.ProviderResourceID != fixture.resources[1].ID) {
			t.Fatalf("request was not attributed to a pooled key: %+v", log)
		}
	}
}

func TestProviderAPIKeyPoolFailsOverAtConcurrencyLimit(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	fixture := newProviderKeyPoolFixture(t, func(key string) {
		if key == "synthetic-pool-key-1" {
			select {
			case entered <- struct{}{}:
			case <-release:
				return
			}
			<-release
		}
	})
	fixture.route.Strategy = RouteStrategyPriorityOnly
	if _, err := fixture.store.UpdateRoute(fixture.route.ID, fixture.route); err != nil {
		t.Fatal(err)
	}
	first := make(chan responseBody, 1)
	go func() { first <- fixture.request(t) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach the upstream")
	}
	second := make(chan responseBody, 1)
	go func() { second <- fixture.request(t) }()
	select {
	case response := <-second:
		if response.Code != http.StatusOK {
			t.Fatalf("concurrent request = %d: %s", response.Code, response.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent request did not fail over to the available key")
	}
	counts := fixture.upstreamCounts()
	if counts["synthetic-pool-key-1"] != 1 || counts["synthetic-pool-key-2"] != 1 {
		t.Fatalf("upstream requests should use separate keys: %v", counts)
	}
	unblock()
	select {
	case response := <-first:
		if response.Code != http.StatusOK {
			t.Fatalf("first request = %d: %s", response.Code, response.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not complete after release")
	}
	var skipped RouteAttemptLog
	if err := fixture.store.db.Where("provider_resource_id = ? AND invoked = ? AND error_code = ?", fixture.resources[0].ID, false, "provider_resource_concurrency_exceeded").First(&skipped).Error; err != nil {
		t.Fatalf("missing concurrency failover attempt: %v", err)
	}
}

func TestProviderAPIKeyPoolPreservesExplicitResourceBinding(t *testing.T) {
	fixture := newProviderKeyPoolFixture(t, nil)
	resource := fixture.resources[0]
	fixture.route.ProviderResourceID = resource.ID
	if _, err := fixture.store.UpdateRoute(fixture.route.ID, fixture.route); err != nil {
		t.Fatal(err)
	}
	leaseID, _, err := fixture.store.CheckProviderResourceCapacity(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.store.ReleaseProviderResourceCapacity(resource.ID, leaseID)
	response := fixture.request(t)
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body, "provider_resource_concurrency_exceeded") {
		t.Fatalf("bound resource at capacity = %d: %s", response.Code, response.Body)
	}
	if counts := fixture.upstreamCounts(); len(counts) != 0 {
		t.Fatalf("explicit binding must not use another pooled key: %v", counts)
	}
	fixture.store.ReleaseProviderResourceCapacity(resource.ID, leaseID)
	response = fixture.request(t)
	if response.Code != http.StatusOK {
		t.Fatalf("bound resource after release = %d: %s", response.Code, response.Body)
	}
	if counts := fixture.upstreamCounts(); len(counts) != 1 || counts["synthetic-pool-key-1"] != 1 {
		t.Fatalf("request did not use the bound key: %v", counts)
	}
}

func TestProviderAPIKeyPoolRotatesLegacyZeroPriorityWithoutReordering(t *testing.T) {
	fixture := newProviderKeyPoolFixture(t, nil)
	fixture.route.Strategy = RouteStrategyPriorityOnly
	if _, err := fixture.store.UpdateRoute(fixture.route.ID, fixture.route); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DeleteProviderResource(fixture.resources[1].ID); err != nil {
		t.Fatal(err)
	}
	created := doJSON(t, fixture.app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"provider_id": fixture.provider.ID, "name": "Legacy default-priority key",
		"resource_type": ProviderResourceAPIKey, "api_key": "synthetic-legacy-pool-key",
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create legacy key = %d: %s", created.Code, created.Body)
	}
	var resource ProviderResource
	if err := json.Unmarshal([]byte(created.Body), &resource); err != nil {
		t.Fatal(err)
	}
	if resource.Priority != 0 {
		t.Fatalf("omitted resource priority = %d, want zero", resource.Priority)
	}
	if response := fixture.request(t); response.Code != http.StatusOK {
		t.Fatalf("request before rotation = %d: %s", response.Code, response.Body)
	}
	rotatedKey := "synthetic-pool-key-2"
	updated := doJSON(t, fixture.app, http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, map[string]any{
		"name": "Renamed default-priority key", "api_key": rotatedKey,
		"priority": 0, "weight": resource.Weight, "status": resource.Status, "healthy": resource.Healthy,
	}, "")
	if updated.Code != http.StatusOK || strings.Contains(updated.Body, rotatedKey) {
		t.Fatalf("rotate legacy key = %d: %s", updated.Code, updated.Body)
	}
	stored, ok := fixture.store.GetProviderResource(resource.ID)
	if !ok || stored.Priority != 0 || stored.Name != "Renamed default-priority key" || stored.APIKey != rotatedKey {
		t.Fatal("rotation must preserve zero priority while updating the name and credential")
	}
	if response := fixture.request(t); response.Code != http.StatusOK {
		t.Fatalf("request after rotation = %d: %s", response.Code, response.Body)
	}
	if counts := fixture.upstreamCounts(); len(counts) != 1 || counts["synthetic-pool-key-1"] != 2 {
		t.Fatalf("zero-priority key must remain behind the explicitly prioritized key: %v", counts)
	}
	fixture.route.ProviderResourceID = resource.ID
	if _, err := fixture.store.UpdateRoute(fixture.route.ID, fixture.route); err != nil {
		t.Fatal(err)
	}
	if response := fixture.request(t); response.Code != http.StatusOK {
		t.Fatalf("bound request after rotation = %d: %s", response.Code, response.Body)
	}
	if counts := fixture.upstreamCounts(); counts[rotatedKey] != 1 {
		t.Fatalf("bound request did not use the rotated credential: %v", counts)
	}
}

func TestProviderAPIKeyPoolBulkEnableRestoresFailedBoundKey(t *testing.T) {
	fixture := newProviderKeyPoolFixture(t, nil)
	resource := fixture.resources[0]
	resource.BaseURL = fixture.provider.BaseURL
	resource.Group = "key-pool-lifecycle"
	resource.Priority = 2
	resource.Weight = 75
	resource.RateLimitRPM = 20
	resource.TokenLimitTPM = 10000
	if _, err := fixture.store.UpdateProviderResource(resource.ID, resource); err != nil {
		t.Fatal(err)
	}
	fixture.route.ProviderResourceID = resource.ID
	if _, err := fixture.store.UpdateRoute(fixture.route.ID, fixture.route); err != nil {
		t.Fatal(err)
	}
	fixture.store.failureThreshold = 2
	tripBreaker(t, fixture.store, resource.ID)

	for _, action := range []string{"disable", "enable"} {
		response := doJSON(t, fixture.app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
			"action": action, "ids": []string{resource.ID},
		}, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s pooled key = %d: %s", action, response.Code, response.Body)
		}
		var result ProviderResourceBulkResult
		if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
			t.Fatal(err)
		}
		if result.Success != 1 || result.Failed != 0 || strings.Contains(response.Body, "synthetic-pool-key-") {
			t.Fatalf("unexpected %s result: %s", action, response.Body)
		}
		stored, ok := fixture.store.GetProviderResource(resource.ID)
		if !ok || stored.APIKey != "synthetic-pool-key-1" {
			t.Fatalf("%s lost the pooled key credential", action)
		}
		if stored.BaseURL != resource.BaseURL || stored.Group != resource.Group ||
			stored.Priority != resource.Priority || stored.Weight != resource.Weight ||
			stored.RateLimitRPM != resource.RateLimitRPM || stored.TokenLimitTPM != resource.TokenLimitTPM ||
			stored.MaxConcurrency != resource.MaxConcurrency {
			t.Fatalf("%s changed connection or scheduling settings", action)
		}
		var route ModelRoute
		if err := fixture.store.db.First(&route, "id = ?", fixture.route.ID).Error; err != nil {
			t.Fatal(err)
		}
		if route.ProviderResourceID != resource.ID || route.Status != StatusActive {
			t.Fatalf("%s changed the explicit route binding: %+v", action, route)
		}
		if action == "disable" {
			if stored.Status != StatusDisabled || stored.Healthy || stored.FailureCount == 0 {
				t.Fatal("disable must deactivate the failed resource without clearing its failure history")
			}
			if response := fixture.request(t); response.Code == http.StatusOK {
				t.Fatal("a route bound to the disabled resource must not use another pooled key")
			}
			if counts := fixture.upstreamCounts(); len(counts) != 0 {
				t.Fatalf("disabled bound key reached the upstream: %v", counts)
			}
		} else if stored.Status != StatusActive || !stored.Healthy || stored.FailureCount != 0 || stored.CooldownUntil != nil {
			t.Fatal("enable must reactivate the resource and reset health, failures, and cooldown")
		}
	}
	if response := fixture.request(t); response.Code != http.StatusOK {
		t.Fatalf("request after enabling the pooled key = %d: %s", response.Code, response.Body)
	}
	if counts := fixture.upstreamCounts(); len(counts) != 1 || counts["synthetic-pool-key-1"] != 1 {
		t.Fatalf("request after enabling did not use the preserved bound key: %v", counts)
	}
}

type providerKeyPoolFixture struct {
	store     *GormStore
	app       http.Handler
	provider  Provider
	resources []ProviderResource
	route     ModelRoute
	secret    string
	mu        sync.Mutex
	counts    map[string]int
}

func newProviderKeyPoolFixture(t *testing.T, beforeResponse func(string)) *providerKeyPoolFixture {
	t.Helper()
	fixture := &providerKeyPoolFixture{counts: map[string]int{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		fixture.mu.Lock()
		fixture.counts[key]++
		fixture.mu.Unlock()
		if r.URL.Path != "/v1/chat/completions" || (key != "synthetic-pool-key-1" && key != "synthetic-pool-key-2") {
			http.Error(w, "unexpected upstream request or credential", http.StatusUnauthorized)
			return
		}
		if beforeResponse != nil {
			beforeResponse(key)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "synthetic-chat", "object": "chat.completion", "model": "pool-chat",
			"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": "Pool response"}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	t.Cleanup(upstream.Close)
	fixture.store = NewMemoryStoreWithConfig(Config{SecretKey: "synthetic-key-pool-encryption-key"})
	t.Cleanup(func() { _ = fixture.store.Close() })
	project := fixture.store.CreateProject(Project{Name: "Key pool application", Status: StatusActive})
	_, secret, err := fixture.store.CreateAPIKey(project.ID, APIKey{Name: "Key pool client", Status: StatusActive, Allowed: []string{"pool-chat"}}, "thk_synthetic_pool_client")
	if err != nil {
		t.Fatal(err)
	}
	fixture.secret = secret
	fixture.provider = fixture.store.AddProvider(Provider{
		ID: "prv_key_pool", Name: "Key pool provider", Type: ProviderOpenAICompatible,
		BaseURL: upstream.URL + "/v1", APIKey: "synthetic-default-provider-key", Status: StatusActive, Healthy: true,
	})
	fixture.store.AddModel(Model{Name: "pool-chat", Modality: "chat", Status: StatusActive})
	fixture.route = fixture.store.AddRoute(ModelRoute{
		ID: "route_key_pool", ModelName: "pool-chat", ProviderID: fixture.provider.ID, ProviderModel: "pool-chat",
		Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityWeighted,
	})
	fixture.app = New(fixture.store).Handler()
	for index := 1; index <= 2; index++ {
		key := fmt.Sprintf("synthetic-pool-key-%d", index)
		created := doJSON(t, fixture.app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
			"id": fmt.Sprintf("rsrc_key_pool_%d", index), "provider_id": fixture.provider.ID,
			"name": fmt.Sprintf("Pool key %d", index), "resource_type": ProviderResourceAPIKey, "api_key": key,
			"status": StatusActive, "healthy": true, "priority": 1, "weight": 100, "max_concurrency": 1,
		}, "")
		if created.Code != http.StatusCreated {
			t.Fatalf("create pooled key = %d: %s", created.Code, created.Body)
		}
		if strings.Contains(created.Body, key) {
			t.Fatal("resource create response exposed the key")
		}
		var resource ProviderResource
		if err := json.Unmarshal([]byte(created.Body), &resource); err != nil {
			t.Fatal(err)
		}
		fixture.resources = append(fixture.resources, resource)
		var stored ProviderResource
		if err := fixture.store.db.First(&stored, "id = ?", resource.ID).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stored.APIKey, "enc:v1:") || strings.Contains(stored.APIKey, key) {
			t.Fatal("pooled resource key was not encrypted at rest")
		}
	}
	listed := doJSON(t, fixture.app, http.MethodGet, "/api/admin/provider-resources", nil, "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body, "synthetic-pool-key-") {
		t.Fatalf("resource list did not redact keys: status=%d", listed.Code)
	}
	audit, err := json.Marshal(fixture.store.ListAuditEvents())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), "synthetic-pool-key-") {
		t.Fatal("resource audit events exposed pooled keys")
	}
	return fixture
}

func (fixture *providerKeyPoolFixture) request(t *testing.T) responseBody {
	t.Helper()
	return doJSON(t, fixture.app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "pool-chat", "messages": []map[string]string{{"role": "user", "content": "Use a pooled key"}},
	}, fixture.secret)
}

func (fixture *providerKeyPoolFixture) upstreamCounts() map[string]int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	counts := make(map[string]int, len(fixture.counts))
	for key, count := range fixture.counts {
		counts[key] = count
	}
	return counts
}
