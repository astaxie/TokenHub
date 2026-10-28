package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkBuddyBufferedInferenceUsesStreamingUpstream(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, providerID := range []string{"workbuddy", "workbuddy-ai"} {
		for _, operation := range []string{"probe", "chat", "chat-tools", "chat-error", "chat-cancel", "chat-idle", "stream"} {
			t.Run(providerID+"/"+operation, func(t *testing.T) {
				var inferenceCalls, nonStreamingCalls atomic.Int32
				streamStarted, streamClosed := make(chan struct{}), make(chan struct{})
				client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
					status, contentType, body := http.StatusOK, "application/json", ""
					switch request.URL.Path {
					case "/v3/config":
						body = `{"code":0,"data":{"agents":[{"name":"cli","models":["fixture-model"]}],"models":[{"id":"fixture-model","name":"Fixture model","maxInputTokens":32000,"maxOutputTokens":4096}]}}`
					case "/v2/chat/completions":
						var payload map[string]any
						if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
							return nil, err
						}
						inferenceCalls.Add(1)
						if payload["model"] != "fixture-model" {
							return nil, fmt.Errorf("unexpected upstream model: %v", payload["model"])
						}
						if operation != "probe" && (payload["max_tokens"] != float64(17) || payload["temperature"] != 0.0 || payload["top_p"] != 0.8 || payload["user"] != "synthetic-user-context") {
							return nil, fmt.Errorf("sampling or output parameters were not preserved")
						}
						if payload["stream"] != true {
							nonStreamingCalls.Add(1)
							status = http.StatusBadRequest
							body = `{"error":{"code":11101,"message":"This upstream accepts streaming inference only"}}`
						} else {
							options, _ := payload["stream_options"].(map[string]any)
							if options["include_usage"] != true {
								return nil, fmt.Errorf("stream usage was not requested")
							}
							contentType = "text/event-stream"
							if operation == "chat-cancel" || operation == "chat-idle" {
								return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: &workBuddyBlockedBody{ctx: request.Context(), started: streamStarted, closed: streamClosed}, Request: request}, nil
							}
							body = "data: {\"id\":\"chatcmpl_fixture\",\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
								"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n" +
								"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n" +
								"data: [DONE]\n\n"
							if operation == "chat-tools" {
								body = workBuddyToolStreamFixture
							} else if operation == "chat-error" {
								body = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n" +
									"event: error\ndata: {\"error\":{\"code\":401,\"type\":\"authentication_error\",\"message\":\"Rejected Bearer synthetic-workbuddy-token\"}}\n\n"
							}
						}
					default:
						return nil, fmt.Errorf("unexpected upstream path: %s", request.URL.Path)
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})}
				pool := newSubscriptionWorkerPool(client)
				defer pool.Close()
				auth, err := json.Marshal(map[string]any{"type": "oauth", "access": "synthetic-workbuddy-token", "uid": "synthetic-user", "expires": time.Now().Add(time.Hour).UnixMilli()})
				if err != nil {
					t.Fatal(err)
				}
				adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{providerID, "WorkBuddy"}, Pool: pool,
					RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
						return ProviderResourceCredentials{SessionJSON: string(auth)}, nil
					}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
				if operation == "chat-idle" {
					adapter.StreamIdleTimeout = 100 * time.Millisecond
				}
				provider := Provider{ID: "provider-fixture", Type: adapter.Definition.providerType(), Options: map[string]string{"resource_id": "account-fixture"}}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if operation == "chat-cancel" {
					go func() {
						select {
						case <-streamStarted:
							cancel()
						case <-ctx.Done():
						}
					}()
				}
				temperature, topP := 0.0, 0.8
				request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Reply with OK."}}, MaxTokens: 17, Temperature: &temperature, TopP: &topP, User: "synthetic-user-context"}
				if operation == "chat-tools" {
					request.Tools = []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}}}}
				}
				var usage Usage
				started := time.Now()
				switch operation {
				case "probe":
					var result ProviderProbeResult
					result, err = adapter.Probe(ctx, provider, ProviderResource{ID: "account-fixture", ResourceType: adapter.Definition.resourceType()}, ProviderProbeRequest{Model: "fixture-model"})
					usage = result.Usage
					if err == nil && result.OutputText != "Hello" {
						t.Fatalf("probe lost assembled output: %q", result.OutputText)
					}
				case "chat", "chat-tools", "chat-error", "chat-cancel", "chat-idle":
					var result any
					result, usage, err = adapter.Chat(ctx, provider, "fixture-model", request)
					if strings.HasPrefix(operation, "chat-") && operation != "chat-tools" {
						if err == nil || result != nil {
							t.Fatalf("failed or cancelled upstream returned a successful completion: %#v", result)
						}
						if strings.Contains(err.Error(), "synthetic-workbuddy-token") {
							t.Fatal("collected stream error exposed the account token")
						}
						if operation == "chat-cancel" || operation == "chat-idle" {
							if time.Since(started) >= 3*time.Second || operation == "chat-idle" && ctx.Err() != nil {
								t.Fatalf("blocked upstream did not stop promptly: %v", err)
							}
							select {
							case <-streamClosed:
							case <-time.After(time.Second):
								t.Fatal("cancelled stream retained its upstream body")
							}
							return
						}
						if usage.PromptTokens != 2 || usage.CompletionTokens != 3 || usage.TotalTokens != 5 {
							t.Fatalf("error discarded reported usage: %#v", usage)
						}
						return
					}
					if err == nil {
						body, ok := result.(map[string]any)
						if !ok {
							t.Fatalf("chat lost assembled output: %#v", result)
						}
						if operation == "chat-tools" {
							assertWorkBuddyCollectedTools(t, body)
						} else if choiceText(body) != "Hello" {
							t.Fatalf("chat lost assembled output: %#v", result)
						}
					}
				case "stream":
					var output bytes.Buffer
					usage, err = adapter.ChatStream(ctx, provider, "fixture-model", request, &output)
					if err == nil && (!strings.Contains(output.String(), "[DONE]") || !strings.Contains(output.String(), `"content":"Hel"`) || !strings.Contains(output.String(), `"content":"lo"`)) {
						t.Fatalf("stream lost output or termination: %s", output.String())
					}
				}
				if err != nil {
					t.Fatalf("%s failed (non-streaming upstream calls: %d): %v", operation, nonStreamingCalls.Load(), err)
				}
				if inferenceCalls.Load() != 1 || nonStreamingCalls.Load() != 0 {
					t.Fatalf("expected exactly one streaming upstream request; inference=%d non-streaming=%d", inferenceCalls.Load(), nonStreamingCalls.Load())
				}
				if usage.PromptTokens != 2 || usage.CompletionTokens != 3 || usage.TotalTokens != 5 {
					t.Fatalf("lost upstream usage: %#v", usage)
				}
			})
		}
	}
}

