package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseUpstream serves one streamed chat completion, writing each chunk after the
// delay that precedes it. Timings are scaled down from the reported minutes so
// the tests stay fast while exercising the same boundaries.
func sseUpstream(t *testing.T, headerDelay time.Duration, chunks []time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test upstream cannot stream")
			return
		}
		// Waits abandon themselves when the client goes away, so a test that
		// deliberately gives up early does not also wait out the server.
		if !waitOrDone(r, headerDelay) {
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for index, delay := range chunks {
			if !waitOrDone(r, delay) {
				return
			}
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"chunk-%d\"}}]}\n\n", index)
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// waitOrDone reports whether the wait completed rather than being cut short by
// the client hanging up.
func waitOrDone(r *http.Request, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

func streamingAdapter(upstream *httptest.Server, totalTimeout time.Duration, idleTimeout time.Duration) OpenAICompatibleAdapter {
	// The non-streaming client keeps a total deadline deliberately shorter than
	// the stream under test: the point is that it must not apply to the stream.
	client := upstream.Client()
	client.Timeout = totalTimeout
	streamClient := &http.Client{Transport: client.Transport}
	return OpenAICompatibleAdapter{Client: client, StreamClient: streamClient, StreamIdleTimeout: idleTimeout}
}

func chatStream(t *testing.T, adapter OpenAICompatibleAdapter, upstream *httptest.Server) (string, error) {
	t.Helper()
	var out bytes.Buffer
	_, err := adapter.ChatStream(context.Background(), Provider{BaseURL: upstream.URL, APIKey: "test-key"}, "model-x",
		ChatCompletionRequest{Model: "model-x", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, &out)
	return out.String(), err
}

// The reported failure: an active stream living longer than the total timeout
// that used to apply to every upstream call.
func TestChatStreamOutlivesTheNonStreamingTimeout(t *testing.T) {
	upstream := sseUpstream(t, 0, []time.Duration{80 * time.Millisecond, 80 * time.Millisecond, 80 * time.Millisecond})
	defer upstream.Close()

	body, err := chatStream(t, streamingAdapter(upstream, 100*time.Millisecond, time.Second), upstream)
	if err != nil {
		t.Fatalf("an active stream was cut short: %v", err)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream was truncated before [DONE]: %q", body)
	}
	if !strings.Contains(body, "chunk-2") {
		t.Fatalf("stream lost its last chunk: %q", body)
	}
}

// A reasoning model returns headers immediately and only then thinks. That gap
// belongs to the idle budget, so a model that thinks for less than it still
// finishes.
func TestChatStreamSurvivesADelayedFirstChunk(t *testing.T) {
	upstream := sseUpstream(t, 0, []time.Duration{250 * time.Millisecond})
	defer upstream.Close()

	body, err := chatStream(t, streamingAdapter(upstream, 100*time.Millisecond, 2*time.Second), upstream)
	if err != nil {
		t.Fatalf("a stream that thought before its first chunk failed: %v", err)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream was truncated before [DONE]: %q", body)
	}
}

// The other half of the same property: ResponseHeaderTimeout is already
// satisfied once the status line arrives, so nothing but the idle budget covers
// the silence before the first chunk. It therefore has to start when the body is
// handed over rather than at the first Read.
func TestChatStreamFailsWhenTheFirstChunkNeverArrives(t *testing.T) {
	upstream := sseUpstream(t, 0, []time.Duration{2 * time.Second})
	defer upstream.Close()

	_, err := chatStream(t, streamingAdapter(upstream, time.Minute, 150*time.Millisecond), upstream)
	if err == nil {
		t.Fatal("a stream whose first chunk never arrives must fail")
	}
	if httpErr := AsHTTPError(err); httpErr.Code != "provider_stream_idle_timeout" {
		t.Fatalf("silent stream reported %q, want provider_stream_idle_timeout", httpErr.Code)
	}
}

func TestChatStreamFailsAfterGoingIdle(t *testing.T) {
	upstream := sseUpstream(t, 0, []time.Duration{0, 2 * time.Second})
	defer upstream.Close()

	body, err := chatStream(t, streamingAdapter(upstream, time.Minute, 150*time.Millisecond), upstream)
	if err == nil {
		t.Fatal("a stream that went silent past its idle budget must fail")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "provider_stream_idle_timeout" || httpErr.Status != http.StatusGatewayTimeout {
		t.Fatalf("idle stream reported %d %s, want 504 provider_stream_idle_timeout", httpErr.Status, httpErr.Code)
	}
	if !strings.Contains(body, "chunk-0") {
		t.Fatalf("bytes delivered before the stream went idle were discarded: %q", body)
	}
}

func TestChatStreamFailsWhenHeadersNeverArrive(t *testing.T) {
	upstream := sseUpstream(t, 500*time.Millisecond, []time.Duration{0})
	defer upstream.Close()

	adapter := streamingAdapter(upstream, time.Minute, 100*time.Millisecond)
	adapter.StreamClient = newUpstreamStreamClient(100 * time.Millisecond)
	if _, err := chatStream(t, adapter, upstream); err == nil {
		t.Fatal("a stream whose headers never arrive must fail")
	}
}

// With no total deadline left, the idle budget is the only thing bounding an
// upstream that announces an error and then stalls without sending its body.
func TestStreamingErrorBodyIsBounded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("transfer-encoding", "chunked")
		w.WriteHeader(http.StatusBadGateway)
		w.(http.Flusher).Flush()
		waitOrDone(r, 5*time.Second)
	}))
	defer upstream.Close()

	done := make(chan error, 1)
	go func() {
		_, err := chatStream(t, streamingAdapter(upstream, time.Minute, 150*time.Millisecond), upstream)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled error response must fail")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an upstream that stalled after its error status hung the request")
	}
}

