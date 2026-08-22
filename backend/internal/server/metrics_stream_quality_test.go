package server

// Empty-body stream-quality tests live in their own file so metrics_test.go
// stays under the repository's 1500-line source ceiling.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// An upstream that answers a stream request with an empty 200 body is a
// confirmed empty success: the handler commits the response and TTFB is
// synthesized from the commit time — the moment the client finally saw the
// 200 — so the metric still records the latency the client experienced.
func TestMetricsEmptyBodySuccessRecordsSynthesizedTimeToFirstByte(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		// No bytes follow: the stream ends as an empty-body 200.
	}))
	defer upstream.Close()

	server, secret := newMetricsFailoverGateway(t, upstream.URL, "http://localhost:1")
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "metrics-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected the committed 200, got %d: %s", resp.Code, resp.Body.String())
	}

	body := scrapeMetrics(t, server.Handler())
	count := findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count", `provider_type="openai_compatible"`)
	if count == "" {
		t.Fatalf("missing TTFB count for an empty-body success:\n%s", body)
	}
	if got := metricLineValue(t, count); got != 1 {
		t.Fatalf("expected TTFB observed once for an empty-body success, got %v", got)
	}
	if strings.Contains(body, "tokenhub_gateway_stream_interruptions_total{") {
		t.Fatalf("an empty-body success must not count as an interruption:\n%s", body)
	}
}

// An empty-body success has no byte to point at, so firstByteTime synthesizes
// the commit time — the moment the client saw the 200. A later real write
// supersedes the synthesized time, and a second read is stable.
func TestStreamWriteTrackerEmptyBodySuccessSynthesizesCommitTime(t *testing.T) {
	tracker := &streamWriteTracker{writer: &bytes.Buffer{}}
	start := time.Now()
	tracker.ensureStarted()
	got := tracker.firstByteTime(true)
	if got.IsZero() || got.Before(start) || got.After(time.Now()) {
		t.Fatalf("synthesized first-byte time must fall within the commit window, got %v", got)
	}
	if again := tracker.firstByteTime(true); !again.Equal(got) {
		t.Fatalf("synthesized first-byte time drifted: %v then %v", got, again)
	}
	mid := time.Now()
	if _, err := tracker.Write([]byte("data: ok\n\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if written := tracker.firstByteTime(true); written.IsZero() || written.Before(mid) || written.After(time.Now()) {
		t.Fatalf("a real write must supersede the commit time, got %v", written)
	}
}

// Transport-originated idle timeouts are artifacts of the upstream connection,
// not of the provider surface, so the metric boundary collapses them to
// internal_error while HTTP-level codes keep their value.
func TestInterruptionErrorCodeNormalizesIdleTimeouts(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"provider_stream_idle_timeout", "internal_error"},
		{"codex_stream_idle_timeout", "internal_error"},
		{"codex_stream_incomplete", "internal_error"},
		{"codex_stream_failed", "internal_error"},
		{"provider_upstream_timeout", "internal_error"},
		{"provider_stream_interrupted", "internal_error"},
		{"provider_upstream_unreachable", "internal_error"},
		{"provider_stream_error", "provider_stream_error"},
		{"upstream_http_502", "upstream_http_502"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := interruptionErrorCode(tc.code); got != tc.want {
			t.Errorf("interruptionErrorCode(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}

	m := NewGatewayMetrics(false)
	m.ObserveGatewayCall(GatewayCallSample{
		Model:           "m",
		ProviderType:    "t",
		ProviderID:      "p",
		ResourceID:      "r",
		StatusCode:      502,
		ErrorCode:       "provider_stream_idle_timeout",
		Stream:          true,
		TimeToFirstByte: time.Second,
		StreamFailed:    true,
	})
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "internal_error")); got != 1 {
		t.Fatalf("expected one interruption labeled internal_error, got %v", got)
	}
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "provider_stream_idle_timeout")); got != 0 {
		t.Fatalf("the idle-timeout code must not leak through, got %v", got)
	}

	// Post-byte Codex transport failures collapse the same way.
	m.ObserveGatewayCall(GatewayCallSample{
		Model:           "m",
		ProviderType:    "t",
		ProviderID:      "p",
		ResourceID:      "r",
		StatusCode:      502,
		ErrorCode:       "codex_stream_failed",
		Stream:          true,
		TimeToFirstByte: time.Second,
		StreamFailed:    true,
	})
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "internal_error")); got != 2 {
		t.Fatalf("expected two interruptions labeled internal_error, got %v", got)
	}
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "codex_stream_failed")); got != 0 {
		t.Fatalf("the codex_stream_failed code must not leak through, got %v", got)
	}
}

