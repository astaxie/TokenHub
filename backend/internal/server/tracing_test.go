package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// fakeOTLPEndpoint is an in-process OTLP/HTTP receiver. Tests assert against the
// decoded protobuf rather than against TokenHub's own view of what it sent, so a
// mapping that is wrong on the wire cannot pass.
type fakeOTLPEndpoint struct {
	server  *httptest.Server
	mu      sync.Mutex
	spans   []*tracepb.Span
	delay   time.Duration
	release chan struct{}
}

func newFakeOTLPEndpoint(t *testing.T) *fakeOTLPEndpoint {
	t.Helper()
	endpoint := &fakeOTLPEndpoint{release: make(chan struct{})}
	endpoint.server = httptest.NewServer(http.HandlerFunc(endpoint.serve))
	t.Cleanup(endpoint.server.Close)
	return endpoint
}

func (f *fakeOTLPEndpoint) serve(w http.ResponseWriter, r *http.Request) {
	if f.delay > 0 {
		select {
		case <-f.release:
		case <-time.After(f.delay):
		case <-r.Context().Done():
			return
		}
	}
	body := io.Reader(r.Body)
	if r.Header.Get("content-encoding") == "gzip" {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = reader.Close() }()
		body = reader
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var request coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(raw, &request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	for _, resourceSpans := range request.GetResourceSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			f.spans = append(f.spans, scopeSpans.GetSpans()...)
		}
	}
	f.mu.Unlock()

	response, err := proto.Marshal(&coltracepb.ExportTraceServiceResponse{})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "application/x-protobuf")
	_, _ = w.Write(response)
}

func (f *fakeOTLPEndpoint) tracesURL() string {
	return f.server.URL + "/api/public/otel/v1/traces"
}

func (f *fakeOTLPEndpoint) collected() []*tracepb.Span {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*tracepb.Span{}, f.spans...)
}

func tracingTestConfig(endpoint string) Config {
	config := Config{
		AdminToken:            "dev_admin_token",
		Environment:           "test",
		AppVersion:            "0.0.0-test",
		TracingEnabled:        true,
		TracingEndpoint:       endpoint,
		TracingHeaders:        "Authorization=Basic cGs6c2s=,x-langfuse-ingestion-version=4",
		TracingSampleRatio:    1,
		TracingTimeoutSeconds: 5,
		TracingQueueSize:      64,
	}
	return config
}

func newTracingTestServer(t *testing.T, config Config) *Server {
	t.Helper()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, config)
	if app.traceEmitter == nil {
		t.Fatal("tracing was configured but no emitter was installed")
	}
	return app
}

