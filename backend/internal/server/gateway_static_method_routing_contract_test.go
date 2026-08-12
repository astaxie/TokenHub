package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type staticGatewayMethodRoute struct {
	method    string
	wrong     string
	path      string
	anthropic bool
	inFlight  bool
	authCode  string
}

var staticGatewayMethodRoutes = []staticGatewayMethodRoute{
	{method: http.MethodGet, wrong: http.MethodPost, path: "/v1/models", authCode: "invalid_api_key"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/chat/completions", inFlight: true, authCode: "invalid_api_key"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/responses", inFlight: true, authCode: "invalid_api_key"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/responses/compact", inFlight: true, authCode: "invalid_api_key"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/embeddings", inFlight: true, authCode: "invalid_api_key"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/images/generations", authCode: "invalid_api_key"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/images/edits", authCode: "invalid_api_key"},
	{method: http.MethodGet, wrong: http.MethodPost, path: "/api/v1/analytics/token-costs", authCode: "invalid_analytics_credential"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/messages", anthropic: true, inFlight: true, authCode: "invalid_api_key"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/messages/count_tokens", anthropic: true, authCode: "invalid_api_key"},
}

func TestStaticGatewayMethodRoutesRejectWrongMethods(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	requestLogsBefore := len(store.ListRequestLogs())
	auditEventsBefore := len(store.ListAuditEvents())
	imageJobsBefore := len(store.ListImageJobs(1000))

	for _, route := range staticGatewayMethodRoutes {
		t.Run(route.wrong+" "+route.path, func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrong, route.path, "")
			if route.anthropic {
				assertAnthropicMethodError(t, response)
			} else {
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			}
			assertAllowHeader(t, response, route.method)
		})
	}

	if got := len(store.ListRequestLogs()); got != requestLogsBefore {
		t.Fatalf("wrong methods created request logs: got %d, want %d", got, requestLogsBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditEventsBefore {
		t.Fatalf("wrong methods created audit events: got %d, want %d", got, auditEventsBefore)
	}
	if got := len(store.ListImageJobs(1000)); got != imageJobsBefore {
		t.Fatalf("wrong methods created image jobs: got %d, want %d", got, imageJobsBefore)
	}
}

func TestStaticGatewayMethodRoutesReachAuthentication(t *testing.T) {
	app := newTestServer()
	for _, route := range staticGatewayMethodRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := methodRoutingRequest(app, route.method, route.path, "")
			assertAllowHeader(t, response, "")
			if route.anthropic {
				assertAnthropicError(t, response, http.StatusUnauthorized, "authentication_error", route.authCode, "Invalid API key")
			} else {
				assertJSONError(t, response, http.StatusUnauthorized, route.authCode)
			}
		})
	}
}

func TestStaticGatewayGETRoutesRejectRealHEAD(t *testing.T) {
	server := httptest.NewServer(newTestServer())
	t.Cleanup(server.Close)

	for _, path := range []string{"/v1/models", "/api/v1/analytics/token-costs"} {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405, got %d", response.StatusCode)
			}
			if got := response.Header.Get("Allow"); got != http.MethodGet {
				t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
			}
			if contentType := response.Header.Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
				t.Fatalf("content type = %q, want application/json", contentType)
			}
			if response.Header.Get("x-request-id") == "" {
				t.Fatal("x-request-id is empty")
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != 0 {
				t.Fatalf("HEAD response body = %q, want empty", body)
			}
		})
	}
}

func TestStaticGatewayMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newTestServer()
	for _, route := range staticGatewayMethodRoutes {
		t.Run(route.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.method)
			response := httptest.NewRecorder()

			app.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
				t.Fatalf("access-control-allow-methods = %q", got)
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestStaticGatewayMethodRoutesPreserveMetricsScope(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	spy := &incrementCountingGauge{Gauge: server.metrics.inFlight}
	server.metrics.inFlight = spy

	for _, route := range staticGatewayMethodRoutes {
		if !route.inFlight {
			continue
		}
		t.Run(route.path, func(t *testing.T) {
			inFlightBefore := spy.increments.Load()
			requestSeriesBefore, requestCountBefore := gatewayRequestMetricSnapshot(t, server.metrics)
			response := methodRoutingRequest(server.Handler(), route.wrong, route.path, "")
			if route.anthropic {
				assertAnthropicMethodError(t, response)
			} else {
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			}
			assertAllowHeader(t, response, route.method)
			if got := spy.increments.Load(); got != inFlightBefore {
				t.Fatalf("wrong method entered in-flight middleware: increments = %d, want %d", got, inFlightBefore)
			}
			requestSeriesAfter, requestCountAfter := gatewayRequestMetricSnapshot(t, server.metrics)
			if requestSeriesAfter != requestSeriesBefore || requestCountAfter != requestCountBefore {
				t.Fatalf(
					"wrong method changed gateway request metrics: series %d -> %d, count %v -> %v",
					requestSeriesBefore, requestSeriesAfter, requestCountBefore, requestCountAfter,
				)
			}

			response = methodRoutingRequest(server.Handler(), route.method, route.path, "")
			assertAllowHeader(t, response, "")
			if route.anthropic {
				assertAnthropicError(t, response, http.StatusUnauthorized, "authentication_error", route.authCode, "Invalid API key")
			} else {
				assertJSONError(t, response, http.StatusUnauthorized, route.authCode)
			}
			if got := spy.increments.Load(); got != inFlightBefore+1 {
				t.Fatalf("allowed method in-flight increments = %d, want %d", got, inFlightBefore+1)
			}
		})
	}
}

func gatewayRequestMetricSnapshot(t *testing.T, metrics *GatewayMetrics) (int, float64) {
	t.Helper()
	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "tokenhub_gateway_requests_total" {
			continue
		}
		var total float64
		for _, metric := range family.GetMetric() {
			total += metric.GetCounter().GetValue()
		}
		return len(family.GetMetric()), total
	}
	return 0, 0
}

type incrementCountingGauge struct {
	prometheus.Gauge
	increments atomic.Int64
}

func (g *incrementCountingGauge) Inc() {
	g.increments.Add(1)
	g.Gauge.Inc()
}

func assertAnthropicMethodError(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	assertAnthropicError(t, response, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "Method not allowed")
}

func assertAnthropicError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantType string, wantCode string, wantMessage string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("expected %d, got %d: %s", wantStatus, response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode Anthropic error: %v", err)
	}
	if payload.Type != "error" || payload.Error.Type != wantType || payload.Error.Code != wantCode || payload.Error.Message != wantMessage {
		t.Fatalf("unexpected Anthropic error: %#v", payload)
	}
	assertRequestID(t, response, payload.RequestID)
}
