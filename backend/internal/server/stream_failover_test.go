package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newStreamFailoverGateway wires one model to two upstreams in priority order so
// tests can fail the preferred one and observe the fallback.
func newStreamFailoverGateway(t *testing.T, primaryURL string, secondaryURL string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "stream-failover", Status: StatusActive})
	const model = "stream-failover-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "stream-failover-key",
		Allowed: []string{model},
		Status:  StatusActive,
	}, "thk_stream_failover")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	for index, upstreamURL := range []string{primaryURL, secondaryURL} {
		provider := store.AddProvider(Provider{
			ID:      fmt.Sprintf("prv_stream_%d", index),
			Name:    fmt.Sprintf("stream-%d", index),
			Type:    ProviderOpenAICompatible,
			BaseURL: upstreamURL,
			Status:  StatusActive,
			Healthy: true,
		})
		store.AddRoute(ModelRoute{
			ID:            fmt.Sprintf("route_stream_%d", index),
			ModelName:     model,
			ProviderID:    provider.ID,
			ProviderModel: fmt.Sprintf("upstream-model-%d", index),
			Priority:      index + 1,
			Weight:        100,
			Status:        StatusActive,
			Strategy:      RouteStrategyPriorityOnly,
		})
	}
	return New(store), secret
}

func writeChatStreamChunk(w http.ResponseWriter, content string) {
	w.Header().Set("content-type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	_, _ = io.WriteString(w, fmt.Sprintf(
		"data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content))
	if flusher != nil {
		flusher.Flush()
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func postStream(t *testing.T, handler http.Handler, path string, payload map[string]any, secret string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// A failure before the first byte must move to the next candidate.
func TestChatCompletionStreamFailsOverBeforeFirstByte(t *testing.T) {
	var primaryHits, secondaryHits int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryHits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryHits, 1)
		writeChatStreamChunk(w, "recovered")
	}))
	defer secondary.Close()

	server, secret := newStreamFailoverGateway(t, primary.URL, secondary.URL)
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "stream-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected the fallback to succeed, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body.String(), "recovered") {
		t.Fatalf("expected the secondary upstream's stream, got %q", resp.Body.String())
	}
	if atomic.LoadInt32(&primaryHits) != 1 || atomic.LoadInt32(&secondaryHits) != 1 {
		t.Fatalf("expected one attempt each, got primary=%d secondary=%d", primaryHits, secondaryHits)
	}
	// Headers must describe the route that actually served the request.
	if got := resp.Header().Get("x-tokenhub-model"); got != "upstream-model-1" {
		t.Fatalf("route headers must reflect the serving route, got %q", got)
	}
	if got := resp.Header().Get("x-tokenhub-route-attempts"); got != "2" {
		t.Fatalf("expected two attempts recorded, got %q", got)
	}
}

// Once bytes reached the client, switching upstreams would emit two contradictory
// streams, so the request must fail in place.
func TestChatCompletionStreamDoesNotFailOverAfterCommitted(t *testing.T) {
	var secondaryHits int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		// Drop the connection mid-stream without the terminating [DONE].
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
		atomic.AddInt32(&secondaryHits, 1)
		writeChatStreamChunk(w, "should-not-be-used")
	}))
	defer secondary.Close()

	server, secret := newStreamFailoverGateway(t, primary.URL, secondary.URL)
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "stream-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)

	if atomic.LoadInt32(&secondaryHits) != 0 {
		t.Fatal("a committed stream must not fail over to another upstream")
	}
	if strings.Contains(resp.Body.String(), "should-not-be-used") {
		t.Fatalf("client received a second upstream's stream: %q", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "partial") {
		t.Fatalf("expected the already-committed bytes to survive, got %q", resp.Body.String())
	}
}

// The Anthropic Messages stream must behave the same way.
func TestAnthropicStreamFailsOverBeforeFirstByte(t *testing.T) {
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

	server, secret := newStreamFailoverGateway(t, primary.URL, secondary.URL)
	resp := postStream(t, server.Handler(), "/v1/messages", map[string]any{
		"model":      "stream-failover-model",
		"max_tokens": 16,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		"stream":     true,
	}, secret)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected the fallback to succeed, got %d: %s", resp.Code, resp.Body)
	}
	if atomic.LoadInt32(&secondaryHits) != 1 {
		t.Fatalf("expected the secondary upstream to serve the request, hits=%d", secondaryHits)
	}
	if got := resp.Header().Get("x-tokenhub-route-attempts"); got != "2" {
		t.Fatalf("expected two attempts recorded, got %q", got)
	}
}

// When every candidate fails before writing, the client gets a JSON error that
// still carries the routing diagnostics.
func TestStreamFailureReportsAttemptsWhenAllCandidatesFail(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	defer failing.Close()

	server, secret := newStreamFailoverGateway(t, failing.URL, failing.URL)
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "stream-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)

	if resp.Code == http.StatusOK {
		t.Fatal("expected an error once every candidate failed")
	}
	if contentType := resp.Header().Get("content-type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected a JSON error response, got content-type %q", contentType)
	}
	if got := resp.Header().Get("x-tokenhub-route-attempts"); got != "2" {
		t.Fatalf("expected both attempts reported, got %q", got)
	}
}