// flushTracing shuts the server down, which is what drains the queue and flushes the
// exporter. Exercising the real shutdown path is the point: a flush that only works
// when a test calls it directly is not a flush.
func flushTracing(t *testing.T, app *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

func spanAttribute(span *tracepb.Span, key string) (*commonpb.AnyValue, bool) {
	for _, attribute := range span.GetAttributes() {
		if attribute.GetKey() == key {
			return attribute.GetValue(), true
		}
	}
	return nil, false
}

func spanString(t *testing.T, span *tracepb.Span, key string) string {
	t.Helper()
	value, ok := spanAttribute(span, key)
	if !ok {
		t.Fatalf("span %q has no attribute %q", span.GetName(), key)
	}
	return value.GetStringValue()
}

func hasAttribute(span *tracepb.Span, key string) bool {
	_, ok := spanAttribute(span, key)
	return ok
}

func TestTracingExportsRootAndGenerationSpans(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hello tokenhub"}},
	}, "thk_demo_local")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	spans := endpoint.collected()
	if len(spans) != 2 {
		t.Fatalf("expected a root span and one generation, got %d", len(spans))
	}
	var root, generation *tracepb.Span
	for _, span := range spans {
		switch spanString(t, span, attrObservationType) {
		case observationTypeSpan:
			root = span
		case observationTypeGeneration:
			generation = span
		}
	}
	if root == nil || generation == nil {
		t.Fatal("expected exactly one root span and one generation span")
	}

	// A real root, not a span parented to something that was never exported. Langfuse
	// expects the trace to have one.
	for _, parent := range root.GetParentSpanId() {
		if parent != 0 {
			t.Fatalf("root span has a parent (%x), so the exported trace has no root", root.GetParentSpanId())
		}
	}
	if string(generation.GetParentSpanId()) != string(root.GetSpanId()) {
		t.Fatal("generation is not a child of the root span")
	}
	if string(generation.GetTraceId()) != string(root.GetTraceId()) {
		t.Fatal("root and generation are in different traces")
	}

	// Usage and cost belong to the generation alone. Repeating them on the root
	// would double every token and every dollar in Langfuse's project totals.
	if hasAttribute(root, attrUsageDetails) || hasAttribute(root, attrCostDetails) {
		t.Fatal("root span carries usage or cost, which double-counts under Langfuse v4")
	}
	if !hasAttribute(generation, attrUsageDetails) {
		t.Fatal("generation carries no usage details")
	}
	if !hasAttribute(generation, attrCostDetails) {
		t.Fatal("generation carries no cost details")
	}
	if got := spanString(t, generation, attrProviderName); got != ProviderMock {
		t.Fatalf("expected provider %q, got %q", ProviderMock, got)
	}
	if hasAttribute(generation, "gen_ai.system") {
		t.Fatal("gen_ai.system is deprecated and must not be emitted")
	}
	if got := spanString(t, generation, attrRequestModel); got == "" {
		t.Fatal("generation carries no request model")
	}

	// Langfuse v4 cannot filter or aggregate a child by an attribute that only
	// exists on its root, so every trace-level attribute is repeated on both.
	for _, key := range []string{attrTraceName, attrEnvironment, attrRelease, attrUserID, attrTraceMetadata + "project_id", attrTraceMetadata + "request_id"} {
		if !hasAttribute(root, key) {
			t.Fatalf("root span is missing trace-level attribute %q", key)
		}
		if !hasAttribute(generation, key) {
			t.Fatalf("generation is missing trace-level attribute %q", key)
		}
	}

	requestID := spanString(t, root, attrTraceMetadata+"request_id")
	expectedTrace := traceIDForRequest(requestID)
	if string(root.GetTraceId()) != string(expectedTrace[:]) {
		t.Fatal("trace ID is not derived from the request ID, so x-request-id cannot find the trace")
	}
	// Span IDs are derived too. OTLP delivery is at-least-once, so a retried batch
	// must rewrite the same observations rather than add a second copy of the trace.
	expectedRoot := spanIDForRequest(requestID, "request")
	if string(root.GetSpanId()) != string(expectedRoot[:]) {
		t.Fatal("root span ID is not deterministic, so an OTLP retry would duplicate it")
	}
	expectedAttempt := spanIDForRequest(requestID, "attempt:1")
	if string(generation.GetSpanId()) != string(expectedAttempt[:]) {
		t.Fatal("generation span ID is not deterministic, so an OTLP retry would duplicate it")
	}
}

// TestTracingAttributesUpstreamMetadataPerAttempt guards against labelling a failed
// candidate with the upstream request ID of the one that eventually succeeded.
func TestTracingAttributesUpstreamMetadataPerAttempt(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	}, "thk_demo_local")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	for _, span := range endpoint.collected() {
		// The call's own upstream identifiers must not be repeated onto every span:
		// they belong to whichever attempt produced them.
		if hasAttribute(span, attrTraceMetadata+"upstream_request_id") {
			t.Fatalf("span %q carries the call's upstream request ID as a trace attribute", span.GetName())
		}
		if hasAttribute(span, attrTraceMetadata+"transport") {
			t.Fatalf("span %q carries the call's transport as a trace attribute", span.GetName())
		}
	}
}