// The interruption series keys off the StreamFailed flag, not the projected
// status: a committed stream keeps its 200, so a stale 5xx projection without
// the flag, or a flag without a first byte, must not count.
func TestMetricsStreamInterruptionRequiresFailedFlag(t *testing.T) {
	m := NewGatewayMetrics(false)
	m.ObserveGatewayCall(GatewayCallSample{
		Model: "m", ProviderType: "t", ProviderID: "p", ResourceID: "r",
		StatusCode: 502, ErrorCode: "provider_stream_error",
		Stream: true, TimeToFirstByte: time.Second, StreamFailed: false,
	})
	m.ObserveGatewayCall(GatewayCallSample{
		Model: "m", ProviderType: "t", ProviderID: "p", ResourceID: "r",
		StatusCode: 502, ErrorCode: "provider_stream_error",
		Stream: true, StreamFailed: true,
	})
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "provider_stream_error")); got != 0 {
		t.Fatalf("neither the status projection alone nor a flag without a first byte may count, got %v", got)
	}

	m.ObserveGatewayCall(GatewayCallSample{
		Model: "m", ProviderType: "t", ProviderID: "p", ResourceID: "r",
		StatusCode: 502, ErrorCode: "provider_stream_error",
		Stream: true, TimeToFirstByte: time.Second, StreamFailed: true,
	})
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "provider_stream_error")); got != 1 {
		t.Fatalf("a first byte plus the failed flag must count exactly one interruption, got %v", got)
	}
}

// An OpenAI-compatible upstream that ends its 200 stream with an explicit
// error frame is an interruption: the frame is forwarded once and the stream
// classifies as failed with provider_stream_error.
func TestMetricsChatInBandErrorCountsInterruption(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"partial\"},\"index\":0}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"error\":{\"type\":\"server_error\",\"message\":\"boom\"}}\n\n")
	}))
	defer upstream.Close()

	server, secret := newMetricsFailoverGateway(t, upstream.URL, "http://localhost:1")
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "metrics-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("a committed stream still returns the committed 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if strings.Count(resp.Body.String(), `"error"`) != 1 {
		t.Fatalf("the in-band error frame must reach the client exactly once:\n%s", resp.Body.String())
	}

	body := scrapeMetrics(t, server.Handler())
	interruption := findMetricLine(body, "tokenhub_gateway_stream_interruptions_total", `error_code="provider_stream_error"`)
	if interruption == "" {
		t.Fatalf("missing in-band error interruption series:\n%s", body)
	}
	if metricLineValue(t, interruption) != 1 {
		t.Fatalf("expected one in-band error interruption")
	}
	if findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count") == "" {
		t.Fatalf("TTFB must still be observed for the interrupted stream")
	}
}

