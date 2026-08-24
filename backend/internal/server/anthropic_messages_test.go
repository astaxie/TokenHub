package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAnthropicMessagesConvertsToolsAndToolResultsForOpenAI(t *testing.T) {
	var mu sync.Mutex
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		if r.Header.Get("authorization") != "Bearer upstream-secret" {
			t.Errorf("unexpected upstream authorization %q", r.Header.Get("authorization"))
		}
		var payload map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		mu.Lock()
		upstreamRequests = append(upstreamRequests, payload)
		requestNumber := len(upstreamRequests)
		mu.Unlock()

		w.Header().Set("content-type", "application/json")
		if requestNumber == 1 {
			_, _ = io.WriteString(w, `{
				"id":"chatcmpl_tools",
				"choices":[{
					"index":0,
					"message":{
						"role":"assistant",
						"content":"I will inspect both files.",
						"tool_calls":[
							{"id":"call_read_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"README.md\"}"}},
							{"id":"call_read_2","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"backend/internal/server/http.go\"}"}}
						]
					},
					"finish_reason":"tool_calls"
				}],
				"usage":{
					"prompt_tokens":120,
					"completion_tokens":30,
					"total_tokens":150,
					"prompt_tokens_details":{"cached_tokens":80}
				}
			}`)
			return
		}
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl_final",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"TokenHub is a Go gateway with a Next.js console."},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":180,"completion_tokens":20,"total_tokens":200}
		}`)
	}))
	defer upstream.Close()

	handler, store, secret := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
	first := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 2048,
		"system": []any{
			map[string]any{"type": "text", "text": "Understand the repository.", "cache_control": map[string]any{"type": "ephemeral"}},
		},
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "Inspect the repository."},
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type": "url",
							"url":  "https://example.com/architecture.png",
						},
					},
				},
			},
		},
		"tools": []any{
			map[string]any{
				"name":        "Read",
				"description": "Read a file",
				"input_schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"file_path": map[string]any{"type": "string"}},
					"required":   []any{"file_path"},
				},
				"cache_control": map[string]any{"type": "ephemeral"},
			},
		},
		"tool_choice": map[string]any{"type": "auto", "disable_parallel_tool_use": false},
	}, "Bearer "+secret, "")
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", first.Code, first.Body.String())
	}
	var firstResponse map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse["type"] != "message" || firstResponse["model"] != "claude-tokenhub-test" || firstResponse["stop_reason"] != "tool_use" {
		t.Fatalf("unexpected Anthropic response: %s", first.Body)
	}
	content, _ := firstResponse["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("expected text and two tool_use blocks: %s", first.Body)
	}
	for _, index := range []int{1, 2} {
		block, _ := content[index].(map[string]any)
		if block["type"] != "tool_use" || block["name"] != "Read" {
			t.Fatalf("unexpected tool block: %#v", block)
		}
	}
	firstUsage, _ := firstResponse["usage"].(map[string]any)
	if firstUsage["input_tokens"] != float64(40) || firstUsage["cache_read_input_tokens"] != float64(80) {
		t.Fatalf("expected Anthropic cache usage without double counting: %#v", firstUsage)
	}

	mu.Lock()
	firstUpstream := upstreamRequests[0]
	mu.Unlock()
	if firstUpstream["model"] != "upstream-model" {
		t.Fatalf("expected routed provider model, got %#v", firstUpstream["model"])
	}
	upstreamMessages, _ := firstUpstream["messages"].([]any)
	if len(upstreamMessages) != 2 {
		t.Fatalf("expected system and user messages, got %#v", upstreamMessages)
	}
	userMessage, _ := upstreamMessages[1].(map[string]any)
	userContent, _ := userMessage["content"].([]any)
	imagePart, _ := userContent[1].(map[string]any)
	if imagePart["type"] != "image_url" {
		t.Fatalf("expected OpenAI image_url conversion, got %#v", imagePart)
	}
	upstreamTools, _ := firstUpstream["tools"].([]any)
	if len(upstreamTools) != 1 {
		t.Fatalf("expected one converted tool, got %#v", firstUpstream["tools"])
	}
	if firstUpstream["parallel_tool_calls"] != true {
		t.Fatalf("expected parallel tool calls to remain enabled: %#v", firstUpstream)
	}

	second := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 2048,
		"messages": []any{
			map[string]any{"role": "user", "content": "Inspect the repository."},
			map[string]any{"role": "assistant", "content": content},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "call_read_1", "content": "# TokenHub"},
					map[string]any{"type": "tool_result", "tool_use_id": "call_read_2", "content": "package server"},
				},
			},
		},
		"tools": []any{
			map[string]any{
				"name":         "Read",
				"description":  "Read a file",
				"input_schema": map[string]any{"type": "object"},
			},
		},
	}, "Bearer "+secret, "")
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "TokenHub is a Go gateway") {
		t.Fatalf("unexpected final response: %s", second.Body)
	}

	mu.Lock()
	secondUpstream := upstreamRequests[1]
	mu.Unlock()
	secondMessages, _ := secondUpstream["messages"].([]any)
	if len(secondMessages) != 4 {
		t.Fatalf("expected user, assistant, and two tool messages, got %#v", secondMessages)
	}
	assistantMessage, _ := secondMessages[1].(map[string]any)
	assistantCalls, _ := assistantMessage["tool_calls"].([]any)
	if len(assistantCalls) != 2 {
		t.Fatalf("expected two upstream tool_calls, got %#v", assistantMessage)
	}
	for _, index := range []int{2, 3} {
		toolMessage, _ := secondMessages[index].(map[string]any)
		if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] == "" {
			t.Fatalf("unexpected tool result message: %#v", toolMessage)
		}
	}

	if len(store.ListUsageRecords()) != 2 {
		t.Fatalf("expected two billed inference records, got %d", len(store.ListUsageRecords()))
	}
}

func TestAnthropicMessagesOmitsEmptyToolCallsForOpenAI(t *testing.T) {
	var mu sync.Mutex
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		mu.Lock()
		upstreamRequests = append(upstreamRequests, payload)
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl_nocalls",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Understood."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`)
	}))
	defer upstream.Close()

	handler, _, secret := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Summarize the plan."},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "No tool call needed."},
					map[string]any{"type": "text", "text": "Let me check."},
				},
			},
		},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(upstreamRequests) != 1 {
		t.Fatalf("expected one upstream request, got %d", len(upstreamRequests))
	}
	messages, _ := upstreamRequests[0]["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("expected two upstream messages, got %#v", messages)
	}
	assistant, _ := messages[1].(map[string]any)
	if _, present := assistant["tool_calls"]; present {
		t.Fatalf("assistant message without tool_use must not carry tool_calls, got %#v", assistant)
	}
}

