package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

type countingRateLimitAdapter struct {
	MockAdapter
	calls atomic.Int64
}

type blockingTPMAdapter struct {
	MockAdapter
	started chan struct{}
	release <-chan struct{}
	calls   atomic.Int64
}

type failFirstRateLimitAdapter struct {
	MockAdapter
	calls atomic.Int64
}

type usageFailFirstRateLimitAdapter struct {
	MockAdapter
	calls atomic.Int64
}

type partialStreamRateLimitAdapter struct {
	MockAdapter
	wrote chan struct{}
	calls atomic.Int64
}

func (a *failFirstRateLimitAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	if a.calls.Add(1) == 1 {
		return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "upstream_failed", "upstream failed")
	}
	return a.MockAdapter.Chat(ctx, provider, providerModel, req)
}

func (a *usageFailFirstRateLimitAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	if a.calls.Add(1) == 1 {
		return nil, Usage{TotalTokens: 8}, NewHTTPError(http.StatusBadGateway, "upstream_failed", "upstream failed after token usage")
	}
	return a.MockAdapter.Chat(ctx, provider, providerModel, req)
}

func (a *blockingTPMAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	a.calls.Add(1)
	close(a.started)
	select {
	case <-ctx.Done():
		return Usage{}, ctx.Err()
	case <-a.release:
		return a.MockAdapter.ChatStream(ctx, provider, providerModel, req, w)
	}
}

func (a *partialStreamRateLimitAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	a.calls.Add(1)
	if _, err := io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"); err != nil {
		return Usage{}, err
	}
	close(a.wrote)
	<-ctx.Done()
	return Usage{}, ctx.Err()
}

func (a *countingRateLimitAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	a.calls.Add(1)
	return a.MockAdapter.Chat(ctx, provider, providerModel, req)
}

func (a *countingRateLimitAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	a.calls.Add(1)
	return a.MockAdapter.ChatStream(ctx, provider, providerModel, req, w)
}

func TestAPIKeyRPMRejectsBeforeProviderAndReturnsRetryHeaders(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "RPM Limited", Status: StatusActive})
	rpm := int64(1)
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:         "rpm-limited",
		Allowed:      []string{"gpt-4.1-mini"},
		RateLimitRPM: &rpm,
		Status:       StatusActive,
	}, "thk_rpm_limited")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: provider.ID, ProviderModel: "mock-chat", Status: StatusActive})
	adapter := &countingRateLimitAdapter{}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityEmbeddings)
	app := server.Handler()

	first := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 1,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, secret)
	if first.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d: %s", first.Code, first.Body)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1-mini","max_tokens":1,"messages":[{"role":"user","content":"again"}]}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+secret)
	resp := httptest.NewRecorder()
	app.ServeHTTP(resp, req)

	if resp.Code != http.StatusTooManyRequests || !strings.Contains(resp.Body.String(), `"code":"api_key_rpm_exceeded"`) {
		t.Fatalf("second request expected api_key_rpm_exceeded, got %d: %s", resp.Code, resp.Body.String())
	}
	if resp.Header().Get("Retry-After") == "" || resp.Header().Get("X-RateLimit-Limit-Requests") != "1" || resp.Header().Get("X-RateLimit-Remaining-Requests") != "0" {
		t.Fatalf("expected retry and request limit headers, got %+v", resp.Header())
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("rejected request reached provider: calls=%d", calls)
	}
}

func TestAPIKeyRPMConcurrentAdmissionIsAtomic(t *testing.T) {
	store, _ := newRateLimitedGateway(t, APIKey{RateLimitRPM: int64Pointer(1)})
	project := store.ListProjects()[0]
	key := store.ListAPIKeys()[0]
	start := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			<-start
			_, err := store.StartCall(context.Background(), project, key, "gpt-4.1-mini", 0)
			results <- err
		}()
	}
	close(start)
	allowed := 0
	limited := 0
	for range 8 {
		err := <-results
		if err == nil {
			allowed++
		} else if AsHTTPError(err).Code == "api_key_rpm_exceeded" {
			limited++
		} else {
			t.Fatalf("unexpected concurrent admission error: %v", err)
		}
	}
	if allowed != 1 || limited != 7 {
		t.Fatalf("concurrent RPM admission allowed=%d limited=%d, want 1 and 7", allowed, limited)
	}
}

