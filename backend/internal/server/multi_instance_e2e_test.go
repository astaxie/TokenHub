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

	"gorm.io/gorm"
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
	t.Run("Responses lease renewal and recovery have one winner", func(t *testing.T) {
		testResponseRecoveryRenewalRace(t, storeA, storeB)
	})
	t.Run("durable Responses jobs execute once across replicas", func(t *testing.T) {
		testSharedResponseJobExecution(t, storeA, storeB, config)
	})
	t.Run("concurrent migrations work with a one-connection runtime pool", func(t *testing.T) {
		testConcurrentMigrations(t, storeA, config)
	})
	t.Run("HTTP quotas and concurrency are cluster wide", func(t *testing.T) {
		testClusterWideHTTPEnforcement(t, storeA, storeB, config)
	})
	t.Run("gateway hot paths do not serialize unrelated API keys", func(t *testing.T) {
		testPostgresGatewayHotPathConcurrency(t, storeA)
	})
	t.Run("gateway read snapshots use repeatable read", func(t *testing.T) {
		testPostgresGatewayReadSnapshot(t, storeA)
	})
	t.Run("API key deletion is ordered with admission and settlement", func(t *testing.T) {
		testPostgresAPIKeyDeletionOrdering(t, storeA)
	})
	t.Run("project disable is ordered with admission", func(t *testing.T) {
		testPostgresProjectDisableOrdering(t, storeA)
	})
	t.Run("API key admin updates preserve concurrent last used", func(t *testing.T) {
		testPostgresAPIKeyUpdatePreservesLastUsed(t, storeA)
	})
	t.Run("adaptive stats failure falls back inside the read snapshot", func(t *testing.T) {
		testPostgresAdaptiveStatsFailureFallback(t, storeA)
	})
	t.Run("analytics checkpoints do not serialize replica writes", func(t *testing.T) {
		testAnalyticsCommitSequence(t, storeA, storeB)
	})
	t.Run("route candidates use one read snapshot", func(t *testing.T) {
		testRouteCandidateReadSnapshot(t, storeA, storeB)
	})
	t.Run("adaptive route stats failures remain best effort", func(t *testing.T) {
		testRouteCandidateStatsFailure(t, storeA)
	})
	t.Run("route candidate lookup errors preserve failover", func(t *testing.T) {
		for _, target := range []string{"providers", "provider_resources"} {
			t.Run(target, func(t *testing.T) {
				testRouteCandidateLookupFailover(t, storeA, target)
			})
		}
	})
	t.Run("analytics migration preserves legacy time windows", func(t *testing.T) {
		testPostgresAnalyticsLegacySequenceMigration(t, storeA, config)
	})
	t.Run("v0.4 bootstrap upgrade preserves customized admin teams", func(t *testing.T) {
		testPostgresV040BootstrapUpgrade(t, storeA, config)
	})
	t.Run("OAuth state and refresh coordination survive replica changes", func(t *testing.T) {
		testSharedOAuthAndRefresh(t, storeA, storeB, config)
	})
	t.Run("admin OAuth records use the shared database clock", func(t *testing.T) {
		assertAdminOAuthFlowUsesDatabaseClockAcrossInstances(t, storeA, storeB)
		assertAdminOAuthExchangeUsesDatabaseClockAcrossInstances(t, storeA, storeB)
	})
	t.Run("startup task revision runs once", func(t *testing.T) {
		testClusterTaskRunsOnce(t, storeA, storeB)
	})
	t.Run("request payload retention deletes in PostgreSQL", func(t *testing.T) {
		testRequestPayloadRetentionPostgres(t, storeA)
	})
	t.Run("request payload retention index recovers in PostgreSQL", func(t *testing.T) {
		testRequestPayloadRetentionIndexRecoveryPostgres(t, storeA)
	})
	t.Run("startup operations run on every start and serialize replicas", func(t *testing.T) {
		testClusterOperationRunsEveryStart(t, storeA, storeB)
	})
	t.Run("lost cluster leases cancel guarded work", func(t *testing.T) {
		testClusterLeaseLossCancelsWork(t, storeA, storeB)
	})
}

