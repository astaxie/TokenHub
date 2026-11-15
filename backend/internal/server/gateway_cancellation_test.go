package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestGatewayCancellationChatStream(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	app, secret := newMetricsFailoverGateway(t, upstream.URL, upstream.URL)
	emitter := &recordingTraceEmitter{}
	app.traceEmitter = emitter
	store := app.store.(*GormStore)
	store.FinishProviderResourceAttempt(context.Background(), "rsrc_metrics_0", "", AttemptFailed, Usage{})
	done := make(chan struct{})
	handler := app.Handler()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		handler.ServeHTTP(w, r)
	}))
	defer func() {
		close(release)
		gateway.Close()
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"metrics-failover-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	frame, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || !strings.Contains(frame, "partial") || response.StatusCode != 200 {
		t.Fatalf("first frame: status=%d frame=%q error=%v", response.StatusCode, frame, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not finish after caller cancellation")
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != 499 || logs[0].ErrorCode != "request_cancelled" {
		t.Fatalf("request outcome = %+v, want one 499/request_cancelled", logs)
	}
	detail, err := store.GetRequestDetail(logs[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := detail["attempts"].([]RouteAttemptLog)
	if len(attempts) != 1 || attempts[0].StatusCode != 499 || attempts[0].ErrorCode != "request_cancelled" || !attempts[0].Invoked {
		t.Fatalf("attempt outcome = %+v", attempts)
	}
	if completed := emitter.take(); len(completed) != 1 || hits.Load() != 1 {
		t.Fatalf("completions=%d upstream calls=%d, want one each", len(completed), hits.Load())
	}
	if resource := findResource(t, store, "rsrc_metrics_0"); resource.FailureCount != 1 || !resource.Healthy {
		t.Fatalf("disconnect changed breaker state: %+v", resource)
	}
	metrics := scrapeMetrics(t, handler)
	for _, fragments := range [][]string{
		{"tokenhub_gateway_requests_total", `status_code="499"`, `error_code="request_cancelled"`},
		{"tokenhub_gateway_route_attempts_total", `status_code="499"`, `error_code="request_cancelled"`},
		{"tokenhub_gateway_stream_interruptions_total", `error_code="request_cancelled"`},
	} {
		if line := findMetricLine(metrics, fragments...); line == "" || metricLineValue(t, line) != 1 {
			t.Fatalf("missing single cancellation metric %v:\n%s", fragments, metrics)
		}
	}
}

func TestGatewayCancellationClassification(t *testing.T) {
	live := context.Background()
	canceled, cancel := context.WithCancel(live)
	cancel()
	child, cancelChild := context.WithCancel(live)
	cancelChild()
	internal, cancelInternal := context.WithCancelCause(live)
	cancelInternal(errors.New("internal shutdown"))
	leaseLost, cancelLease := context.WithCancelCause(live)
	cancelLease(ErrCoordinationLeaseLost)
	for _, tc := range []struct {
		name            string
		caller, attempt context.Context
		err             error
		status          int
		code            string
	}{
		{"direct", canceled, canceled, context.Canceled, 499, "request_cancelled"},
		{"wrapped", canceled, canceled, fmt.Errorf("read: %w", context.Canceled), 499, "request_cancelled"},
		{"stream write", canceled, canceled, classifyStreamError(canceled, &net.OpError{Op: "write", Err: syscall.EPIPE}, true), 499, "request_cancelled"},
		{"live caller", live, live, context.Canceled, 500, "internal_error"},
		{"child only", live, child, context.Canceled, 500, "internal_error"},
		{"internal caller cause", internal, internal, context.Canceled, 500, "internal_error"},
		{"internal lease race", canceled, internal, context.Canceled, 500, "internal_error"},
		{"coordination cause", canceled, leaseLost, context.Canceled, 503, "coordination_lease_lost"},
		{"coordination error", canceled, canceled, ErrCoordinationLeaseLost, 503, "coordination_lease_lost"},
		{"deadline", canceled, canceled, context.DeadlineExceeded, 500, "internal_error"},
		{"upstream timeout", canceled, canceled, NewHTTPError(504, "provider_upstream_timeout", "timeout"), 504, "provider_upstream_timeout"},
		{"typed upstream race", canceled, canceled, classifyStreamError(canceled, newProviderHTTPError(503, nil, []byte(`{"error":{"message":"unavailable"}}`)), true), 503, "provider_upstream_unavailable"},
		{"policy race", canceled, canceled, &ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorPolicy}, 500, "internal_error"},
		{"unrelated error", canceled, canceled, errors.New("write failed"), 500, "internal_error"},
		{"success then cancellation", canceled, canceled, nil, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyCallerCancellation(tc.caller, tc.attempt, tc.err)
			status, code := statusAndCode(err)
			if status != tc.status || code != tc.code {
				t.Fatalf("outcome = %d/%s, want %d/%s", status, code, tc.status, tc.code)
			}
			if status == 499 && AsHTTPError(err).Message != "Request canceled by the client" {
				t.Fatalf("cancellation message contains original error detail: %v", err)
			}
			if status == 499 && (shouldFailoverRoutedError(err, false) || providerAttemptOutcome(err) != AttemptNeutral) {
				t.Fatal("confirmed caller cancellation must be neutral and non-retryable")
			}
		})
	}

	for _, stream := range []bool{false, true} {
		for _, wrote := range []bool{false, true} {
			if !stream && wrote {
				continue
			}
			t.Run(fmt.Sprintf("execution/stream=%t/wrote=%t", stream, wrote), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				calls := 0
				_, _, _, attempts, err := executeRoutedWithStore(ctx, NewMemoryStore(), RoutedCall{
					Call: CallContext{Stream: stream}, Routes: []RouteSelection{{}, {}},
				}, true, func(context.Context, RouteSelection, bool, int) (struct{}, Usage, error) {
					calls++
					cancel()
					var err error = context.Canceled
					if stream {
						err = classifyStreamError(ctx, err, wrote)
					}
					return struct{}{}, Usage{}, err
				})
				if status, code := statusAndCode(err); status != 499 || code != "request_cancelled" || calls != 1 || len(attempts) != 1 || attempts[0].Status != 499 {
					t.Fatalf("execution outcome=%d/%s calls=%d attempts=%+v", status, code, calls, attempts)
				}
			})
		}
	}

	t.Run("effort rejection racing cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		_, _, _, attempts, err := executeRoutedWithStore(ctx, NewMemoryStore(), RoutedCall{Routes: []RouteSelection{{}, {}}}, true,
			func(context.Context, RouteSelection, bool, int) (struct{}, Usage, error) {
				calls++
				cancel()
				return struct{}{}, Usage{}, newProviderHTTPError(400, nil, []byte(`{"error":{"message":"reasoning_effort is unsupported"}}`))
			})
		if status, code := statusAndCode(err); status != 400 || code != "provider_invalid_request" || calls != 1 || len(attempts) != 1 {
			t.Fatalf("effort failure outcome=%d/%s calls=%d attempts=%+v", status, code, calls, attempts)
		}
	})

	t.Run("completion preserves attempt classification", func(t *testing.T) {
		store := NewMemoryStore()
		app := New(store)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		routed := RoutedCall{Call: CallContext{RequestID: "internal-cancellation", StartedAt: time.Now()}, Routes: []RouteSelection{{}, {}}}
		_, _, usage, attempts, err := executeRoutedWithStore(ctx, store, routed, true, func(context.Context, RouteSelection, bool, int) (struct{}, Usage, error) {
			return struct{}{}, Usage{}, context.Canceled
		})
		cancel()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		app.finishFailedRoutedCall(request, routed, attempts, usage, err, nil)
		logs := store.ListRequestLogs()
		if len(attempts) != 1 || attempts[0].Status != 500 || len(logs) != 1 || logs[0].StatusCode != 500 || logs[0].ErrorCode != "internal_error" {
			t.Fatalf("internal cancellation reclassified after completion: attempts=%+v logs=%+v", attempts, logs)
		}
	})

	t.Run("capacity and early completion", func(t *testing.T) {
		store := NewMemoryStore()
		_, _, _, attempts, err := executeRoutedWithStore(canceled, store, RoutedCall{
			Routes: []RouteSelection{{Route: ModelRoute{ProviderResourceID: "resource"}}, {}},
		}, true, func(context.Context, RouteSelection, bool, int) (struct{}, Usage, error) {
			t.Fatal("canceled capacity acquisition invoked a provider")
			return struct{}{}, Usage{}, nil
		})
		if status, code := statusAndCode(err); status != 499 || code != "request_cancelled" || len(attempts) != 1 || attempts[0].Invoked || attempts[0].Status != 499 {
			t.Fatalf("capacity outcome=%d/%s attempts=%+v", status, code, attempts)
		}
		app := New(store)
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(canceled)
		app.finishFailedRoutedCall(request, RoutedCall{Call: CallContext{RequestID: "early-failure", StartedAt: time.Now()}}, nil, Usage{}, context.Canceled, nil)
		app.finishRejectedCall(request, time.Now(), Project{}, APIKey{}, "model", false, context.Canceled, nil)
		app.finishFailedRoutedCall(request, RoutedCall{Call: CallContext{RequestID: "post-attempt-failure", StartedAt: time.Now()}}, []RouteAttempt{{Invoked: true, Status: 200}}, Usage{}, context.Canceled, nil)
		logs := store.ListRequestLogs()
		if len(logs) != 3 {
			t.Fatalf("completions outside failed attempts=%d, want 3", len(logs))
		}
		for _, log := range logs {
			if log.StatusCode != 499 || log.ErrorCode != "request_cancelled" {
				t.Fatalf("early outcome=%+v", log)
			}
		}
	})
}