type workBuddyBlockedBody struct {
	ctx     context.Context
	started chan struct{}
	closed  chan struct{}
	read    sync.Once
	close   sync.Once
}

func (b *workBuddyBlockedBody) Read([]byte) (int, error) {
	b.read.Do(func() { close(b.started) })
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.closed:
		return 0, io.ErrClosedPipe
	}
}

func (b *workBuddyBlockedBody) Close() error {
	b.close.Do(func() { close(b.closed) })
	return nil
}

const workBuddyToolStreamFixture = `data: {"id":"chatcmpl_fixture","model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":"inspect ","tool_calls":[{"index":0,"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"reasoning_content":"input","tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}

data: [DONE]

`

func assertWorkBuddyCollectedTools(t *testing.T, body map[string]any) {
	t.Helper()
	choices, ok := body["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("missing collected choice: %#v", body)
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	tools, _ := message["tool_calls"].([]any)
	if choice["finish_reason"] != "tool_calls" || message["role"] != "assistant" || message["reasoning_content"] != "inspect input" || len(tools) != 1 {
		t.Fatalf("lost collected tool choice or reasoning: %#v", choice)
	}
	tool, _ := tools[0].(map[string]any)
	function, _ := tool["function"].(map[string]any)
	if tool["id"] != "call_lookup" || tool["type"] != "function" || function["name"] != "lookup" || function["arguments"] != `{"q":"x"}` {
		t.Fatalf("lost streamed tool argument fragments: %#v", tool)
	}
	if message["content"] != nil {
		t.Fatalf("tool-only completion must retain null content: %#v", message)
	}
}