func TestAnthropicMessagesConvertsOpenAIStreamingTextAndToolCall(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if payload["stream"] != true {
			t.Errorf("expected streaming upstream request: %#v", payload)
		}
		options, _ := payload["stream_options"].(map[string]any)
		if options["include_usage"] != true {
			t.Errorf("expected include_usage stream option: %#v", options)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"I will inspect \"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_read\",\"type\":\"function\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"file_path\\\":\"}}]},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"README.md\\\"}\"}}]},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":90,\"completion_tokens\":12,\"total_tokens\":102,\"prompt_tokens_details\":{\"cached_tokens\":80}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	handler, store, secret := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []any{map[string]any{"role": "user", "content": "Understand this repo."}},
		"tools": []any{
			map[string]any{
				"name":         "Read",
				"description":  "Read a file",
				"input_schema": map[string]any{"type": "object"},
			},
		},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, expected := range []string{
		"event: message_start",
		"event: content_block_start",
		`"text":"I will inspect ","type":"text_delta"`,
		`"id":"call_read","input":{},"name":"Read","type":"tool_use"`,
		`"partial_json":"{\"file_path\":\"README.md\"}","type":"input_json_delta"`,
		`"stop_reason":"tool_use"`,
		"event: message_stop",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream is missing %q:\n%s", expected, body)
		}
	}
	events := parsePlaygroundSSE(t, body)
	finalUsage, _ := findPlaygroundSSEEvent(t, events, "message_delta").Data["usage"].(map[string]any)
	if finalUsage["input_tokens"] != float64(10) || finalUsage["cache_read_input_tokens"] != float64(80) || finalUsage["cache_creation_input_tokens"] != float64(0) || finalUsage["output_tokens"] != float64(12) {
		t.Fatalf("expected authoritative Anthropic cache usage in the final event, got %#v", finalUsage)
	}
	records := store.ListUsageRecords()
	if len(records) != 1 || records[0].InputTokens != 90 || records[0].CachedInputTokens != 80 || records[0].OutputTokens != 12 || records[0].TotalTokens != 102 {
		t.Fatalf("unexpected streaming usage records: %+v", records)
	}
}

