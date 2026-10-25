package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChatGPTSubscriptionRedactsActualStreamCredentials(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("refresh_%t", refresh), func(t *testing.T) {
			var calls atomic.Int32
			a, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 && refresh {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":%q}}\n\n", r.Header.Get("Authorization"))
			})
			a.RefreshCredentials = func(_ context.Context, _ string, force bool) (ProviderResourceCredentials, error) {
				if force {
					return ProviderResourceCredentials{AccessToken: "rotated-token-secret"}, nil
				}
				return ProviderResourceCredentials{AccessToken: "original-token-secret"}, nil
			}
			provider.Headers = map[string]string{"X-Custom": "unchanged"}
			opened, err := a.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Body.Close()
			var output bytes.Buffer
			_, _, _, err = consumeRoutedResponsesStream(CallContext{}, provider, opened.Body, &output)
			if err == nil {
				t.Fatal("upstream error was accepted")
			}
			for _, secret := range []string{"original-token-secret", "rotated-token-secret"} {
				if strings.Contains(output.String(), secret) || strings.Contains(err.Error(), secret) {
					t.Errorf("actual credential leaked in stream or returned error")
				}
			}
			if refresh && calls.Load() != 2 || !refresh && calls.Load() != 1 {
				t.Fatalf("unexpected upstream attempts: %d", calls.Load())
			}
			if len(provider.Headers) != 1 || len(provider.SensitiveHeaders) != 0 {
				t.Fatal("request redaction context mutated the shared provider")
			}
		})
	}
}

func TestChatGPTSubscriptionFailedResponsePreservesUsage(t *testing.T) {
	const stream = "data: {\"type\":\"response.failed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6},\"error\":{\"message\":\"failed\"}}}\n\n"
	a, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	})
	_, usage, err := a.Responses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"})
	if err == nil || usage.TotalTokens != 6 {
		t.Fatalf("buffered failure lost usage: %+v, %v", usage, err)
	}
	var output bytes.Buffer
	_, _, usage, err = consumeRoutedResponsesStream(CallContext{}, provider, strings.NewReader(stream), &output)
	if err == nil || usage.TotalTokens != 6 || output.String() != stream {
		t.Fatalf("public Responses failure lost usage or framing: %+v, %v", usage, err)
	}
	usage, err = consumeCodexCompatibilityStream(strings.NewReader(stream), newCodexAnthropicStreamSink(httptest.NewRecorder(), "model", 0, nil))
	if err == nil || usage.TotalTokens != 6 {
		t.Fatalf("compatibility failure lost usage: %+v, %v", usage, err)
	}
}

func TestSubscriptionResponsesRejectsInvalidTerminalAndOversizedFrames(t *testing.T) {
	const item = "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\"}}\n\n"
	for name, stream := range map[string]string{
		"missing response": item + "data: {\"type\":\"response.completed\"}\n\n",
		"null response":    item + "data: {\"type\":\"response.completed\",\"response\":null}\n\n",
		"missing terminal": item,
		"premature done":   item + "data: [DONE]\n\n",
		"invalid JSON":     "data: {broken}\n\n",
		"nonobject JSON":   "data: []\n\n",
		"oversized frame":  "data: " + strings.Repeat("x", maxSSEEventBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			_, _, _, err := consumeRoutedResponsesStream(CallContext{}, Provider{Type: ProviderChatGPTSubscription}, strings.NewReader(stream), &output)
			if err == nil {
				t.Fatal("invalid upstream response was accepted")
			}
			if name == "oversized frame" && output.Len() != 0 {
				t.Fatal("oversized frame reached the client")
			}
		})
	}
	// Exercise the buffered adapter too: it previously panicked while hydrating
	// output items into a terminal event without a response object.
	a, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, item+"data: {\"type\":\"response.completed\"}\n\n")
	})
	if _, _, err := a.Responses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}); err == nil {
		t.Fatal("buffered adapter accepted an invalid terminal response")
	}
}

func TestSubscriptionResponsesPreservesMultilineFramesAndToolOutput(t *testing.T) {
	const stream = ": keepalive\r\nevent: response.output_item.done\r\ndata: {\"output_index\":0,\r\ndata: \"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\",\"arguments\":\"{}\"}}\r\n\r\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"id\":\"response_1\",\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n"
	var output bytes.Buffer
	response, text, usage, err := consumeSubscriptionResponsesStream(strings.NewReader(stream), &output)
	if err != nil || text != "hello" || usage.TotalTokens != 6 || output.String() != stream {
		t.Fatalf("stream semantics changed: text=%q usage=%+v err=%v", text, usage, err)
	}
	items, _ := response["output"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["call_id"] != "call_1" {
		t.Fatalf("completed tool output was lost: %v", response)
	}
}
