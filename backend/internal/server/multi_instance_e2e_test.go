//go:build integration

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
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type multiInstanceBlockingAdapter struct {
	MockAdapter
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (a *multiInstanceBlockingAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	a.once.Do(func() { close(a.started) })
	select {
	case <-ctx.Done():
		return nil, Usage{}, ctx.Err()
	case <-a.release:
		return a.MockAdapter.Chat(ctx, provider, providerModel, req)
	}
}

func (a *multiInstanceBlockingAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	a.once.Do(func() { close(a.started) })
	select {
	case <-ctx.Done():
		return Usage{}, ctx.Err()
	case <-a.release:
		return a.MockAdapter.ChatStream(ctx, provider, providerModel, req, w)
	}
}

func TestMultiInstancePostgresE2E(t *testing.T) {
	storeA, storeB, config := openSharedPostgresStores(t)
	t.Run("concurrent migrations work with a one-connection runtime pool", func(t *testing.T) {
		testConcurrentMigrations(t, storeA, config)
	})
	t.Run("HTTP quotas and concurrency are cluster wide", func(t *testing.T) {
		testClusterWideHTTPEnforcement(t, storeA, storeB, config)
	})
	t.Run("OAuth state and refresh coordination survive replica changes", func(t *testing.T) {
		testSharedOAuthAndRefresh(t, storeA, storeB, config)
	})
	t.Run("startup task revision runs once", func(t *testing.T) {
		testClusterTaskRunsOnce(t, storeA, storeB)
	})
	t.Run("startup operations run on every start and serialize replicas", func(t *testing.T) {
		testClusterOperationRunsEveryStart(t, storeA, storeB)
	})
	t.Run("lost cluster leases cancel guarded work", func(t *testing.T) {
		testClusterLeaseLossCancelsWork(t, storeA, storeB)
	})
}

func testConcurrentMigrations(t *testing.T, adminStore *GormStore, config Config) {
	t.Helper()
	schema := fmt.Sprintf("tokenhub_e2e_migration_%d", time.Now().UnixNano())
	if err := adminStore.db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create fresh migration schema: %v", err)
	}
	defer func() {
		if err := adminStore.db.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop migration schema: %v", err)
		}
	}()
	parsedURL, err := url.Parse(config.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	config.DatabaseURL = parsedURL.String()
	config.DBMaxOpenConns = 1
	config.DBMaxIdleConns = 1
	stores := make(chan *GormStore, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store, err := NewStoreWithDialect(config.DatabaseURL, config)
			stores <- store
			errors <- err
		}()
	}
	wg.Wait()
	close(stores)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}
	for store := range stores {
		if store == nil {
			continue
		}
		if sqlDB, err := store.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	var tableCount int64
	if err := adminStore.db.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = ?", schema).Scan(&tableCount).Error; err != nil {
		t.Fatal(err)
	}
	if tableCount == 0 {
		t.Fatal("concurrent constructors did not create tables in the fresh schema")
	}
}

