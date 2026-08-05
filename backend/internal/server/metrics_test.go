package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newMetricsTestServer(t *testing.T, projectLabel bool) (*GormStore, *Server, string) {
	t.Helper()
	store, secret, _ := newResourceRoutedStore(t, ProviderMock)
	config := Config{
		AdminToken:          "dev_admin_token",
		MetricsEnabled:      true,
		MetricsProjectLabel: projectLabel,
	}
	server := NewWithConfig(store, config)
	if server.metrics == nil {
		t.Fatal("expected metrics to be enabled")
	}
	return store, server, secret
}

func chatOnce(t *testing.T, app http.Handler, secret string, stream bool) int {
	t.Helper()
	body := map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	if stream {
		body["stream"] = true
	}
	return doJSON(t, app, http.MethodPost, "/v1/chat/completions", body, secret).Code
}

func TestMetricsRecordCompletedCall(t *testing.T) {
	_, server, secret := newMetricsTestServer(t, false)
	app := server.Handler()

	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}

	if got := testutil.CollectAndCount(server.metrics.requests); got != 1 {
		t.Fatalf("expected one request series, got %d", got)
	}
	if got := testutil.ToFloat64(server.metrics.requests.WithLabelValues(
		"gpt-4.1-mini", ProviderMock, "prv_"+ProviderMock, "rsrc_"+ProviderMock, "200", metricsLabelUnset, "false",
	)); got != 1 {
		t.Fatalf("expected the request to be counted once, got %v", got)
	}
	if got := testutil.CollectAndCount(server.metrics.tokens); got == 0 {
		t.Fatal("expected token series to be recorded")
	}
	if got := testutil.CollectAndCount(server.metrics.duration); got != 1 {
		t.Fatalf("expected one duration series, got %d", got)
	}
}

func TestMetricsRecordStreamingCall(t *testing.T) {
	_, server, secret := newMetricsTestServer(t, false)
	app := server.Handler()

	if code := chatOnce(t, app, secret, true); code != http.StatusOK {
		t.Fatalf("streaming chat failed: %d", code)
	}

	if got := testutil.ToFloat64(server.metrics.requests.WithLabelValues(
		"gpt-4.1-mini", ProviderMock, "prv_"+ProviderMock, "rsrc_"+ProviderMock, "200", metricsLabelUnset, "true",
	)); got != 1 {
		t.Fatalf("expected a stream=true series, got %v", got)
	}
}

// A request refused before routing contributes to the request counter only: it never
// reached a provider, so tokens, cost and duration would all be fabrications.
func TestMetricsRecordRejectedCall(t *testing.T) {
	_, server, _ := newMetricsTestServer(t, false)
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, "thk_not_a_real_key")
	if resp.Code == http.StatusOK {
		t.Fatal("expected the unauthorized request to be refused")
	}

	if got := testutil.CollectAndCount(server.metrics.tokens); got != 0 {
		t.Fatalf("a rejected request must not record tokens, got %d series", got)
	}
	if got := testutil.CollectAndCount(server.metrics.cost); got != 0 {
		t.Fatalf("a rejected request must not record cost, got %d series", got)
	}
}

func TestMetricsRecordAPIKeyRateLimitHitsWithMaskedIdentifier(t *testing.T) {
	store, server, secret := newMetricsTestServer(t, false)
	key := store.ListAPIKeys()[0]
	rpm := int64(1)
	if _, err := store.UpdateAPIKey(key.ID, APIKey{RateLimitRPM: &rpm, RateLimitSet: true}); err != nil {
		t.Fatal(err)
	}
	app := server.Handler()
	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("first chat failed: %d", code)
	}
	if code := chatOnce(t, app, secret, false); code != http.StatusTooManyRequests {
		t.Fatalf("second chat should be rate limited: %d", code)
	}
	masked := maskedAPIKeyMetricLabel(key.ID)
	if strings.Contains(masked, key.ID) {
		t.Fatalf("masked metric identifier exposed the API key ID: %q", masked)
	}
	if got := testutil.ToFloat64(server.metrics.rateLimitHits.WithLabelValues("api_key", "rpm", masked)); got != 1 {
		t.Fatalf("expected one RPM limit hit, got %v", got)
	}
}

