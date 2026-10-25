package server

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeSubscriptionResultRetainsUsageWithoutDoubleCounting(t *testing.T) {
	const assistant = `{"type":"assistant","message":{"content":[],"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":5}}}}`
	const resultUsage = `"usage":{"input_tokens":14,"output_tokens":5,"cache_read_input_tokens":5,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":7}}`
	want := Usage{PromptTokens: 29, CompletionTokens: 5, TotalTokens: 34, CachedInputTokens: 5, CacheWriteInputTokens: 10, CacheWrite5mInputTokens: 3, CacheWrite1hInputTokens: 7}
	for _, test := range []struct {
		name   string
		prefix string
		failed bool
		result string
	}{
		{name: "successful result", result: `{"type":"result","is_error":false,` + resultUsage + `}`},
		{name: "successful result after assistant", prefix: assistant + "\n", result: `{"type":"result","is_error":false,` + resultUsage + `}`},
		{name: "failed result", failed: true, result: `{"type":"result","is_error":true,` + resultUsage + `}`},
		{name: "failed result after assistant", prefix: assistant + "\n", failed: true, result: `{"type":"result","is_error":true,` + resultUsage + `}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := &claudeSubscriptionRun{ctx: context.Background(), events: make(chan claudeSubscriptionEvent, 4)}
			run.readOutput(strings.NewReader(test.prefix + test.result + "\n"))
			var usage Usage
			var final claudeSubscriptionEvent
			for len(run.events) > 0 {
				final = <-run.events
				usage = claudeSubscriptionAddUsage(usage, final.Usage)
			}
			if !reflect.DeepEqual(usage, want) {
				t.Fatalf("result usage = %+v, want %+v", usage, want)
			}
			if test.failed {
				if final.Done || AsHTTPError(final.Err).Code != "claude_subscription_failed" {
					t.Fatalf("failed result lost its error: %+v", final)
				}
			} else if !final.Done || final.Err != nil {
				t.Fatalf("successful result did not finish: %+v", final)
			}
		})
	}
}

func TestClaudeSubscriptionUsageSnapshotsAreScopedToMessage(t *testing.T) {
	// Claude Code repeats the initial snapshot on each assistant content block;
	// message_delta can then contain only the fields updated at message end.
	const lines = `{"type":"stream_event","event":{"type":"message_start","message":{"id":"first","usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":3,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":5}}}}}
{"type":"assistant","message":{"id":"first","content":[{"type":"text","text":"one"}],"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":3,"cache_creation_input_tokens":7}}}
{"type":"assistant","message":{"id":"first","content":[{"type":"text","text":"two"}],"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":3,"cache_creation_input_tokens":7}}}
{"type":"stream_event","event":{"type":"message_delta","usage":{"input_tokens":12,"output_tokens":4}}}
{"type":"stream_event","event":{"type":"message_stop"}}
{"type":"stream_event","event":{"type":"message_start","message":{"id":"second","usage":{"input_tokens":6,"output_tokens":1,"cache_read_input_tokens":5}}}}
{"type":"assistant","message":{"id":"second","content":[{"type":"text","text":"three"}],"usage":{"input_tokens":6,"output_tokens":1,"cache_read_input_tokens":5}}}
{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":2}}}
{"type":"stream_event","event":{"type":"message_stop"}}
{"type":"result","is_error":false,"usage":{"input_tokens":18,"output_tokens":6,"cache_read_input_tokens":8,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":5}}}
`
	run := &claudeSubscriptionRun{ctx: context.Background(), events: make(chan claudeSubscriptionEvent, 16)}
	run.readOutput(strings.NewReader(lines))
	var usage Usage
	var text strings.Builder
	for len(run.events) > 0 {
		event := <-run.events
		usage = claudeSubscriptionAddUsage(usage, event.Usage)
		text.WriteString(event.Text)
	}
	want := Usage{PromptTokens: 33, CompletionTokens: 6, TotalTokens: 39, CachedInputTokens: 8, CacheWriteInputTokens: 7, CacheWrite5mInputTokens: 2, CacheWrite1hInputTokens: 5}
	if !reflect.DeepEqual(usage, want) || text.String() != "onetwothree" {
		t.Fatalf("message snapshots lost usage or text: usage=%+v text=%q", usage, text.String())
	}
}
