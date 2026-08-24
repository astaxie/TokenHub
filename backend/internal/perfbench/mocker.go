package perfbench

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const maxMockRequestBytes = 16 << 20

type MockConfig struct {
	Latency       time.Duration
	ResponseBytes int
	StreamChunks  int
	ChunkInterval time.Duration
	FailureEvery  uint64
	FailureStatus int
}

func NewMockHandler(config MockConfig) http.Handler {
	if config.ResponseBytes <= 0 {
		config.ResponseBytes = 256
	}
	if config.StreamChunks <= 0 {
		config.StreamChunks = 4
	}
	if config.FailureStatus == 0 {
		config.FailureStatus = http.StatusServiceUnavailable
	}
	return &mockHandler{config: config}
}

type mockHandler struct {
	config  MockConfig
	request atomic.Uint64
}

func (h *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/health" || r.URL.Path == "/healthz"):
		writeMockJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
		writeMockJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []map[string]any{{"id": "benchmark-model", "object": "model"}}})
		return
	case r.Method != http.MethodPost:
		writeMockError(w, http.StatusNotFound, "not_found", "benchmark endpoint not found")
		return
	}

	sequence := h.request.Add(1)
	if h.config.FailureEvery > 0 && sequence%h.config.FailureEvery == 0 {
		time.Sleep(h.config.Latency)
		writeMockError(w, h.config.FailureStatus, "benchmark_injected_failure", "deterministic benchmark failure")
		return
	}

	payload, err := decodeMockRequest(r.Body)
	if err != nil {
		writeMockError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	model, _ := payload["model"].(string)
	if model == "" {
		model = "benchmark-model"
	}
	stream, _ := payload["stream"].(bool)
	time.Sleep(h.config.Latency)
	w.Header().Set("x-tokenhub-benchmark-upstream-latency-ms", strconv.FormatFloat(float64(h.config.Latency)/float64(time.Millisecond), 'f', 3, 64))

	switch {
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		if stream {
			h.streamChat(w, model, sequence)
		} else {
			h.chat(w, model, sequence)
		}
	case strings.HasSuffix(r.URL.Path, "/responses"):
		if stream {
			h.streamResponse(w, model, sequence)
		} else {
			h.response(w, model, sequence)
		}
	case strings.HasSuffix(r.URL.Path, "/embeddings"):
		h.embedding(w, model, sequence)
	default:
		writeMockError(w, http.StatusNotFound, "not_found", "benchmark endpoint not found")
	}
}

func decodeMockRequest(body io.Reader) (map[string]any, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxMockRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	if len(data) > maxMockRequestBytes {
		return nil, fmt.Errorf("request exceeds %d bytes", maxMockRequestBytes)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("request is not valid JSON")
	}
	return payload, nil
}

func (h *mockHandler) chat(w http.ResponseWriter, model string, sequence uint64) {
	writeMockJSON(w, http.StatusOK, map[string]any{
		"id":      fmt.Sprintf("chatcmpl_benchmark_%d", sequence),
		"object":  "chat.completion",
		"created": 0,
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": strings.Repeat("x", h.config.ResponseBytes)},
			"finish_reason": "stop",
		}},
		"usage": mockUsage(),
	})
}

func (h *mockHandler) response(w http.ResponseWriter, model string, sequence uint64) {
	writeMockJSON(w, http.StatusOK, map[string]any{
		"id":     fmt.Sprintf("resp_benchmark_%d", sequence),
		"object": "response",
		"status": "completed",
		"model":  model,
		"output": []map[string]any{{
			"id":   fmt.Sprintf("msg_benchmark_%d", sequence),
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text", "text": strings.Repeat("x", h.config.ResponseBytes), "annotations": []any{},
			}},
		}},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 10, "total_tokens": 20},
	})
}

func (h *mockHandler) embedding(w http.ResponseWriter, model string, sequence uint64) {
	writeMockJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"model":  model,
		"data": []map[string]any{{
			"object": "embedding", "index": 0, "embedding": []float64{float64(sequence % 7), 0.25, 0.5, 0.75},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "total_tokens": 10},
	})
}

func (h *mockHandler) streamChat(w http.ResponseWriter, model string, sequence uint64) {
	prepareMockStream(w)
	for index, content := range splitMockContent(h.config.ResponseBytes, h.config.StreamChunks) {
		writeMockSSE(w, "", map[string]any{
			"id":     fmt.Sprintf("chatcmpl_benchmark_%d", sequence),
			"object": "chat.completion.chunk",
			"model":  model,
			"choices": []map[string]any{{
				"index": index, "delta": map[string]any{"content": content}, "finish_reason": nil,
			}},
		})
		time.Sleep(h.config.ChunkInterval)
	}
	writeMockSSE(w, "", map[string]any{"choices": []any{}, "usage": mockUsage()})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flushMockStream(w)
}

func (h *mockHandler) streamResponse(w http.ResponseWriter, model string, sequence uint64) {
	prepareMockStream(w)
	responseID := fmt.Sprintf("resp_benchmark_%d", sequence)
	writeMockSSE(w, "response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress", "model": model}})
	for _, content := range splitMockContent(h.config.ResponseBytes, h.config.StreamChunks) {
		writeMockSSE(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": content})
		time.Sleep(h.config.ChunkInterval)
	}
	writeMockSSE(w, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": responseID, "status": "completed", "model": model, "output": []any{},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 10, "total_tokens": 20},
		},
	})
	flushMockStream(w)
}

func prepareMockStream(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.WriteHeader(http.StatusOK)
}

func writeMockSSE(w http.ResponseWriter, event string, payload any) {
	data, _ := json.Marshal(payload)
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flushMockStream(w)
}

func flushMockStream(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func splitMockContent(size int, chunks int) []string {
	parts := make([]string, chunks)
	base := size / chunks
	remainder := size % chunks
	for index := range parts {
		partSize := base
		if index < remainder {
			partSize++
		}
		parts[index] = strings.Repeat("x", partSize)
	}
	return parts
}

func mockUsage() map[string]any {
	return map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}
}

func writeMockError(w http.ResponseWriter, status int, code string, message string) {
	writeMockJSON(w, status, map[string]any{"error": map[string]any{"type": code, "code": code, "message": message}})
}

func writeMockJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
