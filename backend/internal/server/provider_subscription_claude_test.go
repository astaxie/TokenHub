package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func claudeSubscriptionFixture(t *testing.T) (*ClaudeSubscriptionAdapter, Provider, context.Context) {
	t.Helper()
	adapter := &ClaudeSubscriptionAdapter{Executable: os.Args[0], ExecutableArgs: []string{"-test.run=^TestClaudeSubscriptionHelperProcess$", "--", "tokenhub-claude-helper"}, StreamIdleTimeout: 3 * time.Second, ParkTimeout: 3 * time.Second, RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{AccessToken: "fixture-token"}, nil
	}}
	t.Cleanup(adapter.Close)
	return adapter, Provider{Type: providerClaudeSubscription, Options: map[string]string{"resource_id": "fixture-account"}}, withSubscriptionCaller(context.Background(), "project-a", "key-a")
}
func claudeSubscriptionToolRequest() ChatCompletionRequest {
	return ChatCompletionRequest{Messages: []ChatMessage{{Role: "system", Content: "Be concise."}, {Role: "user", Content: "Check Paris weather"}}, Tools: []any{map[string]any{"type": "function", "function": map[string]any{"name": "weather", "description": "Read weather", "parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}}}}}
}
func claudeSubscriptionContinue(t *testing.T, request ChatCompletionRequest, body any) ChatCompletionRequest {
	t.Helper()
	response := body.(map[string]any)
	message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	calls := message["tool_calls"].([]map[string]any)
	next := request
	next.Messages = append(append([]ChatMessage(nil), request.Messages...), ChatMessage{Role: "assistant", Content: message["content"], ToolCalls: calls})
	for _, call := range calls {
		next.Messages = append(next.Messages, ChatMessage{Role: "tool", ToolCallID: call["id"].(string), Content: "sunny"})
	}
	return next
}

func TestClaudeSubscriptionRetainsProcessForBoundToolContinuation(t *testing.T) {
	adapter, provider, ctx := claudeSubscriptionFixture(t)
	request := claudeSubscriptionToolRequest()
	first, usage, err := adapter.Chat(ctx, provider, "tools", request)
	if err != nil || usage.TotalTokens != 5 {
		t.Fatalf("first tool request failed: usage=%+v error=%v", usage, err)
	}
	continuation := claudeSubscriptionContinue(t, request, first)
	id := continuation.Messages[len(continuation.Messages)-1].ToolCallID
	if !strings.HasPrefix(id, "call_") || strings.Contains(id, "internal") {
		t.Fatal("CLI tool identifier escaped")
	}
	for _, other := range []context.Context{withSubscriptionCaller(context.Background(), "project-b", "key-a"), withSubscriptionCaller(context.Background(), "project-a", "key-b")} {
		if _, _, err := adapter.Chat(other, provider, "tools", continuation); AsHTTPError(err).Code != "claude_subscription_continuation_invalid" {
			t.Fatalf("foreign caller resumed tool: %v", err)
		}
	}
	changed := continuation
	changed.Messages = append([]ChatMessage(nil), continuation.Messages...)
	changed.Messages[0].Content = "Different policy"
	if _, _, err := adapter.Chat(ctx, provider, "tools", changed); AsHTTPError(err).Code != "claude_subscription_continuation_invalid" {
		t.Fatalf("changed conversation resumed: %v", err)
	}
	second, usage, err := adapter.Chat(ctx, provider, "tools", continuation)
	if err != nil || usage.PromptTokens != 7 || usage.CompletionTokens != 2 {
		t.Fatalf("tool continuation failed: usage=%+v error=%v", usage, err)
	}
	encoded, _ := json.Marshal(second)
	if !strings.Contains(string(encoded), "Paris: sunny") {
		t.Fatalf("missing MCP result: %s", encoded)
	}
	if _, _, err := adapter.Chat(ctx, provider, "tools", continuation); AsHTTPError(err).Code != "claude_subscription_continuation_invalid" {
		t.Fatalf("tool continuation replayed: %v", err)
	}
}

func TestClaudeSubscriptionPersistsSessionBeforeParking(t *testing.T) {
	adapter, provider, ctx := claudeSubscriptionFixture(t)
	current := `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"refresh-original"}}`
	adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{SessionJSON: current}, nil
	}
	saves := 0
	adapter.SaveSession = func(_ context.Context, _, _, previous, next string) error {
		if previous != current {
			t.Fatal("rotation used stale session")
		}
		current = next
		saves++
		return nil
	}
	request := claudeSubscriptionToolRequest()
	first, _, err := adapter.Chat(ctx, provider, "refresh-tools", request)
	if err != nil || saves != 1 {
		t.Fatalf("rotation was not saved before tool handoff: saves=%d error=%v", saves, err)
	}
	if _, _, err := adapter.Chat(ctx, provider, "refresh-tools", claudeSubscriptionContinue(t, request, first)); err != nil {
		t.Fatalf("rotated session could not resume: %v", err)
	}
}

func TestClaudeSubscriptionStreamsWithoutDuplicatingAssistantText(t *testing.T) {
	adapter, provider, ctx := claudeSubscriptionFixture(t)
	var output bytes.Buffer
	usage, err := adapter.ChatStream(ctx, provider, "stream", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}, StreamOptions: map[string]any{"include_usage": true}}, &output)
	if err != nil || usage.TotalTokens != 6 {
		t.Fatalf("stream failed: %v %+v", err, usage)
	}
	if strings.Count(output.String(), `"content":"hello"`) != 1 || strings.Count(output.String(), `"content":" world"`) != 1 || !strings.Contains(output.String(), "[DONE]") {
		t.Fatalf("incorrect stream: %s", output.String())
	}
}

func TestClaudeSubscriptionRejectsHostCredentialsAndUndeclaredTools(t *testing.T) {
	adapter, provider, ctx := claudeSubscriptionFixture(t)
	t.Setenv("ANTHROPIC_API_KEY", "host-secret")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "host-oauth")
	if _, _, err := adapter.Chat(ctx, provider, "stream", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}); err != nil {
		t.Fatalf("isolated CLI failed: %v", err)
	}
	if _, _, err := adapter.Chat(ctx, provider, "forbidden", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}); AsHTTPError(err).Code != "claude_subscription_tool_refused" {
		t.Fatalf("undeclared CLI tool accepted: %v", err)
	}
	adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{}, nil
	}
	if _, _, err := adapter.Chat(ctx, provider, "stream", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}); AsHTTPError(err).Code != "claude_subscription_credentials_required" {
		t.Fatalf("host credential fallback was permitted: %v", err)
	}
}