func TestMetricsRecordInheritedRateLimitScopeWithoutPerKeySeries(t *testing.T) {
	store, server, secret := newMetricsTestServer(t, false)
	project := store.ListProjects()[0]
	store.CreateResource("quota-policies", AdminResource{
		Name:   "Project RPM",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":          "project",
			"scope_id":       project.ID,
			"rate_limit_rpm": int64(1),
		},
	})
	app := server.Handler()
	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("first chat failed: %d", code)
	}
	if code := chatOnce(t, app, secret, false); code != http.StatusTooManyRequests {
		t.Fatalf("second chat should be rate limited: %d", code)
	}
	if got := testutil.ToFloat64(server.metrics.rateLimitHits.WithLabelValues("project", "rpm", metricsLabelUnset)); got != 1 {
		t.Fatalf("expected one project RPM limit hit without a key reference, got %v", got)
	}
}

// The cost reported to Prometheus must be the priced value, which is only computed
// inside FinishCall — instrumenting earlier would have reported zero.
func TestMetricsCostMatchesUsageRecord(t *testing.T) {
	store, server, secret := newMetricsTestServer(t, false)
	store.AddModel(Model{
		Name:                "gpt-4.1-mini",
		Modality:            "chat",
		Status:              StatusActive,
		InputPriceUSDPer1M:  1000,
		OutputPriceUSDPer1M: 2000,
	})
	app := server.Handler()

	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}

	recorded := testutil.ToFloat64(server.metrics.cost.WithLabelValues(
		"gpt-4.1-mini", ProviderMock, "prv_"+ProviderMock,
	))
	if recorded <= 0 {
		t.Fatalf("expected a priced cost to reach the counter, got %v", recorded)
	}

	var total float64
	for _, record := range store.ListUsageRecords() {
		total += record.CostUSD
	}
	if total <= 0 {
		t.Fatal("expected a usage record with cost")
	}
	if diff := recorded - total; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("metric cost %v does not match usage record cost %v", recorded, total)
	}
}

func TestMetricsProjectLabelIsOptional(t *testing.T) {
	_, off, secret := newMetricsTestServer(t, false)
	if code := chatOnce(t, off.Handler(), secret, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}
	// Seven label values, without project_id.
	if got := testutil.ToFloat64(off.metrics.requests.WithLabelValues(
		"gpt-4.1-mini", ProviderMock, "prv_"+ProviderMock, "rsrc_"+ProviderMock, "200", metricsLabelUnset, "false",
	)); got != 1 {
		t.Fatalf("expected a series without project_id, got %v", got)
	}

	storeOn, on, secretOn := newMetricsTestServer(t, true)
	project := storeOn.ListProjects()[0]
	if code := chatOnce(t, on.Handler(), secretOn, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}
	if got := testutil.ToFloat64(on.metrics.requests.WithLabelValues(
		"gpt-4.1-mini", ProviderMock, "prv_"+ProviderMock, "rsrc_"+ProviderMock, "200", metricsLabelUnset, "false", project.ID,
	)); got != 1 {
		t.Fatalf("expected a series carrying project_id, got %v", got)
	}
}

func TestMetricsEndpointRequiresBearerToken(t *testing.T) {
	_, server, _ := newMetricsTestServer(t, false)
	app := server.Handler()

	anonymous := doJSON(t, app, http.MethodGet, "/metrics", nil, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous scrape must be refused, got %d", anonymous.Code)
	}

	wrong := doRawRequest(t, app, http.MethodGet, "/metrics", map[string]string{"Authorization": "Bearer nope"})
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token must be refused, got %d", wrong.Code)
	}

	// A token in the query string must not work: it would leak into access logs.
	query := doRawRequest(t, app, http.MethodGet, "/metrics?token=dev_admin_token", nil)
	if query.Code != http.StatusUnauthorized {
		t.Fatalf("a query-string token must be refused, got %d", query.Code)
	}
}

