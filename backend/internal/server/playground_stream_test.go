package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type playgroundSSEEvent struct {
	Name string
	Data map[string]any
}

func parsePlaygroundSSE(t *testing.T, body string) []playgroundSSEEvent {
	t.Helper()
	var events []playgroundSSEEvent
	var name string
	var data []string
	flush := func() {
		if name == "" && len(data) == 0 {
			return
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &payload); err != nil {
			t.Fatalf("decode SSE %q: %v\n%s", name, err, body)
		}
		events = append(events, playgroundSSEEvent{Name: name, Data: payload})
		name = ""
		data = nil
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	return events
}

func findPlaygroundSSEEvent(t *testing.T, events []playgroundSSEEvent, name string) playgroundSSEEvent {
	t.Helper()
	for _, event := range events {
		if event.Name == name {
			return event
		}
	}
	t.Fatalf("SSE event %q not found in %+v", name, events)
	return playgroundSSEEvent{}
}

func newPlaygroundTestServer(t *testing.T) (*Server, *GormStore) {
	t.Helper()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	return New(store), store
}

func newPlaygroundTestSink() *playgroundDeltaSink {
	return newPlaygroundDeltaSink(newPlaygroundEventStream(httptest.NewRecorder(), "pg_test"), "pg_test")
}

func TestPlaygroundDeltaSinkRejectsOversizedUnterminatedEvent(t *testing.T) {
	sink := newPlaygroundTestSink()
	line := []byte("data: " + strings.Repeat("x", 1024) + "\n")
	var err error
	for err == nil {
		_, err = sink.Write(line)
	}
	if httpErr := AsHTTPError(err); httpErr.Code != "provider_invalid_response" {
		t.Fatalf("expected provider_invalid_response, got %#v", httpErr)
	}
}

func TestPlaygroundDeltaSinkRejectsMalformedJSONFrame(t *testing.T) {
	sink := newPlaygroundTestSink()
	if _, err := sink.Write([]byte("data: {not-json}\n\n")); AsHTTPError(err).Code != "provider_invalid_response" {
		t.Fatalf("expected malformed frame to fail, got %v", err)
	}
	if _, err := newPlaygroundTestSink().Write([]byte("data: {\"type\":\"future.event\"}\n\n")); err != nil {
		t.Fatalf("unknown valid JSON events should remain forward-compatible: %v", err)
	}
}

func TestAdminPlaygroundStreamEmitsDeltasAndDiagnostics(t *testing.T) {
	server, _ := newPlaygroundTestServer(t)
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id": "prj_demo",
		"model":      "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "system", "content": "Be concise."},
			{"role": "user", "content": "stream diagnostics"},
		},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	events := parsePlaygroundSSE(t, response.Body)
	started := findPlaygroundSSEEvent(t, events, "playground.started")
	if started.Data["request_id"] == "" {
		t.Fatalf("started event should include request id: %#v", started.Data)
	}
	var output strings.Builder
	for _, event := range events {
		if event.Name == "playground.delta" {
			delta, _ := event.Data["delta"].(string)
			output.WriteString(delta)
		}
	}
	if !strings.Contains(output.String(), "Echo: Be concise.\nstream diagnostics") {
		t.Fatalf("unexpected streamed output: %q", output.String())
	}
	completed := findPlaygroundSSEEvent(t, events, "playground.completed")
	if completed.Data["request_id"] != started.Data["request_id"] {
		t.Fatalf("request id changed across stream: %#v", completed.Data)
	}
	if _, present := completed.Data["request"]; present {
		t.Fatalf("completed event must not expose the playground request: %#v", completed.Data["request"])
	}
	timing, _ := completed.Data["timing"].(map[string]any)
	if timing["mode"] != "stream" || timing["ttft_ms"] == nil || timing["first_token_at"] == nil || timing["completed_at"] == nil {
		t.Fatalf("stream timing should include authoritative token timestamps: %#v", timing)
	}
	usage, _ := completed.Data["usage"].(map[string]any)
	if usage["prompt_tokens"] == nil || usage["completion_tokens"] == nil || usage["estimated_cost_usd"] == nil {
		t.Fatalf("completed event should include priced usage: %#v", usage)
	}
	route, _ := completed.Data["route"].(map[string]any)
	if route["provider_name"] != "Mock Provider" || route["provider_model"] != "mock-chat" {
		t.Fatalf("platform admin should receive route diagnostics: %#v", route)
	}
	attempts, _ := completed.Data["attempts"].([]any)
	if len(attempts) != 1 {
		t.Fatalf("expected one route attempt: %#v", completed.Data["attempts"])
	}
	attempt, _ := attempts[0].(map[string]any)
	if attempt["invoked"] != true || attempt["started_at"] == nil || attempt["ended_at"] == nil {
		t.Fatalf("attempt should include invocation timeline: %#v", attempt)
	}
	attemptUsage, _ := attempt["usage"].(map[string]any)
	if attemptUsage["prompt_tokens"] == nil || attemptUsage["completion_tokens"] == nil || attemptUsage["estimated_cost_usd"] == nil {
		t.Fatalf("attempt should include priced usage: %#v", attemptUsage)
	}
}

