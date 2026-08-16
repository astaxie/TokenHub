package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const geminiCodexTestModel = "gpt-5.5"

func TestGeminiNativeGenerateContentUsesCodexSubscription(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(request map[string]any) string {
		if request["model"] != geminiCodexTestModel || request["stream"] != true || request["store"] != false {
			t.Fatalf("unexpected Codex envelope: %#v", request)
		}
		input, _ := anySlice(request["input"])
		if len(input) != 1 || geminiTestMap(t, input[0], "input")["role"] != "user" {
			t.Fatalf("unexpected Codex input: %#v", request["input"])
		}
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_text", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "TOKENHUB_GEMINI_OK"}},
				}},
				"usage": map[string]any{"input_tokens": 17, "output_tokens": 6, "total_tokens": 23},
			},
		})
	})

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Reply with the marker."}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("generateContent failed: %d %s", response.Code, response.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	candidates := geminiTestSlice(t, body["candidates"], "candidates")
	candidate := geminiTestMap(t, candidates[0], "candidate")
	content := geminiTestMap(t, candidate["content"], "content")
	parts := geminiTestSlice(t, content["parts"], "parts")
	if geminiTestMap(t, parts[0], "text part")["text"] != "TOKENHUB_GEMINI_OK" || candidate["finishReason"] != "STOP" {
		t.Fatalf("unexpected Gemini response: %#v", body)
	}
	usage := geminiTestMap(t, body["usageMetadata"], "usage")
	if usage["totalTokenCount"] != float64(23) {
		t.Fatalf("unexpected Gemini usage: %#v", usage)
	}
}

func TestGeminiNativeStreamGenerateContentEmitsToolCall(t *testing.T) {
	longName := "read_workspace_file_with_a_long_gemini_cli_specific_function_name"
	server, secret := newGeminiCodexTestServer(t, func(request map[string]any) string {
		tools := geminiTestSlice(t, request["tools"], "Codex tools")
		upstreamTool := geminiTestMap(t, tools[0], "Codex tool")
		shortName, _ := upstreamTool["name"].(string)
		if shortName == "" || len(shortName) > codexIdentifierLimit {
			t.Fatalf("invalid upstream tool name: %q", shortName)
		}
		return geminiCodexTestSSE(
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_gemini_tool", "status": "in_progress"}},
			map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{
				"type": "reasoning", "id": "rs_gemini", "summary": []any{}, "encrypted_content": "codex-gemini-state",
			}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{
				"type": "reasoning", "id": "rs_gemini", "summary": []any{}, "encrypted_content": "codex-gemini-state",
			}},
			map[string]any{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{
				"type": "function_call", "id": "fc_gemini", "call_id": "call_gemini", "name": shortName, "arguments": "",
			}},
			map[string]any{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc_gemini", "delta": `{"path":"README.md"}`},
			map[string]any{"type": "response.output_item.done", "output_index": 1, "item": map[string]any{
				"type": "function_call", "id": "fc_gemini", "call_id": "call_gemini", "name": shortName, "arguments": `{"path":"README.md"}`,
			}},
			map[string]any{"type": "response.completed", "response": map[string]any{
				"id": "resp_gemini_tool", "status": "completed", "output": []any{},
				"usage": map[string]any{"input_tokens": 30, "output_tokens": 10, "total_tokens": 40},
			}},
		)
	})
	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:streamGenerateContent?alt=sse", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Read README.md"}}}},
		"tools": []any{map[string]any{"functionDeclarations": []any{map[string]any{
			"name": longName, "description": "Read a file", "parameters": map[string]any{
				"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"},
			},
		}}}},
	}, secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"functionCall"`) {
		t.Fatalf("streamGenerateContent failed: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"name":"`+longName+`"`) || !strings.Contains(response.Body, `"path":"README.md"`) {
		t.Fatalf("Gemini stream did not restore tool metadata: %s", response.Body)
	}
	if !strings.Contains(response.Body, `"thoughtSignature":"codex:`) || !strings.Contains(response.Body, `"totalTokenCount":40`) {
		t.Fatalf("Gemini stream did not preserve signature or usage: %s", response.Body)
	}
}