func TestClaudeSubscriptionPersistsRotatedSession(t *testing.T) {
	adapter, provider, ctx := claudeSubscriptionFixture(t)
	session := `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"refresh-original"}}`
	adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{SessionJSON: session}, nil
	}
	saved := ""
	adapter.SaveSession = func(_ context.Context, id, kind, previous, next string) error {
		if id != "fixture-account" || kind != providerClaudeSubscription || previous != session {
			t.Fatal("rotation lost account binding")
		}
		saved = next
		return nil
	}
	_, _, err := adapter.Chat(ctx, provider, "refresh", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}})
	if err != nil || !strings.Contains(saved, "rotated-refresh") {
		t.Fatalf("rotated credentials not saved: %v", err)
	}
}

func TestClaudeSubscriptionCancellationAndParkExpiry(t *testing.T) {
	adapter, provider, ctx := claudeSubscriptionFixture(t)
	canceled, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, _, err := adapter.Chat(canceled, provider, "idle", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Wait"}}})
		done <- err
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not end CLI")
	}
	adapter.ParkTimeout = 50 * time.Millisecond
	first, _, err := adapter.Chat(ctx, provider, "tools", claudeSubscriptionToolRequest())
	if err != nil {
		t.Fatal(err)
	}
	continuation := claudeSubscriptionContinue(t, claudeSubscriptionToolRequest(), first)
	time.Sleep(100 * time.Millisecond)
	if _, _, err := adapter.Chat(ctx, provider, "tools", continuation); AsHTTPError(err).Code != "claude_subscription_continuation_invalid" {
		t.Fatalf("expired tool process resumed: %v", err)
	}
}