func TestAPIKeyTPMReservesStreamingBudgetBeforeProvider(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "TPM Limited", Status: StatusActive})
	tpm := int64(10)
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:          "tpm-limited",
		Allowed:       []string{"gpt-4.1-mini"},
		TokenLimitTPM: &tpm,
		Status:        StatusActive,
	}, "thk_tpm_limited")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: provider.ID, ProviderModel: "mock-chat", Status: StatusActive})
	release := make(chan struct{})
	adapter := &blockingTPMAdapter{started: make(chan struct{}), release: release}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat, AdapterCapabilityChatStream)
	app := server.Handler()

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1-mini","stream":true,"max_tokens":2,"messages":[{"role":"user","content":"a"}]}`))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+secret)
		resp := httptest.NewRecorder()
		app.ServeHTTP(resp, req)
		firstDone <- resp
	}()

	select {
	case <-adapter.started:
	case <-t.Context().Done():
		t.Fatal("first streaming request did not reach provider")
	}
	second := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1-mini","max_tokens":1,"messages":[{"role":"user","content":"b"}]}`))
	second.Header.Set("content-type", "application/json")
	second.Header.Set("authorization", "Bearer "+secret)
	secondResp := httptest.NewRecorder()
	app.ServeHTTP(secondResp, second)

	if secondResp.Code != http.StatusTooManyRequests || !strings.Contains(secondResp.Body.String(), `"code":"api_key_tpm_exceeded"`) {
		t.Fatalf("concurrent request expected api_key_tpm_exceeded, got %d: %s", secondResp.Code, secondResp.Body.String())
	}
	if secondResp.Header().Get("X-RateLimit-Limit-Tokens") != "10" || secondResp.Header().Get("X-RateLimit-Remaining-Tokens") != "0" {
		t.Fatalf("expected exhausted token limit headers, got %+v", secondResp.Header())
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("TPM-rejected request reached provider: calls=%d", calls)
	}
	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first streaming request expected 200, got %d: %s", first.Code, first.Body.String())
	}
}

func TestAdminCanSetModifyAndClearAPIKeyMinuteLimitsIndependently(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+defaultProjectID+"/keys", map[string]any{
		"name":            "Minute Limited Key",
		"rate_limit_rpm":  2,
		"token_limit_tpm": 5,
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create key expected 201, got %d: %s", created.Code, created.Body)
	}
	keys := store.ListProjectKeys(defaultProjectID)
	if len(keys) != 1 || keys[0].RateLimitRPM == nil || *keys[0].RateLimitRPM != 2 || keys[0].TokenLimitTPM == nil || *keys[0].TokenLimitTPM != 5 {
		t.Fatalf("created key did not preserve minute limits: %+v", keys)
	}

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+keys[0].ID, map[string]any{
		"rate_limit_rpm": 0,
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("set explicit zero expected 200, got %d: %s", updated.Code, updated.Body)
	}
	keys = store.ListProjectKeys(defaultProjectID)
	if keys[0].RateLimitRPM == nil || *keys[0].RateLimitRPM != 0 || keys[0].TokenLimitTPM == nil || *keys[0].TokenLimitTPM != 5 {
		t.Fatalf("explicit zero changed the other minute limit: %+v", keys[0])
	}

	cleared := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+keys[0].ID, map[string]any{
		"rate_limit_rpm": nil,
	}, "")
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear RPM expected 200, got %d: %s", cleared.Code, cleared.Body)
	}
	keys = store.ListProjectKeys(defaultProjectID)
	if keys[0].RateLimitRPM != nil || keys[0].TokenLimitTPM == nil || *keys[0].TokenLimitTPM != 5 {
		t.Fatalf("clearing RPM should preserve TPM: %+v", keys[0])
	}
}

func TestAPIKeyTPMReturnsFailedRequestReservation(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{TokenLimitTPM: int64Pointer(10)})
	adapter := &failFirstRateLimitAdapter{}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
	app := server.Handler()
	payload := map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 2,
		"messages":   []map[string]any{{"role": "user", "content": "a"}},
	}

	failed := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret)
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("first request expected upstream failure, got %d: %s", failed.Code, failed.Body)
	}
	retried := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret)
	if retried.Code != http.StatusOK {
		t.Fatalf("failed request reservation was not returned, got %d: %s", retried.Code, retried.Body)
	}
}