// The /v1/messages OpenAI bridge converts an in-band error frame into one
// Anthropic error event and classifies the stream as an interruption instead
// of appending a successful completion.
func TestMetricsOpenAIAsAnthropicBridgeInBandErrorCountsInterruption(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"partial\"},\"index\":0}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slow down\"}}\n\n")
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "metrics-bridge", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "metrics-bridge-key",
		Allowed: []string{"claude-metrics-test"},
		Status:  StatusActive,
	}, "thk_metrics_bridge")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		Name:          "claude-metrics-test",
		Family:        "claude",
		Modality:      "chat",
		ContextWindow: 200000,
		Capabilities:  []string{"chat"},
		Status:        StatusActive,
	})
	provider := store.AddProvider(Provider{
		ID:      "prv_metrics_bridge",
		Name:    "Metrics Bridge",
		Type:    ProviderOpenAICompatible,
		BaseURL: upstream.URL,
		APIKey:  "upstream-secret",
		Status:  StatusActive,
		Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID:            "route_metrics_bridge",
		ModelName:     "claude-metrics-test",
		ProviderID:    provider.ID,
		ProviderModel: "upstream-model",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityOnly,
	})

	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	resp := doAnthropicRequest(t, server.Handler(), "/v1/messages", map[string]any{
		"model":      "claude-metrics-test",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("a committed bridge stream still returns the committed 200, got %d", resp.Code)
	}
	if strings.Count(resp.Body.String(), "event: error") != 1 {
		t.Fatalf("the bridge must emit exactly one Anthropic error event:\n%s", resp.Body.String())
	}

	body := scrapeMetrics(t, server.Handler())
	interruption := findMetricLine(body, "tokenhub_gateway_stream_interruptions_total", `provider_id="prv_metrics_bridge"`)
	if interruption == "" {
		t.Fatalf("missing bridge interruption series:\n%s", body)
	}
	if metricLineValue(t, interruption) != 1 {
		t.Fatalf("expected one bridge interruption")
	}
	if strings.Contains(interruption, `error_code="none"`) {
		t.Fatalf("bridge interruption must carry a non-none error_code:\n%s", interruption)
	}
	if findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count") == "" {
		t.Fatalf("TTFB must still be observed for the interrupted bridge stream")
	}
}

// newMetricsKronkTestServer creates a full gateway with a Kronk provider
// that routes to the given upstream URL.
func newMetricsKronkTestServer(t *testing.T, upstreamURL string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "metrics-kronk", Status: StatusActive})
	const model = "kronk-metrics-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "metrics-kronk-key",
		Allowed: []string{model},
		Status:  StatusActive,
	}, "thk_metrics_kronk")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID:      "prv_metrics_kronk",
		Name:    "Metrics Kronk",
		Type:    ProviderKronk,
		BaseURL: upstreamURL,
		APIKey:  "test-key",
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "rsrc_metrics_kronk",
		ProviderID:     provider.ID,
		Name:           "Metrics Kronk Resource",
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
		ID:                 "route_metrics_kronk",
		ModelName:          model,
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      model,
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
		Strategy:           RouteStrategyPriorityOnly,
	})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	if server.metrics == nil {
		t.Fatal("expected metrics to be enabled")
	}
	return server, secret
}

// A Kronk stream that writes a partial response and then hangs up is a
// transport interruption: the error code is normalized to internal_error
// at the metric boundary.
func TestMetricsKronkTransportInterruptionNormalizesErrorCode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("content-length", "4096")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"index\":0}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Close the connection without sending the advertised content-length.
		// The Kronk adapter reads the body and gets io.ErrUnexpectedEOF, which
		// normalizeKronkTransportError maps to provider_stream_interrupted.
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer upstream.Close()

	server, secret := newMetricsKronkTestServer(t, upstream.URL)
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "kronk-metrics-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)
	// A committed stream still returns 200 even though the gateway recorded
	// the interruption.
	if resp.Code != http.StatusOK {
		t.Fatalf("expected the committed 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "partial") {
		t.Fatalf("partial data must reach the client:\n%s", resp.Body.String())
	}

	body := scrapeMetrics(t, server.Handler())
	// The interruption must carry internal_error, not provider_stream_interrupted.
	interruption := findMetricLine(body, "tokenhub_gateway_stream_interruptions_total", `provider_id="prv_metrics_kronk"`)
	if interruption == "" {
		t.Fatalf("missing Kronk interruption series:\n%s", body)
	}
	if strings.Contains(interruption, `error_code="provider_stream_interrupted"`) {
		t.Fatalf("Kronk transport interruption must not leak provider_stream_interrupted:\n%s", body)
	}
	if !strings.Contains(interruption, `error_code="internal_error"`) {
		t.Fatalf("Kronk transport interruption must be normalized to internal_error:\n%s", body)
	}
	if metricLineValue(t, interruption) != 1 {
		t.Fatalf("expected one Kronk transport interruption, got %v", metricLineValue(t, interruption))
	}
	// TTFB is still observed for the interrupted stream.
	if findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count") == "" {
		t.Fatalf("TTFB must still be observed for the interrupted Kronk stream")
	}
}

