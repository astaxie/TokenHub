package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestClaudeSubscriptionBatchesStreamedToolBlocksBeforeContinuation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "stream"}[stream], func(t *testing.T) {
			adapter, provider, ctx := claudeSubscriptionFixture(t)
			adapter.ExecutableArgs = []string{"-test.run=^TestClaudeSubscriptionBlockHelperProcess$", "--", "tokenhub-claude-block-helper"}
			request := claudeSubscriptionToolRequest()
			first, usage, err := adapter.Chat(ctx, provider, "blocks", request)
			if err != nil || usage.PromptTokens != 10 || usage.CompletionTokens != 4 {
				t.Fatalf("tool round usage = %+v, error = %v", usage, err)
			}
			next := claudeSubscriptionContinue(t, request, first)
			if len(next.Messages) != len(request.Messages)+3 {
				t.Fatalf("parallel tool calls were not returned together: %+v", first)
			}
			var text string
			if stream {
				var output bytes.Buffer
				usage, err = adapter.ChatStream(ctx, provider, "blocks", next, &output)
				text = output.String()
			} else {
				var body any
				body, usage, err = adapter.Chat(ctx, provider, "blocks", next)
				raw, _ := json.Marshal(body)
				text = string(raw)
			}
			if err != nil || usage.PromptTokens != 10 || usage.CompletionTokens != 4 || !strings.Contains(text, "Both sunny") {
				t.Fatalf("parallel continuation failed: usage=%+v error=%v body=%s", usage, err, text)
			}
		})
	}
}

func TestClaudeSubscriptionRejectsDisabledParallelToolCalls(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "stream"}[stream], func(t *testing.T) {
			adapter, provider, ctx := claudeSubscriptionFixture(t)
			adapter.ExecutableArgs = []string{"-test.run=^TestClaudeSubscriptionBlockHelperProcess$", "--", "tokenhub-claude-block-helper"}
			request := claudeSubscriptionToolRequest()
			disabled := false
			request.ParallelToolCalls = &disabled
			var err error
			if stream {
				_, err = adapter.ChatStream(ctx, provider, "blocks", request, io.Discard)
			} else {
				_, _, err = adapter.Chat(ctx, provider, "blocks", request)
			}
			if failure := AsHTTPError(err); failure == nil || failure.Code != "provider_capability_not_supported" {
				t.Fatalf("unsupported parallel tool constraint was accepted: %v", err)
			}
		})
	}
}

func TestClaudeSubscriptionAcceptsParallelToolControlsWithoutConflict(t *testing.T) {
	enabled, disabled := true, false
	for _, test := range []struct {
		name     string
		parallel *bool
		choice   any
		noTools  bool
		want     int
	}{
		{name: "default", want: 1},
		{name: "enabled", parallel: &enabled, want: 1},
		{name: "tools disabled", parallel: &disabled, choice: "none"},
		{name: "no tools", parallel: &disabled, noTools: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := claudeSubscriptionToolRequest()
			request.ParallelToolCalls, request.ToolChoice = test.parallel, test.choice
			if test.noTools {
				request.Tools = nil
			}
			tools, err := claudeSubscriptionTools(request)
			if err != nil || len(tools) != test.want {
				t.Fatalf("supported tool controls failed: tools=%d error=%v", len(tools), err)
			}
		})
	}
}

// This fixture follows the genuine CLI's one-assistant-event-per-content-block
// shape, with the final usage arriving in message_delta after both tool blocks.
func TestClaudeSubscriptionBlockHelperProcess(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "tokenhub-claude-block-helper") {
		return
	}
	fail := func() { os.Exit(12) }
	var mcpPath string
	for i, arg := range os.Args {
		if arg == "--mcp-config" && i+1 < len(os.Args) {
			mcpPath = os.Args[i+1]
		}
	}
	raw, err := os.ReadFile(mcpPath)
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
	var input any
	if json.NewDecoder(os.Stdin).Decode(&input) != nil {
		fail()
	}
	encoder := json.NewEncoder(os.Stdout)
	emit := func(value any) {
		if encoder.Encode(value) != nil {
			fail()
		}
	}
	stream := func(event map[string]any) { emit(map[string]any{"type": "stream_event", "event": event}) }
	start := func(id string) {
		stream(map[string]any{"type": "message_start", "message": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1}}})
	}
	block := func(id string, content map[string]any) {
		emit(map[string]any{"type": "assistant", "message": map[string]any{"id": id, "content": []any{content}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1}}})
	}
	stop := func() {
		stream(map[string]any{"type": "message_delta", "usage": map[string]any{"output_tokens": 4}})
		stream(map[string]any{"type": "message_stop"})
	}
	start("message-1")
	block("message-1", map[string]any{"type": "text", "text": "Checking both cities."})
	cities := []string{"Paris", "Tokyo"}
	ids := []string{"tool-internal-1", "tool-internal-2"}
	for i, city := range cities {
		block("message-1", map[string]any{"type": "tool_use", "id": ids[i], "name": "mcp__tokenhub__tool_0", "input": map[string]any{"city": city}})
	}
	stop()
	for i, city := range cities {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call", "params": map[string]any{"name": "tool_0", "arguments": map[string]any{"city": city}, "_meta": map[string]any{"claudecode/toolUseId": ids[i]}}})
		request, _ := http.NewRequest(http.MethodPost, config.Servers["tokenhub"].URL, bytes.NewReader(payload))
		for key, value := range config.Servers["tokenhub"].Headers {
			request.Header.Set(key, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			fail()
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if !bytes.Contains(body, []byte("sunny")) {
			fail()
		}
	}
	start("message-2")
	block("message-2", map[string]any{"type": "text", "text": "Both sunny."})
	stop()
	emit(map[string]any{"type": "result", "is_error": false, "usage": map[string]any{"input_tokens": 20, "output_tokens": 8}})
	os.Exit(0)
}
