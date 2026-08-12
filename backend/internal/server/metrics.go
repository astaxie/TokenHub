package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	metricsNamespace = "tokenhub"
	metricsSubsystem = "gateway"
	// metricsLabelUnset keeps a label present but explicit when a request never got
	// far enough to have a value. Dropping the label instead would split one metric
	// into series with differing label sets, which breaks aggregation.
	metricsLabelUnset = "none"
)

// gatewayDurationBuckets is sized for model calls, which run from a fast cached
// completion to a multi-minute reasoning request. The client_golang defaults stop at
// 10s and would put almost every LLM request in +Inf.
var gatewayDurationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60, 120, 300}

// gatewayOverheadBuckets is sized for gateway-only work: auth, routing, DB and
// serialization. Values below a millisecond are rounded away by the attempt
// latency clamp, and multi-second overhead is already a clear outlier.
var gatewayOverheadBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// GatewayMetrics holds the Prometheus collectors for the model API.
//
// It owns its registry rather than using the default one, so tests get an isolated
// instance instead of racing on process-global state.
type GatewayMetrics struct {
	registry     *prometheus.Registry
	projectLabel bool

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
	tokens   *prometheus.CounterVec
	cost     *prometheus.CounterVec
	// rateLimitHits uses a per-key reference only for limits owned by an API key.
	// Inherited scopes use a fixed label to avoid multiplying global, project or
	// team policy series by the number of keys that happen to hit them.
	rateLimitHits *prometheus.CounterVec
	// traceCompletions and traceSpans cover the two places a trace can be lost, and
	// they are separate because the fixes are different: a full queue means the
	// gateway is producing faster than it can convert, while a failed export means
	// the backend is unreachable or rejecting. Without both, a gap in Langfuse looks
	// like a gateway that received no traffic.
	traceCompletions *prometheus.CounterVec
	traceSpans       *prometheus.CounterVec

	// routeAttempts counts every candidate the router considered, including
	// capacity-skipped attempts. It is the physical attempt count behind the
	// logical request count in routed_requests_total.
	routeAttempts *prometheus.CounterVec
	// attemptDuration records only invoked attempts, split by outcome. It measures
	// the whole routed attempt — upstream transport, stream translation and
	// writing to the client — so streaming calls include slow-client backpressure;
	// the upstream boundary itself is not separately timed here.
	attemptDuration *prometheus.HistogramVec
	// routedRequests counts logical requests that made at least one candidate
	// attempt. It is the denominator for the failover-depth ratio: requests_total
	// also counts refusals that never reached a provider.
	routedRequests *prometheus.CounterVec
	// overhead is the gateway's own cost: max(0, elapsed - sum(invoked attempt
	// duration)). A request admitted for routing that fails before any attempt
	// contributes its full elapsed time. It is an approximation because attempt
	// durations are rounded to whole milliseconds and clamped to a positive floor.
	overhead *prometheus.HistogramVec

	responseQueueDepth prometheus.Gauge
	responseQueueWait  prometheus.Histogram
	responseExecution  *prometheus.HistogramVec
	responseJobs       *prometheus.CounterVec
	responseRecoveries *prometheus.CounterVec
}

const (
	traceCompletionOutcomeConverted = "converted"
	traceCompletionOutcomeDropped   = "dropped"

	traceSpanOutcomeExported = "exported"
	traceSpanOutcomeFailed   = "failed"
)

// GatewayAttemptSample is one candidate attempt, reduced to the fields the
// metrics layer needs. Keeping it separate from RouteAttempt avoids pulling the
// full routing structure into the metrics package.
type GatewayAttemptSample struct {
	ProviderType string
	ProviderID   string
	ResourceID   string
	StatusCode   int
	ErrorCode    string
	Invoked      bool
	Duration     time.Duration
}

// GatewayCallSample is one finished gateway request, successful or not.
type GatewayCallSample struct {
	Model        string
	ProviderType string
	ProviderID   string
	ResourceID   string
	ProjectID    string
	StatusCode   int
	ErrorCode    string
	Stream       bool
	Duration     time.Duration
	Usage        Usage
	// Attempts is the per-candidate outcome slice. Empty means no candidate was
	// tried — the request was refused before routing or failed during route
	// selection — and both still count in requests_total.
	Attempts []GatewayAttemptSample
}