// An in-band error frame that echoes provider secrets is forwarded to the
// client with secrets redacted, and the classified error that reaches
// RouteAttempt.Error is built from the redacted payload so the secret does
// not survive into the persisted attempt/audit data.

// A full end-to-end regression: an in-band error frame that echoes a provider
// API key is redacted before the classified error reaches RouteAttempt.Error
// (via errorMessage). The forwarded SSE frame is also redacted. This test
// exercises the full pipeline through executeRoutedWithStore and FinishCall.
func TestMetricsInBandErrorSecretsRedactedThroughFullPipeline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"partial\"},\"index\":0}]}\n\n")
		// The upstream echoes the API key in the error message.
		_, _ = io.WriteString(w, "data: {\"error\":{\"type\":\"server_error\",\"message\":\"provider-api-secret failed\"}}\n\n")
	}))
	defer upstream.Close()

	// Build a custom gateway with a provider that has an API key, so the
	// redaction path is exercised through the full streaming pipeline.
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "metrics-secrets", Status: StatusActive})
	const model = "metrics-secrets-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "metrics-secrets-key",
		Allowed: []string{model},
		Status:  StatusActive,
	}, "thk_metrics_secrets")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID:      "prv_metrics_secrets",
		Name:    "Metrics Secrets",
		Type:    ProviderOpenAICompatible,
		BaseURL: upstream.URL,
		APIKey:  "provider-api-secret",
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "rsrc_metrics_secrets",
		ProviderID:     provider.ID,
		Name:           "Metrics Secrets Resource",
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
		ID:                 "route_metrics_secrets",
		ModelName:          model,
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      model,
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
		Strategy:           RouteStrategyPriorityOnly,
	})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	if server.metrics == nil {
		t.Fatal("expected metrics to be enabled")
	}

	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("a committed stream still returns the committed 200, got %d: %s", resp.Code, resp.Body.String())
	}
	// The forwarded SSE frame must not contain the raw API key.
	if strings.Contains(resp.Body.String(), "provider-api-secret") {
		t.Fatalf("the forwarded frame leaked the provider API key:\n%s", resp.Body.String())
	}

	body := scrapeMetrics(t, server.Handler())
	interruption := findMetricLine(body, "tokenhub_gateway_stream_interruptions_total", `provider_id="prv_metrics_secrets"`)
	if interruption == "" {
		t.Fatalf("missing in-band error interruption series:\n%s", body)
	}
	if metricLineValue(t, interruption) != 1 {
		t.Fatalf("expected one in-band error interruption")
	}
	if !strings.Contains(interruption, `error_code="provider_stream_error"`) {
		t.Fatalf("in-band error must carry provider_stream_error:\n%s", interruption)
	}
}

// A successful stream records time_to_first_byte_seconds exactly once.
func TestMetricsStreamRecordsTimeToFirstByteOnce(t *testing.T) {
	_, server, secret := newMetricsTestServer(t, false)
	app := server.Handler()
	if code := chatOnce(t, app, secret, true); code != http.StatusOK {
		t.Fatalf("streaming chat failed: %d", code)
	}

	body := scrapeMetrics(t, app)
	line := findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count")
	if line == "" {
		t.Fatalf("missing time_to_first_byte count:\n%s", body)
	}
	if got := metricLineValue(t, line); got != 1 {
		t.Fatalf("expected TTFB observed once, got %v", got)
	}
}