func TestAPIKeyTPMSettlesFailedRequestToReportedUsage(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{TokenLimitTPM: int64Pointer(10)})
	adapter := &usageFailFirstRateLimitAdapter{}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
	app := server.Handler()
	payload := map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "a"}},
	}
	if failed := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); failed.Code != http.StatusBadGateway {
		t.Fatalf("first request expected upstream failure, got %d: %s", failed.Code, failed.Body)
	}
	if limited := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); limited.Code != http.StatusTooManyRequests {
		t.Fatalf("reported failure usage should remain metered, got %d: %s", limited.Code, limited.Body)
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("TPM-rejected retry reached provider: calls=%d", calls)
	}
}

func TestAPIKeyTPMSettlesUsageAcrossFailoverAttempts(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{TokenLimitTPM: int64Pointer(19)})
	secondProvider := store.AddProvider(Provider{Name: "Second Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: secondProvider.ID, ProviderModel: "mock-chat", Status: StatusActive})
	adapter := &usageFailFirstRateLimitAdapter{}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
	app := server.Handler()
	payload := map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "a"}},
	}
	if succeeded := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); succeeded.Code != http.StatusOK {
		t.Fatalf("failover request expected success, got %d: %s", succeeded.Code, succeeded.Body)
	}
	if limited := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); limited.Code != http.StatusTooManyRequests {
		t.Fatalf("usage from the failed attempt should remain metered, got %d: %s", limited.Code, limited.Body)
	}
	if calls := adapter.calls.Load(); calls != 2 {
		t.Fatalf("TPM-rejected request reached provider: calls=%d", calls)
	}
	records := store.ListUsageRecords()
	_, expectedUsage, err := (MockAdapter{}).Chat(context.Background(), Provider{}, "mock-chat", ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("failover request usage records=%+v, want exactly one", records)
	}
	var successfulAttempt RouteAttemptLog
	if err := store.db.Where("request_id = ? AND status_code = ? AND invoked = ?", records[0].RequestID, http.StatusOK, true).First(&successfulAttempt).Error; err != nil {
		t.Fatal(err)
	}
	if records[0].ProviderID != successfulAttempt.ProviderID || records[0].TotalTokens != expectedUsage.TotalTokens {
		t.Fatalf("final provider usage attribution was polluted by failed attempts: records=%+v expected_total=%d", records, expectedUsage.TotalTokens)
	}
}

func TestAPIKeyTPMSettlesReservationToActualUsage(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{TokenLimitTPM: int64Pointer(11)})
	adapter := &countingRateLimitAdapter{}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
	app := server.Handler()
	payload := map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "a"}},
	}

	first := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret)
	if first.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d: %s", first.Code, first.Body)
	}
	second := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret)
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body, "api_key_tpm_exceeded") {
		t.Fatalf("actual usage should leave insufficient TPM, got %d: %s", second.Code, second.Body)
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("TPM-rejected request reached provider: calls=%d", calls)
	}
}

func TestAPIKeyMinuteLimitUsesStrictestApplicablePolicy(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{RateLimitRPM: int64Pointer(5)})
	project := store.ListProjects()[0]
	store.CreateResource("quota-policies", AdminResource{
		Name:   "Project RPM",
		Status: StatusActive,
		Fields: map[string]any{"scope": "project", "scope_id": project.ID, "rate_limit_rpm": 1},
	})
	app := New(store).Handler()
	payload := map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "policy"}},
	}
	if first := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); first.Code != http.StatusOK {
		t.Fatalf("first policy request expected 200, got %d: %s", first.Code, first.Body)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"policy"}]}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+secret)
	resp := httptest.NewRecorder()
	app.ServeHTTP(resp, req)
	if resp.Code != http.StatusTooManyRequests || resp.Header().Get("X-RateLimit-Limit-Requests") != "1" {
		t.Fatalf("project policy should narrow key RPM to 1, got %d headers=%+v body=%s", resp.Code, resp.Header(), resp.Body.String())
	}
}

