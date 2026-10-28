package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestClaudeSubscriptionRejectsUnsupportedControls(t *testing.T) {
	cases := []struct{ parameter, value string }{
		{"temperature", "0"},
		{"presence_penalty", "-1"}, {"presence_penalty", "1"},
		{"frequency_penalty", "-1"}, {"frequency_penalty", "0.5"},
		{"min_p", "0.1"}, {"min_p", "1"}, {"top_k", "-1"}, {"top_k", "1"},
		{"n", "2"}, {"seed", "42"}, {"logit_bias", `{"123":1}`},
		{"logprobs", "true"}, {"top_logprobs", "2"},
		{"modalities", `["text","audio"]`}, {"audio", `{"voice":"alloy","format":"wav"}`},
		{"prediction", `{"type":"content","content":"Expected text"}`},
		{"functions", `[{"name":"weather","parameters":{"type":"object"}}]`},
		{"function_call", `"auto"`}, {"web_search_options", `{}`},
		{"service_tier", `"priority"`}, {"verbosity", `"low"`},
		{"thinking", `{"type":"enabled","budget_tokens":1000}`},
		{"store", "true"}, {"prompt_cache_retention", `"24h"`}, {"max_tool_calls", "1"},
	}
	for _, test := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s=%s/stream=%t", test.parameter, test.value, stream), func(t *testing.T) {
				adapter, provider, ctx := claudeSubscriptionFixture(t)
				var request ChatCompletionRequest
				body := fmt.Sprintf(`{"messages":[{"role":"user","content":"Answer briefly"}],%q:%s}`, test.parameter, test.value)
				if err := json.Unmarshal([]byte(body), &request); err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				var usage Usage
				var err error
				if stream {
					usage, err = adapter.ChatStream(ctx, provider, "text", request, &output)
				} else {
					_, usage, err = adapter.Chat(ctx, provider, "text", request)
				}
				failure := AsHTTPError(err)
				if failure == nil || failure.Status != http.StatusNotImplemented || failure.Code != "provider_capability_not_supported" {
					t.Fatalf("unsupported inference control was accepted: usage=%+v error=%v", usage, err)
				}
				if usage.TotalTokens != 0 || output.Len() != 0 {
					t.Fatal("unsupported inference control reached inference")
				}
			})
		}
	}
}

func TestClaudeSubscriptionAcceptsDefaultControlsAndMetadata(t *testing.T) {
	for _, tier := range []string{"auto", "default"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tier, stream), func(t *testing.T) {
				adapter, provider, ctx := claudeSubscriptionFixture(t)
				var request ChatCompletionRequest
				body := fmt.Sprintf(`{"messages":[{"role":"user","content":"Answer briefly"}],"presence_penalty":0,"frequency_penalty":0,"min_p":0,"top_k":0,"n":1,"logprobs":false,"top_logprobs":0,"logit_bias":{},"modalities":["text"],"functions":[],"function_call":"none","store":false,"service_tier":%q,"seed":null,"audio":null,"metadata":{"task":"fixture"},"user":"caller","prompt_cache_key":"conversation","stream_options":{"include_usage":true},"reasoning_effort":"medium","max_completion_tokens":64}`, tier)
				if err := json.Unmarshal([]byte(body), &request); err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				var usage Usage
				var err error
				if stream {
					usage, err = adapter.ChatStream(ctx, provider, "text", request, &output)
				} else {
					var response any
					response, usage, err = adapter.Chat(ctx, provider, "text", request)
					if err == nil {
						err = json.NewEncoder(&output).Encode(response)
					}
				}
				if err != nil || usage.TotalTokens != 6 || output.Len() == 0 {
					t.Fatalf("supported defaults or metadata were rejected: usage=%+v error=%v", usage, err)
				}
			})
		}
	}
}

func TestClaudeSubscriptionRejectsChangedContinuationControls(t *testing.T) {
	for _, parameter := range []string{"max_tokens", "reasoning_effort"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", parameter, stream), func(t *testing.T) {
				adapter, provider, ctx := claudeSubscriptionFixture(t)
				request := claudeSubscriptionToolRequest()
				effort := "medium"
				request.ReasoningEffort, request.MaxTokens = &effort, 64
				body, _, err := adapter.Chat(ctx, provider, "tools", request)
				if err != nil {
					t.Fatal(err)
				}
				next := claudeSubscriptionContinue(t, request, body)
				changed := next
				if parameter == "max_tokens" {
					changed.MaxTokens = 128
				} else {
					high := "high"
					changed.ReasoningEffort = &high
				}
				var output bytes.Buffer
				if stream {
					_, err = adapter.ChatStream(ctx, provider, "tools", changed, &output)
				} else {
					_, _, err = adapter.Chat(ctx, provider, "tools", changed)
				}
				if failure := AsHTTPError(err); failure == nil || failure.Code != "claude_subscription_continuation_invalid" {
					t.Fatalf("changed continuation control was silently ignored: %v", err)
				}
				if _, _, err := adapter.Chat(ctx, provider, "tools", next); err != nil {
					t.Fatalf("rejected control change consumed the valid continuation: %v", err)
				}
			})
		}
	}
}
