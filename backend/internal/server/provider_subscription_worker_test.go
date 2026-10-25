package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type subscriptionTestInput struct{ write func([]byte) (int, error) }

func (s subscriptionTestInput) Write(data []byte) (int, error) { return s.write(data) }
func (subscriptionTestInput) Close() error                     { return nil }

func fakeSubscriptionProcess(auth string, handle func(map[string]any) []subscriptionWorkerEvent) *subscriptionWorkerProcess {
	ctx, cancel := context.WithCancel(context.Background())
	p := &subscriptionWorkerProcess{ctx: ctx, cancel: cancel, pending: map[string]chan subscriptionWorkerEvent{}, networks: map[string]context.CancelFunc{}, callGate: make(chan struct{}, 1), done: make(chan struct{}), auth: auth, lastUsed: time.Now()}
	p.input = subscriptionTestInput{write: func(data []byte) (int, error) {
		var command map[string]any
		if err := json.Unmarshal(data, &command); err != nil {
			return 0, err
		}
		go func() {
			id, _ := command["id"].(string)
			for _, event := range handle(command) {
				event.ID = id
				p.mu.Lock()
				ch := p.pending[id]
				p.mu.Unlock()
				if ch != nil {
					select {
					case ch <- event:
					case <-p.ctx.Done():
						return
					}
				}
			}
		}()
		return len(data), nil
	}}
	go func() { <-ctx.Done(); p.mu.Lock(); p.closed = true; p.mu.Unlock(); close(p.done) }()
	return p
}

func TestSubscriptionWorkerConvertsEveryProtocol(t *testing.T) {
	for _, protocol := range []string{"chat", "anthropic", "gemini", "responses"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/collected"
			if stream {
				name = protocol + "/streamed"
			}
			t.Run(name, func(t *testing.T) {
				pool := newSubscriptionWorkerPool(&http.Client{})
				defer pool.Close()
				var fetched map[string]any
				pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
					return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
						switch command["operation"] {
						case "models":
							return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"test-model","name":"Test Model","protocol":"` + protocol + `","api":{"id":"upstream-model"},"limit":{"context":32000,"output":4096}}]}`)}}
						case "fetch":
							fetched = command
							data := subscriptionProtocolFixture(protocol, stream)
							contentType := "application/json"
							if stream || protocol == "responses" {
								contentType = "text/event-stream"
							}
							return []subscriptionWorkerEvent{{Event: "response", Status: 200, Headers: http.Header{"Content-Type": []string{contentType}}}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(data))}, {Event: "end"}}
						}
						return nil
					}), nil
				}
				a := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, Pool: pool, RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
					return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"test-secret"}`}, nil
				}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
				provider := Provider{Type: a.Definition.providerType(), Options: map[string]string{"resource_id": "account-1"}}
				request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}, MaxTokens: 17}
				var result any
				var usage Usage
				var err error
				if stream {
					var buffer bytes.Buffer
					usage, err = a.ChatStream(context.Background(), provider, "test-model", request, &buffer)
					result = buffer.String()
				} else {
					result, usage, err = a.Chat(context.Background(), provider, "test-model", request)
				}
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(result)
				if !bytes.Contains(encoded, []byte("Hello")) {
					t.Fatalf("Missing translated output: %s", encoded)
				}
				if usage.PromptTokens != 2 || usage.CompletionTokens != 3 {
					t.Fatalf("Usage was not preserved: %#v", usage)
				}
				if fetched["protocol"] != protocol {
					t.Fatalf("Wrong protocol: %#v", fetched)
				}
				body := fetched["body"].(map[string]any)
				if protocol != "gemini" && body["model"] != "test-model" {
					t.Fatalf("Wrong requested model: %#v", body)
				}
				if protocol == "responses" && body["max_output_tokens"] != float64(request.MaxTokens) {
					t.Fatalf("Responses request lost the caller's output limit: %#v", body)
				}
				if stream && !bytes.Contains(encoded, []byte("[DONE]")) {
					t.Fatalf("Stream missing completion marker: %s", encoded)
				}
			})
		}
	}
}

func subscriptionProtocolFixture(protocol string, stream bool) string {
	switch protocol {
	case "chat":
		if stream {
			return "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n"
		}
		return `{"choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
	case "anthropic":
		if stream {
			return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		}
		return `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`
	case "gemini":
		body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`
		if stream {
			return "data: " + body + "\n\n"
		}
		return body
	case "responses":
		return "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n"
	}
	return ""
}

func TestSubscriptionWorkerPoolIsBoundedAndDoesNotEvictActiveAccounts(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(map[string]any) []subscriptionWorkerEvent { return nil }), nil
	}
	var releases []func()
	for range subscriptionWorkerPoolLimit {
		_, release, err := pool.acquire(context.Background(), NewID("account"), "{}", nil)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, _, err := pool.acquire(context.Background(), "overflow", "{}", nil); err == nil {
		t.Fatal("Expected bounded pool rejection")
	}
	releases[0]()
	process, release, err := pool.acquire(context.Background(), "replacement", "{}", nil)
	if err != nil {
		t.Fatal(err)
	}
	release()
	pool.evict(time.Now().Add(subscriptionWorkerIdleLifetime + time.Second))
	if process.ctx.Err() == nil {
		t.Fatal("Idle process was not evicted")
	}
	for _, release := range releases {
		release()
	}
}