func TestQuotaPolicyMinuteLimitsRejectInvalidValues(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Invalid Quota Policy", Status: StatusActive})
	policy := store.CreateResource("quota-policies", AdminResource{
		Name: "Existing Policy", Status: StatusActive,
		Fields: map[string]any{"scope": "project", "scope_id": project.ID, "rate_limit_rpm": 1},
	})
	app := New(store).Handler()
	for _, test := range []struct {
		name    string
		method  string
		path    string
		payload map[string]any
	}{
		{
			name: "negative create", method: http.MethodPost, path: "/api/admin/resources/quota-policies",
			payload: map[string]any{"name": "Negative RPM", "fields": map[string]any{"rate_limit_rpm": -1}},
		},
		{
			name: "overflow create", method: http.MethodPost, path: "/api/admin/resources/quota-policies",
			payload: map[string]any{"name": "Overflow TPM", "fields": map[string]any{"token_limit_tpm": json.Number("9223372036854775808")}},
		},
		{
			name: "negative update", method: http.MethodPatch, path: "/api/admin/resources/quota-policies/" + policy.ID,
			payload: map[string]any{"fields": map[string]any{"token_limit_tpm": -1}},
		},
		{
			name: "negative project request", method: http.MethodPost, path: "/api/admin/projects/" + project.ID + "/quota-increase",
			payload: map[string]any{"fields": map[string]any{"rate_limit_rpm": -1}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := doJSON(t, app, test.method, test.path, test.payload, "")
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_quota_policy_rate_limit") {
				t.Fatalf("invalid quota policy returned %d: %s", response.Code, response.Body)
			}
		})
	}
}

func TestStartCallFailsClosedWhenQuotaPolicyReadFails(t *testing.T) {
	store, _ := newRateLimitedGateway(t, APIKey{})
	project := store.ListProjects()[0]
	key := store.ListAPIKeys()[0]
	callbackName := "test:fail-quota-policy-read"
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "admin_resources" {
			_ = tx.AddError(errors.New("quota policy read failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	if _, err := store.StartCall(context.Background(), project, key, "gpt-4.1-mini", 0); err == nil || !strings.Contains(err.Error(), "quota policy read failed") {
		t.Fatalf("StartCall must fail closed on quota policy read error, got %v", err)
	}
}

func TestProjectMinuteLimitsApplyIndependentlyPerKeyUnderConcurrency(t *testing.T) {
	for _, test := range []struct {
		name             string
		policyField      string
		tokenReservation int64
		limitHeader      string
		remainingHeader  string
	}{
		{name: "rpm", policyField: "rate_limit_rpm", limitHeader: "X-RateLimit-Limit-Requests", remainingHeader: "X-RateLimit-Remaining-Requests"},
		{name: "tpm", policyField: "token_limit_tpm", tokenReservation: 1, limitHeader: "X-RateLimit-Limit-Tokens", remainingHeader: "X-RateLimit-Remaining-Tokens"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newRateLimitedGateway(t, APIKey{})
			project := store.ListProjects()[0]
			keys := store.ListAPIKeys()
			for range 4 {
				key, _, err := store.CreateAPIKey(project.ID, APIKey{
					Name:    "concurrent-project-limit",
					Allowed: []string{"gpt-4.1-mini"},
					Status:  StatusActive,
				}, "thk_concurrent_"+NewID("test"))
				if err != nil {
					t.Fatal(err)
				}
				keys = append(keys, key)
			}
			store.CreateResource("quota-policies", AdminResource{
				Name:   "Concurrent project " + test.name,
				Status: StatusActive,
				Fields: map[string]any{
					"scope":          "project",
					"scope_id":       project.ID,
					test.policyField: int64(1),
				},
			})
			type admissionResult struct {
				call CallContext
				err  error
			}
			start := make(chan struct{})
			results := make(chan admissionResult, len(keys))
			for _, key := range keys {
				go func() {
					<-start
					call, err := store.StartCall(context.Background(), project, key, "gpt-4.1-mini", test.tokenReservation)
					results <- admissionResult{call: call, err: err}
				}()
			}
			close(start)
			for range keys {
				result := <-results
				if result.err != nil {
					t.Fatalf("all five keys should receive their own project %s allowance: %v", test.name, result.err)
				}
				if result.call.RateLimitHeaders[test.limitHeader] != "1" || result.call.RateLimitHeaders[test.remainingHeader] != "0" {
					t.Fatalf("unexpected %s headers: %+v", test.name, result.call.RateLimitHeaders)
				}
				store.FinishCall(result.call, RouteSelection{}, Usage{TotalTokens: test.tokenReservation}, http.StatusOK, "", "127.0.0.1", "concurrency-test")
			}
		})
	}
}

func TestAPIKeyRPMIsSharedAcrossCompatibleEndpoints(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{RateLimitRPM: int64Pointer(2)})
	app := New(store).Handler()
	responses := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model":             "gpt-4.1-mini",
		"input":             "one",
		"max_output_tokens": 1,
	}, secret)
	if responses.Code != http.StatusOK {
		t.Fatalf("responses request expected 200, got %d: %s", responses.Code, responses.Body)
	}
	embeddings := doJSON(t, app, http.MethodPost, "/v1/embeddings", map[string]any{
		"model": "gpt-4.1-mini",
		"input": "two",
	}, secret)
	if embeddings.Code != http.StatusOK {
		t.Fatalf("embeddings request expected 200, got %d: %s", embeddings.Code, embeddings.Body)
	}
	chat := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "three"}},
	}, secret)
	if chat.Code != http.StatusTooManyRequests || !strings.Contains(chat.Body, "api_key_rpm_exceeded") {
		t.Fatalf("chat should share the endpoint RPM bucket, got %d: %s", chat.Code, chat.Body)
	}
}