func TestGatewayCancellationAdmissionContext(t *testing.T) {
	for _, callerCanceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("caller_canceled=%t", callerCanceled), func(t *testing.T) {
			app, secret := newMetricsFailoverGateway(t, "http://localhost:1", "http://localhost:1")
			store := app.store.(*GormStore)
			project, key, err := store.ValidateAPIKey(secret, "")
			if err != nil {
				t.Fatal(err)
			}
			key, err = store.UpdateAPIKey(key.ID, APIKey{Limits: QuotaLimits{MaxConcurrency: 1}})
			if err != nil {
				t.Fatal(err)
			}
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(caller)
			call, err := app.admitRoutedCall(httptest.NewRecorder(), request, project, key, "metrics-failover-model", false, 0)
			if err != nil {
				t.Fatal(err)
			}
			if call.requestContext == nil {
				t.Fatal("admission did not acquire a concurrency lease")
			}
			if callerCanceled {
				cancel()
			} else if err := store.stopInFlightLeaseHeartbeat(call.RequestID); err != nil {
				t.Fatal(err)
			}
			if request.Context().Err() != context.Canceled {
				t.Fatal("admission context was not canceled")
			}
			app.finishFailedRoutedCall(request, RoutedCall{Call: call}, nil, Usage{}, context.Canceled, nil)
			wantStatus, wantCode := 500, "internal_error"
			if callerCanceled {
				wantStatus, wantCode = 499, "request_cancelled"
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].StatusCode != wantStatus || logs[0].ErrorCode != wantCode {
				t.Fatalf("admission cancellation lost caller provenance: %+v", logs)
			}
		})
	}
}