func TestMetricsEndpointServesExposition(t *testing.T) {
	_, server, secret := newMetricsTestServer(t, false)
	app := server.Handler()
	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}

	resp := doRawRequest(t, app, http.MethodGet, "/metrics", map[string]string{"Authorization": "Bearer dev_admin_token"})
	if resp.Code != http.StatusOK {
		t.Fatalf("authorised scrape failed: %d %s", resp.Code, resp.Body)
	}
	body := resp.Body
	for _, want := range []string{
		"tokenhub_gateway_requests_total",
		"tokenhub_gateway_request_duration_seconds",
		"tokenhub_gateway_tokens_total",
		"tokenhub_gateway_requests_in_flight",
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("exposition is missing %s", want)
		}
	}
}

func TestMetricsEndpointUsesDedicatedToken(t *testing.T) {
	store, _, _ := newResourceRoutedStore(t, ProviderMock)
	server := NewWithConfig(store, Config{
		AdminToken:     "dev_admin_token",
		MetricsToken:   "scrape-token",
		MetricsEnabled: true,
	})
	app := server.Handler()

	if resp := doRawRequest(t, app, http.MethodGet, "/metrics", map[string]string{"Authorization": "Bearer scrape-token"}); resp.Code != http.StatusOK {
		t.Fatalf("dedicated token must be accepted, got %d", resp.Code)
	}
	// Once a dedicated token is configured the admin token must no longer be accepted,
	// so revoking the scrape credential is independent of admin access.
	if resp := doRawRequest(t, app, http.MethodGet, "/metrics", map[string]string{"Authorization": "Bearer dev_admin_token"}); resp.Code != http.StatusUnauthorized {
		t.Fatalf("admin token must not work once a metrics token is set, got %d", resp.Code)
	}
}

func TestMetricsDisabledHidesEndpoint(t *testing.T) {
	store, _, _ := newResourceRoutedStore(t, ProviderMock)
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: false})
	if server.metrics != nil {
		t.Fatal("metrics must not be constructed when disabled")
	}
	resp := doRawRequest(t, server.Handler(), http.MethodGet, "/metrics", map[string]string{"Authorization": "Bearer dev_admin_token"})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("disabled metrics must 404, got %d", resp.Code)
	}
}

func TestMetricsInFlightReturnsToZero(t *testing.T) {
	_, server, secret := newMetricsTestServer(t, false)
	app := server.Handler()

	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}
	if got := testutil.ToFloat64(server.metrics.inFlight); got != 0 {
		t.Fatalf("in-flight must return to zero after a request, got %v", got)
	}
}

func TestMetricsTokenMatching(t *testing.T) {
	cases := []struct {
		name          string
		authorization string
		want          bool
	}{
		{"exact bearer", "Bearer secret", true},
		{"case-insensitive scheme", "bearer secret", true},
		{"surrounding whitespace", "  Bearer  secret  ", true},
		{"wrong token", "Bearer other", false},
		{"missing scheme", "secret", false},
		{"empty", "", false},
		{"scheme only", "Bearer ", false},
		{"basic auth", "Basic c2VjcmV0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := metricsTokenMatches("secret", tc.authorization); got != tc.want {
				t.Fatalf("metricsTokenMatches(%q) = %v, want %v", tc.authorization, got, tc.want)
			}
		})
	}
}

func TestGatewayMetricsNilIsSafe(t *testing.T) {
	var m *GatewayMetrics
	m.ObserveGatewayCall(GatewayCallSample{
		Model:    "x",
		Duration: time.Second,
		Attempts: []GatewayAttemptSample{{ProviderType: "t", Invoked: true, Duration: time.Second}},
	})
	m.incInFlight()
	m.decInFlight()
	if m.Handler() == nil {
		t.Fatal("nil metrics must still return a handler")
	}
}

// doRawRequest issues a request with explicit headers, so tests can exercise the
// Authorization handling that doJSON always fills in for them.
func doRawRequest(t *testing.T, handler http.Handler, method string, path string, headers map[string]string) responseBody {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return responseBody{Code: rr.Code, Body: rr.Body.String()}
}