// NewGatewayMetrics builds the collectors. When projectLabel is true every metric
// carries a project_id label; it is off by default because project count multiplies
// the series count of every metric here.
func NewGatewayMetrics(projectLabel bool) *GatewayMetrics {
	m := &GatewayMetrics{
		registry:     prometheus.NewRegistry(),
		projectLabel: projectLabel,
	}
	withProject := func(labels ...string) []string {
		if projectLabel {
			return append(labels, "project_id")
		}
		return labels
	}
	m.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "requests_total",
		Help:      "Model API requests by outcome. Counts logical requests, so a request that failed over across several candidates counts once.",
	}, withProject("model", "provider_type", "provider_id", "resource_id", "status_code", "error_code", "stream"))
	m.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "request_duration_seconds",
		Help:      "End-to-end model API request latency, including any failover attempts.",
		Buckets:   gatewayDurationBuckets,
	}, withProject("model", "provider_type", "stream", "outcome"))
	m.inFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "requests_in_flight",
		Help:      "Model API requests currently being routed to an upstream. Scoped to the same endpoints as requests_total, so catalog lookups, count_tokens, admin traffic and scrapes are excluded.",
	})
	m.tokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "tokens_total",
		Help:      "Tokens attributed to model API requests, split by kind. Kinds are NOT a partition and must not be summed: prompt already includes the cached and cache_write tokens, and reasoning is a subset of completion.",
	}, withProject("model", "provider_type", "provider_id", "kind"))
	m.cost = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "cost_usd_total",
		Help:      "Estimated cost in USD attributed to model API requests.",
	}, withProject("model", "provider_type", "provider_id"))
	m.rateLimitHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "rate_limit_hits_total",
		Help:      "Rate-limit rejections by effective policy scope and limit type. key_ref is a masked identifier only for api_key scope and is none for inherited scopes.",
	}, []string{"scope", "limit", "key_ref"})

	m.traceCompletions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "trace_completions_total",
		Help:      "Finished gateway calls by what tracing did with them. \"dropped\" means the conversion queue was full and no span was ever built.",
	}, []string{"outcome"})
	m.traceSpans = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "trace_spans_total",
		Help:      "Spans by what the OTLP exporter did with them. \"failed\" means the trace backend rejected them or could not be reached.",
	}, []string{"outcome"})

	m.routeAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "route_attempts_total",
		Help:      "Candidate attempts by outcome. Counts physical attempts, so a request that failed over across several candidates counts each candidate. The ratio rate(route_attempts_total) / rate(routed_requests_total) is the average failover depth; routed_requests_total counts only requests that made an attempt, so refusals that never reached a provider cannot dilute it. status_code is the gateway-mapped status (an upstream 401 is reported as 502); the raw upstream status is in the RouteAttemptLog.",
	}, withProject("model", "provider_type", "provider_id", "resource_id", "status_code", "error_code", "invoked"))
	m.attemptDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "attempt_duration_seconds",
		Help:      "Duration of one invoked routed attempt, measured around the whole attempt: upstream transport, stream translation, and writing to the client. Streaming calls therefore include slow-client backpressure; gateway overhead is reported separately in overhead_seconds. Excludes candidates skipped for capacity.",
		Buckets:   gatewayDurationBuckets,
	}, withProject("model", "provider_type", "outcome"))
	m.routedRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "routed_requests_total",
		Help:      "Logical model API requests that made at least one candidate attempt. It is the attempt-bearing denominator for the failover-depth ratio; requests_total also counts refusals that never reached a provider. Its provider_type label is the last candidate attempted, so aggregate the depth ratio by model rather than by provider_type when requests fail over across providers.",
	}, withProject("model", "provider_type", "stream"))
	m.overhead = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "overhead_seconds",
		Help:      "Approximate gateway overhead per request: elapsed end-to-end time minus the sum of invoked attempt durations. Clamped at zero. A request admitted for routing that fails before any attempt contributes its full elapsed time. For image jobs the elapsed time includes queue wait, so overhead there is an upper bound.",
		Buckets:   gatewayOverheadBuckets,
	}, withProject("stream"))
	m.responseQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "response_jobs_queued",
		Help:      "Durable Responses jobs waiting in the shared database, sampled by this replica.",
	})
	m.responseQueueWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "response_job_queue_wait_seconds",
		Help:      "Time from durable Responses submission until a worker claims the job.",
		Buckets:   gatewayDurationBuckets,
	})
	m.responseExecution = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "response_job_execution_seconds",
		Help:      "Time from durable Responses worker claim until a terminal state.",
		Buckets:   gatewayDurationBuckets,
	}, []string{"status"})
	m.responseJobs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "response_jobs_total",
		Help:      "Durable Responses jobs reaching a terminal state, split by status and bounded failure code.",
	}, []string{"status", "error_code"})
	m.responseRecoveries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "response_job_recoveries_total",
		Help:      "Expired durable Responses leases by safe recovery decision.",
	}, []string{"decision"})

	m.registry.MustRegister(m.requests, m.duration, m.inFlight, m.tokens, m.cost, m.rateLimitHits, m.traceCompletions, m.traceSpans, m.routeAttempts, m.attemptDuration, m.routedRequests, m.overhead, m.responseQueueDepth, m.responseQueueWait, m.responseExecution, m.responseJobs, m.responseRecoveries)
	// Process and Go runtime metrics are what an operator reaches for first when the
	// gateway itself is the suspect, and they cost nothing to collect.
	m.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *GatewayMetrics) SetResponseQueueDepth(depth int64) {
	if m == nil {
		return
	}
	m.responseQueueDepth.Set(float64(maxInt64(depth, 0)))
}

