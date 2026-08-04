package server

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName = "tokenhub/gateway"
	// tracingBatchQueueSize and tracingExportBatchSize size the SDK's batching buffer.
	// It is intentionally small: it exists to group spans into one HTTP request, not
	// to absorb backlog. Absorbing backlog is the completion queue's job, because that
	// is the queue whose drops are counted.
	tracingBatchQueueSize  = 512
	tracingExportBatchSize = 128
	// tracingDropLogInterval rate limits the dropped-completion log. A queue that is
	// full is full for a while, and a line per drop would turn a telemetry problem
	// into a logging problem.
	tracingDropLogInterval = time.Minute
)

// ParseTracingHeaders splits the comma-separated name=value list used to
// authenticate OTLP export.
//
// Splitting on the first '=' matters: a Basic credential is base64, which ends in
// '=' padding often enough that splitting on the last one, or rejecting extra ones,
// would corrupt exactly the header operators are most likely to configure.
func ParseTracingHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	headers := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		// Checked before trimming. Trimming first would quietly repair a line break
		// at the edge of a name or value, and a line break in an outgoing header is
		// a request-splitting vector rather than a formatting nit.
		if strings.ContainsAny(pair, "\r\n") {
			return nil, fmt.Errorf("a header contains a line break")
		}
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, found := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !found || name == "" {
			// The value is not echoed back: it is a credential, and a parse failure
			// is the moment it is most likely to end up in a log or a ticket.
			return nil, fmt.Errorf("expected name=value pairs")
		}
		if !validHeaderName(name) {
			return nil, fmt.Errorf("header %q is not a valid header name", name)
		}
		if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, fmt.Errorf("header %q has a control character in its value", name)
		}
		// Header names are case-insensitive, so Authorization and authorization are the
		// same header. Detecting the duplicate case-sensitively would accept both and
		// let map iteration decide which one is sent.
		canonical := http.CanonicalHeaderKey(name)
		if _, duplicate := headers[canonical]; duplicate {
			return nil, fmt.Errorf("header %q is set twice", name)
		}
		headers[canonical] = value
	}
	return headers, nil
}

// validHeaderName reports whether name is a valid HTTP field name per RFC 9110's
// token grammar. The exporter would otherwise carry it into a request header, where
// an invalid name fails at send time rather than at startup.
func validHeaderName(name string) bool {
	for _, r := range name {
		if r >= 0x80 {
			return false
		}
		if strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			continue
		}
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		return false
	}
	return name != ""
}

// validateTracing rejects a tracing configuration that would export nothing or
// export somewhere unintended. It is called from ValidateForStartup so a
// misconfigured deployment fails to boot rather than running blind.
//
// Empty headers are allowed on purpose: a generic OTLP collector on a private
// network often needs no credential at all.
func (c Config) validateTracing() error {
	if !c.TracingEnabled {
		return nil
	}
	endpoint := strings.TrimSpace(c.TracingEndpoint)
	if endpoint == "" {
		return fmt.Errorf("TOKENHUB_TRACING_ENABLED is on but TOKENHUB_TRACING_ENDPOINT is empty")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid TOKENHUB_TRACING_ENDPOINT: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid TOKENHUB_TRACING_ENDPOINT: expected an http or https URL")
	}
	if parsed.Host == "" {
		return fmt.Errorf("invalid TOKENHUB_TRACING_ENDPOINT: no host")
	}
	// The exporter is given the URL's scheme, host and path only. Accepting the rest
	// would silently drop it, so anything that would not survive is refused instead
	// of being exported somewhere other than where the operator pointed it.
	if parsed.Path == "" || parsed.Path == "/" {
		return fmt.Errorf("invalid TOKENHUB_TRACING_ENDPOINT: expected the full traces path, for example /api/public/otel/v1/traces")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("invalid TOKENHUB_TRACING_ENDPOINT: a query, fragment or userinfo is not sent; put credentials in TOKENHUB_TRACING_HEADERS")
	}
	if _, err := ParseTracingHeaders(c.TracingHeaders); err != nil {
		return fmt.Errorf("invalid TOKENHUB_TRACING_HEADERS: %w", err)
	}
	if math.IsNaN(c.TracingSampleRatio) {
		return fmt.Errorf("invalid TOKENHUB_TRACING_SAMPLE_RATIO: expected a number")
	}
	if c.TracingSampleRatio < 0 || c.TracingSampleRatio > 1 {
		return fmt.Errorf("invalid TOKENHUB_TRACING_SAMPLE_RATIO: expected a value between 0 and 1")
	}
	if c.TracingTimeoutSeconds <= 0 {
		return fmt.Errorf("invalid TOKENHUB_TRACING_TIMEOUT_SECONDS: expected a positive number of seconds")
	}
	if c.TracingQueueSize <= 0 {
		return fmt.Errorf("invalid TOKENHUB_TRACING_QUEUE_SIZE: expected a positive size")
	}
	return nil
}