// A rejected request carries whatever model name the client sent, including names that
// do not exist. Using it verbatim would let anyone mint unbounded series by looping
// over random names, so unknown models collapse to a single label value.
func TestMetricsUnknownModelDoesNotInflateCardinality(t *testing.T) {
	_, server, _ := newMetricsTestServer(t, false)
	app := server.Handler()

	for i := 0; i < 25; i++ {
		doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model":    fmt.Sprintf("attacker-model-%d", i),
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		}, "thk_not_a_real_key")
	}

	if got := testutil.CollectAndCount(server.metrics.requests); got > 2 {
		t.Fatalf("25 distinct unknown model names must not create 25 series, got %d", got)
	}
}

// A model the catalog knows must still be reported by name, otherwise operators lose
// the ability to see which model is being throttled.
func TestMetricsKnownModelSurvivesRejection(t *testing.T) {
	store, server, _ := newMetricsTestServer(t, false)
	app := server.Handler()

	doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, "thk_not_a_real_key")

	if got := store.knownModelLabel("gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("a catalog model must keep its name, got %q", got)
	}
	if got := store.knownModelLabel("definitely-not-a-model"); got != "unknown" {
		t.Fatalf("an unknown model must collapse, got %q", got)
	}
}

// A streaming request refused before routing must not be mislabelled as non-streaming.
func TestMetricsRejectedStreamingCallKeepsStreamLabel(t *testing.T) {
	store, server, secret := newMetricsTestServer(t, false)
	// Exhaust the key's request quota so the next call is refused inside StartCall.
	keys := store.ListAPIKeys()
	if len(keys) == 0 {
		t.Fatal("expected a seeded api key")
	}
	if err := store.db.Model(&APIKey{}).Where("id = ?", keys[0].ID).
		Update("limit_daily_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	app := server.Handler()

	if code := chatOnce(t, app, secret, true); code != http.StatusOK {
		t.Fatalf("first streaming call should succeed: %d", code)
	}
	if code := chatOnce(t, app, secret, true); code == http.StatusOK {
		t.Fatal("second streaming call should exceed the quota")
	}

	// Asserted positively: a "series is absent" check would silently pass if the
	// error code or status ever changed.
	rejected := testutil.ToFloat64(server.metrics.requests.WithLabelValues(
		"gpt-4.1-mini", metricsLabelUnset, metricsLabelUnset, metricsLabelUnset, "429", "quota_exceeded", "true",
	))
	if rejected != 1 {
		t.Fatalf("the refused streaming request must be counted with stream=true, got %v", rejected)
	}
	sameButNotStreaming := testutil.ToFloat64(server.metrics.requests.WithLabelValues(
		"gpt-4.1-mini", metricsLabelUnset, metricsLabelUnset, metricsLabelUnset, "429", "quota_exceeded", "false",
	))
	if sameButNotStreaming != 0 {
		t.Fatalf("it must not also appear as stream=false, got %v", sameButNotStreaming)
	}
}

// newMetricsFailoverGateway wires one model to two openai_compatible upstreams in
// priority order and enables metrics. It mirrors newStreamFailoverGateway but uses
// NewWithConfig so the /metrics endpoint is available.
func newMetricsFailoverGateway(t *testing.T, primaryURL string, secondaryURL string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "metrics-failover", Status: StatusActive})
	const model = "metrics-failover-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "metrics-failover-key",
		Allowed: []string{model},
		Status:  StatusActive,
	}, "thk_metrics_failover")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	for index, upstreamURL := range []string{primaryURL, secondaryURL} {
		provider := store.AddProvider(Provider{
			ID:      fmt.Sprintf("prv_metrics_%d", index),
			Name:    fmt.Sprintf("metrics-%d", index),
			Type:    ProviderOpenAICompatible,
			BaseURL: upstreamURL,
			Status:  StatusActive,
			Healthy: true,
		})
		resource, err := store.AddProviderResource(ProviderResource{
			ID:             fmt.Sprintf("rsrc_metrics_%d", index),
			ProviderID:     provider.ID,
			Name:           fmt.Sprintf("metrics-resource-%d", index),
			ResourceType:   "openai",
			Status:         StatusActive,
			Healthy:        true,
			Priority:       1,
			Weight:         100,
			MaxConcurrency: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		store.AddRoute(ModelRoute{
			ID:                 fmt.Sprintf("route_metrics_%d", index),
			ModelName:          model,
			ProviderID:         provider.ID,
			ProviderResourceID: resource.ID,
			ProviderModel:      fmt.Sprintf("upstream-model-%d", index),
			Priority:           index + 1,
			Weight:             100,
			Status:             StatusActive,
			Strategy:           RouteStrategyPriorityOnly,
		})
	}
	server := NewWithConfig(store, Config{
		AdminToken:     "dev_admin_token",
		MetricsEnabled: true,
	})
	if server.metrics == nil {
		t.Fatal("expected metrics to be enabled")
	}
	return server, secret
}

// findMetricLine returns the first /metrics exposition line containing every fragment.
// It avoids importing the prometheus client_model package (indirect dependency) and is
// resilient to label ordering.
func findMetricLine(body string, fragments ...string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		ok := true
		for _, f := range fragments {
			if !strings.Contains(line, f) {
				ok = false
				break
			}
		}
		if ok {
			return line
		}
	}
	return ""
}