func (m *GatewayMetrics) ObserveResponseJobClaim(createdAt time.Time, startedAt time.Time) {
	if m == nil || createdAt.IsZero() || startedAt.IsZero() {
		return
	}
	wait := startedAt.Sub(createdAt)
	if wait < 0 {
		wait = 0
	}
	m.responseQueueWait.Observe(wait.Seconds())
}

func (m *GatewayMetrics) ObserveResponseJobTerminal(status string, errorCode string, startedAt *time.Time, completedAt *time.Time) {
	if m == nil {
		return
	}
	m.ObserveResponseJobTerminalCount(status, errorCode, 1)
	if startedAt == nil || completedAt == nil {
		return
	}
	duration := completedAt.Sub(*startedAt)
	if duration < 0 {
		duration = 0
	}
	m.responseExecution.WithLabelValues(metricsLabel(status)).Observe(duration.Seconds())
}

func (m *GatewayMetrics) ObserveResponseJobTerminalCount(status string, errorCode string, count int64) {
	if m == nil || count <= 0 {
		return
	}
	m.responseJobs.WithLabelValues(metricsLabel(status), metricsLabel(errorCode)).Add(float64(count))
}

func (m *GatewayMetrics) ObserveResponseJobRecovery(decision string, count int64) {
	if m == nil || count <= 0 {
		return
	}
	m.responseRecoveries.WithLabelValues(metricsLabel(decision)).Add(float64(count))
}

func (m *GatewayMetrics) ObserveRateLimitHit(keyID string, limit string, scope string) {
	if m == nil {
		return
	}
	scope = minuteLimitMetricScope(scope)
	keyRef := metricsLabelUnset
	if scope == "api_key" {
		if strings.TrimSpace(keyID) == "" {
			return
		}
		keyRef = maskedAPIKeyMetricLabel(keyID)
	}
	m.rateLimitHits.WithLabelValues(scope, metricsLabel(limit), keyRef).Inc()
}

func minuteLimitMetricScope(scope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "api_key", "global", "project", "team":
		return scope
	default:
		return "api_key"
	}
}

func maskedAPIKeyMetricLabel(keyID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(keyID)))
	return "key_" + hex.EncodeToString(digest[:6])
}