// TestTracingCoversEveryRoutedProtocol keeps the exactly-once guarantee honest across
// the protocol surfaces, not only the chat endpoint the other tests use.
func TestTracingCoversEveryRoutedProtocol(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		payload map[string]any
	}{
		{
			name:    "anthropic messages",
			path:    "/v1/messages",
			payload: map[string]any{"model": "gpt-4.1-mini", "max_tokens": 64, "messages": []map[string]any{{"role": "user", "content": "hello"}}},
		},
		{
			name:    "responses",
			path:    "/v1/responses",
			payload: map[string]any{"model": "gpt-4.1-mini", "input": "hello"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			endpoint := newFakeOTLPEndpoint(t)
			app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))
			response := doJSON(t, app.Handler(), http.MethodPost, testCase.path, testCase.payload, "thk_demo_local")
			if response.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
			}
			flushTracing(t, app)

			roots := 0
			traces := map[string]bool{}
			for _, span := range endpoint.collected() {
				traces[string(span.GetTraceId())] = true
				if spanString(t, span, attrObservationType) == observationTypeSpan {
					roots++
				}
			}
			if roots != 1 {
				t.Fatalf("expected exactly one root span, got %d", roots)
			}
			if len(traces) != 1 {
				t.Fatalf("expected one trace, got %d", len(traces))
			}
		})
	}
}

func TestTracingOmitsPayloadsByDefault(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "a secret prompt"}},
	}, "thk_demo_local")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	for _, span := range endpoint.collected() {
		if hasAttribute(span, attrObservationInput) || hasAttribute(span, attrObservationOutput) {
			t.Fatal("payload capture is off but a span carries input or output")
		}
		for _, attribute := range span.GetAttributes() {
			if strings.Contains(attribute.GetValue().GetStringValue(), "a secret prompt") {
				t.Fatalf("prompt leaked through attribute %q", attribute.GetKey())
			}
		}
	}
}

func TestTracingCapturesPayloadsWhenEnabled(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	config := tracingTestConfig(endpoint.tracesURL())
	config.TracingCapturePayloads = true
	app := newTracingTestServer(t, config)

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hello tokenhub"}},
	}, "thk_demo_local")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	var root *tracepb.Span
	for _, span := range endpoint.collected() {
		if spanString(t, span, attrObservationType) == observationTypeSpan {
			root = span
		}
	}
	if root == nil {
		t.Fatal("no root span was exported")
	}
	input := spanString(t, root, attrObservationInput)
	if !strings.Contains(input, "hello tokenhub") {
		t.Fatalf("root input does not carry the prompt: %s", input)
	}
	output := spanString(t, root, attrObservationOutput)
	if !strings.Contains(output, "Echo: hello tokenhub") {
		t.Fatalf("root output does not carry the response: %s", output)
	}
}

// TestTracingRedactsSecretsInPayloads checks that captured payloads go through the
// same redaction as the stored payload log, so enabling capture cannot ship a
// credential to a third party that TokenHub would not write to its own database.
func TestTracingRedactsSecretsInPayloads(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	config := tracingTestConfig(endpoint.tracesURL())
	config.TracingCapturePayloads = true
	app := newTracingTestServer(t, config)

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
		"metadata": map[string]any{"api_key": "sk-must-not-leak"},
	}, "thk_demo_local")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	for _, span := range endpoint.collected() {
		for _, attribute := range span.GetAttributes() {
			if strings.Contains(attribute.GetValue().GetStringValue(), "sk-must-not-leak") {
				t.Fatalf("secret leaked through attribute %q", attribute.GetKey())
			}
		}
	}
}

