package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type recordingTraceEmitter struct {
	mu          sync.Mutex
	completions []GatewayCallCompletion
}

func (e *recordingTraceEmitter) EmitGatewayCall(completion GatewayCallCompletion) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.completions = append(e.completions, completion)
}

func (e *recordingTraceEmitter) Shutdown(context.Context) error { return nil }

func (e *recordingTraceEmitter) take() []GatewayCallCompletion {
	e.mu.Lock()
	defer e.mu.Unlock()
	taken := e.completions
	e.completions = nil
	return taken
}

func newTracedTestServer(t *testing.T) (http.Handler, *recordingTraceEmitter) {
	t.Helper()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store)
	emitter := &recordingTraceEmitter{}
	app.traceEmitter = emitter
	return app.Handler(), emitter
}

func TestGatewayCompletionRunsTraceExportHooks(t *testing.T) {
	store := NewMemoryStore()
	app := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-trace",
		HookID:   "capture",
		Stage:    pluginmeta.StageTraceExport,
		Priority: 2000,
		Reads:    []pluginmeta.GatewayDataClass{pluginmeta.DataAudit, pluginmeta.DataUsage},
	}
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register trace hook: %v", err)
	}
	var captured pluginmeta.GatewayHookInput
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		captured = input
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register trace handler: %v", err)
	}

	app.finishCall(GatewayCallCompletion{
		Kind: CompletionKindRejected,
		Call: CallContext{
			RequestID: "req_trace_hook",
			Project:   Project{ID: "prj_trace"},
			Key:       APIKey{ID: "key_trace"},
			Model:     Model{Name: "gpt-trace"},
		},
		Usage:      Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		StatusCode: http.StatusForbidden,
		ErrorCode:  "blocked",
	})

	if captured.RequestID != "req_trace_hook" {
		t.Fatalf("captured request id = %q, want req_trace_hook", captured.RequestID)
	}
	if captured.Envelope.Model != "gpt-trace" || captured.Envelope.Operation != string(CompletionKindRejected) {
		t.Fatalf("captured envelope = %+v", captured.Envelope)
	}
	if _, ok := captured.Data[pluginmeta.DataAudit]; !ok {
		t.Fatalf("captured data has no audit payload: %+v", captured.Data)
	}
	var usage Usage
	if err := json.Unmarshal(captured.Data[pluginmeta.DataUsage], &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	if usage.TotalTokens != 7 {
		t.Fatalf("captured usage total tokens = %d, want 7", usage.TotalTokens)
	}
}

func TestGatewayCompletionTraceExportRequiresChainRegistration(t *testing.T) {
	app := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-trace",
		HookID:   "unregistered-capture",
		Stage:    pluginmeta.StageTraceExport,
		Priority: 2000,
		Reads:    []pluginmeta.GatewayDataClass{pluginmeta.DataAudit, pluginmeta.DataUsage},
	}
	called := false
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called = true
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register trace handler: %v", err)
	}

	app.finishCall(GatewayCallCompletion{
		Kind: CompletionKindRejected,
		Call: CallContext{
			RequestID: "req_trace_hook_unregistered",
			Project:   Project{ID: "prj_trace"},
			Key:       APIKey{ID: "key_trace"},
			Model:     Model{Name: "gpt-trace"},
		},
		Usage:      Usage{TotalTokens: 7},
		StatusCode: http.StatusForbidden,
		ErrorCode:  "blocked",
	})

	if called {
		t.Fatal("trace export handler ran without chain registration")
	}
}

func TestGatewayCompletionTraceExportFailureIsObserveOnly(t *testing.T) {
	store := NewMemoryStore()
	app := New(store)
	emitter := &recordingTraceEmitter{}
	app.traceEmitter = emitter
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-trace",
		HookID:        "observe-only-error",
		Stage:         pluginmeta.StageTraceExport,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAudit, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyObserveOnly,
	}
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register trace hook: %v", err)
	}
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, io.ErrUnexpectedEOF
	})); err != nil {
		t.Fatalf("register trace handler: %v", err)
	}

	app.finishCall(GatewayCallCompletion{
		Call: CallContext{
			RequestID: "req_trace_observe_only",
			Project:   Project{ID: "prj_trace"},
			Key:       APIKey{ID: "key_trace"},
			Model:     Model{Name: "gpt-trace"},
		},
		Usage:      Usage{TotalTokens: 7},
		StatusCode: http.StatusOK,
	})

	if completions := emitter.take(); len(completions) != 1 {
		t.Fatalf("trace emitter completions = %d, want 1", len(completions))
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].RequestID != "req_trace_observe_only" || logs[0].StatusCode != http.StatusOK {
		t.Fatalf("trace hook failure affected request settlement: %+v", logs)
	}
}