// MetricsSink is implemented by stores that can report gateway metrics. The server
// asserts against this interface rather than a concrete store type, so a wrapper or an
// alternative implementation either satisfies it or is reported at startup instead of
// silently producing empty metrics.
type MetricsSink interface {
	SetGatewayMetrics(metrics *GatewayMetrics)
}

// Handler serves the exposition endpoint.
func (m *GatewayMetrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *GatewayMetrics) incInFlight() {
	if m == nil {
		return
	}
	m.inFlight.Inc()
}

func (m *GatewayMetrics) decInFlight() {
	if m == nil {
		return
	}
	m.inFlight.Dec()
}

// ObserveGatewayCall records one finished request. Every method is nil-safe so callers
// do not have to branch on whether metrics are enabled.
// ObserveTraceCompletion counts one finished gateway call's fate in the conversion
// queue.
func (m *GatewayMetrics) ObserveTraceCompletion(outcome string) {
	if m == nil {
		return
	}
	m.traceCompletions.WithLabelValues(outcome).Inc()
}

// ObserveTraceSpans counts spans by what the OTLP exporter managed to do with them.
func (m *GatewayMetrics) ObserveTraceSpans(outcome string, count int) {
	if m == nil || count <= 0 {
		return
	}
	m.traceSpans.WithLabelValues(outcome).Add(float64(count))
}

func (m *GatewayMetrics) ObserveGatewayCall(sample GatewayCallSample) {
	if m == nil {
		return
	}
	model := metricsLabel(sample.Model)
	providerType := metricsLabel(sample.ProviderType)
	providerID := metricsLabel(sample.ProviderID)
	stream := strconv.FormatBool(sample.Stream)

	m.requests.WithLabelValues(m.labels(
		sample.ProjectID,
		model,
		providerType,
		providerID,
		metricsLabel(sample.ResourceID),
		strconv.Itoa(sample.StatusCode),
		metricsLabel(sample.ErrorCode),
		stream,
	)...).Inc()

	if sample.Duration > 0 {
		m.duration.WithLabelValues(m.labels(
			sample.ProjectID,
			model,
			providerType,
			stream,
			metricsOutcome(sample.StatusCode),
		)...).Observe(sample.Duration.Seconds())
	}

	// Only non-zero counts create a series. Emitting a zero for every kind on every
	// request would multiply the series count by five for no information.
	for kind, count := range map[string]int64{
		"prompt":      sample.Usage.PromptTokens,
		"completion":  sample.Usage.CompletionTokens,
		"cached":      sample.Usage.CachedInputTokens,
		"cache_write": sample.Usage.CacheWriteInputTokens,
		"reasoning":   sample.Usage.ReasoningOutputTokens,
	} {
		if count <= 0 {
			continue
		}
		m.tokens.WithLabelValues(m.labels(sample.ProjectID, model, providerType, providerID, kind)...).Add(float64(count))
	}

	if sample.Usage.CostUSD > 0 {
		m.cost.WithLabelValues(m.labels(sample.ProjectID, model, providerType, providerID)...).Add(sample.Usage.CostUSD)
	}

	// Per-candidate attempt counts and attempt duration. A rejected request has no
	// attempts, so this block naturally does nothing for that path.
	var attemptSum time.Duration
	for _, attempt := range sample.Attempts {
		attemptProviderType := metricsLabel(attempt.ProviderType)
		attemptProviderID := metricsLabel(attempt.ProviderID)
		attemptResourceID := metricsLabel(attempt.ResourceID)
		attemptStatusCode := strconv.Itoa(attempt.StatusCode)
		attemptErrorCode := metricsLabel(attempt.ErrorCode)
		invoked := strconv.FormatBool(attempt.Invoked)
		m.routeAttempts.WithLabelValues(m.labels(
			sample.ProjectID,
			model,
			attemptProviderType,
			attemptProviderID,
			attemptResourceID,
			attemptStatusCode,
			attemptErrorCode,
			invoked,
		)...).Inc()

		if attempt.Invoked && attempt.Duration > 0 {
			attemptSum += attempt.Duration
			m.attemptDuration.WithLabelValues(m.labels(
				sample.ProjectID,
				model,
				attemptProviderType,
				metricsOutcome(attempt.StatusCode),
			)...).Observe(attempt.Duration.Seconds())
		}
	}

	// routed_requests_total is the attempt-bearing denominator for the
	// failover-depth ratio: only requests that actually tried a candidate count.
	if len(sample.Attempts) > 0 {
		m.routedRequests.WithLabelValues(m.labels(sample.ProjectID, model, providerType, stream)...).Inc()
	}

	// Overhead is reported for every admitted request: with no attempts the sum is
	// zero, so a request that failed during route selection contributes its full
	// elapsed time. Rejected requests never reach this gate — their duration is
	// zero by convention (see RecordRejectedRequest), and zeroes create no series.
	if sample.Duration > 0 {
		overhead := sample.Duration - attemptSum
		if overhead < 0 {
			overhead = 0
		}
		m.overhead.WithLabelValues(m.labels(sample.ProjectID, stream)...).Observe(overhead.Seconds())
	}
}