func TestGeminiNativeReplaysFunctionResultAndCodexSignature(t *testing.T) {
	payload := map[string]any{
		"contents": []any{
			map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Inspect."}}},
			map[string]any{"role": "model", "parts": []any{map[string]any{
				"functionCall":     map[string]any{"id": "call_one", "name": "read_file", "args": map[string]any{"path": "go.mod"}},
				"thoughtSignature": encodeProviderSignature(codexSignatureProvider, "opaque-codex-state"),
			}}},
			map[string]any{"role": "user", "parts": []any{map[string]any{
				"functionResponse": map[string]any{"id": "call_one", "name": "read_file", "response": map[string]any{"output": "module tokenhub"}},
			}}},
		},
		"tools": []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": "read_file", "parameters": map[string]any{"type": "object"}}}}},
	}
	request, _, err := geminiToResponsesRequest(geminiCodexTestModel, payload, false)
	if err != nil {
		t.Fatal(err)
	}
	wire := geminiTestResponsesRequestMap(t, request)
	input := geminiTestSlice(t, wire["input"], "input")
	want := []string{"message", "reasoning", "function_call", "function_call_output"}
	if len(input) != len(want) {
		t.Fatalf("input = %#v", input)
	}
	for index, kind := range want {
		if geminiTestMap(t, input[index], "input item")["type"] != kind {
			t.Fatalf("input[%d] = %#v", index, input[index])
		}
	}
	if geminiTestMap(t, input[1], "reasoning")["encrypted_content"] != "opaque-codex-state" {
		t.Fatalf("Codex reasoning signature was not replayed: %#v", input[1])
	}
}

func TestGeminiNativeNormalizesCLIUppercaseToolSchema(t *testing.T) {
	_, tools, err := geminiToolsToCodex([]any{map[string]any{"functionDeclarations": []any{map[string]any{
		"name": "list_directory",
		"parameters": map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"path": map[string]any{"anyOf": []any{map[string]any{"type": "STRING"}, map[string]any{"type": "NULL"}}},
			},
		},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	tool := geminiTestMap(t, tools[0], "tool")
	schema := geminiTestMap(t, tool["parameters"], "schema")
	if schema["type"] != "object" {
		t.Fatalf("root type = %#v", schema["type"])
	}
	properties := geminiTestMap(t, schema["properties"], "properties")
	path := geminiTestMap(t, properties["path"], "path")
	anyOf := geminiTestSlice(t, path["anyOf"], "anyOf")
	if geminiTestMap(t, anyOf[0], "string branch")["type"] != "string" || geminiTestMap(t, anyOf[1], "null branch")["type"] != "null" {
		t.Fatalf("nested types were not normalized: %#v", anyOf)
	}
}

func TestGeminiNativeModelsAndCountTokensUseGoogleAPIKeyHeader(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string { return "" })
	models := doGeminiJSON(t, server.Handler(), http.MethodGet, "/v1beta/models", nil, secret)
	if models.Code != http.StatusOK || !strings.Contains(models.Body, `"name":"models/gpt-5.5"`) {
		t.Fatalf("models failed: %d %s", models.Code, models.Body)
	}
	if strings.Contains(models.Body, "chat-only-model") {
		t.Fatalf("Gemini catalog advertised a model without a compatible Codex Responses route: %s", models.Body)
	}
	chatOnly := doGeminiJSON(t, server.Handler(), http.MethodGet, "/v1beta/models/chat-only-model", nil, secret)
	if chatOnly.Code != http.StatusNotFound {
		t.Fatalf("chat-only model lookup status = %d, want 404: %s", chatOnly.Code, chatOnly.Body)
	}
	count := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:countTokens", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "count these tokens"}}}},
	}, secret)
	if count.Code != http.StatusOK || !strings.Contains(count.Body, `"totalTokens":`) {
		t.Fatalf("countTokens failed: %d %s", count.Code, count.Body)
	}
	unauthorized := doGeminiJSON(t, server.Handler(), http.MethodGet, "/v1beta/models", nil, "wrong-key")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("wrong x-goog-api-key status = %d", unauthorized.Code)
	}
}