func TestAPIKeyMinuteLimitUnsetZeroAndDisabledSemantics(t *testing.T) {
	for _, test := range []struct {
		name string
		rpm  *int64
	}{
		{name: "unset inherits", rpm: nil},
		{name: "zero adds no key cap", rpm: int64Pointer(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, secret := newRateLimitedGateway(t, APIKey{RateLimitRPM: test.rpm})
			project := store.ListProjects()[0]
			store.CreateResource("quota-policies", AdminResource{
				Name:   "Inherited RPM",
				Status: StatusActive,
				Fields: map[string]any{"scope": "project", "scope_id": project.ID, "rate_limit_rpm": 1},
			})
			app := New(store).Handler()
			payload := map[string]any{
				"model":    "gpt-4.1-mini",
				"messages": []map[string]any{{"role": "user", "content": "inherit"}},
			}
			if first := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); first.Code != http.StatusOK {
				t.Fatalf("first inherited request expected 200, got %d: %s", first.Code, first.Body)
			}
			if second := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); second.Code != http.StatusTooManyRequests {
				t.Fatalf("upper policy should still apply, got %d: %s", second.Code, second.Body)
			}
		})
	}

	t.Run("disabled rejects without consuming quota", func(t *testing.T) {
		store, secret := newRateLimitedGateway(t, APIKey{RateLimitRPM: int64Pointer(1)})
		key := store.ListAPIKeys()[0]
		if _, err := store.UpdateAPIKey(key.ID, APIKey{Status: StatusDisabled}); err != nil {
			t.Fatal(err)
		}
		app := New(store).Handler()
		payload := map[string]any{
			"model":    "gpt-4.1-mini",
			"messages": []map[string]any{{"role": "user", "content": "disabled"}},
		}
		if disabled := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); disabled.Code != http.StatusForbidden {
			t.Fatalf("disabled key expected 403, got %d: %s", disabled.Code, disabled.Body)
		}
		if _, err := store.UpdateAPIKey(key.ID, APIKey{Status: StatusActive}); err != nil {
			t.Fatal(err)
		}
		if allowed := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); allowed.Code != http.StatusOK {
			t.Fatalf("authentication rejection consumed RPM, got %d: %s", allowed.Code, allowed.Body)
		}
		if limited := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); limited.Code != http.StatusTooManyRequests {
			t.Fatalf("second authenticated request should consume RPM, got %d: %s", limited.Code, limited.Body)
		}
	})
}

func TestAPIKeyMinuteLimitIgnoresLegacyNestedMinuteFields(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{Limits: QuotaLimits{RateLimitRPM: 1}})
	app := New(store).Handler()
	payload := map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "nested"}},
	}
	for i := range 2 {
		if response := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); response.Code != http.StatusOK {
			t.Fatalf("legacy nested limit affected request %d: %d %s", i+1, response.Code, response.Body)
		}
	}
}

