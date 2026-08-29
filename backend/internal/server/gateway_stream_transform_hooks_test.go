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

func TestChatStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "rewrite-chat-chunks",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
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
		if !strings.Contains(event.Data, "Echo:") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "Echo:", "Plugin:", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":  "gpt-4.1-mini",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Plugin: hello") {
		t.Fatalf("stream body was not transformed: %s", resp.Body)
	}
	if strings.Contains(resp.Body, "Echo: hello") {
		t.Fatalf("stream body still contains the untransformed chunk: %s", resp.Body)
	}
}

func TestResponsesStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Stream Transform Responses App", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "stream-transform-responses-key",
		Allowed: []string{"gpt-stream-transform-responses"},
		Status:  StatusActive,
	}, "thk_stream_transform_responses")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_stream_transform_responses", Name: "Stream Transform Responses",
		Type: "stream_transform_responses", Status: StatusActive, Healthy: true,
	})
	store.AddModel(Model{Name: "gpt-stream-transform-responses", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID: "route_stream_transform_responses", ModelName: "gpt-stream-transform-responses",
		ProviderID: provider.ID, ProviderModel: "upstream-responses", Status: StatusActive, Priority: 1, Weight: 100,
	})
	server := New(store)
	server.adapterRegistry.Register("stream_transform_responses", responsesStreamTransformAdapter{}, AdapterCapabilityResponses, AdapterCapabilityResponseStream)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "rewrite-responses-delta",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
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
		if !strings.Contains(event.Data, "Echo responses") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "Echo responses", "Plugin responses", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model":  "gpt-stream-transform-responses",
		"stream": true,
		"input":  "hello",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Plugin responses") {
		t.Fatalf("responses stream body was not transformed: %s", resp.Body)
	}
}

func TestAnthropicNativeStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_stream_transform\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"upstream-model\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Native stream\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	server, _, secret := newAnthropicGatewayServer(t, upstream.URL, ProviderAnthropic)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "rewrite-native-anthropic",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
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
		if !strings.Contains(event.Data, "Native stream") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "Native stream", "Plugin stream", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	resp := doAnthropicRequest(t, server.Handler(), "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 32,
		"stream":     true,
		"messages": []any{
			map[string]any{"role": "user", "content": "stream"},
		},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "Plugin stream") || strings.Contains(resp.Body.String(), "Native stream") {
		t.Fatalf("native Anthropic stream body was not transformed: %s", resp.Body)
	}
}

func TestAnthropicOpenAIStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_anthropic_stream_transform\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_anthropic_stream_transform\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OpenAI bridge stream\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_anthropic_stream_transform\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	server, _, secret := newAnthropicGatewayServer(t, upstream.URL, ProviderOpenAICompatible)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "rewrite-openai-anthropic",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
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
		if !strings.Contains(event.Data, "OpenAI bridge stream") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "OpenAI bridge stream", "Plugin bridge stream", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	resp := doAnthropicRequest(t, server.Handler(), "/v1/messages", map[string]any{
		"model":      "claude-tokenhub-test",
		"max_tokens": 32,
		"stream":     true,
		"messages": []any{
			map[string]any{"role": "user", "content": "stream"},
		},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "Plugin bridge stream") || strings.Contains(resp.Body.String(), "OpenAI bridge stream") {
		t.Fatalf("OpenAI-backed Anthropic stream body was not transformed: %s", resp.Body)
	}
}

