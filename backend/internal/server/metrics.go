package server

import (
	"crypto/sha256"
	"crypto/subtle"
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

	m.registry.MustRegister(m.requests, m.duration, m.inFlight, m.tokens, m.cost)
	// Process and Go runtime metrics are what an operator reaches for first when the
	// gateway itself is the suspect, and they cost nothing to collect.
	m.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
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
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
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