// otlpTraceEmitter turns finished gateway calls into OTLP spans.
//
// Completions are queued and converted on a dedicated goroutine rather than on the
// request goroutine, for two reasons. Span construction and the handoff to the SDK
// must never sit in a client's latency. And owning the queue is what makes loss
// countable: the SDK's own batcher drops individual spans in silence, which would
// split a trace and never be reported anywhere.
type otlpTraceEmitter struct {
	tracer      trace.Tracer
	provider    *sdktrace.TracerProvider
	queue       chan GatewayCallCompletion
	worker      sync.WaitGroup
	metrics     *GatewayMetrics
	environment string
	release     string

	mu     sync.RWMutex
	closed bool

	// pending counts completions accepted but not yet accounted for, so a shutdown
	// that runs out of time can report what it is about to lose.
	pending atomic.Int64
	// stopConverting makes the worker account for the queue instead of converting it,
	// which is what lets the provider be shut down without silently voiding spans.
	stopConverting atomic.Bool

	dropMu         sync.Mutex
	dropped        int64
	lastDropLogged time.Time
}

// newOTLPTraceEmitter builds the emitter. The returned error is only reachable when
// the configuration was never validated, because ValidateForStartup rejects every
// malformed value before a real deployment gets this far.
func newOTLPTraceEmitter(config Config, metrics *GatewayMetrics) (*otlpTraceEmitter, error) {
	if err := config.validateTracing(); err != nil {
		return nil, err
	}
	headers, err := ParseTracingHeaders(config.TracingHeaders)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(config.TracingTimeoutSeconds) * time.Second
	transport, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(strings.TrimSpace(config.TracingEndpoint)),
		otlptracehttp.WithHeaders(headers),
		otlptracehttp.WithTimeout(timeout),
		// Spans carrying prompts and completions are large and highly compressible.
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
		otlptracehttp.WithHTTPClient(&http.Client{
			Timeout: timeout,
			// Refuse redirects. Go carries arbitrary request headers across a
			// redirect, and only Authorization gets special cross-origin handling, so
			// a credential configured under any other name — X-API-Key and friends —
			// would be forwarded to whatever host the redirect names. A trace endpoint
			// has no reason to redirect, so there is nothing to trade away here.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("trace endpoint redirected; refusing to forward export credentials")
			},
		}),
		// Delivery is at-most-once by choice. With retries on, an acknowledgement lost
		// in transit makes the exporter resend a batch Langfuse already accepted, and
		// Langfuse warns that re-ingesting the same span IDs can create duplicate
		// observations. For a gateway whose traces carry token counts and spend,
		// inflated cost is worse than an occasional missing trace — and this pipeline
		// already drops rather than waits when it is saturated.
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
	)
	if err != nil {
		return nil, err
	}
	// The SDK's batch processor reports export failures through the OTel global error
	// handler, which this server deliberately does not install. Counting them at the
	// exporter keeps the failure visible without reaching for process-global state,
	// and it distinguishes "the backend rejected us" from "our queue overflowed".
	exporter := &countingSpanExporter{inner: transport, metrics: metrics}

	attributes := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("tokenhub"),
		semconv.ServiceVersion(config.AppVersion),
		semconv.DeploymentEnvironmentName(config.Environment),
	)
	// The provider is owned by this server rather than installed as the OTel global.
	// Tests then stay isolated from each other, and a binary that embeds TokenHub
	// keeps whatever tracing it had already configured.
	//
	// WithBlocking is deliberate and is what makes drop accounting truthful. The
	// batch processor has its own queue that silently drops individual spans when it
	// fills; that would split a trace, discard the root while keeping its children,
	// and never be counted anywhere. Blocking instead pushes the backpressure onto
	// this emitter's worker goroutine, which stops draining the completion queue,
	// which then fills and drops whole completions with a counter behind them.
	// Blocking is only safe because that worker is not a request goroutine.
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithExportTimeout(timeout),
			sdktrace.WithBlocking(),
			sdktrace.WithMaxQueueSize(tracingBatchQueueSize),
			sdktrace.WithMaxExportBatchSize(tracingExportBatchSize),
		),
		sdktrace.WithResource(attributes),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(config.TracingSampleRatio)),
		// Deterministic IDs so the x-request-id a client already holds is enough to
		// find the trace. They are not a deduplication mechanism: Langfuse can still
		// create a duplicate observation for a repeated span ID, which is why retries
		// are disabled above rather than relied upon.
		sdktrace.WithIDGenerator(deterministicIDGenerator{}),
	)

	emitter := &otlpTraceEmitter{
		tracer:      provider.Tracer(tracerName),
		provider:    provider,
		queue:       make(chan GatewayCallCompletion, config.TracingQueueSize),
		metrics:     metrics,
		environment: strings.TrimSpace(config.Environment),
		release:     strings.TrimSpace(config.AppVersion),
	}
	emitter.worker.Add(1)
	go emitter.run()
	return emitter, nil
}