func TestAnthropicMessagesPreservesNativeProtocolAndHeaders(t *testing.T) {
	var upstreamPayload map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "upstream-secret" {
			t.Errorf("unexpected upstream key %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected version %q", r.Header.Get("anthropic-version"))
		}
		if r.Header.Get("anthropic-beta") != "interleaved-thinking-2025-05-14" {
			t.Errorf("unexpected beta %q", r.Header.Get("anthropic-beta"))
		}
		if r.Header.Get("User-Agent") != "TokenHub-Anthropic/1.0" {
			t.Errorf("unexpected custom User-Agent %q", r.Header.Get("User-Agent"))
		}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&upstreamPayload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_native",
			"type":"message",
			"role":"assistant",
			"model":"upstream-model",
			"content":[{"type":"tool_use","id":"toolu_native","name":"Read","input":{"file_path":"README.md"}}],
			"stop_reason":"tool_use",
			"stop_sequence":null,
			"usage":{"input_tokens":50,"output_tokens":10}
		}`)
	}))
	defer upstream.Close()

	handler, store, secret := newAnthropicGateway(t, upstream.URL+"/v1", ProviderAnthropic)
	provider, _ := store.GetProvider("prv_claude_code")
	provider.Headers = map[string]string{"User-Agent": "TokenHub-Anthropic/1.0"}
	if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
		t.Fatal(err)
	}
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 1024,
		"thinking":   map[string]any{"type": "adaptive"},
		"messages":   []any{map[string]any{"role": "user", "content": "Inspect the repository."}},
		"tools": []any{
			map[string]any{
				"name":         "Read",
				"input_schema": map[string]any{"type": "object"},
			},
		},
	}, "Bearer "+secret, "interleaved-thinking-2025-05-14")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if upstreamPayload["model"] != "upstream-model" || upstreamPayload["stream"] != false {
		t.Fatalf("unexpected native upstream payload: %#v", upstreamPayload)
	}
	if _, ok := upstreamPayload["thinking"].(map[string]any); !ok {
		t.Fatalf("expected native thinking payload to be preserved: %#v", upstreamPayload)
	}
	if !strings.Contains(resp.Body.String(), `"model":"claude-tokenhub-test"`) ||
		!strings.Contains(resp.Body.String(), `"type":"tool_use"`) {
		t.Fatalf("native response was not preserved and remapped: %s", resp.Body)
	}
}

func TestAnthropicMessagesUsesBearerAuthForNativeProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "Bearer upstream-secret" {
			t.Errorf("unexpected upstream authorization %q", got)
		}
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key must be omitted in bearer mode, got %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_bearer",
			"type":"message",
			"role":"assistant",
			"model":"upstream-model",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`)
	}))
	defer upstream.Close()

	handler, _, secret := newAnthropicGatewayWithOptions(t, upstream.URL, ProviderAnthropic, map[string]string{
		anthropicAuthTypeOption: anthropicAuthTypeBearer,
	})
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 64,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestAnthropicMessagesConvertsMidConversationSystemForOpenAI(t *testing.T) {
	var upstreamPayload map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl_mid_system",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}
		}`)
	}))
	defer upstream.Close()

	handler, _, secret := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doAnthropicRequestWithBeta(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Inspect the repository."},
			map[string]any{
				"role": "system",
				"content": []any{
					map[string]any{
						"type":          "text",
						"text":          "Keep the next response concise.",
						"cache_control": map[string]any{"type": "ephemeral"},
					},
				},
			},
			map[string]any{"role": "user", "content": "Summarize it."},
		},
	}, "Bearer "+secret, "", "interleaved-thinking-2025-05-14, "+anthropicMidConversationSystemBeta)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	messages, _ := upstreamPayload["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("expected three ordered upstream messages, got %#v", messages)
	}
	systemMessage, _ := messages[1].(map[string]any)
	if systemMessage["role"] != "system" || systemMessage["content"] != "Keep the next response concise." {
		t.Fatalf("expected translated mid-conversation system message, got %#v", systemMessage)
	}
}

func TestAnthropicMessagesPreservesMidConversationSystemForNativeRoute(t *testing.T) {
	var upstreamPayload map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-beta") != anthropicMidConversationSystemBeta {
			t.Errorf("unexpected beta %q", r.Header.Get("anthropic-beta"))
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_mid_system",
			"type":"message",
			"role":"assistant",
			"model":"upstream-model",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{"input_tokens":10,"output_tokens":1}
		}`)
	}))
	defer upstream.Close()

	handler, _, secret := newAnthropicGateway(t, upstream.URL, ProviderAnthropic)
	resp := doAnthropicRequestWithBeta(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Inspect the repository."},
			map[string]any{"role": "system", "content": "Keep the next response concise."},
			map[string]any{"role": "user", "content": "Summarize it."},
		},
	}, "Bearer "+secret, "", anthropicMidConversationSystemBeta)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	messages, _ := upstreamPayload["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("expected native messages to be preserved, got %#v", messages)
	}
	systemMessage, _ := messages[1].(map[string]any)
	if systemMessage["role"] != "system" || systemMessage["content"] != "Keep the next response concise." {
		t.Fatalf("expected native mid-conversation system message, got %#v", systemMessage)
	}
}