// Non-streaming calls keep their total deadline: the fix must not turn every
// upstream request into one that can hang indefinitely.
func TestNonStreamingCallKeepsItsTotalTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"late"}}]}`)
	}))
	defer upstream.Close()

	adapter := streamingAdapter(upstream, 100*time.Millisecond, time.Minute)
	_, _, err := adapter.Chat(context.Background(), Provider{BaseURL: upstream.URL}, "model-x",
		ChatCompletionRequest{Model: "model-x", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("a non-streaming call past its total timeout must fail")
	}
}

// Adapters assembled without a stream client — every existing test does this —
// must keep working rather than lose their client entirely.
func TestStreamingFallsBackToTheSharedClient(t *testing.T) {
	upstream := sseUpstream(t, 0, []time.Duration{0})
	defer upstream.Close()

	adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
	body, err := chatStream(t, adapter, upstream)
	if err != nil {
		t.Fatalf("streaming without a stream client failed: %v", err)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream was truncated: %q", body)
	}
}

// OpenResponses is the second streaming entry point; covering only ChatStream
// would leave /v1/responses on the old total deadline.
func TestOpenResponsesUsesTheStreamingClient(t *testing.T) {
	upstream := sseUpstream(t, 0, []time.Duration{80 * time.Millisecond, 80 * time.Millisecond, 80 * time.Millisecond})
	defer upstream.Close()

	adapter := streamingAdapter(upstream, 100*time.Millisecond, time.Second)
	resp, err := adapter.OpenResponses(context.Background(), Provider{BaseURL: upstream.URL}, "model-x",
		ResponsesRequest{Model: "model-x"}, nil)
	if err != nil {
		t.Fatalf("opening a streamed response failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the streamed response failed: %v", err)
	}
	if !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("streamed response was truncated: %q", body)
	}
}

type blockingReadCloser struct {
	release chan struct{}
	closed  chan struct{}
}

func (b *blockingReadCloser) Read([]byte) (int, error) {
	<-b.release
	return 0, io.EOF
}

func (b *blockingReadCloser) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

// A caller closing the body must not be reported as an idle timeout, and must
// not leave a timer armed against a body nobody is reading.
func TestIdleTimeoutReadCloserDoesNotMisreportAnEarlyClose(t *testing.T) {
	source := &blockingReadCloser{release: make(chan struct{}), closed: make(chan struct{})}
	reader := newIdleTimeoutReadCloser(source, time.Hour, errProviderStreamIdle)
	if err := reader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(source.release)
	if _, err := reader.Read(make([]byte, 8)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after close reported %v, want io.EOF", err)
	}
}

func TestUpstreamTimeoutFallsBackForOutOfRangeValues(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{name: "unset", seconds: 0, want: 120 * time.Second},
		{name: "negative", seconds: -5, want: 120 * time.Second},
		{name: "beyond the ceiling", seconds: maxUpstreamTimeoutSeconds + 1, want: 120 * time.Second},
		{name: "configured", seconds: 900, want: 900 * time.Second},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := upstreamTimeout(testCase.seconds, 120); got != testCase.want {
				t.Fatalf("upstreamTimeout(%d) = %v, want %v", testCase.seconds, got, testCase.want)
			}
		})
	}
}

// nativeAnthropicGateway mirrors newAnthropicGateway but pins the upstream
// timeouts, so a native /v1/messages stream can be held past the non-streaming
// deadline without waiting out the two-minute production default.
func nativeAnthropicGateway(t *testing.T, upstreamURL string, config Config) (http.Handler, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Native Anthropic Timeouts", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "native-anthropic-key", Allowed: []string{"claude-tokenhub-test"}, Status: StatusActive,
	}, "thk_native_anthropic_timeout")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "claude-tokenhub-test", Family: "claude", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_native_anthropic", Name: "Native Anthropic", Type: ProviderAnthropic,
		BaseURL: upstreamURL, APIKey: "upstream-secret", Status: StatusActive, Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID: "route_native_anthropic", ModelName: "claude-tokenhub-test", ProviderID: provider.ID,
		ProviderModel: "upstream-model", Priority: 1, Weight: 100, Status: StatusActive,
		Strategy: RouteStrategyPriorityOnly,
	})
	config.AdminToken = "dev_admin_token"
	return NewWithConfig(store, config).Handler(), secret
}

func nativeAnthropicUpstream(t *testing.T, gap time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test upstream cannot stream")
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"upstream-model\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		flusher.Flush()
		if !waitOrDone(r, gap) {
			return
		}
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"late\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
}

// The native Anthropic path builds its own upstream request rather than going
// through the adapter, so it needs its own proof that the non-streaming deadline
// does not apply to it.
func TestNativeAnthropicStreamOutlivesTheNonStreamingTimeout(t *testing.T) {
	upstream := nativeAnthropicUpstream(t, 1400*time.Millisecond)
	defer upstream.Close()

	handler, secret := nativeAnthropicGateway(t, upstream.URL, Config{
		UpstreamNonStreamTimeoutSeconds: 1, UpstreamStreamIdleTimeoutSeconds: 30,
	})
	response := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model": "claude-tokenhub-test", "max_tokens": 1024, "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "Think, then stream."}},
	}, "Bearer "+secret, "")
	if response.Code != http.StatusOK {
		t.Fatalf("native stream returned %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "message_stop") {
		t.Fatalf("native stream was truncated: %s", response.Body.String())
	}
}

func TestNativeAnthropicStreamFailsAfterGoingIdle(t *testing.T) {
	upstream := nativeAnthropicUpstream(t, 3*time.Second)
	defer upstream.Close()

	handler, secret := nativeAnthropicGateway(t, upstream.URL, Config{
		UpstreamNonStreamTimeoutSeconds: 60, UpstreamStreamIdleTimeoutSeconds: 1,
	})
	response := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model": "claude-tokenhub-test", "max_tokens": 1024, "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": "Go quiet."}},
	}, "Bearer "+secret, "")
	if strings.Contains(response.Body.String(), "message_stop") {
		t.Fatalf("an idle native stream was reported as complete: %s", response.Body.String())
	}
}