// Cancel exactly when the executor asks to retry the same resource, after its
// initial effort rejection. The real store still performs the capacity check.
type retryCancellationStore struct {
	Store
	cancel context.CancelFunc
}

func (s retryCancellationStore) CheckProviderResourceRetryCapacity(ctx context.Context, resourceID, leaseID string) error {
	s.cancel()
	return s.Store.CheckProviderResourceRetryCapacity(ctx, resourceID, leaseID)
}

func TestGatewayCancellationRetryCapacity(t *testing.T) {
	app, _ := newMetricsFailoverGateway(t, "http://localhost:1", "http://localhost:1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := app.store.(*GormStore)
	calls := 0
	_, _, _, attempts, err := executeRoutedWithStore(ctx, retryCancellationStore{Store: store, cancel: cancel}, RoutedCall{
		Routes: []RouteSelection{{Route: ModelRoute{ProviderResourceID: "rsrc_metrics_0"}}, {}},
	}, true, func(context.Context, RouteSelection, bool, int) (struct{}, Usage, error) {
		calls++
		return struct{}{}, Usage{}, newProviderHTTPError(400, nil, []byte(`{"error":{"message":"reasoning_effort is unsupported"}}`))
	})
	if status, code := statusAndCode(err); status != 499 || code != "request_cancelled" || calls != 1 || len(attempts) != 2 || attempts[1].Invoked || attempts[1].Status != 499 || attempts[1].ErrorCode != "request_cancelled" {
		t.Fatalf("retry capacity outcome=%d/%s calls=%d attempts=%+v", status, code, calls, attempts)
	}
	var leases int64
	if err := store.db.Model(&InFlightLease{}).Count(&leases).Error; err != nil || leases != 0 {
		t.Fatalf("remaining leases=%d error=%v", leases, err)
	}
}

type cancellationUsageAdapter struct {
	MockAdapter
	cancel context.CancelFunc
	usage  Usage
	err    error
	calls  int
}

func (a *cancellationUsageAdapter) Chat(context.Context, Provider, string, ChatCompletionRequest) (any, Usage, error) {
	a.calls++
	a.cancel()
	return map[string]any{"id": "observed-usage"}, a.usage, a.err
}

func TestGatewayCancellationPreservesObservedUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		usage  Usage
		err    error
		status int
	}{
		{"observed", Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5, CostUSD: 0.25}, context.Canceled, 499},
		{"unobserved", Usage{}, context.Canceled, 499},
		{"completed", Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5, CostUSD: 0.25}, nil, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, secret := newMetricsFailoverGateway(t, "http://localhost:1", "http://localhost:1")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			adapter := &cancellationUsageAdapter{cancel: cancel, usage: tc.usage, err: tc.err}
			app.adapterRegistry.Register(ProviderOpenAICompatible, adapter, AdapterCapabilityChat)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"metrics-failover-model","messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer "+secret)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != tc.status || adapter.calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, adapter.calls, response.Body)
			}
			store := app.store.(*GormStore)
			logs, records := store.ListRequestLogs(), store.ListUsageRecords()
			wantRecords := 1
			if tc.usage.TotalTokens == 0 {
				wantRecords = 0
			}
			if len(logs) != 1 || logs[0].StatusCode != tc.status || len(records) != wantRecords {
				t.Fatalf("logs=%+v usage=%+v", logs, records)
			}
			if len(records) > 0 && (records[0].InputTokens != tc.usage.PromptTokens || records[0].OutputTokens != tc.usage.CompletionTokens || records[0].TotalTokens != tc.usage.TotalTokens || records[0].CostUSD != tc.usage.CostUSD) {
				t.Fatalf("persisted usage=%+v, want %+v", records[0], tc.usage)
			}
			detail, err := store.GetRequestDetail(logs[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			attempts := detail["attempts"].([]RouteAttemptLog)
			if len(attempts) != 1 || attempts[0].InputTokens != tc.usage.PromptTokens || attempts[0].OutputTokens != tc.usage.CompletionTokens || attempts[0].TotalTokens != tc.usage.TotalTokens || attempts[0].CostUSD != tc.usage.CostUSD || attempts[0].StatusCode != tc.status {
				t.Fatalf("attempt usage=%+v", attempts)
			}
			var leases int64
			if err := store.db.Model(&InFlightLease{}).Count(&leases).Error; err != nil || leases != 0 {
				t.Fatalf("remaining leases=%d error=%v", leases, err)
			}
			var quota QuotaBucket
			if err := store.db.Where("key_id = ? AND scope = ?", logs[0].APIKeyID, "day").First(&quota).Error; err != nil {
				t.Fatal(err)
			}
			if quota.Requests != 1 || quota.TotalTokens != tc.usage.TotalTokens || quota.CostUSD != tc.usage.CostUSD {
				t.Fatalf("quota=%+v", quota)
			}
			metrics := scrapeMetrics(t, app.Handler())
			for _, check := range []struct {
				metric, label string
				want          float64
			}{
				{"tokenhub_gateway_tokens_total", `kind="prompt"`, float64(tc.usage.PromptTokens)},
				{"tokenhub_gateway_tokens_total", `kind="completion"`, float64(tc.usage.CompletionTokens)},
				{"tokenhub_gateway_cost_usd_total", "", tc.usage.CostUSD},
			} {
				line := findMetricLine(metrics, check.metric, check.label)
				if line == "" && check.want == 0 {
					continue
				}
				if line == "" || metricLineValue(t, line) != check.want {
					t.Fatalf("metric %s %s != %v:\n%s", check.metric, check.label, check.want, metrics)
				}
			}
		})
	}
}
