package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatGPTSubscriptionJSONErrorDoesNotExposeAccountCredentials(t *testing.T) {
	adapter, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"error":{"type":"authentication_error","message":%q},"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`, r.Header.Get("Authorization"))
	})
	opened, err := adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
	var output bytes.Buffer
	if err == nil {
		defer opened.Body.Close()
		_, _, _, err = consumeRoutedResponsesStream(CallContext{}, provider, opened.Body, &output)
	}
	if strings.Contains(output.String()+fmt.Sprint(err), "direct-token") {
		t.Fatal("JSON error exposed the account credential through the public stream")
	}
	if err == nil || providerErrorDisposition(err) != ProviderErrorAuthBroken {
		t.Fatalf("JSON error lost its authentication classification: %v", err)
	}
	_, usage, err := adapter.Responses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"})
	if err == nil || usage.TotalTokens != 6 {
		t.Fatalf("JSON error lost its reported usage: usage=%+v error=%v", usage, err)
	}
}

func TestChatGPTSubscriptionJSONFailureGatewayFallbackAndAccounting(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", path, stream), func(t *testing.T) {
				var primaryCalls, fallbackCalls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/responses" {
						t.Errorf("unexpected upstream request: %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if strings.Contains(r.Header.Get("Authorization"), "rsrc_classified_0") {
						primaryCalls.Add(1)
						w.Header().Set("Content-Type", "application/json; charset=utf-8")
						_, _ = fmt.Fprintf(w, `{"error":{"type":"rate_limit_error","message":%q},"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`, r.Header.Get("Authorization"))
						return
					}
					fallbackCalls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fallback answer\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-fallback\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"fallback answer\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n")
				}))
				defer upstream.Close()
				server, store, key := classificationGateway(t, chatGPTSubscriptionBaseURL, chatGPTSubscriptionBaseURL, ProviderChatGPTSubscription)
				defer func() { _ = server.Shutdown(context.Background()) }()
				for _, resource := range store.ListProviderResources() {
					resource.ResourceType = ProviderResourceChatGPTSubscription
					resource.Credentials = &ProviderResourceCredentials{AccessToken: resource.ID + "-account-secret"}
					if _, err := store.UpdateProviderResource(resource.ID, resource); err != nil {
						t.Fatal(err)
					}
				}
				registerTestAdapter(server, ProviderChatGPTSubscription, &ChatGPTSubscriptionAdapter{
					Client: upstream.Client(), StreamClient: upstream.Client(), APIBaseURL: upstream.URL,
					RefreshCredentials: store.RefreshProviderResourceCredentials,
				})
				payload := map[string]any{"model": "classified-model", "stream": stream}
				if path == "/v1/responses" {
					payload["input"] = "hello"
				} else {
					payload["messages"] = []map[string]any{{"role": "user", "content": "hello"}}
					payload["max_tokens"] = 20
				}
				response := doJSON(t, server.Handler(), http.MethodPost, path, payload, key)
				if response.Code != http.StatusOK || !strings.Contains(response.Body, "fallback answer") || primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
					t.Fatalf("JSON failure prevented route fallback: status=%d primary=%d fallback=%d body=%s", response.Code, primaryCalls.Load(), fallbackCalls.Load(), response.Body)
				}
				var attempts []RouteAttemptLog
				if err := store.db.Order("attempt_index ASC").Find(&attempts).Error; err != nil {
					t.Fatal(err)
				}
				if len(attempts) != 2 || attempts[0].StatusCode != http.StatusTooManyRequests || attempts[0].UpstreamStatus != http.StatusOK || attempts[0].TotalTokens != 6 || attempts[1].TotalTokens != 4 {
					t.Fatalf("JSON failure lost route classification or usage: %+v", attempts)
				}
				audit, err := json.Marshal(map[string]any{"attempts": attempts, "requests": store.ListRequestLogs()})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(response.Body+string(audit), "account-secret") {
					t.Fatal("JSON failure exposed credentials in the public response or request diagnostics")
				}
			})
		}
	}
}

func TestChatGPTSubscriptionSuccessfulStreamRemainsIncremental(t *testing.T) {
	const first = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"direct-token\"}\n\n"
	release := make(chan struct{})
	defer close(release)
	adapter, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	})
	opened, err := adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	result := make(chan error, 1)
	data := make([]byte, len(first))
	go func() { _, err := io.ReadFull(opened.Body, data); result <- err }()
	select {
	case err := <-result:
		if err != nil || string(data) != first {
			t.Fatalf("normal output changed: %q, %v", data, err)
		}
	case <-time.After(time.Second):
		t.Fatal("successful stream waited for the terminal event")
	}
}

func TestChatGPTSubscriptionJSONReadRetainsCancellationAndIdleTimeout(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		for _, idle := range []bool{false, true} {
			t.Run(fmt.Sprintf("buffered_%t/idle_%t", buffered, idle), func(t *testing.T) {
				started := make(chan struct{})
				adapter, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{`)
					w.(http.Flusher).Flush()
					close(started)
					<-r.Context().Done()
				})
				if idle {
					adapter.StreamIdleTimeout = 20 * time.Millisecond
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				result := make(chan error, 1)
				go func() {
					if buffered {
						_, _, err := adapter.Responses(ctx, provider, "model", ResponsesRequest{Input: "hello"})
						result <- err
					} else {
						_, err := adapter.OpenResponses(ctx, provider, "model", ResponsesRequest{Input: "hello"}, nil)
						result <- err
					}
				}()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("JSON request did not start")
				}
				if !idle {
					cancel()
				}
				select {
				case err := <-result:
					if idle {
						if failure := AsHTTPError(err); failure.Status != http.StatusGatewayTimeout || failure.Code != "subscription_stream_idle" {
							t.Fatalf("JSON read lost the stream idle timeout: %v", err)
						}
					} else if !errors.Is(err, context.Canceled) {
						t.Fatalf("JSON read lost caller cancellation: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("JSON read did not stop")
				}
			})
		}
	}
}