func openSharedPostgresStores(t *testing.T) (*GormStore, *GormStore, Config) {
	t.Helper()
	pgURL := strings.TrimSpace(os.Getenv("TEST_POSTGRES_URL"))
	if pgURL == "" {
		t.Skip("TEST_POSTGRES_URL not set, skipping multi-instance PostgreSQL E2E test")
	}
	config := Config{
		Environment:              "test",
		AdminToken:               "multi-instance-e2e-admin-token",
		SecretKey:                "multi-instance-e2e-secret-key",
		DatabaseURL:              pgURL,
		ResourceFailureThreshold: 3,
		ResourceCooldownSeconds:  300,
		InFlightLeaseTTLSeconds:  30,
		ClusterLockTTLSeconds:    30,
		DBMaxOpenConns:           10,
		DBMaxIdleConns:           2,
		DBConnMaxLifetimeMinutes: 5,
	}
	storeA, err := NewStoreWithDialect(pgURL, config)
	if err != nil {
		t.Fatalf("open first PostgreSQL store: %v", err)
	}
	storeB, err := NewStoreWithDialect(pgURL, config)
	if err != nil {
		t.Fatalf("open second PostgreSQL store: %v", err)
	}
	t.Cleanup(func() {
		for _, store := range []*GormStore{storeA, storeB} {
			if sqlDB, err := store.db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
	})
	return storeA, storeB, config
}

func testClusterWideHTTPEnforcement(t *testing.T, storeA *GormStore, storeB *GormStore, config Config) {
	t.Helper()
	suffix := NewID("e2e")
	project := storeA.CreateProject(Project{ID: "prj_" + suffix, Name: "Multi-instance E2E", Status: StatusActive})
	modelName := "model-" + suffix
	storeA.AddModel(Model{ID: modelName, Name: modelName, Modality: "chat", Status: StatusActive})
	provider := storeA.AddProvider(Provider{ID: "prv_" + suffix, Name: "E2E Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := storeA.AddProviderResource(ProviderResource{
		ID:           "rsrc_" + suffix,
		ProviderID:   provider.ID,
		Name:         "E2E Resource",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	storeA.AddRoute(ModelRoute{ID: "route_" + suffix, ModelName: modelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: modelName, Status: StatusActive, Priority: 1, Weight: 100})
	t.Cleanup(func() {
		_ = storeA.DeleteProvider(provider.ID)
		_ = storeA.DeleteModel(modelName)
		_ = storeA.DeleteProject(project.ID)
	})

	concurrencyKey, concurrencySecret, err := storeA.CreateAPIKey(project.ID, APIKey{
		ID:     "key_concurrency_" + suffix,
		Name:   "Cluster concurrency",
		Status: StatusActive,
		Limits: QuotaLimits{DailyRequests: 100, MonthlyRequests: 100, MaxConcurrency: 1},
	}, "thk_concurrency_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.DeleteAPIKey(concurrencyKey.ID)

	release := make(chan struct{})
	blocking := &multiInstanceBlockingAdapter{started: make(chan struct{}), release: release}
	serverA := NewWithConfig(storeA, config)
	serverA.adapters[ProviderMock] = blocking
	httpA := httptest.NewServer(serverA.Handler())
	defer httpA.Close()
	httpB := httptest.NewServer(NewWithConfig(storeB, config).Handler())
	defer httpB.Close()

	firstStatus := make(chan int, 1)
	go func() {
		status, _ := postChat(httpA.URL, concurrencySecret, modelName)
		firstStatus <- status
	}()
	select {
	case <-blocking.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach the blocking upstream")
	}
	status, body := postChat(httpB.URL, concurrencySecret, modelName)
	if status != http.StatusTooManyRequests || !strings.Contains(body, "rate_limit_exceeded") {
		t.Fatalf("second replica bypassed API key concurrency limit: status=%d body=%s", status, body)
	}
	close(release)
	if status := <-firstStatus; status != http.StatusOK {
		t.Fatalf("first request failed: status=%d", status)
	}
	if status, body := postChat(httpB.URL, concurrencySecret, modelName); status != http.StatusOK {
		t.Fatalf("capacity was not released cluster-wide: status=%d body=%s", status, body)
	}
	expiredLeaseID := "expired_" + suffix
	if err := storeA.db.Create(&InFlightLease{
		ID:        expiredLeaseID,
		ScopeType: "api_key",
		ScopeID:   concurrencyKey.ID,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		CreatedAt: time.Now().UTC().Add(-2 * time.Minute),
		UpdatedAt: time.Now().UTC().Add(-2 * time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if status, body := postChat(httpA.URL, concurrencySecret, modelName); status != http.StatusOK {
		t.Fatalf("expired concurrency lease was not reclaimed: status=%d body=%s", status, body)
	}
	var expiredCount int64
	if err := storeB.db.Model(&InFlightLease{}).Where("id = ?", expiredLeaseID).Count(&expiredCount).Error; err != nil || expiredCount != 0 {
		t.Fatalf("expired lease remained after acquisition: count=%d err=%v", expiredCount, err)
	}

	func() {
		previousTTL := storeA.inFlightLeaseTTL
		storeA.inFlightLeaseTTL = 600 * time.Millisecond
		defer func() { storeA.inFlightLeaseTTL = previousTTL }()
		lostLeaseAdapter := &multiInstanceBlockingAdapter{started: make(chan struct{}), release: make(chan struct{})}
		serverA.adapters[ProviderMock] = lostLeaseAdapter
		defer func() { serverA.adapters[ProviderMock] = MockAdapter{} }()
		result := make(chan struct {
			status int
			body   string
		}, 1)
		go func() {
			status, body := postChat(httpA.URL, concurrencySecret, modelName)
			result <- struct {
				status int
				body   string
			}{status: status, body: body}
		}()
		select {
		case <-lostLeaseAdapter.started:
		case <-time.After(5 * time.Second):
			t.Fatal("lease-loss request did not reach the blocking upstream")
		}
		if err := storeB.db.Where("scope_type = ? AND scope_id = ?", "api_key", concurrencyKey.ID).Delete(&InFlightLease{}).Error; err != nil {
			t.Fatal(err)
		}
		select {
		case response := <-result:
			if response.status != http.StatusServiceUnavailable || !strings.Contains(response.body, "coordination_lease_lost") {
				t.Fatalf("lost request lease did not cancel upstream work: status=%d body=%s", response.status, response.body)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("request continued after its concurrency lease was lost")
		}
		var updatedResource ProviderResource
		if err := storeB.db.First(&updatedResource, "id = ?", resource.ID).Error; err != nil {
			t.Fatal(err)
		}
		if updatedResource.FailureCount != 0 || !updatedResource.Healthy || updatedResource.CooldownUntil != nil {
			t.Fatalf("coordination lease loss was counted as a provider failure: %+v", updatedResource)
		}
	}()

	func() {
		previousTTL := storeA.inFlightLeaseTTL
		storeA.inFlightLeaseTTL = 600 * time.Millisecond
		defer func() { storeA.inFlightLeaseTTL = previousTTL }()
		lostLeaseAdapter := &multiInstanceBlockingAdapter{started: make(chan struct{}), release: make(chan struct{})}
		serverA.adapters[ProviderMock] = lostLeaseAdapter
		defer func() { serverA.adapters[ProviderMock] = MockAdapter{} }()
		result := make(chan struct{}, 1)
		go func() {
			_, _ = postChatStream(httpA.URL, concurrencySecret, modelName)
			result <- struct{}{}
		}()
		select {
		case <-lostLeaseAdapter.started:
		case <-time.After(5 * time.Second):
			t.Fatal("streaming lease-loss request did not reach the blocking upstream")
		}
		if err := storeB.db.Where("scope_type = ? AND scope_id = ?", "api_key", concurrencyKey.ID).Delete(&InFlightLease{}).Error; err != nil {
			t.Fatal(err)
		}
		select {
		case <-result:
		case <-time.After(3 * time.Second):
			t.Fatal("streaming request continued after its concurrency lease was lost")
		}
		var updatedResource ProviderResource
		if err := storeB.db.First(&updatedResource, "id = ?", resource.ID).Error; err != nil {
			t.Fatal(err)
		}
		if updatedResource.FailureCount != 0 || !updatedResource.Healthy || updatedResource.CooldownUntil != nil {
			t.Fatalf("streaming coordination lease loss was counted as a provider failure: %+v", updatedResource)
		}
	}()

	quotaKey, quotaSecret, err := storeA.CreateAPIKey(project.ID, APIKey{
		ID:     "key_quota_" + suffix,
		Name:   "Atomic daily quota",
		Status: StatusActive,
		Limits: QuotaLimits{DailyRequests: 1, MonthlyRequests: 100},
	}, "thk_quota_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.DeleteAPIKey(quotaKey.ID)

	const requests = 16
	statuses := make(chan int, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			baseURL := httpA.URL
			if index%2 == 1 {
				baseURL = httpB.URL
			}
			status, _ := postChat(baseURL, quotaSecret, modelName)
			statuses <- status
		}(i)
	}
	wg.Wait()
	close(statuses)
	okCount := 0
	limitedCount := 0
	for status := range statuses {
		switch status {
		case http.StatusOK:
			okCount++
		case http.StatusTooManyRequests:
			limitedCount++
		default:
			t.Fatalf("unexpected quota response status %d", status)
		}
	}
	if okCount != 1 || limitedCount != requests-1 {
		t.Fatalf("daily quota was not atomic: ok=%d limited=%d", okCount, limitedCount)
	}

	if _, err := storeA.UpdateProviderResource(resource.ID, ProviderResource{
		ResourceType:   "mock",
		Status:         StatusActive,
		Healthy:        true,
		MaxConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}
	providerKey, providerSecret, err := storeA.CreateAPIKey(project.ID, APIKey{
		ID:     "key_provider_" + suffix,
		Name:   "Provider concurrency",
		Status: StatusActive,
		Limits: QuotaLimits{DailyRequests: 100, MonthlyRequests: 100},
	}, "thk_provider_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.DeleteAPIKey(providerKey.ID)
	providerRelease := make(chan struct{})
	providerBlocking := &multiInstanceBlockingAdapter{started: make(chan struct{}), release: providerRelease}
	serverA.adapters[ProviderMock] = providerBlocking
	providerFirstStatus := make(chan int, 1)
	go func() {
		status, _ := postChat(httpA.URL, providerSecret, modelName)
		providerFirstStatus <- status
	}()
	select {
	case <-providerBlocking.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider concurrency request did not reach the blocking upstream")
	}
	status, body = postChat(httpB.URL, providerSecret, modelName)
	if status != http.StatusTooManyRequests || !strings.Contains(body, "provider_resource_concurrency_exceeded") {
		t.Fatalf("second replica bypassed provider concurrency limit: status=%d body=%s", status, body)
	}
	close(providerRelease)
	if status := <-providerFirstStatus; status != http.StatusOK {
		t.Fatalf("provider concurrency first request failed: status=%d", status)
	}
	if _, err := storeA.UpdateProviderResource(resource.ID, ProviderResource{
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
		RateLimitRPM: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rpmKey, rpmSecret, err := storeA.CreateAPIKey(project.ID, APIKey{
		ID:     "key_rpm_" + suffix,
		Name:   "Provider RPM",
		Status: StatusActive,
		Limits: QuotaLimits{DailyRequests: 100, MonthlyRequests: 100},
	}, "thk_rpm_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.DeleteAPIKey(rpmKey.ID)
	serverA.adapters[ProviderMock] = MockAdapter{}
	if status, body := postChat(httpA.URL, rpmSecret, modelName); status != http.StatusOK {
		t.Fatalf("first provider RPM request failed: status=%d body=%s", status, body)
	}
	if status, body := postChat(httpB.URL, rpmSecret, modelName); status != http.StatusTooManyRequests || !strings.Contains(body, "provider_resource_rpm_exceeded") {
		t.Fatalf("provider RPM limit was not shared: status=%d body=%s", status, body)
	}
}

func testSharedOAuthAndRefresh(t *testing.T, storeA *GormStore, storeB *GormStore, config Config) {
	t.Helper()
	var tokenRequests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "shared-access-token",
			"refresh_token": "rotated-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	httpA := httptest.NewServer(NewWithConfig(storeA, config).Handler())
	defer httpA.Close()
	httpB := httptest.NewServer(NewWithConfig(storeB, config).Handler())
	defer httpB.Close()

	generatePayload := map[string]any{
		"redirect_uri": httpB.URL + "/api/admin/provider-account-oauth/openai/oauth/callback",
		"return_url":   httpA.URL + "/providers",
	}
	status, body := postJSON(httpA.URL+"/api/admin/provider-account-oauth/openai/generate-auth-url", config.AdminToken, generatePayload)
	if status != http.StatusOK {
		t.Fatalf("generate OAuth URL failed: status=%d body=%s", status, body)
	}
	var generated providerAccountOAuthGenerateResponse
	if err := json.Unmarshal([]byte(body), &generated); err != nil {
		t.Fatal(err)
	}
	callbackClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	callbackResp, err := callbackClient.Get(httpB.URL + "/api/admin/provider-account-oauth/openai/oauth/callback?state=" + generated.State + "&code=e2e-code")
	if err != nil {
		t.Fatal(err)
	}
	_ = callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound || !strings.Contains(callbackResp.Header.Get("Location"), generated.SessionID) {
		t.Fatalf("cross-replica OAuth callback failed: status=%d", callbackResp.StatusCode)
	}
	status, body = postJSON(httpB.URL+"/api/admin/provider-account-oauth/openai/exchange-code", config.AdminToken, map[string]any{
		"session_id": generated.SessionID,
		"state":      generated.State,
		"code":       "e2e-code",
	})
	if status != http.StatusOK || !strings.Contains(body, "shared-access-token") {
		t.Fatalf("cross-replica OAuth exchange failed: status=%d body=%s", status, body)
	}
	status, _ = postJSON(httpA.URL+"/api/admin/provider-account-oauth/openai/exchange-code", config.AdminToken, map[string]any{
		"session_id": generated.SessionID,
		"state":      generated.State,
		"code":       "e2e-code",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("consumed OAuth session was reusable from another replica: status=%d", status)
	}

	tokenRequests.Store(0)
	suffix := NewID("oauth")
	provider := storeA.AddProvider(Provider{ID: "prv_" + suffix, Name: "OAuth Provider", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	resource, err := storeA.AddProviderResource(ProviderResource{
		ID:           "rsrc_" + suffix,
		ProviderID:   provider.ID,
		Name:         "Shared OAuth Resource",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AuthType:     "oauth",
			AccessToken:  "expired-access-token",
			RefreshToken: "refresh-token",
			ClientID:     openAIAccountOAuthClientID,
			ExpiresAt:    time.Now().UTC().Add(30 * time.Second).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.DeleteProvider(provider.ID)

	results := make(chan ProviderResourceCredentials, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*GormStore{storeA, storeB} {
		wg.Add(1)
		go func(store *GormStore) {
			defer wg.Done()
			creds, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
			results <- creds
			errors <- err
		}(store)
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("coordinated credential refresh failed: %v", err)
		}
	}
	for creds := range results {
		if creds.AccessToken != "shared-access-token" || creds.RefreshToken != "rotated-refresh-token" {
			t.Fatalf("replicas observed different refreshed credentials: %+v", creds)
		}
	}
	if got := tokenRequests.Load(); got != 1 {
		t.Fatalf("expected one upstream refresh request, got %d", got)
	}
}

func testClusterTaskRunsOnce(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	name := "e2e-task-" + NewID("task")
	var executions atomic.Int64
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, store := range []*GormStore{storeA, storeB} {
		wg.Add(1)
		go func(store *GormStore) {
			defer wg.Done()
			errors <- store.RunClusterTask(context.Background(), name, 1, func(context.Context) error {
				executions.Add(1)
				time.Sleep(150 * time.Millisecond)
				return nil
			})
		}(store)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("cluster task ran %d times", got)
	}
	var reran atomic.Bool
	if err := storeB.RunClusterTask(context.Background(), name, 1, func(context.Context) error {
		reran.Store(true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if reran.Load() {
		t.Fatal("completed cluster task revision ran again")
	}
}

func testClusterOperationRunsEveryStart(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	name := "e2e-operation-" + NewID("operation")
	var executions atomic.Int64
	var active atomic.Int64
	var maxActive atomic.Int64
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, store := range []*GormStore{storeA, storeB} {
		wg.Add(1)
		go func(store *GormStore) {
			defer wg.Done()
			errors <- store.RunClusterOperation(context.Background(), name, func(context.Context) error {
				executions.Add(1)
				current := active.Add(1)
				for observed := maxActive.Load(); current > observed && !maxActive.CompareAndSwap(observed, current); observed = maxActive.Load() {
				}
				time.Sleep(150 * time.Millisecond)
				active.Add(-1)
				return nil
			})
		}(store)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := executions.Load(); got != 2 {
		t.Fatalf("startup operation ran %d times, want once per replica start", got)
	}
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("startup operations overlapped across replicas: max_active=%d", got)
	}
	if err := storeA.RunClusterOperation(context.Background(), name, func(context.Context) error {
		executions.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := executions.Load(); got != 3 {
		t.Fatalf("later restart did not rerun startup operation: executions=%d", got)
	}
}

func testClusterLeaseLossCancelsWork(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	previousTTL := storeA.clusterLockTTL
	storeA.clusterLockTTL = 600 * time.Millisecond
	defer func() { storeA.clusterLockTTL = previousTTL }()

	name := "e2e-lost-task-" + NewID("task")
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- storeA.RunClusterTask(context.Background(), name, 1, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return context.Cause(ctx)
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("cluster task did not acquire its lease")
	}
	if err := storeB.db.Delete(&ClusterLease{}, "name = ?", "task:"+name).Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrCoordinationLeaseLost) {
			t.Fatalf("expected lost cluster lease error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("guarded task continued after its cluster lease was lost")
	}
	var stateCount int64
	if err := storeB.db.Model(&ClusterTaskState{}).Where("name = ?", name).Count(&stateCount).Error; err != nil {
		t.Fatal(err)
	}
	if stateCount != 0 {
		t.Fatalf("lost cluster task was recorded as complete: count=%d", stateCount)
	}
}

func postChat(baseURL string, secret string, model string) (int, string) {
	return postJSON(baseURL+"/v1/chat/completions", secret, map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": "multi-instance e2e"}},
	})
}

func postChatStream(baseURL string, secret string, model string) (int, string) {
	return postJSON(baseURL+"/v1/chat/completions", secret, map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": "multi-instance streaming e2e"}},
		"stream":   true,
	})
}

func postJSON(url string, bearer string, payload any) (int, string) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("content-type", "application/json")
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}