// Non-streamed requests do not create a time_to_first_byte_seconds series.
func TestMetricsNonStreamRecordsNoTimeToFirstByte(t *testing.T) {
	_, server, secret := newMetricsTestServer(t, false)
	app := server.Handler()
	if code := chatOnce(t, app, secret, false); code != http.StatusOK {
		t.Fatalf("chat failed: %d", code)
	}

	body := scrapeMetrics(t, app)
	if strings.Contains(body, "tokenhub_gateway_time_to_first_byte_seconds{") {
		t.Fatalf("non-stream request must not produce TTFB series:\n%s", body)
	}
}

// After failover, TTFB is attributed to the serving provider and observed once.
func TestMetricsFailoverObservesTimeToFirstByteOnce(t *testing.T) {
	var secondaryHits int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream down"}}`)
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
	count := findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count", `provider_type="openai_compatible"`)
	if count == "" {
		t.Fatalf("missing TTFB count:\n%s", body)
	}
	if got := metricLineValue(t, count); got != 1 {
		t.Fatalf("expected TTFB observed once after failover, got %v", got)
	}
	// The metric carries model and provider_type only; the request_total series
	// below confirms the logical request was attributed to the serving provider.
	if got := testutil.ToFloat64(server.metrics.requests.WithLabelValues(
		"metrics-failover-model", ProviderOpenAICompatible, "prv_metrics_1", "rsrc_metrics_1", "200", metricsLabelUnset, "true",
	)); got != 1 {
		t.Fatalf("expected one logical request on serving provider, got %v", got)
	}
}

// A committed stream that then fails counts as one interruption.
func TestMetricsCommittedStreamFailureCountsInterruption(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, hijackErr := hijacker.Hijack()
			if hijackErr == nil {
				_ = conn.Close()
				return
			}
		}
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChatStreamChunk(w, "should-not-be-used")
	}))
	defer secondary.Close()

	server, secret := newMetricsFailoverGateway(t, primary.URL, secondary.URL)
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "metrics-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)

	if strings.Contains(resp.Body.String(), "should-not-be-used") {
		t.Fatalf("client received a second upstream's stream: %q", resp.Body.String())
	}

	body := scrapeMetrics(t, server.Handler())
	interruption := findMetricLine(body, "tokenhub_gateway_stream_interruptions_total", `provider_id="prv_metrics_0"`)
	if interruption == "" {
		t.Fatalf("missing interruption series:\n%s", body)
	}
	if metricLineValue(t, interruption) != 1 {
		t.Fatalf("expected one interruption")
	}
	if strings.Contains(interruption, `error_code="none"`) {
		t.Fatalf("interruption must carry a non-none error_code:\n%s", interruption)
	}

	// TTFB is still observed for the interrupted stream.
	if findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count") == "" {
		t.Fatalf("TTFB must still be observed for an interrupted stream")
	}
}

// A stream that fails before the first byte is not an interruption.
func TestMetricsPreCommitStreamFailureIsNotAnInterruption(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream down"}}`)
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if strings.Contains(body, "tokenhub_gateway_stream_interruptions_total{") {
		t.Fatalf("pre-commit failure must not count as interruption:\n%s", body)
	}
}

