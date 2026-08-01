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
	})
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "internal_error")); got != 2 {
		t.Fatalf("expected two interruptions labeled internal_error, got %v", got)
	}
	if got := testutil.ToFloat64(m.interruptions.WithLabelValues("m", "t", "p", "codex_stream_failed")); got != 0 {
		t.Fatalf("the codex_stream_failed code must not leak through, got %v", got)
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