func TestPlaygroundChatStreamTransformHookCanRewriteSSEEventData(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "rewrite-playground-chunks",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
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
		if !strings.Contains(event.Data, "Echo:") {
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		}
		return streamEventPatchResult(t, map[string]any{
			"data": strings.Replace(event.Data, "Echo:", "Playground plugin:", 1),
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id": defaultProjectID,
		"model":      "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Playground plugin: hello") {
		t.Fatalf("playground stream body was not transformed: %s", resp.Body)
	}
}

func TestStreamTransformHookFailurePolicyHandlesInvalidPatches(t *testing.T) {
	tests := []struct {
		name          string
		failurePolicy pluginmeta.GatewayHookFailurePolicy
		wantErrCode   string
	}{
		{name: "fail open preserves original event", failurePolicy: pluginmeta.FailurePolicyFailOpen},
		{name: "fail closed returns stream patch error", failurePolicy: pluginmeta.FailurePolicyFailClosed, wantErrCode: "gateway_hook_stream_event_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := New(NewMemoryStore())
			hook := streamTransformHook(tt.failurePolicy)
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatalf("register stream transform hook: %v", err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return pluginmeta.GatewayHookResult{
					Decision: pluginmeta.HookDecisionContinue,
					Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
						pluginmeta.DataStreamEvents: {Value: json.RawMessage(`{"event":"bad
name"}`)},
					},
				}, nil
			})); err != nil {
				t.Fatalf("register stream transform handler: %v", err)
			}

			original := serverSentEvent{Event: "message", Data: `{"delta":"original"}`}
			transformed, emit, err := server.runGatewayStreamTransformHooks(context.Background(), gatewayPluginTestCall(), streamTransformRoute(), providerRouteProtocolChatCompletions, original)
			if tt.wantErrCode != "" {
				httpErr := AsHTTPError(err)
				if httpErr.Code != tt.wantErrCode || httpErr.Status != http.StatusBadGateway {
					t.Fatalf("stream transform error = %d/%s, want 502/%s", httpErr.Status, httpErr.Code, tt.wantErrCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("fail-open stream transform returned error: %v", err)
			}
			if !emit || transformed.Event != original.Event || transformed.Data != original.Data {
				t.Fatalf("fail-open transformed event = %+v emit=%v, want original %+v", transformed, emit, original)
			}
		})
	}
}

func TestStreamTransformHookCanDropEventAndRewriteMultilineData(t *testing.T) {
	server := New(NewMemoryStore())
	hook := streamTransformHook(pluginmeta.FailurePolicyFailClosed)
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register stream transform hook: %v", err)
	}
	callCount := 0
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		callCount++
		var event gatewayStreamEventView
		if err := json.Unmarshal(input.Data[pluginmeta.DataStreamEvents], &event); err != nil {
			t.Fatalf("decode stream event: %v", err)
		}
		if strings.Contains(event.Data, "drop-me") {
			return streamEventPatchResult(t, map[string]any{"drop": true}), nil
		}
		return streamEventPatchResult(t, map[string]any{
			"event": "message.delta",
			"data":  "first line\nsecond line",
		}), nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	kept, emit, err := server.runGatewayStreamTransformHooks(context.Background(), gatewayPluginTestCall(), streamTransformRoute(), providerRouteProtocolChatCompletions, serverSentEvent{Event: "message", Data: "keep-me"})
	if err != nil {
		t.Fatalf("run stream transform keep: %v", err)
	}
	if !emit || kept.Event != "message.delta" || kept.Data != "first line\nsecond line" {
		t.Fatalf("kept event = %+v emit=%v", kept, emit)
	}
	decoded, err := newSSEDecoder(bytes.NewReader(renderSSEEvent(kept))).Next()
	if err != nil {
		t.Fatalf("decode rendered multiline event: %v", err)
	}
	if decoded.Event != kept.Event || decoded.Data != kept.Data {
		t.Fatalf("decoded rendered event = %+v, want %+v", decoded, kept)
	}

	dropped, emit, err := server.runGatewayStreamTransformHooks(context.Background(), gatewayPluginTestCall(), streamTransformRoute(), providerRouteProtocolChatCompletions, serverSentEvent{Event: "message", Data: "drop-me"})
	if err != nil {
		t.Fatalf("run stream transform drop: %v", err)
	}
	if emit || dropped.Data != "drop-me" {
		t.Fatalf("dropped event = %+v emit=%v, want original data with emit=false", dropped, emit)
	}
	if callCount != 2 {
		t.Fatalf("stream transform hook calls = %d, want 2", callCount)
	}
}