// A client-disconnected stream records an interruption with the error_code that
// classifyStreamError assigns to context.Canceled.
func TestMetricsClientDisconnectInterruptionKeepsItsCode(t *testing.T) {
	// Derive the code through the real classification chain rather than hardcoding
	// a string that could drift.
	err := classifyStreamError(context.Background(), context.Canceled, true)
	httpErr := AsHTTPError(err)
	expectedCode := httpErr.Code
	if expectedCode == "" {
		t.Fatal("could not derive client disconnect error code")
	}

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		// Wait until the client gives up.
		<-r.Context().Done()
	}))
	defer primary.Close()

	server, secret := newMetricsFailoverGateway(t, primary.URL, "http://localhost:1")

	ctx, cancel := context.WithCancel(context.Background())
	body, _ := json.Marshal(map[string]any{
		"model":    "metrics-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body))).WithContext(ctx)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()

	// Cancel shortly after the first byte has been written.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	server.Handler().ServeHTTP(rec, req)

	scrapeBody := scrapeMetrics(t, server.Handler())
	interruption := findMetricLine(scrapeBody, "tokenhub_gateway_stream_interruptions_total", `error_code="`+expectedCode+`"`)
	if interruption == "" {
		t.Fatalf("missing interruption with code %q:\n%s", expectedCode, scrapeBody)
	}
}

// newMetricsCodexTestServer wires one Codex subscription model with metrics
// enabled. upstream returns the response the mock subscription should serve.
func newMetricsCodexTestServer(t *testing.T, upstream func(*http.Request) *http.Response) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "metrics-codex", Status: StatusActive})
	const model = "metrics-codex-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "metrics-codex-key",
		Allowed: []string{model},
		Status:  StatusActive,
	}, "thk_metrics_codex")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_metrics_codex",
		Name:    "Metrics Codex",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	_, err = store.AddProviderResource(ProviderResource{
		ID:           "rsrc_metrics_codex",
		ProviderID:   provider.ID,
		Name:         "Metrics Codex Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest(model),
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access_metrics",
			AccountID:   "account_metrics",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:            "route_metrics_codex",
		ModelName:     model,
		ProviderID:    provider.ID,
		ProviderModel: model,
		Status:        StatusActive,
	})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	if server.metrics == nil {
		t.Fatal("expected metrics to be enabled")
	}
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return upstream(req), nil
	})}
	return server, secret
}

// delayedFirstChunkReader sleeps once before serving its first chunk, simulating
// an upstream that flushes headers immediately but delays its first SSE event.
type delayedFirstChunkReader struct {
	delay   time.Duration
	body    io.Reader
	started bool
}

func (r *delayedFirstChunkReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		time.Sleep(r.delay)
	}
	return r.body.Read(p)
}

// The Codex gateway measures TTFB on the first SSE event, not on the upstream
// headers: an upstream that commits the 200 and then waits before its first
// event must still report roughly its real first-token latency.
func TestMetricsCodexTimeToFirstByteWaitsForFirstEvent(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_metrics","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")
	server, secret := newMetricsCodexTestServer(t, func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(&delayedFirstChunkReader{delay: 300 * time.Millisecond, body: strings.NewReader(stream)}),
			Request:    req,
		}
	})
	resp := postStream(t, server.Handler(), "/v1/responses", map[string]any{
		"model":  "metrics-codex-model",
		"input":  "hi",
		"stream": true,
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("responses stream failed: %d %s", resp.Code, resp.Body)
	}

	body := scrapeMetrics(t, server.Handler())
	count := findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count")
	if count == "" {
		t.Fatalf("missing Codex TTFB count:\n%s", body)
	}
	if got := metricLineValue(t, count); got != 1 {
		t.Fatalf("expected one Codex TTFB observation, got %v", got)
	}
	// 300ms before the first event: the observation must land beyond the 250ms
	// bucket, not in the near-zero buckets an upstream-header timestamp would hit.
	bucket := findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_bucket", `le="0.25"`)
	if bucket == "" {
		t.Fatalf("missing TTFB bucket:\n%s", body)
	}
	if got := metricLineValue(t, bucket); got != 0 {
		t.Fatalf("TTFB must reflect the delayed first event, got %v observations in le=0.25", got)
	}
}

// A Codex stream that ends before any event wrote nothing the client could
// perceive: it must not count as an interruption and must not record TTFB,
// matching the chat and Anthropic gateways.
func TestMetricsCodexEmptyBodyFailureIsNotAnInterruption(t *testing.T) {
	server, secret := newMetricsCodexTestServer(t, func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}
	})
	resp := postStream(t, server.Handler(), "/v1/responses", map[string]any{
		"model":  "metrics-codex-model",
		"input":  "hi",
		"stream": true,
	}, secret)
	// ensureStarted committed the 200 before the body was read, so the client
	// sees the committed response even though the gateway recorded a 502.
	if resp.Code != http.StatusOK {
		t.Fatalf("expected the committed 200, got %d: %s", resp.Code, resp.Body)
	}

	body := scrapeMetrics(t, server.Handler())
	if strings.Contains(body, "tokenhub_gateway_stream_interruptions_total{") {
		t.Fatalf("empty-body Codex failure must not count as interruption:\n%s", body)
	}
	if strings.Contains(body, "tokenhub_gateway_time_to_first_byte_seconds{") {
		t.Fatalf("empty-body Codex failure must not record TTFB:\n%s", body)
	}
}

