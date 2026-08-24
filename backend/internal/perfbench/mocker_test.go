package perfbench_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/perfbench"
)

func TestMockHandlerServesDeterministicOpenAICompatibleResponses(t *testing.T) {
	t.Parallel()

	handler := perfbench.NewMockHandler(perfbench.MockConfig{
		Latency:       5 * time.Millisecond,
		ResponseBytes: 256,
		FailureEvery:  2,
		FailureStatus: http.StatusServiceUnavailable,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	requestBody := []byte(`{"model":"benchmark-model","messages":[{"role":"user","content":"hello"}]}`)
	started := time.Now()
	first := postJSON(t, server.URL+"/v1/chat/completions", requestBody)
	if elapsed := time.Since(started); elapsed < 5*time.Millisecond {
		t.Fatalf("response arrived before configured latency: %s", elapsed)
	}
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode)
	}
	var payload map[string]any
	decodeResponse(t, first, &payload)
	if payload["model"] != "benchmark-model" {
		t.Fatalf("model = %v, want benchmark-model", payload["model"])
	}
	if len(payload["choices"].([]any)) != 1 {
		t.Fatalf("unexpected choices: %v", payload["choices"])
	}

	second := postJSON(t, server.URL+"/v1/chat/completions", requestBody)
	defer second.Body.Close()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", second.StatusCode)
	}
}

func TestMockHandlerStreamsConfiguredChunks(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(perfbench.NewMockHandler(perfbench.MockConfig{
		StreamChunks:  3,
		ChunkInterval: time.Millisecond,
		ResponseBytes: 60,
	}))
	t.Cleanup(server.Close)

	response := postJSON(t, server.URL+"/v1/chat/completions", []byte(`{"model":"benchmark-model","stream":true,"messages":[]}`))
	defer response.Body.Close()
	if got := response.Header.Get("content-type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}

	scanner := bufio.NewScanner(response.Body)
	var dataFrames []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			dataFrames = append(dataFrames, strings.TrimPrefix(line, "data: "))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(dataFrames) != 5 {
		t.Fatalf("data frames = %d, want 3 chunks, usage, and DONE: %v", len(dataFrames), dataFrames)
	}
	if dataFrames[len(dataFrames)-1] != "[DONE]" {
		t.Fatalf("last frame = %q, want DONE", dataFrames[len(dataFrames)-1])
	}
}

func postJSON(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode response: %v: %s", err, data)
	}
}
