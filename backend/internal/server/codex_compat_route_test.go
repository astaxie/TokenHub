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

const codexCompatibilityRouteModel = "gpt-5.5"

func newCodexCompatibilityRouteTestServer(t *testing.T, transport http.RoundTripper) (*Server, *GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Codex Bridge Route Test", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "Codex Bridge Key", Allowed: []string{codexCompatibilityRouteModel}, Status: StatusActive,
	}, "thk_codex_bridge_route")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_codex_bridge_route", Name: "Codex Bridge", Type: ProviderOpenAICodex,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_codex_bridge_route", ProviderID: provider.ID, Name: "Codex Bridge Account",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Options: codexCapabilityOptionsForTest(codexCompatibilityRouteModel),
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access_codex_bridge", AccountID: "account_codex_bridge",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: codexCompatibilityRouteModel, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID: "route_codex_bridge", ModelName: codexCompatibilityRouteModel, ProviderID: provider.ID,
		ProviderResourceID: resource.ID, ProviderModel: codexCompatibilityRouteModel,
		Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
	})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "codex-bridge-route-secret"})
	server.codexSubscription.Client = &http.Client{Transport: transport}
	server.codexSubscription.MaxRequestRetries = 1
	return server, store, secret
}

func codexCompatibilityRouteResponse(t *testing.T, request *http.Request) (*http.Response, error) {
	t.Helper()
	if request.Header.Get("authorization") != "Bearer access_codex_bridge" || request.Header.Get("chatgpt-account-id") != "account_codex_bridge" {
		t.Fatalf("missing Codex route credentials: %#v", request.Header)
	}
	if sessionID := request.Header.Get("session-id"); sessionID == "" || len(sessionID) > codexIdentifierLimit {
		t.Fatalf("invalid forwarded session id %q", sessionID)
	}
	var payload map[string]any
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	toolName := "inspect_workspace"
	if tools, _ := anySlice(payload["tools"]); len(tools) > 0 {
		if tool, ok := tools[0].(map[string]any); ok {
			toolName = stringFromMap(tool, "name")
		}
	}
	body := codexCompatibilityRouteSSE(t, toolName)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func codexCompatibilityRouteSSE(t *testing.T, toolName string) string {
	t.Helper()
	return codexCompatibilityTestSSE(t,
		map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_codex_bridge", "status": "in_progress"}},
		map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{
			"type": "reasoning", "id": "rs_codex_bridge", "summary": []any{}, "encrypted_content": "codex-bridge-signature",
		}},
		map[string]any{"type": "response.reasoning_summary_text.delta", "output_index": 0, "item_id": "rs_codex_bridge", "delta": "Bridge reasoning"},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{
			"type": "reasoning", "id": "rs_codex_bridge", "summary": []any{map[string]any{"type": "summary_text", "text": "Bridge reasoning"}}, "encrypted_content": "codex-bridge-signature",
		}},
		map[string]any{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{
			"type": "message", "id": "msg_codex_bridge", "role": "assistant", "content": []any{},
		}},
		map[string]any{"type": "response.output_text.delta", "output_index": 1, "item_id": "msg_codex_bridge", "delta": "bridge text"},
		map[string]any{"type": "response.output_text.done", "output_index": 1, "item_id": "msg_codex_bridge", "text": "bridge text"},
		map[string]any{"type": "response.output_item.done", "output_index": 1, "item": map[string]any{
			"type": "message", "id": "msg_codex_bridge", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "bridge text"}},
		}},
		map[string]any{"type": "response.output_item.added", "output_index": 2, "item": map[string]any{
			"type": "function_call", "id": "fc_codex_bridge", "call_id": "call_codex_bridge", "name": toolName, "arguments": "",
		}},
		map[string]any{"type": "response.function_call_arguments.delta", "output_index": 2, "item_id": "fc_codex_bridge", "delta": `{"path":"README.md"}`},
		map[string]any{"type": "response.function_call_arguments.done", "output_index": 2, "item_id": "fc_codex_bridge", "arguments": `{"path":"README.md"}`},
		map[string]any{"type": "response.output_item.done", "output_index": 2, "item": map[string]any{
			"type": "function_call", "id": "fc_codex_bridge", "call_id": "call_codex_bridge", "name": toolName, "arguments": `{"path":"README.md"}`,
		}},
		map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp_codex_bridge", "status": "completed", "output": []any{},
			"usage": map[string]any{"input_tokens": 12, "output_tokens": 8, "total_tokens": 20},
		}},
	)
}