func TestTracingRecordsFailoverPerCandidate(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Failover App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "failover-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_trace_failover")
	if err != nil {
		t.Fatal(err)
	}
	failing := store.AddProvider(Provider{ID: "prv_partial", Name: "Partial", Type: "partial_usage_mock", Status: StatusActive, Healthy: true})
	backup := store.AddProvider(Provider{ID: "prv_backup", Name: "Backup", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 3, OutputPriceUSDPer1M: 12})
	store.AddRoute(ModelRoute{ID: "route_partial", ModelName: "gpt-4.1-mini", ProviderID: failing.ID, ProviderModel: "partial-chat", Priority: 1, Weight: 100, Status: StatusActive, Strategy: "priority_only"})
	store.AddRoute(ModelRoute{ID: "route_backup", ModelName: "gpt-4.1-mini", ProviderID: backup.ID, ProviderModel: "backup-chat", Priority: 2, Weight: 100, Status: StatusActive, Strategy: "priority_only"})

	app := NewWithConfig(store, tracingTestConfig(endpoint.tracesURL()))
	registerTestAdapter(app, "partial_usage_mock", partialUsageFailingAdapter{})

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "fail over please"}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	generations := make([]*tracepb.Span, 0, 2)
	for _, span := range endpoint.collected() {
		if spanString(t, span, attrObservationType) == observationTypeGeneration {
			generations = append(generations, span)
		}
	}
	if len(generations) != 2 {
		t.Fatalf("expected one generation per invoked candidate, got %d", len(generations))
	}
	models := map[string]bool{}
	for _, generation := range generations {
		models[spanString(t, generation, attrRequestModel)] = true
		if !hasAttribute(generation, attrUsageDetails) {
			t.Fatalf("generation %q carries no usage", generation.GetName())
		}
	}
	if !models["partial-chat"] || !models["backup-chat"] {
		t.Fatalf("both candidates should appear as generations, got %v", models)
	}
}

// TestTracingUsageDetailsAreMutuallyExclusive is the check that keeps token
// accounting honest: TokenHub nests cached tokens inside prompt tokens and reasoning
// tokens inside completion tokens, while Langfuse sums usage_details verbatim.
func TestTracingUsageDetailsAreMutuallyExclusive(t *testing.T) {
	cases := []struct {
		name  string
		usage Usage
	}{
		{name: "plain", usage: Usage{PromptTokens: 100, CompletionTokens: 40, TotalTokens: 140}},
		{name: "cached", usage: Usage{PromptTokens: 100, CachedInputTokens: 60, CompletionTokens: 40, TotalTokens: 140}},
		{name: "cache write", usage: Usage{PromptTokens: 100, CachedInputTokens: 20, CacheWriteInputTokens: 30, CompletionTokens: 40, TotalTokens: 140}},
		{name: "reasoning", usage: Usage{PromptTokens: 100, CompletionTokens: 40, ReasoningOutputTokens: 25, TotalTokens: 140}},
		{name: "everything", usage: Usage{PromptTokens: 100, CachedInputTokens: 20, CacheWriteInputTokens: 30, CompletionTokens: 40, ReasoningOutputTokens: 25, TotalTokens: 140}},
		{name: "audio", usage: Usage{PromptTokens: 100, InputAudioTokens: 40, CompletionTokens: 40, OutputAudioTokens: 15, TotalTokens: 140}},
		{name: "prediction", usage: Usage{PromptTokens: 100, CompletionTokens: 40, AcceptedPredictionTokens: 12, RejectedPredictionTokens: 8, TotalTokens: 140}},
		{name: "every kind", usage: Usage{
			PromptTokens: 100, CachedInputTokens: 20, CacheWriteInputTokens: 10, InputAudioTokens: 15,
			CompletionTokens: 40, ReasoningOutputTokens: 9, OutputAudioTokens: 6,
			AcceptedPredictionTokens: 5, RejectedPredictionTokens: 4, TotalTokens: 140,
		}},
		// An upstream that reports detail counts exceeding the totals it also reported
		// must not be able to inflate what Langfuse sums.
		{name: "details exceed their totals", usage: Usage{
			PromptTokens: 10, CachedInputTokens: 900, CacheWriteInputTokens: 900, InputAudioTokens: 900,
			CompletionTokens: 5, ReasoningOutputTokens: 900, OutputAudioTokens: 900,
			AcceptedPredictionTokens: 900, RejectedPredictionTokens: 900, TotalTokens: 15,
		}},
		{name: "negative counts", usage: Usage{PromptTokens: 10, CachedInputTokens: -5, CompletionTokens: 5, ReasoningOutputTokens: -3, TotalTokens: 15}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encoded := usageDetails(testCase.usage)
			if encoded == "" {
				t.Fatal("expected usage details")
			}
			var details map[string]int64
			if err := json.Unmarshal([]byte(encoded), &details); err != nil {
				t.Fatal(err)
			}
			var sum int64
			for key, value := range details {
				if value < 0 {
					t.Fatalf("bucket %q is negative: %d", key, value)
				}
				if key == "total" {
					continue
				}
				sum += value
			}
			expected := testCase.usage.PromptTokens + testCase.usage.CompletionTokens
			if sum != expected {
				t.Fatalf("buckets sum to %d, expected %d: %s", sum, expected, encoded)
			}
			if details["total"] != testCase.usage.TotalTokens {
				t.Fatalf("total is %d, expected %d", details["total"], testCase.usage.TotalTokens)
			}
		})
	}
}

