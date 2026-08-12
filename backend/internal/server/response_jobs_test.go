package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func responseJobTestConfig() Config {
	return Config{
		AdminToken:                      "dev_admin_token",
		SecretKey:                       "response-job-test-secret",
		ResponseWorkerConcurrency:       1,
		ResponsePollIntervalMillis:      10,
		ResponseJobTimeoutSeconds:       2,
		ResponseLeaseTTLSeconds:         1,
		ResponseResultTTLSeconds:        60,
		ResponseMaxQueuedJobs:           100,
		InFlightLeaseTTLSeconds:         2,
		ClusterLockTTLSeconds:           2,
		ImageCapabilityRetrySecs:        60,
		UpstreamNonStreamTimeoutSeconds: 2,
	}
}

func newBackgroundResponseTestServer(t *testing.T) (*Server, *GormStore, string) {
	t.Helper()
	return newBackgroundResponseTestServerWithConfig(t, responseJobTestConfig())
}

func newBackgroundResponseTestServerWithConfig(t *testing.T, config Config) (*Server, *GormStore, string) {
	t.Helper()
	store, secret := newBackgroundResponseTestStore(t, config)
	server := NewWithConfig(store, config)
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	})
	return server, store, secret
}

func newBackgroundResponseTestStore(t *testing.T, config Config) (*GormStore, string) {
	t.Helper()
	store := NewMemoryStoreWithConfig(config)
	project := store.CreateProject(Project{Name: "Background Responses", Status: StatusActive})
	tokenLimit := int64(10000)
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:          "background-response-key",
		Allowed:       []string{"gpt-background"},
		TokenLimitTPM: &tokenLimit,
		Limits:        QuotaLimits{MaxConcurrency: 1},
		Status:        StatusActive,
	}, "thk_background_responses")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_background", Name: "Background Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "rsrc_background",
		ProviderID:     provider.ID,
		Name:           "Background Mock Resource",
		ResourceType:   "mock",
		Status:         StatusActive,
		Healthy:        true,
		MaxConcurrency: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{ID: "gpt-background", Name: "gpt-background", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:                 "route_background",
		ModelName:          "gpt-background",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "gpt-background-upstream",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
		Strategy:           "priority_only",
	})
	return store, secret
}