// metricLineValue parses the trailing float from an exposition line.
func metricLineValue(t *testing.T, line string) float64 {
	t.Helper()
	line = strings.TrimSpace(line)
	idx := strings.LastIndexByte(line, ' ')
	if idx < 0 {
		t.Fatalf("no value in metric line: %q", line)
	}
	v, err := strconv.ParseFloat(line[idx+1:], 64)
	if err != nil {
		t.Fatalf("parse metric value %q: %v", line[idx+1:], err)
	}
	return v
}

func scrapeMetrics(t *testing.T, handler http.Handler) string {
	t.Helper()
	resp := doRawRequest(t, handler, http.MethodGet, "/metrics", map[string]string{"Authorization": "Bearer dev_admin_token"})
	if resp.Code != http.StatusOK {
		t.Fatalf("metrics scrape failed: %d %s", resp.Code, resp.Body)
	}
	return resp.Body
}

// A failover produces two route_attempts_total series but only one requests_total.
func TestMetricsFailoverAttributesEveryAttempt(t *testing.T) {
	var secondaryHits int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryHits, 1)
		writeChatStreamChunk(w, "recovered")
	}))
	defer secondary.Close()

	server, secret := newMetricsFailoverGateway(t, primary.URL, secondary.URL)
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "metrics-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected fallback to succeed, got %d: %s", resp.Code, resp.Body.String())
	}

	body := scrapeMetrics(t, server.Handler())

	primaryAttempt := findMetricLine(body, "tokenhub_gateway_route_attempts_total", `provider_id="prv_metrics_0"`, `invoked="true"`, `status_code="429"`)
	if primaryAttempt == "" {
		t.Fatalf("missing primary attempt series in metrics:\n%s", body)
	}
	if metricLineValue(t, primaryAttempt) != 1 {
		t.Fatalf("expected exactly one primary attempt")
	}
	secondaryAttempt := findMetricLine(body, "tokenhub_gateway_route_attempts_total", `provider_id="prv_metrics_1"`, `invoked="true"`, `status_code="200"`)
	if secondaryAttempt == "" {
		t.Fatalf("missing secondary attempt series in metrics:\n%s", body)
	}

	if got := testutil.ToFloat64(server.metrics.requests.WithLabelValues(
		"metrics-failover-model", ProviderOpenAICompatible, "prv_metrics_1", "rsrc_metrics_1", "200", metricsLabelUnset, "true",
	)); got != 1 {
		t.Fatalf("expected one logical request, got %v", got)
	}

	upstreamPrimary := findMetricLine(body, "tokenhub_gateway_attempt_duration_seconds", `provider_type="openai_compatible"`)
	if upstreamPrimary == "" {
		t.Fatalf("missing attempt duration series in metrics:\n%s", body)
	}

	overhead := findMetricLine(body, "tokenhub_gateway_overhead_seconds_bucket")
	if overhead == "" {
		t.Fatalf("missing overhead series in metrics:\n%s", body)
	}
}

