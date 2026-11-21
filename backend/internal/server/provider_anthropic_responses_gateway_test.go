package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These probes mirror the review findings on #437: once the Anthropic adapter
// declares the Responses capability, Responses requests must convert and reach
// an Anthropic Messages upstream — string input, message-array input and
// streaming, with no 501 in sight.

func anthropicResponsesUpstream(t *testing.T, requests *[][]byte) *httptest.Server {
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
		// Anthropic streams via the JSON body field, not a query parameter.
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if streamRequested, _ := payload["stream"].(bool); streamRequested {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = w.Write([]byte(strings.Join([]string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"id":"msg_stream01","model":"upstream-model","usage":{"input_tokens":9}}}`,
				``,
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"，世界"}}`,
				``,
				`event: content_block_stop`,
				`data: {"type":"content_block_stop","index":0}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":6}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
				``,
			}, "\n")))
			return
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

func newResponsesGatewayServer(t *testing.T, upstreamURL string) (*Server, *GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Responses Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "responses-key",
		Allowed: []string{"claude-tokenhub-test"},
		Status:  StatusActive,
	}, "thk_responses_test")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		Name:          "claude-tokenhub-test",
		Family:        "claude",
		Modality:      "chat",
		ContextWindow: 200000,
		Capabilities:  []string{"chat", "tools", "reasoning"},
		Status:        StatusActive,
	})
	provider := store.AddProvider(Provider{
		ID:      "prv_responses",
		Name:    "Responses Test Provider",
		Type:    ProviderAnthropic,
		BaseURL: upstreamURL,
		APIKey:  "upstream-secret",
		Status:  StatusActive,
		Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID:            "route_responses",
		ModelName:     "claude-tokenhub-test",
		ProviderID:    provider.ID,
		ProviderModel: "upstream-model",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityOnly,
	})
	return New(store), store, secret
}

// Probe 1: plain string input with instructions and tools reaches the upstream
// as a Messages request and returns a real Responses object.
func TestResponsesStringInputReachesAnthropicUpstream(t *testing.T) {
	var upstreamRequests [][]byte
	upstream := anthropicResponsesUpstream(t, &upstreamRequests)
	defer upstream.Close()
	server, _, secret := newResponsesGatewayServer(t, upstream.URL)

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model":             "claude-tokenhub-test",
		"input":             "Hi",
		"instructions":      "Be brief.",
		"max_output_tokens": 64,
		"tools": []map[string]any{
			{"type": "function", "name": "get_time", "description": "Read the clock", "parameters": map[string]any{"type": "object"}},
		},
		"tool_choice": "required",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"output_text":"Hello from Claude"`) {
		t.Fatalf("response is missing the converted text: %s", resp.Body)
	}
	if !strings.Contains(resp.Body, `"input_tokens":7`) {
		t.Fatalf("response is missing converted usage: %s", resp.Body)
	}

	if len(upstreamRequests) != 1 {
		t.Fatalf("upstream request count = %d, want 1", len(upstreamRequests))
	}
	var upstreamBody map[string]any
	if err := json.Unmarshal(upstreamRequests[0], &upstreamBody); err != nil {
		t.Fatal(err)
	}
	if upstreamBody["max_tokens"] != float64(64) {
		t.Fatalf("upstream max_tokens = %v, want 64", upstreamBody["max_tokens"])
	}
	if upstreamBody["system"] != "Be brief." {
		t.Fatalf("upstream system = %v, want the instructions", upstreamBody["system"])
	}
	tools, _ := upstreamBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("upstream tools = %v, want one entry", upstreamBody["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["input_schema"] == nil {
		t.Fatalf("upstream tool must carry input_schema: %v", tool)
	}
	choice, _ := upstreamBody["tool_choice"].(map[string]any)
	if choice == nil || choice["type"] != "any" {
		t.Fatalf("upstream tool_choice = %v, want any", upstreamBody["tool_choice"])
	}
}

// Probe 2: a message array with function_call and function_call_output items
// becomes Anthropic tool_use / tool_result blocks on the wire.
func TestResponsesToolItemsReachAnthropicUpstream(t *testing.T) {
	var upstreamRequests [][]byte
	upstream := anthropicResponsesUpstream(t, &upstreamRequests)
	defer upstream.Close()
	server, _, secret := newResponsesGatewayServer(t, upstream.URL)

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "claude-tokenhub-test",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "What time is it?"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "get_time", "arguments": `{"tz":"utc"}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "12:00"},
		},
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
	messages, _ := upstreamBody["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("upstream messages = %v, want question/tool-use/tool-result", upstreamBody["messages"])
	}
	assistant, _ := messages[1].(map[string]any)
	assistantContent, _ := assistant["content"].([]any)
	toolUse, _ := assistantContent[0].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["id"] != "call_1" {
		t.Fatalf("tool use block = %v", toolUse)
	}
	user, _ := messages[2].(map[string]any)
	userContent, _ := user["content"].([]any)
	toolResult, _ := userContent[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "call_1" {
		t.Fatalf("tool result block = %v", toolResult)
	}
}

// Probe 3: streaming requests must reach the upstream and come back as
// Responses SSE events, with multibyte text intact.
func TestResponsesStreamingReachesAnthropicUpstream(t *testing.T) {
	var upstreamRequests [][]byte
	upstream := anthropicResponsesUpstream(t, &upstreamRequests)
	defer upstream.Close()
	server, _, secret := newResponsesGatewayServer(t, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model": "claude-tokenhub-test",
		"input": "用中文打个招呼",
		"stream": true
	}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+secret)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: response.created") {
		t.Fatalf("stream is missing response.created: %s", body)
	}
	if !strings.Contains(body, "event: response.output_text.delta") {
		t.Fatalf("stream is missing output_text deltas: %s", body)
	}
	if !strings.Contains(body, "你好") || !strings.Contains(body, "，世界") {
		t.Fatalf("stream lost multibyte deltas: %s", body)
	}
	if !strings.Contains(body, "event: response.completed") || !strings.Contains(body, `"input_tokens":9`) {
		t.Fatalf("stream is missing the completed response with usage: %s", body)
	}
	if len(upstreamRequests) != 1 {
		t.Fatalf("upstream request count = %d, want 1", len(upstreamRequests))
	}
	var upstreamBody map[string]any
	if err := json.Unmarshal(upstreamRequests[0], &upstreamBody); err != nil {
		t.Fatal(err)
	}
	if upstreamBody["stream"] != true {
		t.Fatalf("upstream stream = %v, want true", upstreamBody["stream"])
	}
}

// Probe 4: previous_response_id pulls the stored turn and splices it in front
// of the new input, so Anthropic routes keep multi-turn context.
func TestResponsesPreviousTurnExpandsForAnthropicUpstream(t *testing.T) {
	var upstreamRequests [][]byte
	upstream := anthropicResponsesUpstream(t, &upstreamRequests)
	defer upstream.Close()
	server, _, secret := newResponsesGatewayServer(t, upstream.URL)

	first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "claude-tokenhub-test",
		"input": "Remember the number 7.",
	}, secret)
	if first.Code != http.StatusOK {
		t.Fatalf("first turn status = %d, body = %s", first.Code, first.Body)
	}
	var firstResponse map[string]any
	if err := json.Unmarshal([]byte(first.Body), &firstResponse); err != nil {
		t.Fatal(err)
	}
	responseID, _ := firstResponse["id"].(string)
	if responseID == "" {
		t.Fatalf("first turn produced no response id: %s", first.Body)
	}

	second := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model":                "claude-tokenhub-test",
		"input":                "What was the number?",
		"previous_response_id": responseID,
	}, secret)
	if second.Code != http.StatusOK {
		t.Fatalf("second turn status = %d, body = %s", second.Code, second.Body)
	}
	if len(upstreamRequests) != 2 {
		t.Fatalf("upstream request count = %d, want 2", len(upstreamRequests))
	}
	var upstreamBody map[string]any
	if err := json.Unmarshal(upstreamRequests[1], &upstreamBody); err != nil {
		t.Fatal(err)
	}
	messages, _ := upstreamBody["messages"].([]any)
	// The stored turn contributes its input and its output before the new turn.
	if len(messages) != 3 {
		t.Fatalf("upstream messages = %v, want the prior turn plus the new question", upstreamBody["messages"])
	}

	missing := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model":                "claude-tokenhub-test",
		"input":                "Hello again",
		"previous_response_id": "resp_does_not_exist",
	}, secret)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body, "previous_response_not_found") {
		t.Fatalf("missing reference = %d %s, want 400 previous_response_not_found", missing.Code, missing.Body)
	}
}