func TestClaudeSubscriptionHelperProcess(t *testing.T) {
	helper := false
	for _, arg := range os.Args {
		if arg == "tokenhub-claude-helper" {
			helper = true
		}
	}
	if !helper {
		return
	}
	fail := func() { os.Exit(12) }
	value := func(flag string) string {
		for i, arg := range os.Args {
			if arg == flag && i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		return "missing"
	}
	if value("--tools") != "" || value("--setting-sources") != "" || value("--mcp-config") == "missing" || os.Getenv("ANTHROPIC_API_KEY") != "" {
		fail()
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if info, err := os.Stat(configDir); err != nil || info.Mode().Perm() != 0700 {
		fail()
	}
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" && token != "fixture-token" {
		fail()
	}
	raw, err := os.ReadFile(value("--mcp-config"))
	if err != nil {
		fail()
	}
	var config struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &config) != nil {
		fail()
	}
	var input map[string]any
	if json.NewDecoder(os.Stdin).Decode(&input) != nil || input["type"] != "user" {
		fail()
	}
	out := json.NewEncoder(os.Stdout)
	emit := func(value any) {
		if out.Encode(value) != nil {
			fail()
		}
	}
	assistant := func(content []any, input, output int) {
		emit(map[string]any{"type": "assistant", "message": map[string]any{"content": content, "usage": map[string]any{"input_tokens": input, "output_tokens": output}}})
	}
	finish := func(input, output int) {
		emit(map[string]any{"type": "result", "is_error": false, "usage": map[string]any{"input_tokens": input, "output_tokens": output}})
	}
	mode := value("--model")
	if mode == "refresh-tools" || mode == "refresh-idle" {
		if os.WriteFile(filepath.Join(configDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"rotated-access","refreshToken":"rotated-refresh"}}`), 0600) != nil {
			fail()
		}
		if mode == "refresh-idle" {
			mode = "idle"
		} else {
			mode = "tools"
		}
	}
	switch mode {
	case "idle":
		time.Sleep(time.Minute)
	case "tools":
		assistant([]any{map[string]any{"type": "tool_use", "id": "internal-call-1", "name": "mcp__tokenhub__tool_0", "input": map[string]any{"city": "Paris"}}}, 4, 1)
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "tool_0", "arguments": map[string]any{"city": "Paris"}, "_meta": map[string]any{"claudecode/toolUseId": "internal-call-1"}}})
		req, _ := http.NewRequest(http.MethodPost, config.Servers["tokenhub"].URL, bytes.NewReader(payload))
		for key, value := range config.Servers["tokenhub"].Headers {
			req.Header.Set(key, value)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			fail()
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if !bytes.Contains(body, []byte("sunny")) {
			fail()
		}
		assistant([]any{map[string]any{"type": "text", "text": "Paris: sunny"}}, 7, 2)
		finish(11, 3)
	case "forbidden":
		assistant([]any{map[string]any{"type": "tool_use", "id": "internal-danger", "name": "Bash", "input": map[string]any{"command": "bad"}}}, 1, 1)
	case "refresh":
		path := filepath.Join(configDir, ".credentials.json")
		if _, err := os.Stat(path); err != nil {
			fail()
		}
		if os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"rotated-access","refreshToken":"rotated-refresh"}}`), 0600) != nil {
			fail()
		}
		assistant([]any{map[string]any{"type": "text", "text": "refreshed"}}, 1, 1)
		finish(1, 1)
	default:
		for _, text := range []string{"hello", " world"} {
			emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": text}}})
			time.Sleep(10 * time.Millisecond)
		}
		assistant([]any{map[string]any{"type": "text", "text": "hello world"}}, 4, 2)
		finish(4, 2)
	}
	os.Exit(0)
}
