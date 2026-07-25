package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockingWriter reports each frame as it is written so a test can prove the
// stream is delivered incrementally rather than buffered until completion.
type blockingWriter struct {
	mu     sync.Mutex
	frames []string
	notify chan string
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{notify: make(chan string, 32)}
}

func (w *blockingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	w.frames = append(w.frames, string(data))
	w.mu.Unlock()
	w.notify <- string(data)
	return len(data), nil
}

func (w *blockingWriter) Flush() {}

func TestAnthropicAdapterStreamsIncrementally(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if payload["stream"] != true {
			t.Errorf("the adapter must ask the provider for a stream, got %v", payload["stream"])
		}
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4}}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first\"}}\n\n")
		flusher.Flush()

		// Hold the rest of the response back; the client must already have the
		// first chunk. A buffered implementation would deadlock this test.
		<-release

		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"second\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":6}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{Client: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL, APIKey: "test-key"}
	writer := newBlockingWriter()

	var (
		usage Usage
		err   error
		done  = make(chan struct{})
	)
	go func() {
		defer close(done)
		usage, err = adapter.ChatStream(context.Background(), provider, "claude-test",
			ChatCompletionRequest{Model: "model-x", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, writer)
	}()

	// Wait for the first content frame to reach the writer while the upstream
	// response is still open.
	deadline := time.After(5 * time.Second)
	sawFirst := false
	for !sawFirst {
		select {
		case frame := <-writer.notify:
			if strings.Contains(frame, "first") {
				sawFirst = true
			}
		case <-deadline:
			t.Fatalf("the first content chunk was not delivered before the upstream response completed")
		}
	}

	close(release)
	<-done

	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if usage.PromptTokens != 4 || usage.CompletionTokens != 6 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	writer.mu.Lock()
	combined := strings.Join(writer.frames, "")
	writer.mu.Unlock()
	if !strings.HasSuffix(combined, "data: [DONE]\n\n") {
		t.Fatalf("a completed stream must end with the [DONE] sentinel")
	}
}

func TestGeminiAdapterStreamRequestTargetsSSEEndpoint(t *testing.T) {
	var captured *http.Request
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		w.Header().Set("content-type", "text/event-stream")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3}}\n\n")
	}))
	defer upstream.Close()

	adapter := GeminiAdapter{Client: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL, APIKey: "secret-key"}
	writer := &recordingWriter{}

	usage, err := adapter.ChatStream(context.Background(), provider, "gemini-test",
		ChatCompletionRequest{Model: "model-x", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, writer)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if captured == nil {
		t.Fatalf("upstream never received a request")
	}
	query := captured.URL.Query()
	// The action already carries a query string, so the API key has to be
	// appended with the right separator or authentication silently breaks.
	if query.Get("alt") != "sse" {
		t.Fatalf("streaming must request the SSE transport, got %q", captured.URL.RawQuery)
	}
	if query.Get("key") != "secret-key" {
		t.Fatalf("the API key must survive alongside the alt parameter, got %q", captured.URL.RawQuery)
	}
	if !strings.Contains(captured.URL.Path, ":streamGenerateContent") {
		t.Fatalf("unexpected upstream path: %q", captured.URL.Path)
	}
	if usage.PromptTokens != 2 || usage.CompletionTokens != 3 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestAnthropicAdapterChatConvertsToolCalls(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if _, present := payload["tools"]; !present {
			t.Errorf("tools must reach the provider, got %v", payload)
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"content":[{"type":"tool_use","id":"toolu_1","name":"ping","input":{"a":1}}],`+
			`"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":5}}`)
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{Client: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL, APIKey: "test-key"}

	response, usage, err := adapter.Chat(context.Background(), provider, "claude-test", ChatCompletionRequest{
		Model:    "model-x",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:    []any{map[string]any{"function": map[string]any{"name": "ping"}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	body, _ := response.(map[string]any)
	choices, _ := body["choices"].([]map[string]any)
	message, _ := choices[0]["message"].(map[string]any)
	if _, present := message["tool_calls"]; !present {
		t.Fatalf("the provider tool_use block must surface as tool_calls: %v", message)
	}
	if usage.PromptTokens != 3 || usage.CompletionTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestAnthropicAdapterPropagatesUpstreamHTTPError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"type":"rate_limit_error","message":"slow down"}}`)
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{Client: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL, APIKey: "test-key"}
	writer := &recordingWriter{}

	_, err := adapter.ChatStream(context.Background(), provider, "claude-test",
		ChatCompletionRequest{Model: "model-x", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}, writer)
	if err == nil {
		t.Fatalf("expected the upstream error to propagate")
	}
	// Nothing was written, so the caller is still free to fail over to another route.
	if writer.builder.Len() != 0 {
		t.Fatalf("a pre-stream failure must not write anything to the client: %q", writer.builder.String())
	}
	if status := AsHTTPError(err).Status; status != http.StatusTooManyRequests {
		t.Fatalf("expected the upstream status to be preserved, got %d", status)
	}
}