// A single successful request records exactly one invoked attempt.
func TestMetricsDirectSuccessRecordsSingleAttempt(t *testing.T) {
	_, server, secret := newMetricsTestServer(t, false)
	app := server.Handler()
	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}

	body := scrapeMetrics(t, app)
	attempt := findMetricLine(body, "tokenhub_gateway_route_attempts_total", `provider_id="prv_`+ProviderMock+`"`, `invoked="true"`, `status_code="200"`)
	if attempt == "" {
		t.Fatalf("missing direct attempt series in metrics:\n%s", body)
	}
	if metricLineValue(t, attempt) != 1 {
		t.Fatalf("expected exactly one direct attempt")
	}
}

// A capacity-skipped attempt contributes to route_attempts_total with invoked="false"
// but does not create an attempt_duration_seconds series.
func TestMetricsNonInvokedAttemptSkipsAttemptDuration(t *testing.T) {
	m := NewGatewayMetrics(false)
	m.ObserveGatewayCall(GatewayCallSample{
		Model:        "m",
		ProviderType: "t",
		ProviderID:   "p",
		ResourceID:   "r",
		StatusCode:   200,
		Duration:     time.Second,
		Attempts: []GatewayAttemptSample{
			{ProviderType: "t", ProviderID: "p1", ResourceID: "r1", StatusCode: 429, ErrorCode: "capacity_exceeded", Invoked: false},
			{ProviderType: "t", ProviderID: "p2", ResourceID: "r2", StatusCode: 200, Invoked: true, Duration: 500 * time.Millisecond},
		},
	})

	if got := testutil.ToFloat64(m.routeAttempts.WithLabelValues("m", "t", "p1", "r1", "429", "capacity_exceeded", "false")); got != 1 {
		t.Fatalf("expected non-invoked attempt counter to be 1, got %v", got)
	}
	if got := testutil.ToFloat64(m.routeAttempts.WithLabelValues("m", "t", "p2", "r2", "200", metricsLabelUnset, "true")); got != 1 {
		t.Fatalf("expected invoked attempt counter to be 1, got %v", got)
	}
	if got := testutil.CollectAndCount(m.attemptDuration); got != 1 {
		t.Fatalf("expected one attempt_duration series (only invoked), got %d", got)
	}
}

// Overhead is clamped at zero when the sum of attempt latencies exceeds elapsed time.
func TestMetricsOverheadClampsAtZero(t *testing.T) {
	m := NewGatewayMetrics(false)
	m.ObserveGatewayCall(GatewayCallSample{
		Model:      "m",
		StatusCode: 200,
		Duration:   time.Second,
		Attempts: []GatewayAttemptSample{
			{StatusCode: 200, Invoked: true, Duration: 1500 * time.Millisecond},
		},
	})

	handler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape failed: %d", rec.Code)
	}
	sumLine := findMetricLine(rec.Body.String(), "tokenhub_gateway_overhead_seconds_sum")
	if sumLine == "" {
		t.Fatal("missing overhead sum")
	}
	if got := metricLineValue(t, sumLine); got != 0 {
		t.Fatalf("overhead must be clamped at 0, got %v", got)
	}
}

// A rejected request never reached a provider, so it must not create attempt series.
func TestMetricsRejectedRequestAddsNoAttemptSeries(t *testing.T) {
	_, server, _ := newMetricsTestServer(t, false)
	app := server.Handler()

	doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, "thk_not_a_real_key")

	body := scrapeMetrics(t, app)
	if strings.Contains(body, "tokenhub_gateway_route_attempts_total{") {
		t.Fatalf("rejected request must not produce route_attempts_total series:\n%s", body)
	}
	if strings.Contains(body, "tokenhub_gateway_attempt_duration_seconds{") {
		t.Fatalf("rejected request must not produce attempt_duration_seconds series:\n%s", body)
	}
	if strings.Contains(body, "tokenhub_gateway_overhead_seconds{") {
		t.Fatalf("rejected request must not produce overhead_seconds series:\n%s", body)
	}
	if strings.Contains(body, "tokenhub_gateway_routed_requests_total{") {
		t.Fatalf("rejected request must not produce routed_requests_total series:\n%s", body)
	}
}