func TestAPIKeyMinuteLimitResetsAtWindowBoundary(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{RateLimitRPM: int64Pointer(1)})
	key := store.ListAPIKeys()[0]
	now, err := store.databaseNow(store.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&QuotaBucket{
		KeyID: key.ID, Scope: "minute", Bucket: minuteBucket(now.Add(-time.Minute)),
		QuotaCounter: QuotaCounter{Requests: 100},
	}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	payload := map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "new window"}},
	}
	if current := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); current.Code != http.StatusOK {
		t.Fatalf("previous window should not block current request, got %d: %s", current.Code, current.Body)
	}
	if limited := doJSON(t, app, http.MethodPost, "/v1/chat/completions", payload, secret); limited.Code != http.StatusTooManyRequests {
		t.Fatalf("current window should enforce RPM, got %d: %s", limited.Code, limited.Body)
	}
}

func TestAPIKeyMinuteBucketsPruneExpiredHistory(t *testing.T) {
	store, _ := newRateLimitedGateway(t, APIKey{RateLimitRPM: int64Pointer(10)})
	project := store.ListProjects()[0]
	key := store.ListAPIKeys()[0]
	now, err := store.databaseNow(store.db)
	if err != nil {
		t.Fatal(err)
	}
	oldBucket := minuteBucket(now.Add(-apiKeyMinuteBucketRetention - time.Minute))
	currentBucket := minuteBucket(now)
	for _, bucket := range []QuotaBucket{
		{KeyID: key.ID, Scope: "minute", Bucket: oldBucket, QuotaCounter: QuotaCounter{Requests: 99}},
		{KeyID: key.ID, Scope: "minute", Bucket: currentBucket},
	} {
		if err := store.db.Create(&bucket).Error; err != nil {
			t.Fatal(err)
		}
	}
	call, err := store.StartCall(context.Background(), project, key, "gpt-4.1-mini", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "retention-test")
	var oldCount, currentCount int64
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ? AND scope = ? AND bucket = ?", key.ID, "minute", oldBucket).Count(&oldCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ? AND scope = ? AND bucket = ?", key.ID, "minute", currentBucket).Count(&currentCount).Error; err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 || currentCount != 1 {
		t.Fatalf("minute bucket retention old=%d current=%d, want 0 and 1", oldCount, currentCount)
	}
}

func TestAPIKeyTPMReturnsReservationAfterStreamingInterruption(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{TokenLimitTPM: int64Pointer(10)})
	release := make(chan struct{})
	adapter := &blockingTPMAdapter{started: make(chan struct{}), release: release}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat, AdapterCapabilityChatStream)
	app := server.Handler()
	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1-mini","stream":true,"max_tokens":2,"messages":[{"role":"user","content":"a"}]}`)).WithContext(requestContext)
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer "+secret)
	done := make(chan struct{})
	go func() {
		app.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	select {
	case <-adapter.started:
	case <-t.Context().Done():
		t.Fatal("streaming request did not reach provider")
	}
	cancel()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("interrupted streaming request did not finish")
	}
	server.adapterRegistry.Register(ProviderMock, MockAdapter{}, AdapterCapabilityChat, AdapterCapabilityChatStream)
	retried := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 2,
		"messages":   []map[string]any{{"role": "user", "content": "a"}},
	}, secret)
	if retried.Code != http.StatusOK {
		t.Fatalf("stream interruption reservation was not returned, got %d: %s", retried.Code, retried.Body)
	}
}

func TestAPIKeyTPMReturnsCodexReservationWhenStreamHasNoBody(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Codex Empty Stream", Status: StatusActive})
	tokenLimit := int64(10)
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "codex-empty-stream", Allowed: []string{"gpt-codex-empty"},
		TokenLimitTPM: &tokenLimit, Status: StatusActive,
	}, "thk_codex_empty_stream")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		Name: "Codex Empty Stream", Type: ProviderOpenAICodex, BaseURL: openAICodexBaseURL,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Codex Empty Account", ResourceType: ProviderResourceOpenAISubscription,
		Status: StatusActive, Healthy: true, Options: codexCapabilityOptionsForTest("gpt-codex-empty"),
		Credentials: &ProviderResourceCredentials{AccessToken: "access_empty", AccountID: "account_empty"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-codex-empty", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName: "gpt-codex-empty", ProviderID: provider.ID, ProviderResourceID: resource.ID,
		ProviderModel: "gpt-codex-empty", Status: StatusActive,
	})
	server := New(store)
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("")), Request: req,
		}, nil
	})}
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "gpt-codex-empty", "input": "a", "stream": true, "max_output_tokens": 2,
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("empty Codex stream returned %d: %s", response.Code, response.Body)
	}
	var bucket QuotaBucket
	if err := store.db.First(&bucket, "key_id = ? AND scope = ?", key.ID, "minute").Error; err != nil {
		t.Fatal(err)
	}
	if bucket.TotalTokens != 0 {
		t.Fatalf("empty Codex stream kept %d reserved tokens, want 0", bucket.TotalTokens)
	}
}