func TestTracingUsageDetailsEmptyWhenNothingWasUsed(t *testing.T) {
	if got := usageDetails(Usage{}); got != "" {
		t.Fatalf("expected no usage details for an unused call, got %q", got)
	}
}

func TestParseTracingHeaders(t *testing.T) {
	// Basic credentials are base64 and routinely end in '=' padding, which is
	// exactly what a naive split would corrupt.
	headers, err := ParseTracingHeaders("Authorization=Basic cGstbGY6c2stbGY=,x-langfuse-ingestion-version=4")
	if err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] != "Basic cGstbGY6c2stbGY=" {
		t.Fatalf("padded credential was corrupted: %q", headers["Authorization"])
	}
	// Names are canonicalised, so a duplicate that differs only in case is caught
	// rather than resolved by map iteration order.
	if headers["X-Langfuse-Ingestion-Version"] != "4" {
		t.Fatalf("unexpected version header: %v", headers)
	}
	if _, err := ParseTracingHeaders("Authorization=a,authorization=b"); err == nil {
		t.Fatal("a duplicate differing only in case must be rejected")
	}

	if headers, err := ParseTracingHeaders("   "); err != nil || headers != nil {
		t.Fatalf("blank headers should be absent, got %v %v", headers, err)
	}
	for _, malformed := range []string{"Authorization", "=value", "A=1,A=2", "A=va\nlue", "A\r=1", "bad name=1", "A=va\x00lue"} {
		if _, err := ParseTracingHeaders(malformed); err == nil {
			t.Fatalf("expected %q to be rejected", malformed)
		}
	}
}

func TestTracingConfigValidationFailsClosed(t *testing.T) {
	valid := tracingTestConfig("https://cloud.langfuse.com/api/public/otel/v1/traces")
	if err := valid.validateTracing(); err != nil {
		t.Fatalf("valid configuration was rejected: %v", err)
	}
	if err := (Config{TracingEnabled: false, TracingEndpoint: "nonsense"}).validateTracing(); err != nil {
		t.Fatalf("tracing is off, nothing should be validated: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "no endpoint", mutate: func(c *Config) { c.TracingEndpoint = "" }},
		{name: "not a url", mutate: func(c *Config) { c.TracingEndpoint = "://" }},
		{name: "wrong scheme", mutate: func(c *Config) { c.TracingEndpoint = "grpc://langfuse:4317" }},
		{name: "no host", mutate: func(c *Config) { c.TracingEndpoint = "https:///v1/traces" }},
		{name: "malformed headers", mutate: func(c *Config) { c.TracingHeaders = "Authorization" }},
		{name: "ratio not a number", mutate: func(c *Config) { c.TracingSampleRatio = math.NaN() }},
		{name: "ratio too high", mutate: func(c *Config) { c.TracingSampleRatio = 1.5 }},
		{name: "ratio negative", mutate: func(c *Config) { c.TracingSampleRatio = -0.1 }},
		{name: "timeout not set", mutate: func(c *Config) { c.TracingTimeoutSeconds = -1 }},
		{name: "queue not set", mutate: func(c *Config) { c.TracingQueueSize = -1 }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := valid
			testCase.mutate(&config)
			if err := config.validateTracing(); err == nil {
				t.Fatal("expected the configuration to be rejected")
			}
			if err := config.ValidateForStartup(); err == nil {
				t.Fatal("expected startup validation to reject the configuration")
			}
		})
	}
}