// labels appends the project id when that label is enabled. It takes the project id as
// a named leading parameter rather than as the last variadic value, so a future caller
// cannot silently shift it into a neighbouring label position.
//
// The project label is fixed at construction: a CounterVec's label set is part of its
// identity, so it cannot be varied per observation.
func (m *GatewayMetrics) labels(projectID string, values ...string) []string {
	if !m.projectLabel {
		return values
	}
	out := make([]string, 0, len(values)+1)
	out = append(out, values...)
	return append(out, metricsLabel(projectID))
}

func metricsLabel(value string) string {
	if value == "" {
		return metricsLabelUnset
	}
	return value
}

func metricsOutcome(statusCode int) string {
	if statusCode >= 200 && statusCode < 400 {
		return "success"
	}
	return "error"
}

// gatewayInFlight tracks concurrency for one model API route. The decrement is
// deferred so a handler that panics or writes a partial stream cannot leak the gauge
// upward for the lifetime of the process.
func (s *Server) gatewayInFlight(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.metrics.incInFlight()
		defer s.metrics.decInFlight()
		next(w, r)
	}
}

// handleMetrics serves the Prometheus exposition endpoint.
//
// Metrics disclose internal topology — model names, provider and resource identifiers,
// spend — so the endpoint always authenticates. It fails closed: with no usable token
// configured it reports 404 rather than degrading to anonymous access.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimSpace(s.config.MetricsToken)
	if token == "" {
		token = strings.TrimSpace(s.config.AdminToken)
	}
	if token == "" {
		http.NotFound(w, r)
		return
	}
	if !metricsTokenMatches(token, r.Header.Get("Authorization")) {
		writeError(w, r, NewHTTPError(http.StatusUnauthorized, "unauthorized", "Metrics token is required"))
		return
	}
	s.metrics.Handler().ServeHTTP(w, r)
}

func (s *Server) metricsMethodNotAllowed(allowedMethod string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethod)
	return func(w http.ResponseWriter, r *http.Request) {
		if s.metrics == nil {
			http.NotFound(w, r)
			return
		}
		reject(w, r)
	}
}

// metricsTokenMatches accepts the token only from an Authorization: Bearer header.
// A query parameter is deliberately not supported: it would land the credential in
// access logs and proxy history.
//
// Both sides are hashed before comparison so the comparison runs over fixed-length
// input: ConstantTimeCompare returns early on a length mismatch, which would otherwise
// leak the token length.
func metricsTokenMatches(expected string, authorization string) bool {
	presented := strings.TrimSpace(authorization)
	const prefix = "Bearer "
	if len(presented) <= len(prefix) || !strings.EqualFold(presented[:len(prefix)], prefix) {
		return false
	}
	presented = strings.TrimSpace(presented[len(prefix):])
	if presented == "" || expected == "" {
		return false
	}
	presentedSum := sha256.Sum256([]byte(presented))
	expectedSum := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(presentedSum[:], expectedSum[:]) == 1
}