func TestGeminiNativeCallsEmitGatewayTraces(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_trace", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "trace me"}},
				}},
				"usage": map[string]any{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6},
			},
		})
	})
	emitter := &recordingTraceEmitter{}
	server.traceEmitter = emitter
	payload := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "trace this"}}}},
	}
	for _, path := range []string{
		"/v1beta/models/gpt-5.5:generateContent",
		"/v1beta/models/gpt-5.5:streamGenerateContent?alt=sse",
	} {
		response := doGeminiJSON(t, server.Handler(), http.MethodPost, path, payload, secret)
		if response.Code != http.StatusOK {
			t.Fatalf("%s failed: %d %s", path, response.Code, response.Body)
		}
		completions := emitter.take()
		if len(completions) != 1 || completions[0].StatusCode != http.StatusOK || completions[0].Usage.TotalTokens != 6 {
			t.Fatalf("%s emitted unexpected completions: %+v", path, completions)
		}
	}
}

func newGeminiCodexTestServer(t *testing.T, responder func(map[string]any) string) (*Server, string) {
	return newGeminiCodexTestServerForModel(t, geminiCodexTestModel, responder)
}

func newGeminiCodexTestServerForModel(t *testing.T, modelName string, responder func(map[string]any) string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Gemini CLI Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Gemini CLI Key", Allowed: []string{modelName, "chat-only-model"}, Status: StatusActive}, "thk_gemini_cli_test")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_gemini_codex", Name: "Gemini Codex", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_gemini_codex", ProviderID: provider.ID, Name: "Gemini Codex Account",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Options:     codexCapabilityOptionsForTest(modelName),
		Credentials: &ProviderResourceCredentials{AccessToken: "gemini-codex-access", AccountID: "gemini-codex-account"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: modelName, Modality: "chat", ContextWindow: 128000, Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_gemini_codex", ModelName: modelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: modelName, Status: StatusActive})
	const chatOnlyProviderType = "gemini_chat_only"
	chatProvider := store.AddProvider(Provider{ID: "prv_gemini_chat_only", Name: "Chat Only", Type: chatOnlyProviderType, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "chat-only-model", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_gemini_chat_only", ModelName: "chat-only-model", ProviderID: chatProvider.ID, ProviderModel: "chat-only-model", Status: StatusActive})
	server := New(store)
	server.adapterRegistry.Register(chatOnlyProviderType, MockAdapter{}, AdapterCapabilityChat, AdapterCapabilityChatStream)
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("unexpected Codex path: %s", request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer gemini-codex-access" || request.Header.Get("ChatGPT-Account-ID") != "gemini-codex-account" {
			t.Fatalf("missing Codex subscription credentials: %#v", request.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		body := responder(payload)
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}
	return server, secret
}

func doGeminiJSON(t *testing.T, handler http.Handler, method string, path string, payload any, key string) responseBody {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("x-goog-api-key", key)
	request.Header.Set("user-agent", "GeminiCLI/0.1.9")
	if payload != nil {
		request.Header.Set("content-type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return responseBody{Code: recorder.Code, Body: recorder.Body.String()}
}

func geminiCodexTestSSE(events ...map[string]any) string {
	var result strings.Builder
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		result.WriteString("data: ")
		result.Write(encoded)
		result.WriteString("\n\n")
	}
	return result.String()
}

func geminiTestMap(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %#v", label, value)
	}
	return result
}

func geminiTestSlice(t *testing.T, value any, label string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is not an array: %#v", label, value)
	}
	return result
}

func geminiTestResponsesRequestMap(t *testing.T, request ResponsesRequest) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
