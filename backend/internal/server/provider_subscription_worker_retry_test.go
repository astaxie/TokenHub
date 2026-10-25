package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionWorkerPreservesUpstreamRetryDelay(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic", "gemini"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/collected"
			if stream {
				name = protocol + "/streamed"
			}
			t.Run(name, func(t *testing.T) {
				pool := newSubscriptionWorkerPool(&http.Client{})
				t.Cleanup(pool.Close)
				pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
					return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
						if command["operation"] == "models" {
							return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"model","protocol":"` + protocol + `"}]}`)}}
						}
						return []subscriptionWorkerEvent{
							{Event: "response", Status: http.StatusTooManyRequests, Headers: http.Header{"retry-after": {"90"}, "x-private-token": {"synthetic-secret"}}},
							{Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(`{"error":{"message":"synthetic-secret"}}`))},
							{Event: "end"},
						}
					}), nil
				}
				adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, Pool: pool,
					RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
						return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-secret"}`}, nil
					}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
				provider := Provider{Options: map[string]string{"resource_id": "account"}}
				request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
				var err error
				if stream {
					_, err = adapter.ChatStream(context.Background(), provider, "model", request, io.Discard)
				} else {
					_, _, err = adapter.Chat(context.Background(), provider, "model", request)
				}
				var failure *ProviderInvocationError
				if !errors.As(err, &failure) || failure.RetryAfter != 90*time.Second {
					t.Fatalf("upstream retry delay was lost: %+v", err)
				}
				if AsHTTPError(err).Status != http.StatusTooManyRequests || strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatalf("rate limit classification or error redaction changed: %v", err)
				}
			})
		}
	}
}

func TestSubscriptionWorkerSanitizesRetryHeaders(t *testing.T) {
	for _, retry := range []string{"synthetic-secret", time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)} {
		t.Run(retry, func(t *testing.T) {
			process := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent {
				return []subscriptionWorkerEvent{{Event: "response", Status: http.StatusTooManyRequests, Headers: http.Header{"retry-after": {retry}, "x-private-token": {"synthetic-secret"}}}, {Event: "end"}}
			})
			t.Cleanup(process.cancel)
			transport := subscriptionWorkerTransport{adapter: &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}, process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}}
			request, _ := http.NewRequest(http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if _, err := io.Copy(io.Discard, response.Body); err != nil {
				t.Fatal(err)
			}
			if response.Header.Get("X-Private-Token") != "" || strings.Contains(response.Header.Get("Retry-After"), "synthetic-secret") {
				t.Fatal("unsanitized upstream error headers were retained")
			}
			delay := parseRetryAfter(response.Header.Get("Retry-After"))
			if retry == "synthetic-secret" && delay != 0 || retry != "synthetic-secret" && (delay < 88*time.Second || delay > 90*time.Second) {
				t.Fatalf("invalid retry header normalization: %v", delay)
			}
		})
	}
}