func TestAnthropicMessagesRejectsSystemRoleWithoutBeta(t *testing.T) {
	body := bytes.NewBufferString(`{
		"model":"claude-tokenhub-test",
		"max_tokens":1024,
		"messages":[
			{"role":"user","content":"Inspect the repository."},
			{"role":"system","content":"Keep the next response concise."}
		]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	req.Header.Set("content-type", "application/json")

	_, err := New(NewMemoryStore()).decodeAnthropicMessagesRequest(httptest.NewRecorder(), req, true)
	if err == nil {
		t.Fatal("expected system role without beta to be rejected")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadRequest || httpErr.Code != "invalid_message" {
		t.Fatalf("expected invalid_message, got %#v", httpErr)
	}
}

func TestAnthropicMessagesPreservesNativeStreamingAndUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if payload["stream"] != true || payload["model"] != "upstream-model" {
			t.Errorf("unexpected native stream payload: %#v", payload)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_upstream\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"upstream-model\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":0,\"cache_read_input_tokens\":40,\"output_tokens\":1}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_start\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Native stream\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_stop\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":8}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	handler, store, secret := newAnthropicGateway(t, upstream.URL, ProviderAnthropic)
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 1024,
		"stream":     true,
		"messages":   []any{map[string]any{"role": "user", "content": "Stream natively."}},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"model":"claude-tokenhub-test"`) ||
		strings.Contains(resp.Body.String(), `"model":"upstream-model"`) {
		t.Fatalf("expected public model name in native stream: %s", resp.Body)
	}
	records := store.ListUsageRecords()
	if len(records) != 1 ||
		records[0].InputTokens != 40 ||
		records[0].CachedInputTokens != 40 ||
		records[0].OutputTokens != 8 {
		t.Fatalf("unexpected native stream usage: %+v", records)
	}
}

func TestAnthropicCountTokensAndAuthentication(t *testing.T) {
	handler, store, secret := newAnthropicGateway(t, "http://127.0.0.1:1", ProviderOpenAICompatible)
	payload := map[string]any{
		"model":    "claude-tokenhub-test",
		"system":   "Understand the codebase.",
		"messages": []any{map[string]any{"role": "user", "content": "Inspect backend and frontend."}},
		"tools": []any{
			map[string]any{
				"name":         "Read",
				"description":  "Read a file",
				"input_schema": map[string]any{"type": "object"},
			},
		},
	}
	resp := doAnthropicRequest(t, handler, "/v1/messages/count_tokens", payload, "", secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected x-api-key authentication to work, got %d: %s", resp.Code, resp.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["input_tokens"].(float64) <= 1 {
		t.Fatalf("expected a non-trivial token count: %s", resp.Body)
	}
	if len(store.ListUsageRecords()) != 0 {
		t.Fatalf("token counting must not create billed usage: %+v", store.ListUsageRecords())
	}

	malformed := doAnthropicRequest(t, handler, "/v1/messages/count_tokens", payload, "Basic invalid", secret)
	if malformed.Code != http.StatusUnauthorized {
		t.Fatalf("malformed Authorization must not fall back to x-api-key: %d %s", malformed.Code, malformed.Body.String())
	}
	if !strings.Contains(malformed.Body.String(), `"type":"authentication_error"`) {
		t.Fatalf("expected Anthropic authentication error: %s", malformed.Body)
	}
}