// An image job reaches the same observation funnel as chat: requests_total counts
// once and the routed attempts produce attempt, upstream-duration and overhead
// series, so the failover-depth ratio is not diluted by image traffic.
func TestMetricsImageJobAttributesAttempts(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Metrics Image Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "metrics-image-key", Allowed: []string{openAIImageModelName}, Status: StatusActive,
	}, "thk_metrics_image")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_metrics_image", Name: "Metrics Image Provider", Type: ProviderOpenAI, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_metrics_image", ProviderID: provider.ID, Name: "Metrics Image Resource",
		ResourceType: "openai", Status: StatusActive, Healthy: true,
		Priority: 1, Weight: 100, MaxConcurrency: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID,
		ProviderModel: openAIImageModelName, Priority: 1, Weight: 100, Status: StatusActive,
	})
	server := NewWithConfig(store, Config{
		AdminToken: "dev_admin_token", SecretKey: "metrics-image-secret",
		MetricsEnabled: true, ImageStorageDir: t.TempDir(),
	})
	if server.metrics == nil {
		t.Fatal("expected metrics to be enabled")
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	server.imageRunner = func(_ context.Context, _ RouteSelection, _ ImageJob) ([]byte, string, Usage, error) {
		return realPNGFixture(t), "", Usage{PromptTokens: 7, CompletionTokens: 13, TotalTokens: 20}, nil
	}
	handler := server.Handler()

	create := doImageJSON(t, handler, http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "metrics image",
	}, secret, map[string]string{"Prefer": "respond-async"})
	if create.Code != http.StatusAccepted {
		t.Fatalf("create image job: status=%d body=%s", create.Code, create.Body)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(create.Body), &created); err != nil {
		t.Fatal(err)
	}
	jobID, _ := created["id"].(string)
	if jobID == "" {
		t.Fatalf("missing image job id: %s", create.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, ok := store.GetImageJob(jobID)
		if ok && job.Status == imageJobStatusCompleted {
			break
		}
		if ok && job.Status == imageJobStatusFailed {
			t.Fatalf("image job failed: %+v", job)
		}
		if time.Now().After(deadline) {
			t.Fatalf("image job did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := testutil.ToFloat64(server.metrics.requests.WithLabelValues(
		openAIImageModelName, ProviderOpenAI, provider.ID, resource.ID, "200", metricsLabelUnset, "false",
	)); got != 1 {
		t.Fatalf("expected the image request to be counted once, got %v", got)
	}
	if got := testutil.ToFloat64(server.metrics.routeAttempts.WithLabelValues(
		openAIImageModelName, ProviderOpenAI, provider.ID, resource.ID, "200", metricsLabelUnset, "true",
	)); got != 1 {
		t.Fatalf("expected one invoked attempt series, got %v", got)
	}
	if got := testutil.CollectAndCount(server.metrics.attemptDuration); got != 1 {
		t.Fatalf("expected one attempt duration series, got %d", got)
	}
	if got := testutil.CollectAndCount(server.metrics.overhead); got != 1 {
		t.Fatalf("expected one overhead series, got %d", got)
	}
	if got := testutil.ToFloat64(server.metrics.routedRequests.WithLabelValues(
		openAIImageModelName, ProviderOpenAI, "false",
	)); got != 1 {
		t.Fatalf("expected one routed request, got %v", got)
	}
}

// A request admitted for routing that fails before any candidate attempt — route
// selection, image claim, no-route handling — still records overhead: with no
// attempts the sum is zero, so overhead is the full elapsed time. Rejected
// requests keep the zero-duration convention and stay out of the histogram.
func TestMetricsAdmittedZeroAttemptFailureRecordsOverhead(t *testing.T) {
	m := NewGatewayMetrics(false)
	m.ObserveGatewayCall(GatewayCallSample{
		Model: "m", ProviderType: "t", ProviderID: "p", ResourceID: "r",
		StatusCode: 503, ErrorCode: "no_route", Duration: 250 * time.Millisecond,
	})

	if got := testutil.CollectAndCount(m.attemptDuration); got != 0 {
		t.Fatalf("a failed route selection must not create attempt_duration series, got %d", got)
	}
	if got := testutil.CollectAndCount(m.overhead); got != 1 {
		t.Fatalf("an admitted zero-attempt failure must record overhead, got %d series", got)
	}
	handler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)
	sumLine := findMetricLine(rec.Body.String(), "tokenhub_gateway_overhead_seconds_sum")
	if sumLine == "" {
		t.Fatal("missing overhead sum")
	}
	if got := metricLineValue(t, sumLine); got != 0.25 {
		t.Fatalf("overhead must equal the full elapsed time when no attempt ran, got %v", got)
	}
}

