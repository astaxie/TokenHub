package server

// Empty-body stream-quality tests live in their own file so metrics_test.go
// stays under the repository's 1500-line source ceiling.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		ID:           "rsrc_metrics_kronk",
		ProviderID:   provider.ID,
		Name:         "Metrics Kronk Resource",
		ResourceType: "openai",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
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