type playgroundCaptureAdapter struct {
	MockAdapter
	request ChatCompletionRequest
}

func TestAdminPlaygroundNeverForwardsCaseInsensitiveProjectContext(t *testing.T) {
	for _, path := range []string{"/api/admin/playground/chat", "/api/admin/playground/chat/stream"} {
		t.Run(path, func(t *testing.T) {
			server, _ := newPlaygroundTestServer(t)
			adapter := &playgroundCaptureAdapter{}
			server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
			response := doJSON(t, server.Handler(), http.MethodPost, path, map[string]any{
				"PROJECT_ID": "prj_demo",
				"model":      "gpt-4.1-mini",
				"messages":   []map[string]any{{"role": "user", "content": "do not leak context"}},
			}, "")
			if response.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
			}
			forwarded, err := json.Marshal(adapter.request)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(forwarded, &fields); err != nil {
				t.Fatal(err)
			}
			for key := range fields {
				if strings.EqualFold(key, "project_id") {
					t.Fatalf("playground project context leaked to the provider as %q: %s", key, forwarded)
				}
			}
		})
	}
}

func (a *playgroundCaptureAdapter) Chat(_ context.Context, _ Provider, _ string, req ChatCompletionRequest) (any, Usage, error) {
	a.request = req
	return a.MockAdapter.Chat(context.Background(), Provider{}, "", req)
}

func (a *playgroundCaptureAdapter) ChatStream(_ context.Context, _ Provider, _ string, _ ChatCompletionRequest, _ io.Writer) (Usage, error) {
	panic("ChatStream must not be invoked when the adapter lacks chat_stream capability")
}

func TestAdminPlaygroundStreamFallsBackToBufferedWithoutFakeTTFT(t *testing.T) {
	server, _ := newPlaygroundTestServer(t)
	adapter := &playgroundCaptureAdapter{}
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id":        "prj_demo",
		"model":             "gpt-4.1-mini",
		"messages":          []map[string]any{{"role": "user", "content": "buffer me"}},
		"presence_penalty":  0.4,
		"frequency_penalty": -0.2,
		"min_p":             0.08,
		"top_k":             42,
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	events := parsePlaygroundSSE(t, response.Body)
	delta := findPlaygroundSSEEvent(t, events, "playground.delta")
	deltaText, _ := delta.Data["delta"].(string)
	if delta.Data["mode"] != "buffered" || !strings.Contains(deltaText, "Echo: buffer me") {
		t.Fatalf("expected a buffered delta: %#v", delta.Data)
	}
	completed := findPlaygroundSSEEvent(t, events, "playground.completed")
	timing, _ := completed.Data["timing"].(map[string]any)
	if timing["mode"] != "buffered" || timing["ttft_ms"] != nil || timing["first_token_at"] != nil {
		t.Fatalf("buffered mode must not invent TTFT: %#v", timing)
	}
	if timing["end_to_end_tokens_per_second"] == nil {
		t.Fatalf("buffered mode should report end-to-end throughput: %#v", timing)
	}
	if adapter.request.PresencePenalty == nil || *adapter.request.PresencePenalty != 0.4 ||
		adapter.request.FrequencyPenalty == nil || *adapter.request.FrequencyPenalty != -0.2 ||
		adapter.request.MinP == nil || *adapter.request.MinP != 0.08 ||
		adapter.request.TopK == nil || *adapter.request.TopK != 42 {
		t.Fatalf("playground parameters were not forwarded: %+v", adapter.request)
	}
	if adapter.request.Stream || adapter.request.StreamOptions != nil {
		t.Fatalf("buffered fallback must disable the upstream stream contract: %+v", adapter.request)
	}
	forwarded, err := json.Marshal(adapter.request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(forwarded), "project_id") {
		t.Fatalf("playground project context leaked to the provider: %s", forwarded)
	}
}