// newMetricsGeminiTestServer mirrors newGeminiCodexTestServer with metrics
// enabled so the Gemini streaming endpoint can be asserted against.
func newMetricsGeminiTestServer(t *testing.T, responder func(map[string]any) string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Metrics Gemini Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Metrics Gemini Key", Allowed: []string{geminiCodexTestModel}, Status: StatusActive}, "thk_metrics_gemini")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_gemini_codex", Name: "Gemini Codex", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_gemini_codex", ProviderID: provider.ID, Name: "Gemini Codex Account",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Options:     codexCapabilityOptionsForTest(geminiCodexTestModel),
		Credentials: &ProviderResourceCredentials{AccessToken: "gemini-codex-access", AccountID: "gemini-codex-account"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: geminiCodexTestModel, Modality: "chat", ContextWindow: 128000, Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_gemini_codex", ModelName: geminiCodexTestModel, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: geminiCodexTestModel, Status: StatusActive})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	if server.metrics == nil {
		t.Fatal("expected metrics to be enabled")
	}
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		body := responder(payload)
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}
	return server, secret
}

// The Gemini streaming endpoint records TTFB like the chat, Anthropic and Codex
// gateways: a successful stream is observed exactly once.
func TestMetricsGeminiStreamRecordsTimeToFirstByteOnce(t *testing.T) {
	server, secret := newMetricsGeminiTestServer(t, func(request map[string]any) string {
		return geminiCodexTestSSE(
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_gemini_metrics", "status": "in_progress"}},
			map[string]any{"type": "response.completed", "response": map[string]any{
				"id": "resp_gemini_metrics", "status": "completed",
				"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}},
				"usage":  map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8},
			}},
		)
	})
	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:streamGenerateContent?alt=sse", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("Gemini stream failed: %d %s", response.Code, response.Body)
	}

	body := scrapeMetrics(t, server.Handler())
	line := findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count")
	if line == "" {
		t.Fatalf("missing Gemini TTFB count:\n%s", body)
	}
	if got := metricLineValue(t, line); got != 1 {
		t.Fatalf("expected one Gemini TTFB observation, got %v", got)
	}
}

// A Gemini stream that fails after its first event counts as one interruption
// with the upstream error code preserved.
func TestMetricsGeminiStreamFailureAfterFirstByteCountsInterruption(t *testing.T) {
	server, secret := newMetricsGeminiTestServer(t, func(request map[string]any) string {
		// The stream writes a content delta and then ends without
		// response.completed: the client saw bytes, then the stream broke.
		return geminiCodexTestSSE(
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_gemini_metrics", "status": "in_progress"}},
			map[string]any{"type": "response.output_text.delta", "delta": "partial"},
		)
	})
	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:streamGenerateContent?alt=sse", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("a committed Gemini stream still returns the committed 200, got %d", response.Code)
	}

	body := scrapeMetrics(t, server.Handler())
	interruption := findMetricLine(body, "tokenhub_gateway_stream_interruptions_total", `provider_id="prv_gemini_codex"`)
	if interruption == "" {
		t.Fatalf("missing Gemini interruption series:\n%s", body)
	}
	if metricLineValue(t, interruption) != 1 {
		t.Fatalf("expected one Gemini interruption")
	}
	if strings.Contains(interruption, `error_code="none"`) {
		t.Fatalf("Gemini interruption must carry a non-none error_code:\n%s", interruption)
	}
}