// TestTracingDoesNotDelayRequests covers the property the queue exists for: an
// endpoint that never answers must not become gateway latency.
func TestTracingDoesNotDelayRequests(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	endpoint.delay = 30 * time.Second
	config := tracingTestConfig(endpoint.tracesURL())
	config.TracingQueueSize = 1
	app := newTracingTestServer(t, config)
	t.Cleanup(func() { close(endpoint.release) })

	handler := app.Handler()
	started := time.Now()
	for i := 0; i < 20; i++ {
		response := doJSON(t, handler, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model":    "gpt-4.1-mini",
			"messages": []map[string]any{{"role": "user", "content": "hello"}},
		}, "thk_demo_local")
		if response.Code != http.StatusOK {
			t.Fatalf("request %d failed: %d %s", i, response.Code, response.Body)
		}
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("a stalled trace endpoint delayed the gateway by %s", elapsed)
	}
}

// TestTracingExportsAfterClientDisconnect checks the export context is detached from
// the request: a client hanging up must not cancel the trace of what it did.
func TestTracingExportsAfterClientDisconnect(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer thk_demo_local")
	request = request.WithContext(requestContext)
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	// Cancel after the handler returned, standing in for a client that hangs up
	// while the completion is still queued.
	cancel()

	flushTracing(t, app)
	if len(endpoint.collected()) == 0 {
		t.Fatal("a cancelled request context suppressed the trace")
	}
}

func TestTracingRejectedRequestIsTraced(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "model-that-does-not-exist",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	}, "thk_demo_local")
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	spans := endpoint.collected()
	if len(spans) != 1 {
		t.Fatalf("expected a single root span for a rejected request, got %d", len(spans))
	}
	root := spans[0]
	if got := spanString(t, root, attrObservationLevel); got != observationLevelError {
		t.Fatalf("expected an ERROR observation, got %q", got)
	}
	if got := spanString(t, root, attrTraceMetadata+"completion_kind"); got != string(CompletionKindRejected) {
		t.Fatalf("expected the rejected kind, got %q", got)
	}
	if root.GetStatus().GetCode() != tracepb.Status_STATUS_CODE_ERROR {
		t.Fatal("rejected root span is not marked as an error")
	}
}

func TestTracingTagsPlaygroundTraffic(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	response := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/playground/chat", map[string]any{
		"project_id": "prj_demo",
		"model":      "gpt-4.1-mini",
		"messages":   []map[string]any{{"role": "user", "content": "hello"}},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	spans := endpoint.collected()
	if len(spans) == 0 {
		t.Fatal("playground traffic produced no spans")
	}
	for _, span := range spans {
		value, ok := spanAttribute(span, attrTraceTags)
		if !ok {
			t.Fatalf("span %q carries no tags", span.GetName())
		}
		tagged := false
		for _, tag := range value.GetArrayValue().GetValues() {
			if tag.GetStringValue() == string(CompletionKindPlayground) {
				tagged = true
			}
		}
		if !tagged {
			t.Fatalf("span %q is not tagged as playground traffic", span.GetName())
		}
	}
}

