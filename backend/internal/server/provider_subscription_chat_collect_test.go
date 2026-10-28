package server

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func subscriptionChatFixture(frames ...string) string {
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func TestCollectSubscriptionChatRetainsChoicesReasoningRefusalAndFinalUsage(t *testing.T) {
	stream := subscriptionChatFixture(
		`{"id":"completion-one","model":"served-model","created":123,"system_fingerprint":"test-fingerprint","choices":[{"index":1,"delta":{"role":"assistant","refusal":"Cannot "}},{"index":0,"delta":{"role":"assistant","reasoning_content":"Think ","reasoning":"First ","content":"Hel"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"more","reasoning":"step","content":"lo"},"finish_reason":"stop"},{"index":1,"delta":{"refusal":"comply"},"finish_reason":"content_filter"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":3}}}`,
		`[DONE]`,
	)
	response, usage, err := collectSubscriptionChat(strings.NewReader(stream), "public-model")
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 5 || usage.CachedInputTokens != 4 || usage.ReasoningOutputTokens != 3 || usage.TotalTokens != 17 {
		t.Fatalf("Final usage was lost: %+v", usage)
	}
	body := response.(map[string]any)
	if body["id"] != "completion-one" || body["model"] != "served-model" || body["created"] != int64(123) || body["system_fingerprint"] != "test-fingerprint" || body["object"] != "chat.completion" {
		t.Fatalf("Completion metadata changed: %+v", body)
	}
	choices := body["choices"].([]any)
	first := choices[0].(map[string]any)
	message := first["message"].(map[string]any)
	if first["index"] != 0 || first["finish_reason"] != "stop" || message["role"] != "assistant" || message["content"] != "Hello" || message["reasoning_content"] != "Think more" || message["reasoning"] != "First step" {
		t.Fatalf("Text deltas changed: %+v", first)
	}
	second := choices[1].(map[string]any)
	if second["index"] != 1 || second["finish_reason"] != "content_filter" || second["message"].(map[string]any)["refusal"] != "Cannot comply" {
		t.Fatalf("Refusal deltas changed: %+v", second)
	}
	if usageFromMap(body).CachedInputTokens != 4 {
		t.Fatal("The response object lost detailed usage")
	}
}

func TestCollectSubscriptionChatCombinesInterleavedToolDeltas(t *testing.T) {
	stream := subscriptionChatFixture(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"second","arguments":"{"}},{"index":0,"id":"call_","type":"function","function":{"name":"get_","arguments":"{\"x\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"value","arguments":"1}"}},{"index":1,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
	)
	response, _, err := collectSubscriptionChat(strings.NewReader(stream), "public")
	if err != nil {
		t.Fatal(err)
	}
	choice := response.(map[string]any)["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	if message["content"] != nil || choice["finish_reason"] != "tool_calls" {
		t.Fatalf("Tool completion shape changed: %+v", choice)
	}
	encoded, _ := json.Marshal(message["tool_calls"])
	want := `[{"function":{"arguments":"{\"x\":1}","name":"get_value"},"id":"call_a","type":"function"},{"function":{"arguments":"{}","name":"second"},"id":"call_b","type":"function"}]`
	if string(encoded) != want {
		t.Fatalf("Tool deltas were lost or reordered: %s", encoded)
	}
}

func TestCollectSubscriptionChatRejectsErrorsAndRetainsUsage(t *testing.T) {
	for _, frame := range []string{
		`data: {"error":{"type":"rate_limit_error","message":"Try later"},"usage":{"prompt_tokens":9,"completion_tokens":2}}` + "\n\n",
		"event: error\ndata: {\"message\":\"Failure\",\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2}}\n\n",
	} {
		response, usage, err := collectSubscriptionChat(strings.NewReader(frame), "public")
		if err == nil || response != nil || usage.PromptTokens != 9 || usage.CompletionTokens != 2 {
			t.Fatalf("Error or billed usage lost: response=%v usage=%+v error=%v", response, usage, err)
		}
	}
	_, _, err := collectSubscriptionChat(strings.NewReader(subscriptionChatFixture(`{"error":{"type":"rate_limit_error"}}`)), "public")
	if AsHTTPError(err).Status != 429 || AsHTTPError(err).UpstreamStatus != 200 {
		t.Fatalf("Stream failure lost classification: %v", err)
	}
	priorUsage := subscriptionChatFixture(`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
	for _, reader := range []io.Reader{
		strings.NewReader(priorUsage + subscriptionChatFixture(`{"error":{"type":"rate_limit_error"}}`)),
		io.MultiReader(strings.NewReader(priorUsage), subscriptionChatWrappedFailureReader{}),
	} {
		_, measured, err := collectSubscriptionChat(reader, "public")
		retained := subscriptionJSONFailureUsage(err)
		if err == nil || measured.PromptTokens != 7 || retained.PromptTokens != 7 || retained.CompletionTokens != 2 {
			t.Fatalf("Error wrapper overwrote earlier usage: measured=%+v retained=%+v error=%v", measured, retained, err)
		}
	}
}

func TestCollectSubscriptionChatRejectsTruncationAndInvalidDeltas(t *testing.T) {
	for _, frame := range []string{
		`[DONE]`,
		`{"choices":[{"index":0,"delta":{"content":"Partial"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"},{"index":1,"delta":{"content":"Partial"}}]}`,
		`{"choices":[{"index":99999999999999999999,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[{"index":-1,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[{"index":0.1,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":99999999999999999}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"index":0,"delta":{"content":{}},"finish_reason":"stop"}]}`,
		`{"choices":{}}`,
		`{"choices":[]} {"choices":[]}`,
		`null`,
	} {
		t.Run(frame, func(t *testing.T) {
			response, _, err := collectSubscriptionChat(strings.NewReader(subscriptionChatFixture(frame)), "public")
			if response != nil || err == nil {
				t.Fatalf("Invalid stream was accepted: response=%v error=%v", response, err)
			}
		})
	}
}

func TestCollectSubscriptionChatBoundsTotalResponseAndPreservesReadFailureUsage(t *testing.T) {
	frame := subscriptionChatFixture(`{"choices":[{"index":0,"delta":{"content":"` + strings.Repeat("x", 1<<20) + `"}}]}`)
	readers := make([]io.Reader, 33)
	for i := range readers {
		readers[i] = strings.NewReader(frame)
	}
	if _, _, err := collectSubscriptionChat(io.MultiReader(readers...), "public"); err == nil || !strings.Contains(err.Error(), "response limit") {
		t.Fatalf("Accumulated completion exceeded its limit: %v", err)
	}
	reader := io.MultiReader(strings.NewReader(subscriptionChatFixture(`{"choices":[],"usage":{"prompt_tokens":8}}`)), subscriptionChatFailingReader{})
	if _, usage, err := collectSubscriptionChat(reader, "public"); err != io.ErrUnexpectedEOF || usage.PromptTokens != 8 {
		t.Fatalf("Read failure lost usage: %+v %v", usage, err)
	}
}

type subscriptionChatFailingReader struct{}

func (subscriptionChatFailingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type subscriptionChatWrappedFailureReader struct{}

func (subscriptionChatWrappedFailureReader) Read([]byte) (int, error) {
	return 0, &subscriptionJSONFailure{err: io.ErrUnexpectedEOF}
}