// A first write that produces zero bytes — a disconnected client can yield
// (0, err) — must not be recorded as a first byte: TTFB and interruption both
// key off firstByteTime, so a zero-byte write failure must leave both empty.
func TestStreamWriteTrackerZeroByteFirstWriteRecordsNoFirstByte(t *testing.T) {
	zeroWriter := &zeroByteWrite{err: errors.New("client gone")}
	tracker := &streamWriteTracker{writer: zeroWriter}
	n, err := tracker.Write([]byte("event: ping\n\n"))
	if n != 0 || err == nil {
		t.Fatalf("expected (0, err) from the failing writer, got (%d, %v)", n, err)
	}
	if tracker.WroteData() {
		t.Fatal("a zero-byte write must not count as data written")
	}
	if !tracker.firstByteTime(false).IsZero() {
		t.Fatal("a failed zero-byte first write must not synthesize a first-byte time")
	}
	// A later positive write on the same tracker still records the byte time.
	tracker.writer = &bytes.Buffer{}
	start := time.Now()
	n, err = tracker.Write([]byte("data: ok\n\n"))
	if n == 0 || err != nil {
		t.Fatalf("expected a positive write, got (%d, %v)", n, err)
	}
	if got := tracker.firstByteTime(false); got.IsZero() || got.Before(start) || got.After(time.Now()) {
		t.Fatalf("first-byte time must fall within the write window, got %v", got)
	}
}

// zeroByteWrite simulates a response writer whose client vanished: it accepts
// nothing and reports the connection loss.
type zeroByteWrite struct{ err error }

func (z *zeroByteWrite) Write([]byte) (int, error) { return 0, z.err }

// A native Anthropic stream that forwards an upstream event: error frame after
// the first byte counts as one interruption: the client saw bytes, then the
// provider reported a terminal error, and the handler must not record 200.
func TestMetricsNativeAnthropicErrorAfterFirstByteCountsInterruption(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"upstream-model\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\n")
		_, _ = io.WriteString(w, "event: error\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"upstream overloaded\"}}\n\n")
	}))
	defer upstream.Close()

	server, secret := newMetricsAnthropicTestServer(t, upstream.URL)
	resp := doAnthropicRequest(t, server.Handler(), "/v1/messages", map[string]any{
		"model":      "claude-metrics-test",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("a committed native stream still returns the committed 200, got %d", resp.Code)
	}
	if strings.Count(resp.Body.String(), "event: error") != 1 {
		t.Fatalf("the terminal error event must reach the client exactly once:\n%s", resp.Body)
	}

	body := scrapeMetrics(t, server.Handler())
	interruption := findMetricLine(body, "tokenhub_gateway_stream_interruptions_total", `provider_id="prv_metrics_anthropic"`)
	if interruption == "" {
		t.Fatalf("missing native Anthropic interruption series:\n%s", body)
	}
	if metricLineValue(t, interruption) != 1 {
		t.Fatalf("expected one native Anthropic interruption")
	}
	if strings.Contains(interruption, `error_code="none"`) {
		t.Fatalf("native Anthropic interruption must carry a non-none error_code:\n%s", interruption)
	}
	if findMetricLine(body, "tokenhub_gateway_time_to_first_byte_seconds_count") == "" {
		t.Fatalf("TTFB must still be observed for the interrupted stream")
	}
}

// newMetricsAnthropicTestServer wires one native Anthropic model with metrics
// enabled. upstream serves the SSE stream the gateway should forward.
func newMetricsAnthropicTestServer(t *testing.T, upstreamURL string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "metrics-anthropic", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "metrics-anthropic-key",
		Allowed: []string{"claude-metrics-test"},
		Status:  StatusActive,
	}, "thk_metrics_anthropic")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		Name:          "claude-metrics-test",
		Family:        "claude",
		Modality:      "chat",
		ContextWindow: 200000,
		Capabilities:  []string{"chat"},
		Status:        StatusActive,
	})
	provider := store.AddProvider(Provider{
		ID:      "prv_metrics_anthropic",
		Name:    "Metrics Anthropic",
		Type:    ProviderAnthropic,
		BaseURL: upstreamURL,
		APIKey:  "upstream-secret",
		Status:  StatusActive,
		Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID:            "route_metrics_anthropic",
		ModelName:     "claude-metrics-test",
		ProviderID:    provider.ID,
		ProviderModel: "upstream-model",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityOnly,
	})
	return NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true}), secret
}