func TestOpenAIChatCompletionPreservesDocumentedToolFields(t *testing.T) {
	var upstreamPayload map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl_direct_tools",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}
		}`)
	}))
	defer upstream.Close()

	handler, _, secret := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doJSON(t, handler, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "claude-tokenhub-test",
		"messages": []any{map[string]any{"role": "user", "content": "Inspect the repository."}},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":       "inspect_repository",
					"parameters": map[string]any{"type": "object"},
				},
			},
		},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"response_format":     map[string]any{"type": "json_object"},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	for _, field := range []string{"tools", "tool_choice", "parallel_tool_calls", "response_format"} {
		if _, exists := upstreamPayload[field]; !exists {
			t.Fatalf("expected direct Chat Completions field %q upstream: %#v", field, upstreamPayload)
		}
	}
}

func TestAnthropicMessagesSkipsIncompatibleRouteWithoutProviderPenalty(t *testing.T) {
	var nativeCalls int
	nativeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls++
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_native_fallback",
			"type":"message",
			"role":"assistant",
			"model":"native-upstream-model",
			"content":[{"type":"text","text":"Native server tool route selected."}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{"input_tokens":20,"output_tokens":5}
		}`)
	}))
	defer nativeUpstream.Close()

	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Route Compatibility Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "route-compatibility-key",
		Allowed: []string{"claude-route-compatibility"},
		Status:  StatusActive,
	}, "thk_route_compatibility")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		Name:          "claude-route-compatibility",
		Family:        "claude",
		Modality:      "chat",
		ContextWindow: 200000,
		Status:        StatusActive,
	})
	openAIProvider := store.AddProvider(Provider{
		ID:      "prv_incompatible_openai",
		Name:    "Incompatible OpenAI",
		Type:    ProviderOpenAICompatible,
		BaseURL: "http://127.0.0.1:1",
		Status:  StatusActive,
		Healthy: true,
	})
	openAIResource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_incompatible_openai",
		ProviderID:   openAIProvider.ID,
		Name:         "Incompatible OpenAI Resource",
		ResourceType: ProviderResourceAPIKey,
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	nativeProvider := store.AddProvider(Provider{
		ID:      "prv_compatible_anthropic",
		Name:    "Compatible Anthropic",
		Type:    ProviderAnthropic,
		BaseURL: nativeUpstream.URL,
		APIKey:  "native-secret",
		Status:  StatusActive,
		Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID:                 "route_incompatible_openai",
		ModelName:          "claude-route-compatibility",
		ProviderID:         openAIProvider.ID,
		ProviderResourceID: openAIResource.ID,
		ProviderModel:      "openai-upstream-model",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
		Strategy:           RouteStrategyPriorityOnly,
	})
	store.AddRoute(ModelRoute{
		ID:            "route_compatible_anthropic",
		ModelName:     "claude-route-compatibility",
		ProviderID:    nativeProvider.ID,
		ProviderModel: "native-upstream-model",
		Priority:      2,
		Weight:        100,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityOnly,
	})

	handler := New(store).Handler()
	for attempt := 0; attempt < 3; attempt++ {
		resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
			"model":      "claude-route-compatibility",
			"max_tokens": 256,
			"messages":   []any{map[string]any{"role": "user", "content": "Search the web."}},
			"tools": []any{
				map[string]any{"type": "web_search_20260209", "name": "web_search"},
			},
		}, "Bearer "+secret, "")
		if resp.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected native fallback, got %d: %s", attempt+1, resp.Code, resp.Body.String())
		}
	}
	if nativeCalls != 3 {
		t.Fatalf("expected three native fallback calls, got %d", nativeCalls)
	}
	var routeAttempts []RouteAttemptLog
	if err := store.db.Order("created_at asc, attempt_index asc").Find(&routeAttempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(routeAttempts) != 3 {
		t.Fatalf("expected only the three contacted native routes to be audited, got %#v", routeAttempts)
	}
	for _, attempt := range routeAttempts {
		if attempt.RouteID != "route_compatible_anthropic" ||
			attempt.ProviderID != nativeProvider.ID ||
			attempt.StatusCode != http.StatusOK ||
			attempt.AttemptIndex != 1 {
			t.Fatalf("uncontacted or misordered route was audited: %#v", routeAttempts)
		}
	}
	for _, resource := range store.ListProviderResources() {
		if resource.ID == openAIResource.ID {
			if resource.FailureCount != 0 || !resource.Healthy || resource.CooldownUntil != nil {
				t.Fatalf("route incompatibility penalized provider resource: %+v", resource)
			}
			return
		}
	}
	t.Fatalf("provider resource %q not found", openAIResource.ID)
}

