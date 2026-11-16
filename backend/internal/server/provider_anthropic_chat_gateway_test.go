package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Anthropic adapter declares chat/completions and responses route
// protocols, so an OpenAI-protocol request must convert and reach an
// Anthropic Messages upstream even though the route stores it like any
// other chat route.

func anthropicChatUpstream(t *testing.T, requests *[][]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		body := make([]byte, r.ContentLength)
		if _, err := r.Body.Read(body); err != nil && len(body) == 0 {
			t.Errorf("read upstream request: %v", err)
		}
		if requests != nil {
			*requests = append(*requests, body)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_upstream",
			"type": "message",
			"role": "assistant",
			"model": "upstream-model",
			"content": [{"type": "text", "text": "Hello from Claude"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 7, "output_tokens": 4}
		}`))
	}))
}

func TestChatCompletionsRoutesToAnthropicUpstream(t *testing.T) {
	var upstreamRequests [][]byte
	upstream := anthropicChatUpstream(t, &upstreamRequests)
	defer upstream.Close()

	server, _, secret := newAnthropicGatewayServer(t, upstream.URL, ProviderAnthropic)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "claude-tokenhub-test",
		"messages": []map[string]any{
			{"role": "system", "content": "Be brief."},
			{"role": "user", "content": "Hi"},
		},
		"max_tokens":  64,
		"temperature": 0.5,
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body)
	}

	if len(upstreamRequests) != 1 {
		t.Fatalf("upstream request count = %d, want 1", len(upstreamRequests))
	}
	var upstreamBody map[string]any
	if err := json.Unmarshal(upstreamRequests[0], &upstreamBody); err != nil {
		t.Fatal(err)
	}
	if upstreamBody["model"] != "upstream-model" {
		t.Fatalf("upstream model = %v, want upstream-model", upstreamBody["model"])
	}
	if upstreamBody["max_tokens"] != float64(64) {
		t.Fatalf("upstream max_tokens = %v, want 64", upstreamBody["max_tokens"])
	}
	if upstreamBody["temperature"] != 0.5 {
		t.Fatalf("upstream temperature = %v, want 0.5", upstreamBody["temperature"])
	}
	system, _ := upstreamBody["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("upstream system blocks = %v, want one text block", upstreamBody["system"])
	}
	messages, _ := upstreamBody["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("upstream messages = %v, want a single user turn", upstreamBody["messages"])
	}

	var converted map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &converted); err != nil {
		t.Fatal(err)
	}
	choices, _ := converted["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v, want one choice", converted["choices"])
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message["content"] != "Hello from Claude" {
		t.Fatalf("message content = %v, want converted Anthropic text", message["content"])
	}
	usage, _ := converted["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(7) || usage["completion_tokens"] != float64(4) {
		t.Fatalf("usage = %v, want converted Anthropic usage", usage)
	}
}

func TestChatCompletionsStreamsFromAnthropicUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_stream","usage":{"input_tokens":7}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" from Claude"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	server, _, secret := newAnthropicGatewayServer(t, upstream.URL, ProviderAnthropic)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model": "claude-tokenhub-test",
		"messages": [{"role": "user", "content": "Hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+secret)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"content":"Hello"`) || !strings.Contains(body, `"content":" from Claude"`) {
		t.Fatalf("stream is missing converted deltas: %s", body)
	}
	if !strings.Contains(body, `"prompt_tokens":7`) || !strings.Contains(body, `"completion_tokens":4`) {
		t.Fatalf("stream is missing converted usage: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream is missing the terminal sentinel: %s", body)
	}
}

// Route creation must accept catalog models that advertise the
// chat/completions endpoint against an Anthropic provider now that the
// adapter declares the protocol.
func TestValidateRouteModelProtocolAcceptsChatEndpointForAnthropic(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	store.AddModel(Model{
		Name:     "catalog-chat-model",
		Modality: "chat",
		Status:   StatusActive,
		Metadata: map[string]string{"endpoints": "chat/completions"},
	})
	descriptor, ok := server.adapterRegistry.Describe(ProviderAnthropic)
	if !ok {
		t.Fatal("Anthropic adapter descriptor is missing")
	}
	if err := server.validateRouteModelProtocol("catalog-chat-model", nil, descriptor); err != nil {
		t.Fatalf("chat/completions model rejected for Anthropic provider: %v", err)
	}

	store.AddModel(Model{
		Name:     "catalog-responses-model",
		Modality: "chat",
		Status:   StatusActive,
		Metadata: map[string]string{"endpoints": "responses"},
	})
	if err := server.validateRouteModelProtocol("catalog-responses-model", nil, descriptor); err != nil {
		t.Fatalf("responses model rejected for Anthropic provider: %v", err)
	}
}