func TestAdminPlaygroundStreamForwardsMultimodalContent(t *testing.T) {
	server, store := newPlaygroundTestServer(t)
	project := store.CreateProject(Project{Name: "Multimodal Playground Project"})
	adapter := &playgroundCaptureAdapter{}
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityChat)
	dataURI := "data:image/png;base64,YWJj"
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id": project.ID,
		"model":      "gpt-4.1-mini",
		"messages": []map[string]any{{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "describe"},
			playgroundImagePart(dataURI),
		}}},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	parts, ok := adapter.request.Messages[0].Content.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("multimodal content was not forwarded: %#v", adapter.request.Messages[0].Content)
	}
	image, _ := parts[1].(map[string]any)
	if got, _ := normalizePlaygroundImageURL(image["image_url"]); got != dataURI {
		t.Fatalf("image URL = %q, want %q", got, dataURI)
	}
}

func TestUserPlaygroundStreamRedactsRouteInternals(t *testing.T) {
	server, store := newPlaygroundTestServer(t)
	user, err := store.CreateAdminUser(AdminUser{
		Username: "playground-user", Name: "Playground User", Email: "playground-user@tokenhub.local",
		Role: "user", Status: StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.AuthenticateAdminUser(user.Username, "user123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("project-members", AdminResource{
		Name: "Playground Project Member", Status: StatusActive,
		Fields: map[string]any{"project_id": "prj_demo", "user_id": user.ID, "role": "developer"},
	})
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id": "prj_demo",
		"model":      "gpt-4.1-mini",
		"messages":   []map[string]any{{"role": "user", "content": "redact route"}},
	}, session.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body)
	}
	completed := findPlaygroundSSEEvent(t, parsePlaygroundSSE(t, response.Body), "playground.completed")
	route, _ := completed.Data["route"].(map[string]any)
	if len(route) != 0 {
		t.Fatalf("ordinary users must not receive selected route details: %#v", route)
	}
	if completed.Data["request_id"] == "" || completed.Data["timing"] == nil || completed.Data["usage"] == nil {
		t.Fatalf("ordinary users should retain request ID, timing, and usage diagnostics: %#v", completed.Data)
	}
	if attempts, ok := completed.Data["attempts"].([]any); ok && len(attempts) != 0 {
		t.Fatalf("ordinary users must not receive route-attempt internals: %#v", attempts)
	}
	usage, _ := completed.Data["usage"].(map[string]any)
	for _, field := range []string{"upstream_request_id", "served_model", "model_etag", "transport"} {
		if usage[field] != nil {
			t.Fatalf("ordinary user usage leaked %s: %#v", field, usage)
		}
	}

	legacy := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat", map[string]any{
		"project_id": "prj_demo",
		"model":      "gpt-4.1-mini",
		"messages":   []map[string]any{{"role": "user", "content": "legacy redaction"}},
	}, session.Token)
	if legacy.Code != http.StatusOK {
		t.Fatalf("expected legacy endpoint 200, got %d: %s", legacy.Code, legacy.Body)
	}
	var legacyPayload PlaygroundChatResponse
	if err := json.Unmarshal([]byte(legacy.Body), &legacyPayload); err != nil {
		t.Fatal(err)
	}
	if legacyPayload.Route.ProviderID != "" || len(legacyPayload.Attempts) != 0 {
		t.Fatalf("legacy endpoint leaked route internals: %+v", legacyPayload)
	}
}