func codexCompatibilityTestSSE(t *testing.T, payloads ...map[string]any) string {
	t.Helper()
	var result strings.Builder
	for _, payload := range payloads {
		eventType, _ := payload["type"].(string)
		if eventType == "" {
			t.Fatalf("SSE payload has no type: %#v", payload)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode SSE payload: %v", err)
		}
		result.WriteString("event: ")
		result.WriteString(eventType)
		result.WriteByte('\n')
		result.WriteString("data: ")
		result.Write(encoded)
		result.WriteString("\n\n")
	}
	return result.String()
}

func doCodexCompatibilityRouteJSON(t *testing.T, handler http.Handler, path string, payload map[string]any, secret string, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("authorization", "Bearer "+secret)
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-tokenhub-session-id", sessionID)
	request.Header.Set("user-agent", "codex-compat-route-test/1.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func codexCompatibilityChatPayload(stream bool) map[string]any {
	return map[string]any{
		"model": codexCompatibilityRouteModel, "stream": stream,
		"messages": []any{map[string]any{"role": "user", "content": "Inspect README.md"}},
		"tools": []any{map[string]any{
			"type": "function", "function": map[string]any{
				"name": "inspect_workspace_file_with_a_long_original_name", "description": "Inspect a file",
				"parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
			},
		}},
	}
}

func codexCompatibilityAnthropicPayload(stream bool) map[string]any {
	return map[string]any{
		"model": codexCompatibilityRouteModel, "stream": stream, "max_tokens": 1024,
		"messages": []any{map[string]any{"role": "user", "content": "Inspect README.md"}},
		"tools": []any{map[string]any{
			"name": "inspect_workspace_file_with_a_long_original_name", "description": "Inspect a file",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
	}
}

func TestCodexCompatibilityRoutesBridgeChatAndAnthropicNonStreaming(t *testing.T) {
	server, _, secret := newCodexCompatibilityRouteTestServer(t, roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return codexCompatibilityRouteResponse(t, request)
	}))
	cases := []struct {
		name    string
		path    string
		payload map[string]any
		markers []string
	}{
		{name: "chat", path: "/v1/chat/completions", payload: codexCompatibilityChatPayload(false), markers: []string{"bridge text", `"tool_calls"`, `"reasoning_signature":"codex:`, `"reasoning_details":[{"data":"codex:`}},
		{name: "anthropic", path: "/v1/messages", payload: codexCompatibilityAnthropicPayload(false), markers: []string{"bridge text", `"type":"tool_use"`, `"type":"thinking"`, `"signature":"codex:`}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := doCodexCompatibilityRouteJSON(t, server.Handler(), test.path, test.payload, secret, "non-stream-session-"+test.name)
			if response.Code != http.StatusOK {
				t.Fatalf("%s failed: %d %s", test.name, response.Code, response.Body.String())
			}
			for _, marker := range append(test.markers, "inspect_workspace_file_with_a_long_original_name") {
				if !strings.Contains(response.Body.String(), marker) {
					t.Fatalf("%s response missing %q: %s", test.name, marker, response.Body.String())
				}
			}
		})
	}
}

func TestCodexCompatibilityRoutesBridgeChatAndAnthropicStreaming(t *testing.T) {
	server, _, secret := newCodexCompatibilityRouteTestServer(t, roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return codexCompatibilityRouteResponse(t, request)
	}))
	cases := []struct {
		name    string
		path    string
		payload map[string]any
		markers []string
	}{
		{name: "chat", path: "/v1/chat/completions", payload: codexCompatibilityChatPayload(true), markers: []string{"bridge text", `"tool_calls"`, `"reasoning_details":[{"data":"codex:`, "[DONE]"}},
		{name: "anthropic", path: "/v1/messages", payload: codexCompatibilityAnthropicPayload(true), markers: []string{"bridge text", "content_block_delta", "input_json_delta", "message_stop"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := doCodexCompatibilityRouteJSON(t, server.Handler(), test.path, test.payload, secret, "stream-session-"+test.name)
			if response.Code != http.StatusOK {
				t.Fatalf("%s stream failed: %d %s", test.name, response.Code, response.Body.String())
			}
			for _, marker := range append(test.markers, "inspect_workspace_file_with_a_long_original_name") {
				if !strings.Contains(response.Body.String(), marker) {
					t.Fatalf("%s stream missing %q: %s", test.name, marker, response.Body.String())
				}
			}
		})
	}
}