func TestGatewayCompletionTraceExportOmitsAttemptErrorText(t *testing.T) {
	const upstreamError = "upstream body sentinel must not leak to trace export"
	store := NewMemoryStore()
	app := New(store)
	emitter := &recordingTraceEmitter{}
	app.traceEmitter = emitter
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-trace",
		HookID:        "redacted-attempts",
		Stage:         pluginmeta.StageTraceExport,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAudit, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyObserveOnly,
	}
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register trace hook: %v", err)
	}
	var captured pluginmeta.GatewayHookInput
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		captured = input
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register trace handler: %v", err)
	}

	app.finishCall(GatewayCallCompletion{
		Call: CallContext{
			RequestID: "req_trace_attempt_redaction",
			Project:   Project{ID: "prj_trace"},
			Key:       APIKey{ID: "key_trace"},
			Model:     Model{Name: "gpt-trace"},
		},
		Route: RouteSelection{Provider: Provider{ID: "prv_backup", Type: ProviderMock}, Route: ModelRoute{ID: "route_backup"}, ProviderModel: "backup-model"},
		Usage: Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		Attempts: []RouteAttempt{
			{Selection: RouteSelection{Provider: Provider{ID: "prv_failed", Type: ProviderMock}, Route: ModelRoute{ID: "route_failed"}, ProviderModel: "failed-model"}, Status: http.StatusBadGateway, ErrorCode: "provider_error", Error: upstreamError},
			{Selection: RouteSelection{Provider: Provider{ID: "prv_backup", Type: ProviderMock}, Route: ModelRoute{ID: "route_backup"}, ProviderModel: "backup-model"}, Status: http.StatusOK},
		},
		StatusCode: http.StatusOK,
	})

	if captured.RequestID != "req_trace_attempt_redaction" {
		t.Fatalf("trace export request id = %q", captured.RequestID)
	}
	raw := bytes.Join([][]byte{captured.Data[pluginmeta.DataAudit], captured.Data[pluginmeta.DataUsage]}, nil)
	if bytes.Contains(raw, []byte(upstreamError)) {
		t.Fatalf("trace export included upstream error text: %s", raw)
	}
	completions := emitter.take()
	if len(completions) != 1 {
		t.Fatalf("trace emitter completions = %d, want 1", len(completions))
	}
	for _, attempt := range completions[0].Attempts {
		if attempt.Error != "" {
			t.Fatalf("trace emitter attempt retained upstream error text: %+v", attempt)
		}
	}
}

// TestGatewayCallEmitsExactlyOneCompletion is the load-bearing test for tracing.
// Langfuse v4 turns a re-ingested span into a duplicate observation and inflates
// every metric derived from it, so a path that emits twice is a data-corruption bug
// rather than an extra log line.
func TestGatewayCallEmitsExactlyOneCompletion(t *testing.T) {
	cases := []struct {
		name         string
		path         string
		token        string
		payload      map[string]any
		expectStatus int
		expectKind   CompletionKind
	}{
		{
			name:  "chat completion",
			path:  "/v1/chat/completions",
			token: "thk_demo_local",
			payload: map[string]any{
				"model":    "gpt-4.1-mini",
				"messages": []map[string]any{{"role": "user", "content": "hello"}},
			},
			expectStatus: http.StatusOK,
			expectKind:   CompletionKindRouted,
		},
		{
			name:  "streaming chat completion",
			path:  "/v1/chat/completions",
			token: "thk_demo_local",
			payload: map[string]any{
				"model":    "gpt-4.1-mini",
				"messages": []map[string]any{{"role": "user", "content": "hello"}},
				"stream":   true,
			},
			expectStatus: http.StatusOK,
			expectKind:   CompletionKindRouted,
		},
		{
			name:         "embeddings",
			path:         "/v1/embeddings",
			token:        "thk_demo_local",
			payload:      map[string]any{"model": "text-embedding-3-small", "input": "hello"},
			expectStatus: http.StatusOK,
			expectKind:   CompletionKindRouted,
		},
		{
			name:  "unknown model is rejected before routing",
			path:  "/v1/chat/completions",
			token: "thk_demo_local",
			payload: map[string]any{
				"model":    "model-that-does-not-exist",
				"messages": []map[string]any{{"role": "user", "content": "hello"}},
			},
			expectStatus: http.StatusForbidden,
			expectKind:   CompletionKindRejected,
		},
		{
			name:  "playground",
			path:  "/api/admin/playground/chat",
			token: "",
			payload: map[string]any{
				"project_id": "prj_demo",
				"model":      "gpt-4.1-mini",
				"messages":   []map[string]any{{"role": "user", "content": "hello"}},
			},
			expectStatus: http.StatusOK,
			expectKind:   CompletionKindPlayground,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			app, emitter := newTracedTestServer(t)
			response := doJSON(t, app, http.MethodPost, testCase.path, testCase.payload, testCase.token)
			if response.Code != testCase.expectStatus {
				t.Fatalf("expected %d, got %d: %s", testCase.expectStatus, response.Code, response.Body)
			}
			completions := emitter.take()
			if len(completions) != 1 {
				t.Fatalf("expected exactly one completion, got %d", len(completions))
			}
			if completions[0].Kind != testCase.expectKind {
				t.Fatalf("expected kind %q, got %q", testCase.expectKind, completions[0].Kind)
			}
			if completions[0].Call.RequestID == "" {
				t.Fatal("completion carries no request ID")
			}
			if completions[0].StatusCode != testCase.expectStatus {
				t.Fatalf("expected completion status %d, got %d", testCase.expectStatus, completions[0].StatusCode)
			}
		})
	}
}