func TestSubscriptionWorkerQueuedCancellationDoesNotStopActiveRequest(t *testing.T) {
	process := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent { return nil })
	defer process.cancel()
	process.callGate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := process.invoke(ctx, map[string]any{"operation": "models"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Expected cancellation, got %v", err)
	}
	if process.ctx.Err() != nil {
		t.Fatal("Queued request cancellation stopped the active process")
	}
	<-process.callGate
}

func TestSubscriptionWorkerRotationAcknowledgesOnlyDurableCredentials(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{true: "failed", false: "saved"}[fails], func(t *testing.T) {
			var ack map[string]any
			process := fakeSubscriptionProcess(`{"type":"oauth","access":"old"}`, func(map[string]any) []subscriptionWorkerEvent { return nil })
			defer process.cancel()
			saved := false
			process.saveAuth = func(_ context.Context, previous, next string) error {
				if !strings.Contains(previous, "old") || !strings.Contains(next, "new") {
					t.Fatal("Rotation lost compare-and-swap values")
				}
				if fails {
					return errors.New("secret database diagnostic")
				}
				saved = true
				return nil
			}
			process.input = subscriptionTestInput{write: func(data []byte) (int, error) {
				if !fails && !saved {
					t.Fatal("Acknowledged before persistence")
				}
				err := json.Unmarshal(data, &ack)
				return len(data), err
			}}
			process.updateAuth(subscriptionWorkerEvent{UpdateID: "update", Auth: json.RawMessage(`{"type":"oauth","access":"new"}`)})
			if fails {
				if ack["error"] != "credential_persistence_failed" || strings.Contains(process.authSnapshot(), "new") {
					t.Fatalf("Failed rotation accepted: %#v", ack)
				}
			} else if ack["error"] != nil || !strings.Contains(process.authSnapshot(), "new") {
				t.Fatalf("Saved rotation rejected: %#v", ack)
			}
		})
	}
}

func TestSubscriptionWorkerRedactsUpstreamErrorBody(t *testing.T) {
	p := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent {
		return []subscriptionWorkerEvent{{Event: "response", Status: 401, Headers: http.Header{"Authorization": []string{"secret"}}}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte("access_token=secret"))}, {Event: "end"}}
	})
	defer p.cancel()
	a := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
	transport := subscriptionWorkerTransport{adapter: a, process: p, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}}
	request, _ := http.NewRequest(http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret")) || response.Header.Get("Authorization") != "" {
		t.Fatalf("Leaked upstream credentials: %s", data)
	}
}

func TestSubscriptionWorkerDescribesEveryBundledProvider(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	process, err := startSubscriptionWorker(&http.Client{Transport: subscriptionWorkerDenyTransport{}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer process.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, definition := range subscriptionWorkerDefinitions() {
		result, err := process.invoke(ctx, map[string]any{"operation": "describe", "provider": definition.ID, "resource_id": "test"}, nil)
		if err != nil {
			t.Fatalf("Provider %s: %v", definition.ID, err)
		}
		if !bytes.Contains(result, []byte(`"provider":"`+definition.ID+`"`)) {
			t.Fatalf("Provider %s missing metadata: %s", definition.ID, result)
		}
		var described struct {
			Capabilities struct {
				Usage   bool `json:"usage"`
				Checkin bool `json:"checkin"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(result, &described); err != nil {
			t.Fatal(err)
		}
		if described.Capabilities.Usage != subscriptionWorkerHasQuota(definition.ID) || described.Capabilities.Checkin != subscriptionWorkerHasCheckin(definition.ID) {
			t.Fatalf("Provider %s management registration does not match its bundled hooks: %+v", definition.ID, described.Capabilities)
		}
	}
}

type subscriptionWorkerDenyTransport struct{}

func (subscriptionWorkerDenyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("External network disabled in test")
}

func TestSubscriptionWorkerRelayKeepsRepeatedHeadersAndRefusesRedirects(t *testing.T) {
	var mu sync.Mutex
	var messages []map[string]any
	p := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent { return nil })
	defer p.cancel()
	p.input = subscriptionTestInput{write: func(data []byte) (int, error) {
		var message map[string]any
		if err := json.Unmarshal(data, &message); err != nil {
			return 0, err
		}
		mu.Lock()
		messages = append(messages, message)
		mu.Unlock()
		return len(data), nil
	}}
	calls := 0
	p.client = subscriptionClient(&http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		if len(request.Header.Values("X-Test")) != 2 {
			t.Fatal("Dropped repeated request headers")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://untrusted.example"}, "Set-Cookie": []string{"a=1", "b=2"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: request}, nil
	})})
	p.network(subscriptionWorkerEvent{RequestID: "relay", URL: "https://api.cursor.com/test", Method: "GET", Headers: http.Header{"X-Test": []string{"first", "second"}}})
	if calls != 1 {
		t.Fatalf("Followed redirect: %d requests", calls)
	}
	if len(messages) < 1 {
		t.Fatal("Missing network result")
	}
	headers := messages[0]["headers"].(map[string]any)
	if len(headers["Set-Cookie"].([]any)) != 2 {
		t.Fatalf("Dropped response cookies: %#v", headers)
	}
}

type subscriptionWorkerRoundTripper func(*http.Request) (*http.Response, error)

func (f subscriptionWorkerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
