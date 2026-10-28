package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestClaudeSubscriptionPreservesReasoningAndPublicCacheUsage(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("partial=%t/stream=%t", partial, stream), func(t *testing.T) {
				adapter, provider, ctx := claudeSubscriptionFixture(t)
				adapter.ExecutableArgs = []string{"-test.run=^TestClaudeSubscriptionReasoningHelperProcess$", "--", "tokenhub-claude-reasoning-helper", fmt.Sprint(partial)}
				request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}, StreamOptions: map[string]any{"include_usage": true}}
				var output bytes.Buffer
				var usage Usage
				var err error
				if stream {
					usage, err = adapter.ChatStream(ctx, provider, "sonnet", request, &output)
				} else {
					var response any
					response, usage, err = adapter.Chat(ctx, provider, "sonnet", request)
					if err == nil {
						err = json.NewEncoder(&output).Encode(response)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if usage.CachedInputTokens != 3 || usage.CacheWriteInputTokens != 2 || usage.PromptTokens != 15 || usage.CompletionTokens != 5 {
					t.Fatalf("reported usage changed: %+v", usage)
				}
				text := output.String()
				for _, want := range []string{`"reasoning_content":"Brief reasoning"`, `"cached_tokens":3`, `"cache_write_tokens":2`, `"content":"Hello"`} {
					if strings.Count(text, want) != 1 {
						t.Errorf("output must preserve %s exactly once: %s", want, text)
					}
				}
				if stream && !strings.Contains(text, "[DONE]") {
					t.Fatal("stream did not complete")
				}
			})
		}
	}
}

func TestClaudeSubscriptionReasoningHelperProcess(t *testing.T) {
	partial, helper := false, false
	for i, arg := range os.Args {
		if arg == "tokenhub-claude-reasoning-helper" {
			helper = true
			partial = i+1 < len(os.Args) && os.Args[i+1] == "true"
		}
	}
	if !helper {
		return
	}
	var input any
	if json.NewDecoder(os.Stdin).Decode(&input) != nil {
		os.Exit(12)
	}
	const usage = `"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}`
	fmt.Println(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1",` + usage + `}}}`)
	if partial {
		fmt.Println(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Brief reasoning"}}}`)
	}
	fmt.Println(`{"type":"assistant","message":{"id":"m1","content":[{"type":"thinking","thinking":"Brief reasoning","signature":"fixture-signature"}],` + usage + `}}`)
	if partial {
		fmt.Println(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}}`)
	}
	fmt.Println(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Hello"}],` + usage + `}}`)
	fmt.Println(`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}}`)
	fmt.Println(`{"type":"stream_event","event":{"type":"message_stop"}}`)
	fmt.Println(`{"type":"result","is_error":false,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`)
	os.Exit(0)
}