// TestGatewayCompletionCarriesPricedUsage guards the reason pricing moved out of the
// store: priceUsage runs inside FinishCall and its result never leaves that call, so
// a completion built from the caller's Usage would report every request as free.
func TestGatewayCompletionCarriesPricedUsage(t *testing.T) {
	app, emitter := newTracedTestServer(t)
	response := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hello tokenhub"}},
	}, "thk_demo_local")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}

	completions := emitter.take()
	if len(completions) != 1 {
		t.Fatalf("expected exactly one completion, got %d", len(completions))
	}
	usage := completions[0].Usage
	if usage.TotalTokens <= 0 {
		t.Fatalf("expected tokens on the completion, got %+v", usage)
	}
	if usage.CostUSD <= 0 {
		t.Fatalf("expected a priced completion, got cost %v for %+v", usage.CostUSD, usage)
	}
}

// TestPriceUsageIsIdempotent is what allows pricing at the emission boundary while
// FinishCall keeps pricing too: the second application must be a no-op.
func TestPriceUsageIsIdempotent(t *testing.T) {
	model := Model{
		Name:                   "gpt-4.1-mini",
		InputPriceUSDPer1M:     3,
		OutputPriceUSDPer1M:    12,
		CacheReadPriceUSDPer1M: 1,
	}
	cases := []struct {
		name  string
		usage Usage
	}{
		{name: "plain", usage: Usage{PromptTokens: 1000, CompletionTokens: 500}},
		{name: "cached", usage: Usage{PromptTokens: 1000, CachedInputTokens: 400, CompletionTokens: 500}},
		{name: "cached above prompt", usage: Usage{PromptTokens: 100, CachedInputTokens: 900, CompletionTokens: 5}},
		{name: "already totalled", usage: Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{name: "negative upstream counts", usage: Usage{PromptTokens: -400, CachedInputTokens: 25, CompletionTokens: -50, TotalTokens: -450}},
		{name: "empty", usage: Usage{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			once := priceUsage(model, testCase.usage)
			twice := priceUsage(model, once)
			// Usage embeds an http.Header, which is not comparable, so the
			// pricing-relevant fields are compared explicitly.
			if once.TotalTokens != twice.TotalTokens ||
				once.CachedInputTokens != twice.CachedInputTokens ||
				once.PromptTokens != twice.PromptTokens ||
				once.CompletionTokens != twice.CompletionTokens ||
				once.CostUSD != twice.CostUSD {
				t.Fatalf("priceUsage is not idempotent: once=%+v twice=%+v", once, twice)
			}
		})
	}
}

// TestRejectedCompletionSpansAdmission checks that a refused request reports the time
// it actually spent being admitted rather than collapsing to a zero-width span.
func TestRejectedCompletionSpansAdmission(t *testing.T) {
	app, emitter := newTracedTestServer(t)
	response := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "model-that-does-not-exist",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	}, "thk_demo_local")
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", response.Code, response.Body)
	}
	completions := emitter.take()
	if len(completions) != 1 {
		t.Fatalf("expected exactly one completion, got %d", len(completions))
	}
	completion := completions[0]
	if completion.Call.StartedAt.IsZero() {
		t.Fatal("rejected completion has no start time, so its span would have no duration")
	}
	if completion.ErrorCode == "" {
		t.Fatal("rejected completion carries no error code")
	}
	if completion.Route.Provider.ID != "" {
		t.Fatalf("rejected completion must not name a provider, got %q", completion.Route.Provider.ID)
	}
}

// partialUsageFailingAdapter fails after the upstream already reported usage, which
// is what a truncated or content-filtered response looks like: the tokens were
// consumed and billed even though the request has to fail over.
type partialUsageFailingAdapter struct{}

func (a partialUsageFailingAdapter) partial() (Usage, error) {
	return Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150},
		NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed after emitting tokens")
}

