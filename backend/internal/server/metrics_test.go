package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	m.ObserveGatewayCall(GatewayCallSample{Model: "x", Duration: time.Second})
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