func testRouteCandidateReadSnapshot(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	suffix := NewID("snapshot")
	modelName := "route-snapshot-" + suffix
	providerID := "prv_" + suffix
	routeID := "route_" + suffix
	storeA.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	storeA.AddProvider(Provider{
		ID: providerID, Name: "Route snapshot provider", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	storeA.AddRoute(ModelRoute{
		ID: routeID, ModelName: modelName, ProviderID: providerID,
		ProviderModel: modelName, Priority: 1, Weight: 100, Status: StatusActive,
	})
	t.Cleanup(func() {
		_ = storeA.db.Where("id = ?", routeID).Delete(&ModelRoute{}).Error
		_ = storeA.db.Where("id = ?", providerID).Delete(&Provider{}).Error
		_ = storeA.db.Where("name = ?", modelName).Delete(&Model{}).Error
	})

	routeRead := make(chan struct{}, 1)
	resume := make(chan struct{})
	var pauseOnce sync.Once
	callbackName := "test:route-candidate-snapshot:" + suffix
	if err := storeA.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "model_routes" {
			return
		}
		pauseOnce.Do(func() {
			routeRead <- struct{}{}
			select {
			case <-resume:
			case <-time.After(5 * time.Second):
				_ = tx.AddError(fmt.Errorf("timed out waiting to resume route snapshot query"))
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := storeA.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove route snapshot callback: %v", err)
		}
	}()

	type selectionResult struct {
		candidates []RouteSelection
		err        error
	}
	result := make(chan selectionResult, 1)
	go func() {
		candidates, err := storeA.SelectRouteCandidates(modelName)
		result <- selectionResult{candidates: candidates, err: err}
	}()
	select {
	case <-routeRead:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the route query")
	}
	if err := storeB.db.Model(&Provider{}).Where("id = ?", providerID).Update("healthy", false).Error; err != nil {
		t.Fatal(err)
	}
	close(resume)

	select {
	case selected := <-result:
		if selected.err != nil {
			t.Fatal(selected.err)
		}
		if len(selected.candidates) != 1 || selected.candidates[0].Provider.ID != providerID || !selected.candidates[0].Provider.Healthy {
			t.Fatalf("snapshot candidates = %+v", selected.candidates)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for route candidates")
	}
}

func testRouteCandidateStatsFailure(t *testing.T, store *GormStore) {
	t.Helper()
	suffix := NewID("stats-failure")
	modelName := "route-stats-failure-" + suffix
	providerID := "prv_" + suffix
	routeID := "route_" + suffix
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	store.AddProvider(Provider{
		ID: providerID, Name: "Route stats failure provider", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID: routeID, ModelName: modelName, ProviderID: providerID,
		ProviderModel: modelName, Priority: 1, Weight: 100,
		Status: StatusActive, Strategy: RouteStrategyAdaptive,
	})
	t.Cleanup(func() {
		_ = store.db.Where("id = ?", routeID).Delete(&ModelRoute{}).Error
		_ = store.db.Where("id = ?", providerID).Delete(&Provider{}).Error
		_ = store.db.Where("name = ?", modelName).Delete(&Model{}).Error
	})

	callbackName := "test:route-candidate-stats-error:" + suffix
	resultCallbackName := callbackName + ":result"
	var statsQueryErr error
	if err := store.db.Callback().Row().Before("gorm:row").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "route_attempt_logs" {
			tx.Statement.Table = "missing_route_attempt_logs_" + suffix
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Callback().Row().After("gorm:row").Register(resultCallbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "route_attempt_logs" {
			statsQueryErr = tx.Error
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.db.Callback().Row().Remove(callbackName); err != nil {
			t.Errorf("remove route stats error callback: %v", err)
		}
		if err := store.db.Callback().Row().Remove(resultCallbackName); err != nil {
			t.Errorf("remove route stats result callback: %v", err)
		}
	}()

	candidates, err := store.SelectRouteCandidates(modelName)
	if err != nil {
		t.Fatalf("optional runtime stats failure rejected candidates: %v", err)
	}
	if statsQueryErr == nil {
		t.Fatal("adaptive runtime stats query did not reach the intended database-side failure")
	}
	if len(candidates) != 1 || candidates[0].Provider.ID != providerID || candidates[0].Runtime != (RouteRuntimeStats{}) {
		t.Fatalf("candidates after optional runtime stats failure = %+v", candidates)
	}
}

func testRouteCandidateLookupFailover(t *testing.T, store *GormStore, target string) {
	t.Helper()
	suffix := NewID("lookup-failover")
	modelName := "route-lookup-failover-" + suffix
	providers := make([]Provider, 0, 2)
	resources := make([]ProviderResource, 0, 2)
	routes := make([]ModelRoute, 0, 2)
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	for index, label := range []string{"bad", "good"} {
		provider := store.AddProvider(Provider{
			ID: "prv_" + label + "_" + suffix, Name: "Route lookup " + label,
			Type: ProviderMock, Status: StatusActive, Healthy: true,
		})
		resource, err := store.AddProviderResource(ProviderResource{
			ID: "rsrc_" + label + "_" + suffix, ProviderID: provider.ID,
			Name: "Route lookup " + label, Status: StatusActive, Healthy: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		providers = append(providers, provider)
		resources = append(resources, resource)
		routes = append(routes, store.AddRoute(ModelRoute{
			ID: "route_" + label + "_" + suffix, ModelName: modelName,
			ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: modelName,
			Priority: index + 1, Weight: 100, Status: StatusActive,
		}))
	}
	t.Cleanup(func() {
		for _, route := range routes {
			_ = store.db.Where("id = ?", route.ID).Delete(&ModelRoute{}).Error
		}
		for _, resource := range resources {
			_ = store.db.Where("id = ?", resource.ID).Delete(&ProviderResource{}).Error
		}
		for _, provider := range providers {
			_ = store.db.Where("id = ?", provider.ID).Delete(&Provider{}).Error
		}
		_ = store.db.Where("name = ?", modelName).Delete(&Model{}).Error
	})

	attempts := 0
	databaseErrors := 0
	callbackName := "test:route-candidate-lookup-failover:" + suffix
	resultCallbackName := callbackName + ":result"
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == target {
			attempts++
			if attempts <= 2 {
				tx.Statement.Table = "missing_" + target + "_" + suffix
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Callback().Query().After("gorm:query").Register(resultCallbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == target && tx.Error != nil {
			databaseErrors++
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove route lookup failover callback: %v", err)
		}
		if err := store.db.Callback().Query().Remove(resultCallbackName); err != nil {
			t.Errorf("remove route lookup failover result callback: %v", err)
		}
	}()

	candidates, err := store.SelectRouteCandidates(modelName)
	if err != nil {
		t.Fatalf("lookup failure rejected unaffected candidates: %v", err)
	}
	if attempts != 3 || databaseErrors != 2 {
		t.Fatalf("lookup attempts/errors = %d/%d, want 3/2", attempts, databaseErrors)
	}
	if len(candidates) != 1 || candidates[0].Route.ID != routes[1].ID {
		t.Fatalf("lookup failover candidates = %+v, want route %s", candidates, routes[1].ID)
	}
}

type multiInstanceResponseAdapter struct {
	MockAdapter
	calls atomic.Int64
}

func (a *multiInstanceResponseAdapter) Responses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest) (any, Usage, error) {
	a.calls.Add(1)
	return a.MockAdapter.Responses(ctx, provider, providerModel, request)
}

type multiInstanceBlockingResponseAdapter struct {
	MockAdapter
	started chan struct{}
	once    sync.Once
}

func (a *multiInstanceBlockingResponseAdapter) Responses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest) (any, Usage, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	return nil, Usage{}, ctx.Err()
}

func testSharedResponseJobExecution(t *testing.T, storeA *GormStore, storeB *GormStore, config Config) {
	t.Helper()
	config.ResponseWorkerConcurrency = 1
	config.ResponsePollIntervalMillis = 20
	config.ResponseJobTimeoutSeconds = 5
	config.ResponseLeaseTTLSeconds = 2
	config.ResponseResultTTLSeconds = 60
	config.ResponseMaxQueuedJobs = 100
	suffix := NewID("response-e2e")
	project := storeA.CreateProject(Project{ID: "prj_" + suffix, Name: "Response jobs E2E", Status: StatusActive})
	modelName := "model-" + suffix
	storeA.AddModel(Model{ID: modelName, Name: modelName, Modality: "chat", Status: StatusActive})
	provider := storeA.AddProvider(Provider{ID: "prv_" + suffix, Name: "Response jobs mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := storeA.AddProviderResource(ProviderResource{ID: "rsrc_" + suffix, ProviderID: provider.ID, Name: "Response jobs resource", ResourceType: "mock", Status: StatusActive, Healthy: true})
	if err != nil {
		t.Fatal(err)
	}
	storeA.AddRoute(ModelRoute{ID: "route_" + suffix, ModelName: modelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: modelName, Status: StatusActive, Priority: 1, Weight: 100})
	key, _, err := storeA.CreateAPIKey(project.ID, APIKey{ID: "key_" + suffix, Name: "Response jobs key", Allowed: []string{modelName}, Status: StatusActive}, "thk_"+suffix)
	if err != nil {
		t.Fatal(err)
	}

	serverA := NewWithConfig(storeA, config)
	serverB := NewWithConfig(storeB, config)
	adapter := &multiInstanceResponseAdapter{}
	serverA.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityResponses)
	serverB.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityResponses)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = serverA.Shutdown(ctx)
		_ = serverB.Shutdown(ctx)
		_ = storeA.db.Where("job_id LIKE ?", "%"+suffix+"%").Delete(&ResponseJobEvent{}).Error
		_ = storeA.db.Where("resource_type = ? AND resource_id LIKE ?", "response_job", "%"+suffix+"%").Delete(&AuditEvent{}).Error
		_ = storeA.db.Where("id LIKE ?", "%"+suffix+"%").Delete(&ResponseJob{}).Error
		_ = storeA.DeleteAPIKey(key.ID)
		_ = storeA.DeleteProvider(provider.ID)
		_ = storeA.DeleteModel(modelName)
		_ = storeA.DeleteProject(project.ID)
	})
	// Both replicas started while the queue was empty. Stop the would-be
	// submitting replica before the durable insert so only the already-idle peer
	// can discover and execute the new work.
	time.Sleep(2 * time.Duration(config.ResponsePollIntervalMillis) * time.Millisecond)
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := serverA.Shutdown(shutdownContext); err != nil {
		shutdownCancel()
		t.Fatal(err)
	}
	shutdownCancel()

	createJob := func(input string) ResponseJob {
		t.Helper()
		requestJSON, _ := json.Marshal(ResponsesRequest{Model: modelName, Input: input, Background: true})
		envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: requestJSON})
		job, err := storeA.CreateResponseJob(ResponseJob{
			ID: NewID("resp") + "_" + suffix, ProjectID: project.ID, APIKeyID: key.ID,
			AttributedUserID: usageAttributionUserID(key, project), Model: modelName,
		}, envelopeJSON)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	waitTerminal := func(id string) ResponseJob {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			current, ok, err := storeA.GetResponseJob(id)
			if err != nil {
				t.Fatal(err)
			}
			if ok && responseJobTerminal(current.Status) {
				return current
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("shared response job %s did not complete", id)
		return ResponseJob{}
	}

	secretInput := "postgres ciphertext secret " + suffix
	job := createJob(secretInput)
	current := waitTerminal(job.ID)
	if current.Status != responseJobStatusSucceeded {
		t.Fatalf("shared job failed: %+v", current)
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("shared job reached upstream %d times", calls)
	}
	var persisted ResponseJob
	if err := storeA.db.First(&persisted, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(persisted.RequestCiphertext, "enc:v1:") || strings.Contains(persisted.RequestCiphertext, secretInput) ||
		!strings.HasPrefix(persisted.ResultCiphertext, "enc:v1:") || strings.Contains(persisted.ResultCiphertext, secretInput) {
		t.Fatalf("PostgreSQL did not retain encrypted response payloads: %+v", persisted)
	}
	var requestLogs int64
	if err := storeA.db.Model(&RequestLog{}).Where("request_id = ?", current.RequestID).Count(&requestLogs).Error; err != nil || requestLogs != 1 {
		t.Fatalf("shared response request accounting: logs=%d err=%v", requestLogs, err)
	}
	var usageRecords int64
	if err := storeA.db.Model(&UsageRecord{}).Where("request_id = ?", current.RequestID).Count(&usageRecords).Error; err != nil || usageRecords != 1 {
		t.Fatalf("shared response usage accounting: records=%d err=%v", usageRecords, err)
	}
	var payloadLogs int64
	if err := storeA.db.Model(&RequestPayloadLog{}).Where("request_id = ?", current.RequestID).Count(&payloadLogs).Error; err != nil || payloadLogs != 0 {
		t.Fatalf("background payload escaped encrypted retention: logs=%d err=%v", payloadLogs, err)
	}
	past := time.Now().UTC().Add(-time.Second)
	if err := storeA.db.Model(&ResponseJob{}).Where("id = ?", job.ID).Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if expired, err := storeB.ExpireResponseJobs(); err != nil || expired > 1 {
		t.Fatalf("expire shared response job: expired=%d err=%v", expired, err)
	}
	if err := storeA.db.First(&persisted, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != responseJobStatusExpired || persisted.RequestCiphertext != "" || persisted.ResultCiphertext != "" {
		t.Fatalf("PostgreSQL retention did not scrub response ciphertext: %+v", persisted)
	}

	blocking := &multiInstanceBlockingResponseAdapter{started: make(chan struct{})}
	serverB.adapterRegistry.Register(ProviderMock, blocking, AdapterCapabilityResponses)
	cancelJob := createJob("cancel on peer " + suffix)
	select {
	case <-blocking.started:
	case <-time.After(5 * time.Second):
		t.Fatal("shared cancellation job did not reach the provider")
	}
	if _, retained, err := storeA.CancelResponseJob(cancelJob.ID, "postgres-e2e", time.Minute); err != nil || !retained {
		t.Fatalf("cancel shared response job: retained=%v err=%v", retained, err)
	}
	cancelled := waitTerminal(cancelJob.ID)
	if cancelled.Status != responseJobStatusCancelled || cancelled.ErrorCode != "response_cancelled" {
		t.Fatalf("shared cancellation did not win: %+v", cancelled)
	}
	if err := storeA.db.Model(&RequestLog{}).Where("request_id = ?", cancelled.RequestID).Count(&requestLogs).Error; err != nil || requestLogs != 1 {
		t.Fatalf("cancelled response request accounting: logs=%d err=%v", requestLogs, err)
	}
	if err := storeA.db.Model(&UsageRecord{}).Where("request_id = ?", cancelled.RequestID).Count(&usageRecords).Error; err != nil || usageRecords != 0 {
		t.Fatalf("cancelled response created usage: records=%d err=%v", usageRecords, err)
	}
}

func testResponseRecoveryRenewalRace(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	suffix := NewID("response-recovery-race")
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"race","input":"race","background":true}`)})
	job, err := storeA.CreateResponseJob(ResponseJob{ID: "resp_" + suffix, ProjectID: "project_" + suffix, APIKeyID: "key_" + suffix, Model: "race"}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = storeA.db.Where("job_id = ?", job.ID).Delete(&ResponseJobEvent{}).Error
		_ = storeA.db.Where("resource_type = ? AND resource_id = ?", "response_job", job.ID).Delete(&AuditEvent{}).Error
		_ = storeA.db.Delete(&ResponseJob{}, "id = ?", job.ID).Error
	})
	claimed, ok, err := storeA.ClaimResponseJob("renewing-worker", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim recovery race job: ok=%v err=%v", ok, err)
	}
	past := time.Now().UTC().Add(-time.Second)
	if err := storeA.db.Model(&ResponseJob{}).Where("id = ?", job.ID).Update("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}

	scanned := make(chan struct{})
	releaseRecovery := make(chan struct{})
	var scanOnce sync.Once
	callbackName := "test:response_recovery_scan_" + suffix
	if err := storeB.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "response_jobs" && strings.Contains(tx.Statement.SQL.String(), "lease_expires_at") {
			scanOnce.Do(func() {
				close(scanned)
				<-releaseRecovery
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer storeB.db.Callback().Query().Remove(callbackName)

	type recoveryResult struct {
		requeued  int64
		failed    int64
		cancelled int64
		err       error
	}
	recoveryDone := make(chan recoveryResult, 1)
	go func() {
		requeued, failed, cancelled, err := storeB.RecoverResponseJobs(time.Minute)
		recoveryDone <- recoveryResult{requeued: requeued, failed: failed, cancelled: cancelled, err: err}
	}()
	select {
	case <-scanned:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not scan the expired lease")
	}
	type renewalResult struct {
		retained bool
		err      error
	}
	renewalDone := make(chan renewalResult, 1)
	go func() {
		_, retained, err := storeA.RenewResponseJobLease(claimed.ID, "renewing-worker", claimed.LeaseEpoch, time.Second)
		renewalDone <- renewalResult{retained: retained, err: err}
	}()
	// If recovery did not lock the scanned row, renewal can complete here. The
	// recovery update must still re-check expiry so both paths cannot win.
	var renewal renewalResult
	renewalReady := false
	select {
	case renewal = <-renewalDone:
		renewalReady = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseRecovery)
	if !renewalReady {
		renewal = <-renewalDone
	}
	recovery := <-recoveryDone
	if renewal.err != nil || recovery.err != nil {
		t.Fatalf("lease race returned errors: renewal=%v recovery=%v", renewal.err, recovery.err)
	}
	recovered := recovery.requeued+recovery.failed+recovery.cancelled == 1
	if renewal.retained == recovered {
		t.Fatalf("renewal and recovery must have exactly one winner: renewed=%v recovery=%+v", renewal.retained, recovery)
	}
}

func testAnalyticsCommitSequence(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	suffix := NewID("sequence")
	firstID := "log_first_" + suffix
	secondID := "log_second_" + suffix
	t.Cleanup(func() {
		_ = storeA.db.Delete(&RequestLog{}, "id IN ?", []string{firstID, secondID}).Error
	})
	firstTransaction := storeA.db.Begin()
	if firstTransaction.Error != nil {
		t.Fatal(firstTransaction.Error)
	}
	if err := firstTransaction.Create(&RequestLog{
		ID: firstID, RequestID: "req_first_" + suffix, ProjectID: "project_sequence",
		ModelName: "gpt-sequence", StatusCode: http.StatusOK, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		_ = firstTransaction.Rollback().Error
		t.Fatal(err)
	}
	var firstLog RequestLog
	if err := firstTransaction.First(&firstLog, "id = ?", firstID).Error; err != nil {
		_ = firstTransaction.Rollback().Error
		t.Fatal(err)
	}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- storeB.db.Create(&RequestLog{
			ID: secondID, RequestID: "req_second_" + suffix, ProjectID: "project_sequence",
			ModelName: "gpt-sequence", StatusCode: http.StatusOK, CreatedAt: time.Now().UTC().Add(-48 * time.Hour),
		}).Error
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			_ = firstTransaction.Rollback().Error
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		_ = firstTransaction.Rollback().Error
		t.Fatal("second replica was blocked by the first request-log transaction")
	}
	var secondLog RequestLog
	if err := storeB.db.First(&secondLog, "id = ?", secondID).Error; err != nil {
		_ = firstTransaction.Rollback().Error
		t.Fatal(err)
	}
	now := time.Now().UTC()
	checkpointBefore, err := storeB.TokenCostCheckpoint(t.Context(), TokenCostQuery{
		From: now.Add(-72 * time.Hour), To: now.Add(time.Hour), ProjectID: "project_sequence",
	})
	if err != nil {
		_ = firstTransaction.Rollback().Error
		t.Fatal(err)
	}
	if firstLog.CommitSequence <= 0 || secondLog.CommitSequence <= firstLog.CommitSequence || checkpointBefore >= firstLog.CommitSequence {
		_ = firstTransaction.Rollback().Error
		t.Fatalf("unsafe active-transaction checkpoint: first=%d second=%d checkpoint=%d",
			firstLog.CommitSequence, secondLog.CommitSequence, checkpointBefore)
	}
	if err := firstTransaction.Commit().Error; err != nil {
		t.Fatal(err)
	}
	checkpointAfter, err := storeB.TokenCostCheckpoint(t.Context(), TokenCostQuery{
		From: now.Add(-72 * time.Hour), To: now.Add(time.Hour), ProjectID: "project_sequence",
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkpointAfter < secondLog.CommitSequence {
		t.Fatalf("checkpoint did not advance after the older transaction committed: got %d, want >= %d",
			checkpointAfter, secondLog.CommitSequence)
	}
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
	registerTestAdapter(serverA, ProviderMock, blocking)
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
		registerTestAdapter(serverA, ProviderMock, lostLeaseAdapter)
		defer func() { registerTestAdapter(serverA, ProviderMock, MockAdapter{}) }()
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
		registerTestAdapter(serverA, ProviderMock, lostLeaseAdapter)
		defer func() { registerTestAdapter(serverA, ProviderMock, MockAdapter{}) }()
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

	keyRPM := int64(1)
	minuteKey, _, err := storeA.CreateAPIKey(project.ID, APIKey{
		ID:           "key_minute_" + suffix,
		Name:         "Cluster minute limits",
		Status:       StatusActive,
		RateLimitRPM: &keyRPM,
	}, "thk_minute_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.DeleteAPIKey(minuteKey.ID)
	minuteCall, err := storeA.StartCall(context.Background(), project, minuteKey, modelName, 0)
	if err != nil {
		t.Fatal(err)
	}
	storeA.FinishCall(minuteCall, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "multi-instance-e2e")
	if _, err := storeB.StartCall(context.Background(), project, minuteKey, modelName, 0); AsHTTPError(err).Code != "api_key_rpm_exceeded" {
		t.Fatalf("second replica bypassed API key RPM: %v", err)
	}

	keyTPM := int64(3)
	tokenKey, _, err := storeA.CreateAPIKey(project.ID, APIKey{
		ID:            "key_tokens_" + suffix,
		Name:          "Cluster token reservation",
		Status:        StatusActive,
		TokenLimitTPM: &keyTPM,
	}, "thk_tokens_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.DeleteAPIKey(tokenKey.ID)
	tokenCall, err := storeA.StartCall(context.Background(), project, tokenKey, modelName, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeB.StartCall(context.Background(), project, tokenKey, modelName, 1); AsHTTPError(err).Code != "api_key_tpm_exceeded" {
		t.Fatalf("second replica bypassed API key TPM reservation: %v", err)
	}
	storeA.FinishCall(tokenCall, RouteSelection{}, Usage{}, http.StatusBadGateway, "upstream_failed", "127.0.0.1", "multi-instance-e2e")
	recoveredCall, err := storeB.StartCall(context.Background(), project, tokenKey, modelName, 3)
	if err != nil {
		t.Fatalf("TPM reservation was not returned cluster-wide: %v", err)
	}
	storeB.FinishCall(recoveredCall, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "multi-instance-e2e")

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
	registerTestAdapter(serverA, ProviderMock, providerBlocking)
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
	registerTestAdapter(serverA, ProviderMock, MockAdapter{})
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

func testRequestPayloadRetentionPostgres(t *testing.T, store *GormStore) {
	t.Helper()
	suffix := NewID("payload-retention")
	cutoff := time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)
	expired := RequestPayloadLog{ID: suffix + "-expired", RequestID: suffix + "-expired-request", CreatedAt: cutoff.Add(-time.Second)}
	current := RequestPayloadLog{ID: suffix + "-current", RequestID: suffix + "-current-request", CreatedAt: cutoff}
	if err := store.db.Create(&[]RequestPayloadLog{expired, current}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.db.Where("id IN ?", []string{expired.ID, current.ID}).Delete(&RequestPayloadLog{}).Error
	})
	deleted, err := store.DeleteRequestPayloadLogsBefore(t.Context(), cutoff, 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted < 1 {
		t.Fatalf("PostgreSQL cleanup deleted %d rows, want at least 1", deleted)
	}
	var expiredCount int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", expired.ID).Count(&expiredCount).Error; err != nil || expiredCount != 0 {
		t.Fatalf("expired PostgreSQL payload remained: count=%d err=%v", expiredCount, err)
	}
	var currentCount int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", current.ID).Count(&currentCount).Error; err != nil || currentCount != 1 {
		t.Fatalf("boundary PostgreSQL payload changed: count=%d err=%v", currentCount, err)
	}
}

func testRequestPayloadRetentionIndexRecoveryPostgres(t *testing.T, store *GormStore) {
	t.Helper()
	if err := store.db.Exec("DROP INDEX CONCURRENTLY IF EXISTS " + requestPayloadRetentionIndexName).Error; err != nil {
		t.Fatal(err)
	}
	suffix := NewID("payload-retention-index")
	duplicateTime := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	duplicates := []RequestPayloadLog{
		{ID: suffix + "-a", RequestID: suffix + "-request-a", CreatedAt: duplicateTime},
		{ID: suffix + "-b", RequestID: suffix + "-request-b", CreatedAt: duplicateTime},
	}
	if err := store.db.Create(&duplicates).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.db.Where("id IN ?", []string{duplicates[0].ID, duplicates[1].ID}).Delete(&RequestPayloadLog{}).Error
	})
	if err := store.db.Exec("CREATE UNIQUE INDEX CONCURRENTLY " + requestPayloadRetentionIndexName + " ON request_payload_logs(created_at)").Error; err == nil {
		t.Fatal("expected duplicate payload timestamps to leave a failed concurrent index")
	}
	if err := ensureRequestPayloadRetentionIndex(store.db, "postgres"); err != nil {
		t.Fatal(err)
	}
	type indexState struct {
		Valid   bool
		Columns string
	}
	var state indexState
	if err := store.db.Raw(`SELECT index_state.indisvalid AS valid,
       string_agg(attribute.attname, ',' ORDER BY key_column.ordinality) AS columns
FROM pg_index AS index_state
JOIN LATERAL unnest(index_state.indkey) WITH ORDINALITY AS key_column(attnum, ordinality) ON TRUE
JOIN pg_attribute AS attribute
  ON attribute.attrelid = index_state.indrelid
 AND attribute.attnum = key_column.attnum
WHERE index_state.indexrelid = to_regclass(?)
GROUP BY index_state.indisvalid`, requestPayloadRetentionIndexName).Scan(&state).Error; err != nil {
		t.Fatal(err)
	}
	if !state.Valid || state.Columns != "created_at,id" {
		t.Fatalf("recovered PostgreSQL index = valid:%t columns:%q, want valid:true columns:%q", state.Valid, state.Columns, "created_at,id")
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