// routed_requests_total is the attempt-bearing denominator for the failover-depth
// ratio: only requests that actually tried a candidate count, so a rejection burst
// cannot make the ratio dip below 1 for perfectly healthy traffic.
func TestMetricsRoutedRequestsCountsOnlyAttemptBearingRequests(t *testing.T) {
	m := NewGatewayMetrics(false)
	m.ObserveGatewayCall(GatewayCallSample{
		Model: "m", ProviderType: "t", Duration: time.Second,
		Attempts: []GatewayAttemptSample{{StatusCode: 200, Invoked: true}},
	})
	// Admitted but no candidate tried: routed traffic, not a routed request.
	m.ObserveGatewayCall(GatewayCallSample{
		Model: "m", ProviderType: "t", ProviderID: "p", StatusCode: 503, ErrorCode: "no_route", Duration: time.Second,
	})
	// Refused before routing: zero duration by convention.
	m.ObserveGatewayCall(GatewayCallSample{
		Model: "m", ProviderType: "t", ProviderID: "p", StatusCode: 429, ErrorCode: "quota_exceeded",
	})

	if got := testutil.ToFloat64(m.routedRequests.WithLabelValues("m", "t", "false")); got != 1 {
		t.Fatalf("expected exactly one attempt-bearing request, got %v", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues(
		"m", "t", "p", metricsLabelUnset, "503", "no_route", "false",
	)); got != 1 {
		t.Fatalf("the admitted failure must still count in requests_total, got %v", got)
	}
}

// The attempt window covers writing the stream to the client, so a slow reader
// blocks the gateway inside the attempt and that backpressure must appear in
// attempt_duration_seconds — not be misattributed to overhead. This is the
// documented semantics of the metric, pinned by a regression test.
func TestMetricsStreamAttemptDurationIncludesClientBackpressure(t *testing.T) {
	const backpressure = 500 * time.Millisecond
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// Enough payload to fill the socket buffers: the gateway blocks writing it
		// while the client below stalls, which is the slow-writer scenario.
		chunk := strings.Repeat("x", 4096)
		for i := 0; i < 6000; i++ {
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\""+chunk+"\"}}]}\n\n")
			if i%32 == 0 {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	server, secret := newMetricsFailoverGateway(t, upstream.URL, upstream.URL)
	gateway := httptest.NewServer(server.Handler())
	defer gateway.Close()

	payload, err := json.Marshal(map[string]any{
		"model":    "metrics-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Stall like a slow client: headers are in hand, but the body is not read for
	// a while, so the gateway's stream copy blocks and the attempt window grows.
	time.Sleep(backpressure)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	body := scrapeMetrics(t, server.Handler())
	sumLine := findMetricLine(body, "tokenhub_gateway_attempt_duration_seconds_sum")
	if sumLine == "" {
		t.Fatalf("missing attempt duration sum:\n%s", body)
	}
	if got := metricLineValue(t, sumLine); got < backpressure.Seconds()*0.6 {
		t.Fatalf("attempt duration must include the client backpressure, got %v", got)
	}
	overheadLine := findMetricLine(body, "tokenhub_gateway_overhead_seconds_sum")
	if overheadLine == "" {
		t.Fatalf("missing overhead sum:\n%s", body)
	}
	if got := metricLineValue(t, overheadLine); got >= backpressure.Seconds()*0.6 {
		t.Fatalf("overhead must not include client backpressure, got %v", got)
	}
}