// EmitGatewayCall hands a completion to the conversion goroutine. It never blocks:
// a full queue drops the completion, because telemetry must not become the reason a
// gateway request is slow.
func (e *otlpTraceEmitter) EmitGatewayCall(completion GatewayCallCompletion) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return
	}
	select {
	case e.queue <- completion:
		e.pending.Add(1)
	default:
		e.observe(traceCompletionOutcomeDropped)
		e.noteDrop()
	}
}

// countingSpanExporter reports what actually reached the trace backend. Without it
// the only visible signal is "the gateway built some spans", which stays green while
// every export is being refused.
type countingSpanExporter struct {
	inner   sdktrace.SpanExporter
	metrics *GatewayMetrics
	// failures counts consecutive failed batches so the log stays useful when a
	// backend is down for hours rather than emitting a line per batch.
	failureMu     sync.Mutex
	failures      int64
	lastFailureAt time.Time
}

func (e *countingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.inner.ExportSpans(ctx, spans)
	if err != nil {
		e.metrics.ObserveTraceSpans(traceSpanOutcomeFailed, len(spans))
		e.noteFailure(err)
		return err
	}
	e.metrics.ObserveTraceSpans(traceSpanOutcomeExported, len(spans))
	return nil
}

func (e *countingSpanExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

func (e *countingSpanExporter) noteFailure(err error) {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	e.failures++
	now := time.Now()
	if now.Sub(e.lastFailureAt) < tracingDropLogInterval {
		return
	}
	e.lastFailureAt = now
	failures := e.failures
	e.failures = 0
	log.Printf("[tokenhub] %d trace export batches failed: %v", failures, err)
}

// Shutdown stops accepting completions, drains the ones already queued, and flushes
// the exporter. It must run after every producer has stopped.
func (e *otlpTraceEmitter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	alreadyClosed := e.closed
	e.closed = true
	if !alreadyClosed {
		close(e.queue)
	}
	e.mu.Unlock()
	if alreadyClosed {
		return nil
	}

	// Draining and exporting get separate budgets. Sharing one deadline means a slow
	// drain consumes all of it and the provider is then asked to flush with an
	// already-expired context, which exports nothing.
	drainCtx, cancelDrain := context.WithTimeout(ctx, tracingDrainTimeout)
	defer cancelDrain()
	drained := make(chan struct{})
	go func() {
		e.worker.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-drainCtx.Done():
		// Conversion could not keep up. Tell the worker to stop converting before the
		// provider is shut down under it, then give it a moment to notice: a span
		// ended after provider shutdown silently does nothing, which would leave those
		// completions counted as neither converted nor dropped.
		e.stopConverting.Store(true)
		select {
		case <-drained:
		case <-time.After(tracingDrainGrace):
			// The worker is wedged inside a blocking handoff to the SDK. Report what
			// it still holds rather than letting it vanish from the counters.
			if stuck := e.pending.Load(); stuck > 0 {
				log.Printf("[tokenhub] %d gateway traces were still being converted when shutdown ran out of time", stuck)
			}
		}
	}
	if dropped := e.takeDropped(); dropped > 0 {
		log.Printf("[tokenhub] dropped %d gateway traces because the export queue was full", dropped)
	}
	flushCtx, cancelFlush := context.WithTimeout(context.WithoutCancel(ctx), tracingExportTimeout)
	defer cancelFlush()
	return e.provider.Shutdown(flushCtx)
}

func (e *otlpTraceEmitter) run() {
	defer e.worker.Done()
	for completion := range e.queue {
		// Once shutdown has given up waiting, the rest of the queue is accounted for
		// rather than converted: a span ended after the provider is shut down is a
		// no-op, so converting it would report work that never left the process.
		if e.stopConverting.Load() {
			e.observe(traceCompletionOutcomeDropped)
			e.pending.Add(-1)
			continue
		}
		e.record(completion)
		e.observe(traceCompletionOutcomeConverted)
		e.pending.Add(-1)
	}
}

// noteDrop counts a drop and logs at most once per interval.
func (e *otlpTraceEmitter) noteDrop() {
	e.dropMu.Lock()
	defer e.dropMu.Unlock()
	e.dropped++
	now := time.Now()
	if now.Sub(e.lastDropLogged) < tracingDropLogInterval {
		return
	}
	e.lastDropLogged = now
	dropped := e.dropped
	e.dropped = 0
	log.Printf("[tokenhub] dropped %d gateway traces because the export queue was full", dropped)
}

func (e *otlpTraceEmitter) takeDropped() int64 {
	e.dropMu.Lock()
	defer e.dropMu.Unlock()
	dropped := e.dropped
	e.dropped = 0
	return dropped
}

func (e *otlpTraceEmitter) observe(outcome string) {
	if e.metrics == nil {
		return
	}
	e.metrics.ObserveTraceCompletion(outcome)
}

// The shutdown budget is reserved on top of the graceful shutdown window rather than
// taken from inside it: by the time shutdown reaches tracing, the caller's deadline
// has usually been spent draining HTTP and image work, and flushing with an expired
// context exports nothing at all. It is then split, so a slow drain cannot eat the
// export half.
const (
	tracingDrainTimeout  = 3 * time.Second
	tracingDrainGrace    = 1 * time.Second
	tracingExportTimeout = 5 * time.Second
	tracingFlushTimeout  = tracingDrainTimeout + tracingExportTimeout
)

func (s *Server) shutdownTracing() {
	if s.traceEmitter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), tracingFlushTimeout)
	defer cancel()
	if err := s.traceEmitter.Shutdown(ctx); err != nil {
		log.Printf("[tokenhub] gateway trace export shutdown failed: %v", err)
	}
}

// traceAttribute is the small amount of plumbing needed to build attribute slices
// without repeating the empty checks at every call site.
func appendStringAttribute(attributes []attribute.KeyValue, key string, value string) []attribute.KeyValue {
	if strings.TrimSpace(value) == "" {
		return attributes
	}
	return append(attributes, attribute.String(key, value))
}

// installTraceEmitter attaches the OTLP exporter when tracing is configured.
//
// A construction failure here means the configuration was never validated, because
// ValidateForStartup rejects every malformed tracing value before main gets this
// far. It is reported as loudly as the metrics equivalent rather than exporting
// nothing in silence.
func (s *Server) installTraceEmitter(config Config) {
	if !config.TracingEnabled {
		return
	}
	emitter, err := newOTLPTraceEmitter(config, s.metrics)
	if err != nil {
		log.Printf("[tokenhub] gateway trace export disabled: %v", err)
		return
	}
	s.traceEmitter = emitter
}