// TestTracingCountsExportOutcomes covers the signal that E2E showed was missing: the
// SDK reports export failures through the OTel global error handler, which this
// server does not install, so a backend that refuses every batch would otherwise look
// identical to one that is working.
func TestTracingCountsExportOutcomes(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(refusing.Close)

	for _, testCase := range []struct {
		name    string
		url     string
		outcome string
	}{
		{name: "accepted", outcome: traceSpanOutcomeExported},
		{name: "refused", url: refusing.URL + "/v1/traces", outcome: traceSpanOutcomeFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			endpoint := newFakeOTLPEndpoint(t)
			url := testCase.url
			if url == "" {
				url = endpoint.tracesURL()
			}
			config := tracingTestConfig(url)
			config.MetricsEnabled = true
			// One second, so a refused export gives up long before the test does.
			config.TracingTimeoutSeconds = 1
			store := NewMemoryStore()
			if err := SeedDemoData(store); err != nil {
				t.Fatal(err)
			}
			app := NewWithConfig(store, config)
			if app.metrics == nil {
				t.Fatal("metrics were requested but not installed")
			}

			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
				"model":    "gpt-4.1-mini",
				"messages": []map[string]any{{"role": "user", "content": "hello"}},
			}, "thk_demo_local")
			if response.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
			}
			flushTracing(t, app)

			if got := counterValue(t, app, "tokenhub_gateway_trace_completions_total", traceCompletionOutcomeConverted); got < 1 {
				t.Fatalf("expected a converted completion, got %v", got)
			}
			if got := counterValue(t, app, "tokenhub_gateway_trace_spans_total", testCase.outcome); got < 1 {
				t.Fatalf("expected spans counted as %q, got %v", testCase.outcome, got)
			}
		})
	}
}

func counterValue(t *testing.T, app *Server, name string, outcome string) float64 {
	t.Helper()
	families, err := app.metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "outcome" && label.GetValue() == outcome {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// TestTracingRefusesRedirects is a credential-containment test. Go carries arbitrary
// request headers across a redirect and only gives Authorization special cross-origin
// handling, so a credential configured under any other name would otherwise be
// handed to whatever host a redirect names.
func TestTracingRefusesRedirects(t *testing.T) {
	var receivedHeaders int64
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "" || r.Header.Get("Authorization") != "" {
			atomic.AddInt64(&receivedHeaders, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(attacker.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/v1/traces", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	config := tracingTestConfig(redirector.URL + "/api/public/otel/v1/traces")
	config.TracingHeaders = "X-Api-Key=super-secret-value,Authorization=Basic cGs6c2s="
	config.MetricsEnabled = true
	config.TracingTimeoutSeconds = 2
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, config)

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	}, "thk_demo_local")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	if got := atomic.LoadInt64(&receivedHeaders); got != 0 {
		t.Fatalf("export credentials were forwarded across a redirect %d time(s)", got)
	}
	// The export must be reported as failed rather than silently counted as delivered.
	if got := counterValue(t, app, "tokenhub_gateway_trace_spans_total", traceSpanOutcomeFailed); got < 1 {
		t.Fatalf("a refused redirect should count as a failed export, got %v", got)
	}
}

// TestTracingPricesPlaygroundGenerations guards the reason the playground call
// context carries the catalog model rather than a bare name: without pricing, every
// playground generation exports as free, and the tag that exists to exclude
// synthetic spend would have nothing to exclude.
func TestTracingPricesPlaygroundGenerations(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	response := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/playground/chat", map[string]any{
		"project_id": "prj_demo",
		"model":      "gpt-4.1-mini",
		"messages":   []map[string]any{{"role": "user", "content": "hello"}},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	flushTracing(t, app)

	var generation *tracepb.Span
	for _, span := range endpoint.collected() {
		if spanString(t, span, attrObservationType) == observationTypeGeneration {
			generation = span
		}
	}
	if generation == nil {
		t.Fatal("playground produced no generation span")
	}
	details, ok := spanAttribute(generation, attrCostDetails)
	if !ok {
		t.Fatal("playground generation carries no cost, so the playground tag excludes nothing")
	}
	var cost map[string]float64
	if err := json.Unmarshal([]byte(details.GetStringValue()), &cost); err != nil {
		t.Fatal(err)
	}
	if cost["total"] <= 0 {
		t.Fatalf("playground generation is priced at zero: %v", cost)
	}
}