func TestStreamTransformWriterPassesThroughCommentEvents(t *testing.T) {
	server := New(NewMemoryStore())
	hook := streamTransformHook(pluginmeta.FailurePolicyFailClosed)
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register stream transform hook: %v", err)
	}
	called := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called = true
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	var output bytes.Buffer
	writer := server.newGatewayStreamTransformWriter(context.Background(), gatewayPluginTestCall(), streamTransformRoute(), providerRouteProtocolChatCompletions, &output)
	if _, err := writer.Write([]byte(": keepalive\n")); err != nil {
		t.Fatalf("write keepalive: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stream transform writer: %v", err)
	}
	if called {
		t.Fatal("stream transform hook ran for a comment-only event")
	}
	if output.String() != ": keepalive\n" {
		t.Fatalf("comment output = %q, want raw keepalive", output.String())
	}
}

func TestStreamTransformHookDenyReturnsForbiddenAndRecordsAuditEvent(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	hook := streamTransformHook(pluginmeta.FailurePolicyFailClosed)
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register stream transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionDeny,
			AuditEvents: []json.RawMessage{
				json.RawMessage(`{"outcome":"denied","authorization":"Bearer hidden","nested":{"api_key":"raw-secret"}}`),
			},
		}, nil
	})); err != nil {
		t.Fatalf("register stream transform handler: %v", err)
	}

	_, _, err := server.runGatewayStreamTransformHooks(context.Background(), gatewayPluginTestCall(), streamTransformRoute(), providerRouteProtocolChatCompletions, serverSentEvent{Event: "message", Data: "deny"})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusForbidden || httpErr.Code != "gateway_hook_denied" {
		t.Fatalf("stream deny error = %d/%s, want 403/gateway_hook_denied", httpErr.Status, httpErr.Code)
	}
	event := requireStreamTransformAuditEvent(t, store.ListAuditEvents(), "denied")
	if strings.Contains(event.AfterSnapshot, "Bearer hidden") || strings.Contains(event.AfterSnapshot, "raw-secret") {
		t.Fatalf("stream transform audit snapshot leaked secret material: %s", event.AfterSnapshot)
	}
	if !strings.Contains(event.AfterSnapshot, `"authorization":"[redacted]"`) || !strings.Contains(event.AfterSnapshot, `"api_key":"[redacted]"`) {
		t.Fatalf("stream transform audit snapshot did not redact plugin event: %s", event.AfterSnapshot)
	}
}

type responsesStreamTransformAdapter struct{}

func (a responsesStreamTransformAdapter) OpenResponses(context.Context, Provider, string, ResponsesRequest, http.Header) (*http.Response, error) {
	body := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"Echo responses"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_stream_transform","object":"response","model":"upstream-responses","output_text":"Echo responses","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}}`,
		``,
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func streamEventPatchResult(t *testing.T, value any) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataStreamEvents: {Value: data},
		},
	}
}

func streamTransformHook(policy pluginmeta.GatewayHookFailurePolicy) pluginmeta.GatewayHookDescriptor {
	return pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-stream",
		HookID:        "stream-transform",
		Stage:         pluginmeta.StageStreamTransform,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents},
		FailurePolicy: policy,
	}
}

func streamTransformRoute() RouteSelection {
	return RouteSelection{
		Route:         ModelRoute{ID: "route_stream_transform", Status: StatusActive},
		Provider:      Provider{ID: "prv_stream_transform", Type: ProviderMock, Status: StatusActive, Healthy: true},
		ProviderModel: "upstream-stream-transform",
	}
}

func requireStreamTransformAuditEvent(t *testing.T, events []AuditEvent, status string) AuditEvent {
	t.Helper()
	for _, event := range events {
		if event.Action == "plugin.gateway.stream_transform" && event.Status == status {
			if event.ResourceType != "gateway_request" {
				t.Fatalf("stream transform audit resource type = %q, want gateway_request", event.ResourceType)
			}
			if !strings.Contains(event.AfterSnapshot, `"stage":"stream_transform"`) {
				t.Fatalf("stream transform audit snapshot missing stage: %s", event.AfterSnapshot)
			}
			return event
		}
	}
	t.Fatalf("missing stream transform audit event with status %s: %+v", status, events)
	return AuditEvent{}
}