func submitBackgroundResponse(t *testing.T, handler http.Handler, secret string, input string) string {
	t.Helper()
	response := doJSON(t, handler, http.MethodPost, "/v1/responses", map[string]any{
		"model":      "gpt-background",
		"input":      input,
		"background": true,
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("background submission failed: %d %s", response.Code, response.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	id, _ := payload["id"].(string)
	if id == "" {
		t.Fatalf("background submission returned no id: %s", response.Body)
	}
	return id
}

func waitForResponseJobStatus(t *testing.T, handler http.Handler, secret string, id string, target string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response := doJSON(t, handler, http.MethodGet, "/v1/responses/"+id, nil, secret)
		if response.Code != http.StatusOK {
			t.Fatalf("response job read failed: %d %s", response.Code, response.Body)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["status"] == target {
			return payload
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("response job %s did not reach %s", id, target)
	return nil
}

func TestBackgroundResponsesSuccessPersistsEncryptedPayloadAndAccountsOnce(t *testing.T) {
	server, store, secret := newBackgroundResponseTestServer(t)
	id := submitBackgroundResponse(t, server.Handler(), secret, "durable secret prompt")
	completed := waitForResponseJobStatus(t, server.Handler(), secret, id, "completed")
	if completed["id"] != id || completed["output_text"] != "Echo: durable secret prompt" {
		t.Fatalf("unexpected completed response: %#v", completed)
	}
	var persisted ResponseJob
	if err := store.db.First(&persisted, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(persisted.RequestCiphertext, "enc:v1:") || strings.Contains(persisted.RequestCiphertext, "durable secret prompt") {
		t.Fatalf("request payload was not encrypted at rest: %q", persisted.RequestCiphertext)
	}
	if !strings.HasPrefix(persisted.ResultCiphertext, "enc:v1:") || strings.Contains(persisted.ResultCiphertext, "durable secret prompt") {
		t.Fatalf("result payload was not encrypted at rest: %q", persisted.ResultCiphertext)
	}
	requestJSON, _, err := store.LoadResponseJobPayload(id)
	if err != nil {
		t.Fatal(err)
	}
	var envelope responseJobEnvelope
	if err := json.Unmarshal(requestJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Headers.Get("Authorization") != "" {
		t.Fatal("authentication header was persisted with the response job")
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].RequestID == "" {
		t.Fatalf("expected one successful request log, got %+v", logs)
	}
	if records := store.ListUsageRecords(); len(records) != 1 {
		t.Fatalf("expected one usage record, got %+v", records)
	}
	var events []ResponseJobEvent
	if err := store.db.Where("job_id = ?", id).Order("created_at ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) < 4 || events[0].ToStatus != responseJobStatusQueued || events[len(events)-1].ToStatus != responseJobStatusSucceeded {
		t.Fatalf("unexpected job audit trail: %+v", events)
	}
	var payloadLogs int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("request_id = ?", logs[0].RequestID).Count(&payloadLogs).Error; err != nil || payloadLogs != 0 {
		t.Fatalf("background payload was copied into plaintext audit storage: count=%d err=%v", payloadLogs, err)
	}
}

func TestBackgroundResponsesEnforcesExactKeyAndRejectsStreaming(t *testing.T) {
	server, store, secret := newBackgroundResponseTestServer(t)
	id := submitBackgroundResponse(t, server.Handler(), secret, "private")
	job, ok, err := store.GetResponseJob(id)
	if err != nil || !ok {
		t.Fatalf("load submitted job: ok=%v err=%v", ok, err)
	}
	_, otherSecret, err := store.CreateAPIKey(job.ProjectID, APIKey{
		Name:    "different key",
		Allowed: []string{"gpt-background"},
		Status:  StatusActive,
	}, "thk_background_other")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := doJSON(t, server.Handler(), http.MethodGet, "/v1/responses/"+id, nil, otherSecret)
	if unauthorized.Code != http.StatusNotFound {
		t.Fatalf("different API key read job: %d %s", unauthorized.Code, unauthorized.Body)
	}
	stream := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "gpt-background", "input": "x", "background": true, "stream": true,
	}, secret)
	if stream.Code != http.StatusBadRequest || !strings.Contains(stream.Body, "background_stream_not_supported") {
		t.Fatalf("background streaming was not rejected: %d %s", stream.Code, stream.Body)
	}
}

func TestBackgroundResponsesRevalidatesAuthorizationBeforeExecution(t *testing.T) {
	server, store, _ := newBackgroundResponseTestServer(t)
	key := store.ListAPIKeys()[0]
	project, ok := store.GetProject(key.ProjectID)
	if !ok {
		t.Fatal("test project not found")
	}
	requestJSON, _ := json.Marshal(ResponsesRequest{Model: "gpt-background", Input: "must not run", Background: true})
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: requestJSON})
	job, err := store.CreateResponseJob(ResponseJob{
		ID: NewID("resp"), ProjectID: project.ID, APIKeyID: key.ID,
		AttributedUserID: usageAttributionUserID(key, project), Model: "gpt-background",
	}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAPIKey(key.ID, APIKey{Status: StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	server.startResponseWorkers()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, ok, err := store.GetResponseJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ok && responseJobTerminal(current.Status) {
			if current.Status != responseJobStatusFailed || current.ErrorCode != "response_authorization_lost" {
				t.Fatalf("authorization loss produced wrong terminal state: %+v", current)
			}
			if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].StatusCode != http.StatusUnauthorized || logs[0].ErrorCode != "response_authorization_lost" {
				t.Fatalf("authorization rejection was not audited once: %+v", logs)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("authorization-revoked job did not terminate")
}

func TestBackgroundResponsesReservesQuotaBeforeUpstreamExecution(t *testing.T) {
	server, store, secret := newBackgroundResponseTestServer(t)
	key := store.ListAPIKeys()[0]
	verySmallLimit := int64(10)
	if _, err := store.UpdateAPIKey(key.ID, APIKey{TokenLimitTPM: &verySmallLimit, TokenLimitSet: true}); err != nil {
		t.Fatal(err)
	}
	id := submitBackgroundResponse(t, server.Handler(), secret, "quota must reject before upstream")
	failed := waitForResponseJobStatus(t, server.Handler(), secret, id, "failed")
	errorObject, _ := failed["error"].(map[string]any)
	if errorObject["code"] != "api_key_tpm_exceeded" {
		t.Fatalf("unexpected quota failure: %#v", failed)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].ErrorCode != "api_key_tpm_exceeded" || logs[0].ProviderID != "" {
		t.Fatalf("quota rejection reached routing or was not audited once: %+v", logs)
	}
	if records := store.ListUsageRecords(); len(records) != 0 {
		t.Fatalf("quota-rejected job created billed usage: %+v", records)
	}
}

type blockingResponseAdapter struct {
	MockAdapter
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func (a *blockingResponseAdapter) Responses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest) (any, Usage, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	if a.stopped != nil {
		close(a.stopped)
	}
	return nil, Usage{}, ctx.Err()
}

type failingResponseJobAdapter struct{ MockAdapter }

func (failingResponseJobAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "fake upstream failed")
}

type rejectResponseDispatchStore struct{ *GormStore }

func (s *rejectResponseDispatchStore) MarkResponseJobPhase(id string, owner string, epoch int64, phase string, requestID string) (bool, error) {
	if phase == responseJobPhaseDispatched {
		return false, nil
	}
	return s.GormStore.MarkResponseJobPhase(id, owner, epoch, phase, requestID)
}

type pauseAfterResponseClaimStore struct {
	*GormStore
	claimed chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *pauseAfterResponseClaimStore) ClaimResponseJob(owner string, leaseTTL time.Duration, resultTTL time.Duration) (ResponseJob, bool, error) {
	job, claimed, err := s.GormStore.ClaimResponseJob(owner, leaseTTL, resultTTL)
	if claimed && err == nil {
		s.once.Do(func() { close(s.claimed) })
		<-s.release
	}
	return job, claimed, err
}

func TestBackgroundResponsesShutdownAfterClaimRequeuesForRestart(t *testing.T) {
	config := responseJobTestConfig()
	config.ResponseLeaseTTLSeconds = 10
	store, secret := newBackgroundResponseTestStore(t, config)
	key := store.ListAPIKeys()[0]
	project, ok := store.GetProject(key.ProjectID)
	if !ok {
		t.Fatal("background test project not found")
	}
	requestJSON, _ := json.Marshal(ResponsesRequest{Model: "gpt-background", Input: "resume after shutdown", Background: true})
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: requestJSON})
	job, err := store.CreateResponseJob(ResponseJob{
		ID: NewID("resp"), ProjectID: project.ID, APIKeyID: key.ID,
		AttributedUserID: usageAttributionUserID(key, project), Model: "gpt-background",
	}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	pausedStore := &pauseAfterResponseClaimStore{
		GormStore: store,
		claimed:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	server := NewWithConfig(pausedStore, config)
	select {
	case <-pausedStore.claimed:
	case <-time.After(3 * time.Second):
		t.Fatal("response worker did not claim the queued job")
	}
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		shutdownDone <- server.Shutdown(ctx)
	}()
	select {
	case <-server.responseContext.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not stop response claims")
	}
	close(pausedStore.release)
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	requeued, ok, err := store.GetResponseJob(job.ID)
	if err != nil || !ok || requeued.Status != responseJobStatusQueued || requeued.Phase != responseJobPhaseQueued || requeued.RequestID != "" {
		t.Fatalf("claimed job was not returned to the queue: %+v ok=%v err=%v", requeued, ok, err)
	}
	if logs := store.ListRequestLogs(); len(logs) != 0 {
		t.Fatalf("shutdown turned an undispatched claim into a request failure: %+v", logs)
	}

	restarted := NewWithConfig(store, config)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = restarted.Shutdown(ctx)
	})
	completed := waitForResponseJobStatus(t, restarted.Handler(), secret, job.ID, "completed")
	if completed["output_text"] != "Echo: resume after shutdown" {
		t.Fatalf("restarted worker returned an unexpected result: %#v", completed)
	}
	if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].StatusCode != http.StatusOK {
		t.Fatalf("restarted job was not accounted exactly once: %+v", logs)
	}
}

func TestBackgroundResponsesShutdownAfterDispatchRecordsExecutionLost(t *testing.T) {
	config := responseJobTestConfig()
	config.ResponseJobTimeoutSeconds = 10
	server, store, secret := newBackgroundResponseTestServerWithConfig(t, config)
	adapter := &blockingResponseAdapter{started: make(chan struct{})}
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityResponses)
	id := submitBackgroundResponse(t, server.Handler(), secret, "dispatched before shutdown")
	select {
	case <-adapter.started:
	case <-time.After(3 * time.Second):
		t.Fatal("background response did not reach upstream")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := server.Shutdown(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	job, ok, err := store.GetResponseJob(id)
	if err != nil || !ok || job.Status != responseJobStatusFailed || job.ErrorCode != "response_execution_lost" {
		t.Fatalf("dispatched shutdown did not record explicit execution loss: %+v ok=%v err=%v", job, ok, err)
	}
	if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].RequestID != job.RequestID || logs[0].ErrorCode != "response_execution_lost" {
		t.Fatalf("dispatched shutdown was not audited once: %+v", logs)
	}
}

func TestBackgroundResponsesPersistsUpstreamFailure(t *testing.T) {
	server, store, secret := newBackgroundResponseTestServer(t)
	server.adapterRegistry.Register(ProviderMock, failingResponseJobAdapter{}, AdapterCapabilityResponses)
	id := submitBackgroundResponse(t, server.Handler(), secret, "fail me")
	failed := waitForResponseJobStatus(t, server.Handler(), secret, id, "failed")
	errorObject, _ := failed["error"].(map[string]any)
	if errorObject["code"] != "provider_error" {
		t.Fatalf("unexpected persisted failure: %#v", failed)
	}
	if strings.Contains(stringifyValueForTest(errorObject["message"]), "fake upstream failed") {
		t.Fatalf("upstream error text was persisted without redaction: %#v", failed)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != http.StatusBadGateway || logs[0].ErrorCode != "provider_error" {
		t.Fatalf("upstream failure was not accounted once: %+v", logs)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Where("request_id = ?", logs[0].RequestID).Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].ErrorMessage != "" {
		t.Fatalf("background route audit retained upstream error text: %+v", attempts)
	}
}

func TestBackgroundResponsesLostBeforeDispatchReturnsToQueueWithoutSettlement(t *testing.T) {
	config := responseJobTestConfig()
	config.ResponseLeaseTTLSeconds = 5
	store, secret := newBackgroundResponseTestStore(t, config)
	server := NewWithConfig(&rejectResponseDispatchStore{GormStore: store}, config)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	id := submitBackgroundResponse(t, server.Handler(), secret, "never copy this prompt")
	var admitted ResponseJob
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, ok, err := store.GetResponseJob(id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && current.Phase == responseJobPhaseAdmitted && current.RequestID != "" {
			admitted = current
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if admitted.RequestID == "" {
		t.Fatal("job did not reach admitted phase before dispatch rejection")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var leases int64
		if err := store.db.Model(&InFlightLease{}).Where("id = ?", admitted.RequestID).Count(&leases).Error; err != nil {
			t.Fatal(err)
		}
		_, heartbeatRegistered := store.leaseHeartbeats.Load(admitted.RequestID)
		if leases == 0 && !heartbeatRegistered {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var retainedLeases int64
	if err := store.db.Model(&InFlightLease{}).Where("id = ?", admitted.RequestID).Count(&retainedLeases).Error; err != nil || retainedLeases != 0 {
		t.Fatalf("dispatch rejection retained concurrency: count=%d err=%v", retainedLeases, err)
	}
	if _, loaded := store.leaseHeartbeats.Load(admitted.RequestID); loaded {
		t.Fatal("dispatch rejection retained its process-local heartbeat registration")
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := server.Shutdown(shutdownContext); err != nil {
		shutdownCancel()
		t.Fatal(err)
	}
	shutdownCancel()
	past := time.Now().UTC().Add(-time.Second)
	if err := store.db.Model(&ResponseJob{}).Where("id = ?", id).Update("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	requeued, failed, _, err := store.RecoverResponseJobs(time.Minute)
	if err != nil || requeued != 1 || failed != 0 {
		t.Fatalf("recover undispatched job: requeued=%d failed=%d err=%v", requeued, failed, err)
	}
	current, ok, err := store.GetResponseJob(id)
	if err != nil || !ok || current.Status != responseJobStatusQueued || current.Phase != responseJobPhaseQueued || current.RequestID != "" {
		t.Fatalf("undispatched job was not requeued: %+v ok=%v err=%v", current, ok, err)
	}
	if logs := store.ListRequestLogs(); len(logs) != 0 {
		t.Fatalf("undispatched recovery created a terminal request audit: %+v", logs)
	}
	var payloadLogs int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("request_id = ?", admitted.RequestID).Count(&payloadLogs).Error; err != nil || payloadLogs != 0 {
		t.Fatalf("undispatched recovery copied background content into plaintext audit storage: count=%d err=%v", payloadLogs, err)
	}
	var minuteTokens int64
	if err := store.db.Model(&QuotaBucket{}).Where("scope = ?", "minute").Select("COALESCE(SUM(total_tokens), 0)").Scan(&minuteTokens).Error; err != nil || minuteTokens != 0 {
		t.Fatalf("undispatched recovery retained token reservation: tokens=%d err=%v", minuteTokens, err)
	}
	var requests int64
	if err := store.db.Model(&QuotaBucket{}).Select("COALESCE(SUM(requests), 0)").Scan(&requests).Error; err != nil || requests != 0 {
		t.Fatalf("undispatched recovery retained request quota: requests=%d err=%v", requests, err)
	}
}

func TestBackgroundResponsesTimesOutAndPersistsTerminalReason(t *testing.T) {
	config := responseJobTestConfig()
	config.ResponseJobTimeoutSeconds = 1
	server, store, secret := newBackgroundResponseTestServerWithConfig(t, config)
	adapter := &blockingResponseAdapter{started: make(chan struct{})}
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityResponses)
	id := submitBackgroundResponse(t, server.Handler(), secret, "timeout")
	failed := waitForResponseJobStatus(t, server.Handler(), secret, id, "failed")
	errorObject, _ := failed["error"].(map[string]any)
	if errorObject["code"] != "response_job_timeout" {
		t.Fatalf("unexpected timeout result: %#v", failed)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != http.StatusGatewayTimeout || logs[0].ErrorCode != "response_job_timeout" {
		t.Fatalf("timeout was not settled once: %+v", logs)
	}
}

func TestBackgroundResponsesConcurrencyLeaseLossCancelsUpstream(t *testing.T) {
	config := responseJobTestConfig()
	config.InFlightLeaseTTLSeconds = 1
	config.ResponseJobTimeoutSeconds = 5
	server, store, secret := newBackgroundResponseTestServerWithConfig(t, config)
	adapter := &blockingResponseAdapter{started: make(chan struct{}), stopped: make(chan struct{})}
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityResponses)
	id := submitBackgroundResponse(t, server.Handler(), secret, "cancel when concurrency lease is lost")
	select {
	case <-adapter.started:
	case <-time.After(3 * time.Second):
		t.Fatal("background response did not reach upstream")
	}
	var requestID string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, ok, err := store.GetResponseJob(id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && job.RequestID != "" {
			requestID = job.RequestID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requestID == "" {
		t.Fatal("running background response has no request ID")
	}
	if err := store.db.Delete(&InFlightLease{}, "id = ?", requestID).Error; err != nil {
		t.Fatal(err)
	}
	select {
	case <-adapter.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("lost API-key concurrency lease did not cancel the upstream context")
	}
	failed := waitForResponseJobStatus(t, server.Handler(), secret, id, "failed")
	errorObject, _ := failed["error"].(map[string]any)
	if errorObject["code"] != "response_execution_lost" {
		t.Fatalf("unexpected concurrency lease loss result: %#v", failed)
	}
	if _, loaded := store.leaseHeartbeats.Load(requestID); loaded {
		t.Fatal("completed lease-loss path retained its heartbeat registration")
	}
}

func TestBackgroundResponsesExpiryScrubsSensitivePayloads(t *testing.T) {
	config := responseJobTestConfig()
	config.ResponseResultTTLSeconds = 1
	server, store, secret := newBackgroundResponseTestServerWithConfig(t, config)
	id := submitBackgroundResponse(t, server.Handler(), secret, "expire this secret")
	waitForResponseJobStatus(t, server.Handler(), secret, id, "completed")
	time.Sleep(1100 * time.Millisecond)
	expired := doJSON(t, server.Handler(), http.MethodGet, "/v1/responses/"+id, nil, secret)
	if expired.Code != http.StatusNotFound {
		t.Fatalf("expired response remained readable: %d %s", expired.Code, expired.Body)
	}
	var persisted ResponseJob
	if err := store.db.First(&persisted, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != responseJobStatusExpired || persisted.RequestCiphertext != "" || persisted.ResultCiphertext != "" {
		t.Fatalf("expiry did not scrub sensitive payloads: %+v", persisted)
	}
}

func TestBackgroundResponsesExposeQueueExecutionAndStatusMetrics(t *testing.T) {
	config := responseJobTestConfig()
	config.MetricsEnabled = true
	config.ResponseResultTTLSeconds = 1
	server, _, secret := newBackgroundResponseTestServerWithConfig(t, config)
	id := submitBackgroundResponse(t, server.Handler(), secret, "metrics")
	waitForResponseJobStatus(t, server.Handler(), secret, id, "completed")
	metrics := doJSON(t, server.Handler(), http.MethodGet, "/metrics", nil, "dev_admin_token")
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics request failed: %d %s", metrics.Code, metrics.Body)
	}
	for _, name := range []string{
		"tokenhub_gateway_response_jobs_queued",
		"tokenhub_gateway_response_job_queue_wait_seconds_count",
		"tokenhub_gateway_response_job_execution_seconds_count",
		"tokenhub_gateway_response_jobs_total",
	} {
		if !strings.Contains(metrics.Body, name) {
			t.Fatalf("response job metric %s missing from exposition", name)
		}
	}
	expiredSeries := `tokenhub_gateway_response_jobs_total{error_code="response_expired",status="expired"} 1`
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(metrics.Body, expiredSeries) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		metrics = doJSON(t, server.Handler(), http.MethodGet, "/metrics", nil, "dev_admin_token")
	}
	if !strings.Contains(metrics.Body, expiredSeries) {
		t.Fatalf("expired response transition was not counted: %s", metrics.Body)
	}
}

func TestBackgroundResponsesCancellationWinsCompletionRaceAndSettlesQuota(t *testing.T) {
	server, store, secret := newBackgroundResponseTestServer(t)
	adapter := &blockingResponseAdapter{started: make(chan struct{})}
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityEmbeddings)
	id := submitBackgroundResponse(t, server.Handler(), secret, "cancel me")
	select {
	case <-adapter.started:
	case <-time.After(3 * time.Second):
		t.Fatal("background response did not reach upstream")
	}
	cancelled := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses/"+id+"/cancel", map[string]any{}, secret)
	if cancelled.Code != http.StatusOK || !strings.Contains(cancelled.Body, `"status":"cancelled"`) {
		t.Fatalf("cancel failed: %d %s", cancelled.Code, cancelled.Body)
	}
	waitForResponseJobStatus(t, server.Handler(), secret, id, "cancelled")
	deadline := time.Now().Add(3 * time.Second)
	for len(store.ListRequestLogs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != 499 || logs[0].ErrorCode != "response_cancelled" {
		t.Fatalf("cancelled call was not settled exactly once: %+v", logs)
	}
	var leases int64
	if err := store.db.Model(&InFlightLease{}).Count(&leases).Error; err != nil || leases != 0 {
		t.Fatalf("cancelled call retained a concurrency lease: count=%d err=%v", leases, err)
	}
	var minuteTokens int64
	if err := store.db.Model(&QuotaBucket{}).Where("scope = ?", "minute").Select("COALESCE(SUM(total_tokens), 0)").Scan(&minuteTokens).Error; err != nil || minuteTokens != 0 {
		t.Fatalf("cancelled call retained its token reservation: tokens=%d err=%v", minuteTokens, err)
	}
	job, ok, err := store.GetResponseJob(id)
	if err != nil || !ok || job.Status != responseJobStatusCancelled {
		t.Fatalf("completion overwrote cancellation: %+v ok=%v err=%v", job, ok, err)
	}
}

func TestBackgroundResponsesSQLiteRestartProcessesQueuedJob(t *testing.T) {
	config := responseJobTestConfig()
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "responses-restart.db")
	store, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Restart project", Status: StatusActive})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Restart key", Allowed: []string{"gpt-restart"}, Status: StatusActive}, "thk_restart")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_restart", Name: "Restart mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_restart", ProviderID: provider.ID, Name: "Restart resource", ResourceType: "mock", Status: StatusActive, Healthy: true})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{ID: "gpt-restart", Name: "gpt-restart", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_restart", ModelName: "gpt-restart", ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "gpt-restart-upstream", Status: StatusActive, Priority: 1, Weight: 100})
	requestJSON, _ := json.Marshal(ResponsesRequest{Model: "gpt-restart", Input: "after restart", Background: true})
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: requestJSON})
	job, err := store.CreateResponseJob(ResponseJob{
		ID: NewID("resp"), ProjectID: project.ID, APIKeyID: key.ID,
		AttributedUserID: usageAttributionUserID(key, project), Model: "gpt-restart",
	}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := store.db.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(reopened, config)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		db, _ := reopened.db.DB()
		_ = db.Close()
	})
	completed := waitForResponseJobStatus(t, server.Handler(), secret, job.ID, "completed")
	if completed["output_text"] != "Echo: after restart" {
		t.Fatalf("queued response did not continue after restart: %#v", completed)
	}
}

func TestResponseJobRecoveryDoesNotReplayPossiblyDispatchedWork(t *testing.T) {
	config := responseJobTestConfig()
	store := NewMemoryStoreWithConfig(config)
	requestJSON, _ := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"gpt-x","input":"x","background":true}`)})
	created, err := store.CreateResponseJob(ResponseJob{ID: NewID("resp"), ProjectID: "project", APIKeyID: "key", Model: "gpt-x"}, requestJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimResponseJob("worker-a", 50*time.Millisecond, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := store.db.Model(&ResponseJob{}).
		Where("id = ? AND status = ? AND lease_owner = ? AND lease_epoch = ?", claimed.ID, responseJobStatusRunning, "worker-a", claimed.LeaseEpoch).
		Updates(map[string]any{"phase": responseJobPhaseDispatched, "request_id": "req_maybe_sent"}).Error; err != nil {
		t.Fatalf("seed dispatched state: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	_, failed, _, err := store.RecoverResponseJobs(time.Minute)
	if err != nil || failed != 1 {
		t.Fatalf("recover unsafe job: failed=%d err=%v", failed, err)
	}
	recovered, ok, err := store.GetResponseJob(created.ID)
	if err != nil || !ok || recovered.Status != responseJobStatusFailed || recovered.ErrorCode != "response_execution_lost" {
		t.Fatalf("unsafe job was replayable after lease loss: %+v ok=%v err=%v", recovered, ok, err)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].RequestID != "req_maybe_sent" || logs[0].ErrorCode != "response_execution_lost" {
		t.Fatalf("lost execution did not leave an explicit request audit: %+v", logs)
	}
}

func TestResponseJobCancellationBeforeDispatchPreventsUpstreamTransition(t *testing.T) {
	config := responseJobTestConfig()
	store, _ := newBackgroundResponseTestStore(t, config)
	key := store.ListAPIKeys()[0]
	project, ok := store.GetProject(key.ProjectID)
	if !ok {
		t.Fatal("background test project not found")
	}
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"gpt-background","input":"cancel before dispatch","background":true}`)})
	job, err := store.CreateResponseJob(ResponseJob{
		ID: NewID("resp"), ProjectID: project.ID, APIKeyID: key.ID,
		AttributedUserID: usageAttributionUserID(key, project), Model: "gpt-background",
	}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimResponseJob("cancel-worker", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim cancellation job: ok=%v err=%v", ok, err)
	}
	call, retained, err := store.AdmitResponseJob(context.Background(), job.ID, "cancel-worker", claimed.LeaseEpoch, key, "gpt-background", 5)
	if err != nil || !retained {
		t.Fatalf("admit cancellation job: retained=%v err=%v", retained, err)
	}
	defer func() { _ = store.stopInFlightLeaseHeartbeat(call.RequestID) }()
	if _, ok, err := store.CancelResponseJob(job.ID, "test", time.Minute); err != nil || !ok {
		t.Fatalf("request cancellation: ok=%v err=%v", ok, err)
	}
	if retained, err := store.MarkResponseJobPhase(job.ID, "cancel-worker", claimed.LeaseEpoch, responseJobPhaseDispatched, call.RequestID); err != nil || retained {
		t.Fatalf("cancelled admitted job reached dispatched: retained=%v err=%v", retained, err)
	}
	current, ok, err := store.GetResponseJob(job.ID)
	if err != nil || !ok || current.Phase != responseJobPhaseAdmitted || current.CancelRequestedAt == nil {
		t.Fatalf("cancellation CAS state is inconsistent: %+v ok=%v err=%v", current, ok, err)
	}
	finished, settled, err := store.FinalizeResponseJob(call, job.ID, "cancel-worker", claimed.LeaseEpoch, responseJobStatusCancelled, nil, RouteSelection{}, Usage{}, 499, "response_cancelled", "Response job was cancelled", "", "", time.Minute)
	if err != nil || !settled || finished.Status != responseJobStatusCancelled {
		t.Fatalf("cancelled job did not settle once: settled=%v job=%+v err=%v", settled, finished, err)
	}
	var leases int64
	if err := store.db.Model(&InFlightLease{}).Count(&leases).Error; err != nil || leases != 0 {
		t.Fatalf("pre-dispatch cancellation retained concurrency: count=%d err=%v", leases, err)
	}
	var minuteTokens int64
	if err := store.db.Model(&QuotaBucket{}).Where("scope = ?", "minute").Select("COALESCE(SUM(total_tokens), 0)").Scan(&minuteTokens).Error; err != nil || minuteTokens != 0 {
		t.Fatalf("pre-dispatch cancellation retained token reservation: tokens=%d err=%v", minuteTokens, err)
	}
}