func TestCodexCompatibilityChatFiltersIncompatibleCodexRoute(t *testing.T) {
	codexCalls := 0
	server, store, secret := newCodexCompatibilityRouteTestServer(t, roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		codexCalls++
		return codexCompatibilityRouteResponse(t, request)
	}))
	fallback := store.AddProvider(Provider{ID: "prv_codex_bridge_fallback", Name: "Chat Fallback", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ID: "route_codex_bridge_fallback", ModelName: codexCompatibilityRouteModel, ProviderID: fallback.ID,
		ProviderModel: codexCompatibilityRouteModel, Priority: 2, Weight: 100, Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
	})
	payload := codexCompatibilityChatPayload(false)
	payload["response_format"] = map[string]any{"type": "unsupported_xml"}
	response := doCodexCompatibilityRouteJSON(t, server.Handler(), "/v1/chat/completions", payload, secret, "incompatible-route-session")
	if response.Code != http.StatusOK || codexCalls != 0 || response.Header().Get("x-tokenhub-provider") != fallback.ID {
		t.Fatalf("incompatible route filtering: status=%d codexCalls=%d provider=%q body=%s", response.Code, codexCalls, response.Header().Get("x-tokenhub-provider"), response.Body.String())
	}
}

func TestCodexCompatibilityChatFailsOverAndKeepsSessionAffinity(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Codex Failover Affinity", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Affinity Key", Allowed: []string{codexCompatibilityRouteModel}, Status: StatusActive}, "thk_codex_affinity_route")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_codex_affinity_route", Name: "Codex Affinity", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	for _, resource := range []ProviderResource{
		{ID: "rsrc_codex_bad", ProviderID: provider.ID, Name: "Bad Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true, Priority: 1, Options: codexCapabilityOptionsForTest(codexCompatibilityRouteModel), Credentials: &ProviderResourceCredentials{AccessToken: "access_bad", AccountID: "account_bad"}},
		{ID: "rsrc_codex_good", ProviderID: provider.ID, Name: "Good Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true, Priority: 2, Options: codexCapabilityOptionsForTest(codexCompatibilityRouteModel), Credentials: &ProviderResourceCredentials{AccessToken: "access_good", AccountID: "account_good"}},
	} {
		if _, err := store.AddProviderResource(resource); err != nil {
			t.Fatal(err)
		}
	}
	store.AddModel(Model{Name: codexCompatibilityRouteModel, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_codex_affinity", ModelName: codexCompatibilityRouteModel, ProviderID: provider.ID, ProviderModel: codexCompatibilityRouteModel, Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	badCalls := 0
	goodCalls := 0
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "codex-affinity-route-secret"})
	server.codexSubscription.MaxRequestRetries = 1
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("authorization") == "Bearer access_bad" {
			badCalls++
			return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"server_error"}}`)), Request: request}, nil
		}
		goodCalls++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(codexCompatibilityRouteSSE(t, "inspect_workspace_file_with_a_long_original_name"))), Request: request}, nil
	})}

	first := doCodexCompatibilityRouteJSON(t, server.Handler(), "/v1/chat/completions", codexCompatibilityChatPayload(false), secret, "stable-affinity-session")
	badAfterFirst := badCalls
	if first.Code != http.StatusOK || first.Header().Get("x-tokenhub-provider-resource-id") != "rsrc_codex_good" || badAfterFirst == 0 || goodCalls != 1 {
		t.Fatalf("first failover: status=%d resource=%q bad=%d good=%d body=%s", first.Code, first.Header().Get("x-tokenhub-provider-resource-id"), badCalls, goodCalls, first.Body.String())
	}
	second := doCodexCompatibilityRouteJSON(t, server.Handler(), "/v1/chat/completions", codexCompatibilityChatPayload(false), secret, "stable-affinity-session")
	if second.Code != http.StatusOK || second.Header().Get("x-tokenhub-provider-resource-id") != "rsrc_codex_good" || badCalls != badAfterFirst || goodCalls != 2 {
		t.Fatalf("affinity reuse: status=%d resource=%q bad=%d good=%d body=%s", second.Code, second.Header().Get("x-tokenhub-provider-resource-id"), badCalls, goodCalls, second.Body.String())
	}
}