func (a partialUsageFailingAdapter) Chat(context.Context, Provider, string, ChatCompletionRequest) (any, Usage, error) {
	usage, err := a.partial()
	return nil, usage, err
}

func (a partialUsageFailingAdapter) ChatStream(context.Context, Provider, string, ChatCompletionRequest, io.Writer) (Usage, error) {
	return a.partial()
}

func (a partialUsageFailingAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	usage, err := a.partial()
	return nil, usage, err
}

func (a partialUsageFailingAdapter) Embeddings(context.Context, Provider, string, EmbeddingsRequest) (any, Usage, error) {
	usage, err := a.partial()
	return nil, usage, err
}

// TestFailoverAttemptsCarryTheirOwnUsage covers what per-candidate cost reporting
// needs: the losing candidate's tokens must survive the failover instead of being
// replaced by the winner's.
func TestFailoverAttemptsCarryTheirOwnUsage(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Failover App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "failover-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_attempt_usage")
	if err != nil {
		t.Fatal(err)
	}
	failing := store.AddProvider(Provider{ID: "prv_partial", Name: "Partial", Type: "partial_usage_mock", Status: StatusActive, Healthy: true})
	backup := store.AddProvider(Provider{ID: "prv_backup", Name: "Backup", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{
		Name:                "gpt-4.1-mini",
		Modality:            "chat",
		Status:              StatusActive,
		InputPriceUSDPer1M:  3,
		OutputPriceUSDPer1M: 12,
	})
	store.AddRoute(ModelRoute{ID: "route_partial", ModelName: "gpt-4.1-mini", ProviderID: failing.ID, ProviderModel: "partial-chat", Priority: 1, Weight: 100, Status: StatusActive, Strategy: "priority_only"})
	store.AddRoute(ModelRoute{ID: "route_backup", ModelName: "gpt-4.1-mini", ProviderID: backup.ID, ProviderModel: "backup-chat", Priority: 2, Weight: 100, Status: StatusActive, Strategy: "priority_only"})

	server := New(store)
	registerTestAdapter(server, "partial_usage_mock", partialUsageFailingAdapter{})
	emitter := &recordingTraceEmitter{}
	server.traceEmitter = emitter

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "fail over please"}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("expected failover success, got %d: %s", response.Code, response.Body)
	}

	completions := emitter.take()
	if len(completions) != 1 {
		t.Fatalf("expected exactly one completion, got %d", len(completions))
	}
	attempts := completions[0].Attempts
	if len(attempts) != 2 {
		t.Fatalf("expected two attempts, got %d", len(attempts))
	}

	failed, succeeded := attempts[0], attempts[1]
	if failed.Usage.TotalTokens != 150 {
		t.Fatalf("failed attempt lost its usage: %+v", failed.Usage)
	}
	if failed.Usage.CostUSD <= 0 {
		t.Fatalf("failed attempt is unpriced: %+v", failed.Usage)
	}
	if succeeded.Usage.TotalTokens <= 0 {
		t.Fatalf("successful attempt has no usage: %+v", succeeded.Usage)
	}
	if failed.Usage.TotalTokens == succeeded.Usage.TotalTokens {
		t.Fatal("attempts report identical usage, so per-candidate usage is not being captured")
	}

	for index, attempt := range attempts {
		if !attempt.Invoked {
			t.Fatalf("attempt %d should be invoked", index)
		}
		if attempt.StartedAt.IsZero() || attempt.EndedAt.IsZero() {
			t.Fatalf("attempt %d has no invocation window: %+v", index, attempt)
		}
		if attempt.EndedAt.Before(attempt.StartedAt) {
			t.Fatalf("attempt %d ends before it starts", index)
		}
		if attempt.StartedAt.Before(completions[0].Call.StartedAt) {
			t.Fatalf("attempt %d starts before the call it belongs to", index)
		}
	}
	if succeeded.StartedAt.Before(failed.StartedAt) {
		t.Fatal("failover attempts are not ordered in time")
	}

	detail, err := store.GetRequestDetail(completions[0].Call.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := detail["attempts"].([]RouteAttemptLog)
	if !ok {
		t.Fatalf("request detail carries no attempt logs: %T", detail["attempts"])
	}
	if len(persisted) != 2 {
		t.Fatalf("expected two persisted attempts, got %d", len(persisted))
	}
	for _, attemptLog := range persisted {
		if attemptLog.TotalTokens <= 0 {
			t.Fatalf("attempt log did not persist usage: %+v", attemptLog)
		}
		if attemptLog.CostUSD <= 0 {
			t.Fatalf("attempt log did not persist cost: %+v", attemptLog)
		}
		if attemptLog.StartedAt.IsZero() || attemptLog.EndedAt.IsZero() {
			t.Fatalf("attempt log did not persist its invocation window: %+v", attemptLog)
		}
	}
}
