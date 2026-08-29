package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
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

func TestGeminiNativeModelsUsePluginDeclaredCodexResponsesProtocol(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Gemini Plugin Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Gemini Plugin Key", Allowed: []string{"gemini-plugin-model"}, Status: StatusActive}, "thk_gemini_plugin")
	if err != nil {
		t.Fatal(err)
	}
	const providerType = "plugin_codex_responses"
	provider := store.AddProvider(Provider{ID: "prv_gemini_plugin", Name: "Gemini Plugin", Type: providerType, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gemini-plugin-model", Modality: "chat", ContextWindow: 128000, Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_gemini_plugin", ModelName: "gemini-plugin-model", ProviderID: provider.ID, ProviderModel: "gemini-plugin-model", Status: StatusActive})
	server := New(store)
	descriptor := providerPluginDescriptorWithRouteProtocol(
		"tokenhub.provider.plugin-codex-responses",
		"Plugin Codex Responses",
		providerType,
		AdapterCapabilityResponses,
		providerRouteProtocolCodexResponses,
	)
	if err := server.adapterRegistry.RegisterPlugin(descriptor, AdapterRegistration{
		Type:         providerType,
		Adapter:      routeProtocolTestAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityResponses},
	}); err != nil {
		t.Fatalf("register plugin provider: %v", err)
	}

	response := doGeminiJSON(t, server.Handler(), http.MethodGet, "/v1beta/models", nil, secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"name":"models/gemini-plugin-model"`) {
		t.Fatalf("Gemini model list did not include plugin-declared Codex Responses route: %d %s", response.Code, response.Body)
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
	var modelList struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal([]byte(models.Body), &modelList); err != nil {
		t.Fatalf("decode Gemini model discovery: %v", err)
	}
	if len(modelList.Models) != 1 {
		t.Fatalf("Gemini model discovery returned %d models, want 1: %s", len(modelList.Models), models.Body)
	}
	methods := map[string]bool{}
	for _, method := range geminiTestSlice(t, modelList.Models[0]["supportedGenerationMethods"], "supportedGenerationMethods") {
		name, ok := method.(string)
		if !ok {
			t.Fatalf("supportedGenerationMethods contains non-string value: %#v", method)
		}
		methods[name] = true
	}
	for _, method := range []string{"generateContent", "streamGenerateContent", "countTokens"} {
		if !methods[method] {
			t.Fatalf("Gemini model discovery missing %q in supportedGenerationMethods: %s", method, models.Body)
		}
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

func TestGeminiNativePrivacyPreHookCanRewriteContentBeforeUpstream(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(request map[string]any) string {
		input := geminiTestSlice(t, request["input"], "Codex input")
		item := geminiTestMap(t, input[0], "input item")
		content := geminiTestSlice(t, item["content"], "input content")
		text := geminiTestMap(t, content[0], "input text")["text"]
		if text != "[masked]" {
			t.Fatalf("Codex input text = %#v, want masked", text)
		}
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_privacy", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "privacy ok"}},
				}},
				"usage": map[string]any{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3},
			},
		})
	})
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-gemini-privacy",
		HookID:        "mask-gemini-content",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var payload map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &payload); err != nil {
			t.Fatalf("decode hook request: %v", err)
		}
		payload["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "[masked]"}}}}
		return rawRequestBodyPatch(t, payload), nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "secret"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("generateContent failed: %d %s", response.Code, response.Body)
	}
}

func TestGeminiNativeCacheLookupHookShortCircuitsGenerateContent(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		t.Fatal("upstream should not be called on cache hit")
		return ""
	})
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-gemini-cache",
		HookID:        "lookup",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyReturnFallback,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Data[pluginmeta.DataRequestBody]) == 0 {
			t.Fatal("cache lookup did not receive Gemini request body")
		}
		response, err := json.Marshal(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": "cached gemini"}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"totalTokenCount": 9},
		})
		if err != nil {
			t.Fatal(err)
		}
		usage, err := json.Marshal(Usage{TotalTokens: 9})
		if err != nil {
			t.Fatal(err)
		}
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionShortCircuit,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataProviderResponse: {Value: response},
				pluginmeta.DataUsage:            {Value: usage},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "cache me"}}}},
	}, secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body, "cached gemini") {
		t.Fatalf("cache hit failed: %d %s", response.Code, response.Body)
	}
	if got := response.Header.Get("x-tokenhub-cache"); got != "hit" {
		t.Fatalf("cache header = %q, want hit", got)
	}
}

func TestGeminiNativeStreamSkipsCacheLookupAndWriteHooks(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		return geminiCodexTestSSE(
			map[string]any{"type": "response.output_text.delta", "delta": "stream without cache"},
			map[string]any{"type": "response.completed", "response": map[string]any{
				"id": "resp_gemini_stream_cache_skip", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "stream without cache"}},
				}},
				"usage": map[string]any{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5},
			}},
		)
	})
	registerUnexpectedCacheHooks(t, server)

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:streamGenerateContent?alt=sse", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "stream me"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("streamGenerateContent failed: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "stream without cache") {
		t.Fatalf("Gemini stream did not use provider path: %s", response.Body)
	}
}

func TestGeminiNativeCacheLookupFailOpenContinuesToProvider(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_lookup_fail_open", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "gemini lookup fallback"}},
				}},
				"usage": map[string]any{"input_tokens": 4, "output_tokens": 5, "total_tokens": 9},
			},
		})
	})
	calls := registerFailingCacheHook(t, server, pluginmeta.StageCacheLookup)

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "provider after lookup failure"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("generateContent failed after cache lookup fail-open: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "gemini lookup fallback") {
		t.Fatalf("Gemini response did not come from provider path: %s", response.Body)
	}
	if *calls != 1 {
		t.Fatalf("cache lookup hook calls = %d, want 1", *calls)
	}
}

func TestGeminiNativeRequestTransformHookCanRewriteResponsesPayloadBeforeProvider(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(request map[string]any) string {
		input := geminiTestSlice(t, request["input"], "Codex input")
		item := geminiTestMap(t, input[0], "input item")
		content := geminiTestSlice(t, item["content"], "input content")
		text := geminiTestMap(t, content[0], "input text")["text"]
		if text != "provider-side transform" {
			t.Fatalf("Codex input text = %#v, want provider transform", text)
		}
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_request_transform", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "request transform ok"}},
				}},
				"usage": map[string]any{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
			},
		})
	})
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-gemini-transform",
		HookID:        "rewrite-responses-payload",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var request ResponsesRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &request); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		request.Input = []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": "provider-side transform",
			}},
		}}
		return rawProviderRequestPatch(t, request), nil
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "before transform"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("generateContent failed: %d %s", response.Code, response.Body)
	}
}

func TestGeminiNativeCacheWriteHookReceivesGeminiResponse(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_cache_write", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "write cache"}},
				}},
				"usage": map[string]any{"input_tokens": 4, "output_tokens": 5, "total_tokens": 9},
			},
		})
	})
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-gemini-cache",
		HookID:        "write",
		Stage:         pluginmeta.StageCacheWrite,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache write hook: %v", err)
	}
	var sawGeminiResponse bool
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var response map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderResponse], &response); err != nil {
			t.Fatalf("decode cache write response: %v", err)
		}
		sawGeminiResponse = response["candidates"] != nil && response["usageMetadata"] != nil && len(input.Data[pluginmeta.DataRequestBody]) > 0 && len(input.Data[pluginmeta.DataUsage]) > 0
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register cache write handler: %v", err)
	}

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "store this"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("generateContent failed: %d %s", response.Code, response.Body)
	}
	if !sawGeminiResponse {
		t.Fatal("cache write hook did not receive Gemini request, response, and usage")
	}
}

func TestGeminiNativeCacheWriteFailOpenPreservesProviderResponse(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_write_fail_open", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "gemini write fallback"}},
				}},
				"usage": map[string]any{"input_tokens": 4, "output_tokens": 5, "total_tokens": 9},
			},
		})
	})
	calls := registerFailingCacheHook(t, server, pluginmeta.StageCacheWrite)

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "provider after write failure"}}}},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("generateContent failed after cache write fail-open: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "gemini write fallback") {
		t.Fatalf("Gemini response did not survive cache write failure: %s", response.Body)
	}
	if *calls != 1 {
		t.Fatalf("cache write hook calls = %d, want 1", *calls)
	}
}

func TestGeminiNativeStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		return geminiCodexTestSSE(
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_gemini_stream_transform", "status": "in_progress"}},
			map[string]any{"type": "response.output_text.delta", "delta": "Native Gemini stream"},
			map[string]any{"type": "response.completed", "response": map[string]any{
				"id": "resp_gemini_stream_transform", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "Native Gemini stream"}},
				}},
				"usage": map[string]any{"input_tokens": 4, "output_tokens": 5, "total_tokens": 9},
			}},
		)
	})
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-gemini-stream",
		HookID:        "rewrite",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register stream transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var event gatewayStreamEventView
		if err := json.Unmarshal(input.Data[pluginmeta.DataStreamEvents], &event); err != nil {
			t.Fatalf("decode stream event: %v", err)
		}
		if !strings.Contains(event.Data, "Native Gemini stream") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "Native Gemini stream", "Plugin Gemini stream", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/gpt-5.5:streamGenerateContent?alt=sse", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "stream"}}}},
	}, secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body, "Plugin Gemini stream") || strings.Contains(response.Body, "Native Gemini stream") {
		t.Fatalf("stream transform failed: %d %s", response.Code, response.Body)
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
	return responseBody{Code: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
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