func TestCompatibleAnthropicRoutesUsesHighestPriorityError(t *testing.T) {
	req := anthropicMessagesRequest{
		Raw: map[string]any{
			"model":      "claude-route-compatibility",
			"max_tokens": 256,
			"messages":   []any{map[string]any{"role": "user", "content": "Search the web."}},
			"tools": []any{
				map[string]any{"type": "web_search_20260209", "name": "web_search"},
			},
		},
		Model: "claude-route-compatibility",
		Messages: []any{
			map[string]any{"role": "user", "content": "Search the web."},
		},
		MaxTokens: 256,
	}
	routed := RoutedCall{Routes: []RouteSelection{
		{
			Route:    ModelRoute{ID: "route_high_priority_openai", Priority: 1},
			Provider: Provider{ID: "prv_openai", Type: ProviderOpenAICompatible},
		},
		{
			Route:    ModelRoute{ID: "route_lower_priority_gemini", Priority: 2},
			Provider: Provider{ID: "prv_gemini", Type: ProviderGemini},
		},
	}}

	compatible, err := compatibleAnthropicRoutes(routed, req)
	if len(compatible.Routes) != 0 {
		t.Fatalf("expected no compatible routes, got %#v", compatible.Routes)
	}
	if AsHTTPError(err).Code != "unsupported_tool" {
		t.Fatalf("expected highest-priority route error, got %v", err)
	}
}