// A client that disconnects mid-request must not trigger failover: retrying only
// burns the next account's quota for a response nobody will read.
//
// Asserted at the classification layer rather than by driving a real disconnect:
// an end-to-end version deadlocks because httptest's Close waits for the upstream
// handler, which in turn waits for a cancellation that may never reach it.
func TestClientCancellationIsNotRetryable(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, testCase := range []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{name: "error is context.Canceled", ctx: context.Background(), err: context.Canceled},
		{name: "context reports cancellation", ctx: cancelled, err: io.ErrUnexpectedEOF},
		{
			name: "wrapped cancellation",
			ctx:  context.Background(),
			err:  fmt.Errorf("upstream read: %w", context.Canceled),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			classified := classifyStreamError(testCase.ctx, testCase.err, false)
			if providerErrorDisposition(classified) != ProviderErrorClient {
				t.Fatalf("expected a client disposition, got %q", providerErrorDisposition(classified))
			}
			if shouldFailoverRoutedError(classified, false) {
				t.Fatal("client cancellation must never fail over to another account")
			}
		})
	}
}

// A pre-first-byte upstream failure must stay retryable, otherwise failover dies.
func TestPreCommitStreamErrorRemainsRetryable(t *testing.T) {
	err := NewHTTPError(http.StatusBadGateway, "provider_error", "upstream exploded")
	classified := classifyStreamError(context.Background(), err, false)
	if !shouldFailoverRoutedError(classified, false) {
		t.Fatal("a failure before the first byte must remain retryable")
	}
}

// Once committed, nothing is retryable regardless of the underlying error.
func TestCommittedStreamErrorIsNeverRetryable(t *testing.T) {
	for _, err := range []error{
		NewHTTPError(http.StatusBadGateway, "provider_error", "upstream exploded"),
		NewHTTPError(http.StatusTooManyRequests, "provider_error", "rate limited"),
		io.ErrUnexpectedEOF,
	} {
		classified := classifyStreamError(context.Background(), err, true)
		if providerErrorDisposition(classified) != ProviderErrorStreamCommitted {
			t.Fatalf("expected a committed disposition for %v", err)
		}
		if shouldFailoverRoutedError(classified, false) {
			t.Fatalf("a committed stream must not fail over, error=%v", err)
		}
	}
}

// An upstream that answers 200 with an empty body never triggers onFirstWrite,
// so the handler must install the deferred headers itself.
func TestEmptySuccessfulStreamStillWritesHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	server, secret := newStreamFailoverGateway(t, upstream.URL, upstream.URL)
	resp := postStream(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":    "stream-failover-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}, secret)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if got := resp.Header().Get("content-type"); got != "text/event-stream" {
		t.Fatalf("streaming content-type missing on an empty stream, got %q", got)
	}
	if resp.Header().Get("x-tokenhub-route-id") == "" {
		t.Fatal("route headers missing on an empty successful stream")
	}
	if got := resp.Header().Get("x-tokenhub-route-attempts"); got != "1" {
		t.Fatalf("expected one attempt, got %q", got)
	}
}

// Candidates rejected by the capacity check never reach the callback, so the
// attempt number must come from the executor rather than a callback-local counter.
func TestStreamAttemptCountIncludesCapacityFailures(t *testing.T) {
	var servedHits int32
	served := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&servedHits, 1)
		writeChatStreamChunk(w, "served")
	}))
	defer served.Close()

	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "capacity-attempts", Status: StatusActive})
	const model = "capacity-attempt-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "capacity-attempt-key", Allowed: []string{model}, Status: StatusActive,
	}, "thk_capacity_attempt")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})

	// First candidate: a resource whose RPM budget is already exhausted, so the
	// capacity check rejects it before the callback ever runs.
	exhausted := store.AddProvider(Provider{
		ID: "prv_exhausted", Name: "exhausted", Type: ProviderOpenAICompatible,
		BaseURL: served.URL, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: exhausted.ID, Name: "exhausted-resource", ResourceType: ProviderResourceAPIKey,
		BaseURL: served.URL, APIKey: "k", RateLimitRPM: 1, Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{
		ID: "route_exhausted", ModelName: model, ProviderID: exhausted.ID,
		ProviderResourceID: resource.ID, ProviderModel: "upstream-exhausted",
		Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
	})

	healthy := store.AddProvider(Provider{
		ID: "prv_healthy", Name: "healthy", Type: ProviderOpenAICompatible,
		BaseURL: served.URL, Status: StatusActive, Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID: "route_healthy", ModelName: model, ProviderID: healthy.ID,
		ProviderModel: "upstream-healthy", Priority: 2, Weight: 100,
		Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
	})

	server := New(store)
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	}
	// Burn the first candidate's single request so the next call is rejected.
	first := postStream(t, server.Handler(), "/v1/chat/completions", payload, secret)
	if first.Code != http.StatusOK {
		t.Fatalf("warm-up request failed: %d %s", first.Code, first.Body)
	}

	second := postStream(t, server.Handler(), "/v1/chat/completions", payload, secret)
	if second.Code != http.StatusOK {
		t.Fatalf("expected the fallback to serve the request, got %d: %s", second.Code, second.Body)
	}
	if got := second.Header().Get("x-tokenhub-model"); got != "upstream-healthy" {
		t.Fatalf("expected the healthy candidate to serve, got %q", got)
	}
	if got := second.Header().Get("x-tokenhub-route-attempts"); got != "2" {
		t.Fatalf("capacity rejection must be counted as an attempt, got %q", got)
	}
}