func TestAPIKeyTPMKeepsReservationAfterPartialStreamInterruption(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{TokenLimitTPM: int64Pointer(10)})
	adapter := &partialStreamRateLimitAdapter{wrote: make(chan struct{})}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat, AdapterCapabilityChatStream)
	app := server.Handler()
	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1-mini","stream":true,"max_tokens":2,"messages":[{"role":"user","content":"a"}]}`)).WithContext(requestContext)
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer "+secret)
	done := make(chan struct{})
	go func() {
		app.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	select {
	case <-adapter.wrote:
	case <-t.Context().Done():
		t.Fatal("streaming request did not write partial output")
	}
	cancel()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("partially streamed request did not finish")
	}
	server.adapterRegistry.Register(ProviderMock, MockAdapter{}, AdapterCapabilityChat, AdapterCapabilityChatStream)
	retried := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":      "gpt-4.1-mini",
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "b"}},
	}, secret)
	if retried.Code != http.StatusTooManyRequests || !strings.Contains(retried.Body, "api_key_tpm_exceeded") {
		t.Fatalf("partial stream should keep its TPM reservation, got %d: %s", retried.Code, retried.Body)
	}
}

func TestAPIKeyTPMSaturatesOversizedTokenReservation(t *testing.T) {
	store, secret := newRateLimitedGateway(t, APIKey{TokenLimitTPM: int64Pointer(100)})
	adapter := &countingRateLimitAdapter{}
	server := New(store)
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":                 "gpt-4.1-mini",
		"max_completion_tokens": int64(math.MaxInt64),
		"messages":              []map[string]any{{"role": "user", "content": "overflow"}},
	}, secret)
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body, "api_key_tpm_exceeded") {
		t.Fatalf("oversized reservation should be rejected, got %d: %s", response.Code, response.Body)
	}
	if calls := adapter.calls.Load(); calls != 0 {
		t.Fatalf("overflowing reservation reached provider: calls=%d", calls)
	}
}

func TestTokenReservationUsesExplicitMaximumOrSafeDefault(t *testing.T) {
	if got := requestTokenReservation(ChatCompletionRequest{}); got != defaultOutputTokenReservation {
		t.Fatalf("default reservation = %d, want %d", got, defaultOutputTokenReservation)
	}
	if got := requestTokenReservation(ChatCompletionRequest{MaxTokens: 17}); got != 17 {
		t.Fatalf("explicit reservation = %d, want 17", got)
	}
	var compatible ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{"max_completion_tokens":23}`), &compatible); err != nil {
		t.Fatal(err)
	}
	if got := requestTokenReservation(compatible); got != 23 {
		t.Fatalf("compatible maximum reservation = %d, want 23", got)
	}
}

func TestMeteredTokensDoesNotDoubleCountUsageDetails(t *testing.T) {
	usage := Usage{TotalTokens: 9, PromptTokens: 5, CompletionTokens: 4, CachedInputTokens: 3, ReasoningOutputTokens: 2}
	if got := meteredTokens(usage); got != 9 {
		t.Fatalf("authoritative total = %d, want 9", got)
	}
	usage.TotalTokens = 0
	if got := meteredTokens(usage); got != 9 {
		t.Fatalf("prompt plus completion = %d, want 9", got)
	}
}

func newRateLimitedGateway(t *testing.T, limits APIKey) (*GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Rate Limited", Status: StatusActive})
	limits.Name = "rate-limited"
	limits.Allowed = []string{"gpt-4.1-mini"}
	limits.Status = StatusActive
	_, secret, err := store.CreateAPIKey(project.ID, limits, "thk_rate_limited_"+NewID("test"))
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: provider.ID, ProviderModel: "mock-chat", Status: StatusActive})
	return store, secret
}

func int64Pointer(value int64) *int64 {
	return &value
}