func TestCompatibleAnthropicRoutesSkipsProviderRejectingReasoningEffort(t *testing.T) {
	req := anthropicMessagesRequest{
		Raw: map[string]any{
			"model":         "claude-route-reasoning",
			"max_tokens":    256,
			"output_config": map[string]any{"effort": "xhigh"},
			"messages":      []any{map[string]any{"role": "user", "content": "Plan the change."}},
		},
		Model:     "claude-route-reasoning",
		Messages:  []any{map[string]any{"role": "user", "content": "Plan the change."}},
		MaxTokens: 256,
	}
	routed := RoutedCall{Routes: []RouteSelection{
		{
			Route: ModelRoute{ID: "route_rejecting", Priority: 1},
			Provider: Provider{ID: "prv_rejecting", Type: ProviderOpenAICompatible, Options: map[string]string{
				reasoningEffortValuesOption:      "none,low,medium,high,max",
				reasoningEffortUnsupportedOption: "reject",
			}},
		},
		{
			Route: ModelRoute{ID: "route_compatible", Priority: 2},
			Provider: Provider{ID: "prv_compatible", Type: ProviderOpenAICompatible, Options: map[string]string{
				reasoningEffortValuesOption: "none,low,medium,high,max,xhigh",
			}},
		},
	}}

	compatible, err := compatibleAnthropicRoutes(routed, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(compatible.Routes) != 1 || compatible.Routes[0].Route.ID != "route_compatible" {
		t.Fatalf("expected only the compatible route, got %#v", compatible.Routes)
	}
}

func TestAnthropicMessagesRejectsUnsupportedServerToolOnOpenAIRoute(t *testing.T) {
	handler, _, secret := newAnthropicGateway(t, "http://127.0.0.1:1", ProviderOpenAICompatible)
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 1024,
		"messages":   []any{map[string]any{"role": "user", "content": "Search the web."}},
		"tools": []any{
			map[string]any{"type": "web_search_20260209", "name": "web_search"},
		},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"code":"unsupported_tool"`) {
		t.Fatalf("expected explicit unsupported_tool error: %s", resp.Body)
	}
}

func TestGatewayModelsIncludeAnthropicDiscoveryFields(t *testing.T) {
	handler := newTestServer()
	resp := doJSON(t, handler, http.MethodGet, "/v1/models", nil, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["has_more"] != false || payload["first_id"] == "" || payload["last_id"] == "" {
		t.Fatalf("expected Anthropic pagination fields: %s", resp.Body)
	}
	data, _ := payload["data"].([]any)
	first, _ := data[0].(map[string]any)
	for _, field := range []string{"type", "display_name", "created_at", "max_input_tokens", "max_tokens"} {
		if _, exists := first[field]; !exists {
			t.Fatalf("expected field %q in model discovery response: %s", field, resp.Body)
		}
	}
}

func newAnthropicGateway(t *testing.T, upstreamURL string, providerType string) (http.Handler, *GormStore, string) {
	return newAnthropicGatewayWithOptions(t, upstreamURL, providerType, nil)
}

func newAnthropicGatewayWithOptions(t *testing.T, upstreamURL string, providerType string, options map[string]string) (http.Handler, *GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Claude Code Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "claude-code-key",
		Allowed: []string{"claude-tokenhub-test"},
		Status:  StatusActive,
	}, "thk_claude_code_test")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		Name:                "claude-tokenhub-test",
		Family:              "claude",
		Modality:            "chat",
		ContextWindow:       200000,
		Capabilities:        []string{"chat", "vision", "tools", "reasoning"},
		SupportedParameters: []string{"tools", "image_input", "reasoning"},
		Status:              StatusActive,
	})
	provider := store.AddProvider(Provider{
		ID:      "prv_claude_code",
		Name:    "Claude Code Test Provider",
		Type:    providerType,
		BaseURL: upstreamURL,
		APIKey:  "upstream-secret",
		Status:  StatusActive,
		Healthy: true,
		Options: options,
	})
	store.AddRoute(ModelRoute{
		ID:            "route_claude_code",
		ModelName:     "claude-tokenhub-test",
		ProviderID:    provider.ID,
		ProviderModel: "upstream-model",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityOnly,
	})
	return New(store).Handler(), store, secret
}

func doAnthropicRequest(
	t *testing.T,
	handler http.Handler,
	path string,
	payload any,
	authorization string,
	apiKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	return doAnthropicRequestWithBeta(t, handler, path, payload, authorization, apiKey, findTestBeta(payload))
}

func doAnthropicRequestWithBeta(
	t *testing.T,
	handler http.Handler,
	path string,
	payload any,
	authorization string,
	apiKey string,
	beta string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if authorization != "" {
		req.Header.Set("authorization", authorization)
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	if beta != "" {
		req.Header.Set("anthropic-beta", beta)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func findTestBeta(payload any) string {
	body, _ := json.Marshal(payload)
	if strings.Contains(string(body), `"thinking"`) {
		return "interleaved-thinking-2025-05-14"
	}
	return ""
}

// TestDoNativeAnthropicRequestRejectsMissingAdapter guards the native
// Anthropic path against a registry that does not know the Anthropic adapter:
// it must fail with a controlled 503 instead of dereferencing an
// unconfigured zero-value client.
func TestDoNativeAnthropicRequestRejectsMissingAdapter(t *testing.T) {
	store := NewMemoryStore()
	srv := NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
	// Drop every registered adapter so the Anthropic adapter is unavailable.
	srv.adapterRegistry = NewAdapterRegistry()
	_, err := srv.doNativeAnthropicRequest(context.Background(), Provider{
		ID:      "prv_no_adapter",
		Type:    ProviderAnthropic,
		BaseURL: "http://127.0.0.1:1",
		APIKey:  "secret",
	}, "/v1/messages", map[string]any{"model": "claude-x", "max_tokens": 1}, http.Header{}, false)
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusServiceUnavailable || httpErr.Code != "provider_adapter_missing" {
		t.Fatalf("expected 503 provider_adapter_missing, got %v", err)
	}
}