func TestResponseJobAdmissionPersistsAtomicallyAndRecoversWithoutDoubleCharging(t *testing.T) {
	config := responseJobTestConfig()
	store := NewMemoryStoreWithConfig(config)
	project := store.CreateProject(Project{Name: "Atomic admission", Status: StatusActive})
	rpm := int64(100)
	tpm := int64(100)
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:          "Atomic admission key",
		Allowed:       []string{"gpt-atomic"},
		RateLimitRPM:  &rpm,
		TokenLimitTPM: &tpm,
		Limits:        QuotaLimits{DailyRequests: 1, MonthlyRequests: 10, MaxConcurrency: 1},
		Status:        StatusActive,
	}, "thk_atomic_admission")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{ID: "gpt-atomic", Name: "gpt-atomic", Modality: "chat", Status: StatusActive})
	now := time.Now().UTC()
	if err := store.db.Create(&QuotaBucket{
		KeyID: key.ID, Scope: "day", Bucket: dayBucket(now), QuotaCounter: QuotaCounter{Requests: 1},
	}).Error; err != nil {
		t.Fatal(err)
	}
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"gpt-atomic","input":"atomic","background":true}`)})
	created, err := store.CreateResponseJob(ResponseJob{
		ID: NewID("resp"), ProjectID: project.ID, APIKeyID: key.ID,
		AttributedUserID: usageAttributionUserID(key, project), Model: "gpt-atomic",
	}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimResponseJob("worker-atomic", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim atomic job: ok=%v err=%v", ok, err)
	}
	if _, retained, admitErr := store.AdmitResponseJob(context.Background(), claimed.ID, "worker-atomic", claimed.LeaseEpoch, key, "gpt-atomic", 5); !retained || admitErr == nil || AsHTTPError(admitErr).Code != "quota_exceeded" {
		t.Fatalf("expected transactional quota rejection: retained=%v err=%v", retained, admitErr)
	}
	rolledBack, ok, err := store.GetResponseJob(created.ID)
	if err != nil || !ok || rolledBack.Phase != responseJobPhaseClaimed || rolledBack.RequestID != "" {
		t.Fatalf("failed admission left a partial phase transition: %+v ok=%v err=%v", rolledBack, ok, err)
	}
	var leases int64
	if err := store.db.Model(&InFlightLease{}).Count(&leases).Error; err != nil || leases != 0 {
		t.Fatalf("failed admission leaked concurrency: count=%d err=%v", leases, err)
	}
	var minuteTokens int64
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ? AND scope = ?", key.ID, "minute").Select("COALESCE(SUM(total_tokens), 0)").Scan(&minuteTokens).Error; err != nil || minuteTokens != 0 {
		t.Fatalf("failed admission retained token quota: tokens=%d err=%v", minuteTokens, err)
	}
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ? AND scope = ? AND bucket = ?", key.ID, "day", dayBucket(now)).Update("requests", 0).Error; err != nil {
		t.Fatal(err)
	}
	call, retained, err := store.AdmitResponseJob(context.Background(), claimed.ID, "worker-atomic", claimed.LeaseEpoch, key, "gpt-atomic", 5)
	if err != nil || !retained || call.RequestID == "" {
		t.Fatalf("successful atomic admission: retained=%v call=%+v err=%v", retained, call, err)
	}
	_ = store.stopInFlightLeaseHeartbeat(call.RequestID)
	admitted, ok, err := store.GetResponseJob(created.ID)
	if err != nil || !ok || admitted.Phase != responseJobPhaseAdmitted || admitted.RequestID != call.RequestID || admitted.ReservedTokens != 5 {
		t.Fatalf("admission state and quota commit diverged: %+v ok=%v err=%v", admitted, ok, err)
	}
	past := time.Now().UTC().Add(-time.Second)
	if err := store.db.Model(&ResponseJob{}).Where("id = ?", created.ID).Update("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	requeued, failed, _, err := store.RecoverResponseJobs(time.Minute)
	if err != nil || requeued != 1 || failed != 0 {
		t.Fatalf("recover admitted job: requeued=%d failed=%d err=%v", requeued, failed, err)
	}
	if err := store.db.Model(&InFlightLease{}).Count(&leases).Error; err != nil || leases != 0 {
		t.Fatalf("recovery leaked concurrency: count=%d err=%v", leases, err)
	}
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ? AND scope = ?", key.ID, "minute").Select("COALESCE(SUM(total_tokens), 0)").Scan(&minuteTokens).Error; err != nil || minuteTokens != 0 {
		t.Fatalf("undispatched recovery retained token reservation: tokens=%d err=%v", minuteTokens, err)
	}
	var heldRequests int64
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ?", key.ID).Select("COALESCE(SUM(requests), 0)").Scan(&heldRequests).Error; err != nil || heldRequests != 0 {
		t.Fatalf("undispatched recovery retained request quota: requests=%d err=%v", heldRequests, err)
	}
	recovered, ok, err := store.GetResponseJob(created.ID)
	if err != nil || !ok || recovered.Status != responseJobStatusQueued || recovered.Phase != responseJobPhaseQueued || recovered.RequestID != "" {
		t.Fatalf("admitted job was not returned to the queue: %+v ok=%v err=%v", recovered, ok, err)
	}
	claimedAgain, ok, err := store.ClaimResponseJob("worker-retry", time.Second, time.Minute)
	if err != nil || !ok || claimedAgain.ID != created.ID {
		t.Fatalf("claim recovered job: job=%+v ok=%v err=%v", claimedAgain, ok, err)
	}
	retryCall, retained, err := store.AdmitResponseJob(context.Background(), claimedAgain.ID, "worker-retry", claimedAgain.LeaseEpoch, key, "gpt-atomic", 5)
	if err != nil || !retained {
		t.Fatalf("readmit recovered job: retained=%v err=%v", retained, err)
	}
	_ = store.stopInFlightLeaseHeartbeat(retryCall.RequestID)
	retained, err = store.MarkResponseJobPhase(claimedAgain.ID, "worker-retry", claimedAgain.LeaseEpoch, responseJobPhaseDispatched, retryCall.RequestID)
	if err != nil || !retained {
		t.Fatalf("dispatch recovered job: retained=%v err=%v", retained, err)
	}
	if _, settled, err := store.FinalizeResponseJob(retryCall, created.ID, "worker-retry", claimedAgain.LeaseEpoch, responseJobStatusSucceeded, []byte(`{"id":"response-retry"}`), RouteSelection{}, Usage{}, http.StatusOK, "", "", "", "", time.Minute); err != nil || !settled {
		t.Fatalf("settle recovered job: settled=%v err=%v", settled, err)
	}
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ? AND scope = ?", key.ID, "minute").Update("total_tokens", 7).Error; err != nil {
		t.Fatal(err)
	}
	if _, settled, err := store.FinalizeResponseJob(call, created.ID, "worker-atomic", claimed.LeaseEpoch, responseJobStatusFailed, nil, RouteSelection{}, Usage{}, 500, "stale_worker", "stale", "", "", time.Minute); err != nil || settled {
		t.Fatalf("stale worker performed a second settlement: settled=%v err=%v", settled, err)
	}
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ? AND scope = ?", key.ID, "minute").Select("COALESCE(SUM(total_tokens), 0)").Scan(&minuteTokens).Error; err != nil || minuteTokens != 7 {
		t.Fatalf("stale settlement refunded another request's tokens: tokens=%d err=%v", minuteTokens, err)
	}
	if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].RequestID != retryCall.RequestID || logs[0].ErrorCode != "" {
		t.Fatalf("stale settlement duplicated the request audit: %+v", logs)
	}
}

func TestResponseJobPayloadEncryptionFailsClosed(t *testing.T) {
	config := responseJobTestConfig()
	store := NewMemoryStoreWithConfig(config)
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"gpt-x","input":"secret","background":true}`)})
	created, err := store.CreateResponseJob(ResponseJob{ID: NewID("resp"), ProjectID: "project", APIKeyID: "key", Model: "gpt-x"}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&ResponseJob{}).Where("id = ?", created.ID).Update("request_ciphertext", string(envelopeJSON)).Error; err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimResponseJob("worker", time.Second, time.Minute); err == nil || claimed {
		t.Fatalf("plaintext payload was accepted: claimed=%v err=%v", claimed, err)
	}
	failed, ok, err := store.GetResponseJob(created.ID)
	if err != nil || !ok || failed.Status != responseJobStatusFailed || failed.ErrorCode != "response_payload_unreadable" || failed.RequestCiphertext != "" {
		t.Fatalf("unreadable payload did not fail closed: %+v ok=%v err=%v", failed, ok, err)
	}
}

func TestResponseJobSQLiteReplicasClaimExactlyOnce(t *testing.T) {
	config := responseJobTestConfig()
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "responses-claim.db")
	storeA, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, store := range []*GormStore{storeA, storeB} {
			db, _ := store.db.DB()
			_ = db.Close()
		}
	})
	envelopeJSON, _ := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"gpt-x","input":"x","background":true}`)})
	if _, err := storeA.CreateResponseJob(ResponseJob{ID: NewID("resp"), ProjectID: "project", APIKeyID: "key", Model: "gpt-x"}, envelopeJSON); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		claimed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for index, store := range []*GormStore{storeA, storeB} {
		go func(index int, store *GormStore) {
			<-start
			_, claimed, err := store.ClaimResponseJob("worker-"+string(rune('a'+index)), time.Second, time.Minute)
			results <- claimResult{claimed: claimed, err: err}
		}(index, store)
	}
	close(start)
	claimedCount := 0
	for index := 0; index < 2; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("replica claim returned error: %v", result.err)
		}
		if result.claimed {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("expected exactly one SQLite replica to claim the job, got %d", claimedCount)
	}
}
