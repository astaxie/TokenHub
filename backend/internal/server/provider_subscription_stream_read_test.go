package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// HTTP/2 GOAWAY debug data is supplied by the upstream and appears in Go's
// body-read error. It is not safe to publish even after SSE frames are redacted.
func subscriptionFailingStreamClient(data string) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		body := &failingReader{data: []byte(data), err: http2.GoAwayError{LastStreamID: 1, ErrCode: http2.ErrCodeInternal, DebugData: "private-access-token session-secret transport-secret"}}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(body)}, nil
	})}
}

func assertSubscriptionSafeReadFailure(t *testing.T, usage Usage, wantTokens int64, err error, output string) {
	t.Helper()
	if err == nil || AsHTTPError(err).Status != http.StatusBadGateway || AsHTTPError(err).Code != "subscription_stream_failed" || usage.TotalTokens != wantTokens {
		t.Fatalf("read failure lost its safe status or usage: usage=%+v error=%v", usage, err)
	}
	for _, secret := range []string{"private-access-token", "session-secret", "transport-secret"} {
		if strings.Contains(output+fmt.Sprint(err)+AsHTTPError(err).Message, secret) {
			t.Fatal("stream read failure exposed upstream debug data")
		}
	}
}

func TestSubscriptionPublicResponsesReadFailureHidesTransportDetails(t *testing.T) {
	const stream = "data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n"
	for _, kind := range []string{ProviderChatGPTSubscription, ProviderGitHubCopilotSubscription} {
		t.Run(kind, func(t *testing.T) {
			var opened *http.Response
			var err error
			var provider Provider
			if kind == ProviderChatGPTSubscription {
				adapter, fixtureProvider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
				provider = fixtureProvider
				adapter.StreamClient = subscriptionFailingStreamClient(stream)
				opened, err = adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
			} else {
				adapter, fixtureProvider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{"token":"session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				})
				provider = fixtureProvider
				adapter.StreamClient = subscriptionFailingStreamClient(stream)
				opened, err = adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Body.Close()
			var output bytes.Buffer
			_, _, usage, err := consumeRoutedResponsesStream(CallContext{}, provider, opened.Body, &output)
			assertSubscriptionSafeReadFailure(t, usage, 6, err, output.String())
		})
	}
}

func TestCopilotSubscriptionChatReadFailureHidesTransportDetails(t *testing.T) {
	for endpoint, stream := range map[string]string{
		"/chat/completions": "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\n",
		"/responses":        "data: {\"type\":\"response.created\",\"response\":{\"id\":\"response-1\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n",
		"/v1/messages":      "data: {\"type\":\"message_start\",\"message\":{\"id\":\"message-1\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n",
	} {
		t.Run(endpoint, func(t *testing.T) {
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = fmt.Fprintf(w, `{"token":"session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				} else {
					_, _ = fmt.Fprintf(w, `{"data":[{"id":"model","supported_endpoints":[%q]}]}`, endpoint)
				}
			})
			adapter.StreamClient = subscriptionFailingStreamClient(stream)
			var output bytes.Buffer
			usage, err := adapter.ChatStream(context.Background(), provider, "model", ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}, &output)
			wantTokens := int64(6)
			if endpoint == "/responses" {
				// Responses reports billable usage at its terminal event.
				wantTokens = 0
			}
			assertSubscriptionSafeReadFailure(t, usage, wantTokens, err, output.String())
		})
	}
}

func TestGoogleSubscriptionReadFailureHidesTransportDetails(t *testing.T) {
	const stream = "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"totalTokenCount\":6}}}\n\n"
	for _, kind := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/streaming_%t", kind, streaming), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !googleSubscriptionTestProject(w, r) {
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer upstream.Close()
				adapter := googleSubscriptionTestAdapter(upstream, kind)
				adapter.StreamClient = subscriptionFailingStreamClient(stream)
				provider := Provider{Type: kind, APIKey: "private-access-token"}
				request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
				var output bytes.Buffer
				var usage Usage
				var err error
				if streaming {
					usage, err = adapter.ChatStream(context.Background(), provider, "gemini-test", request, &output)
				} else {
					_, usage, err = adapter.Chat(context.Background(), provider, "gemini-test", request)
				}
				assertSubscriptionSafeReadFailure(t, usage, 6, err, output.String())
			})
		}
	}
}

func TestSubscriptionStreamReadPreservesTypedFailures(t *testing.T) {
	idle := NewHTTPError(http.StatusGatewayTimeout, "subscription_stream_idle", "Subscription stream was idle for too long")
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"canceled", context.Canceled, context.Canceled},
		{"wrapped cancellation", fmt.Errorf("transport-secret: %w", context.Canceled), context.Canceled},
		{"deadline", context.DeadlineExceeded, context.DeadlineExceeded},
		{"idle", idle, idle},
		{"end of stream", io.EOF, io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &subscriptionStreamReadCloser{ReadCloser: io.NopCloser(&failingReader{data: []byte("partial"), err: tc.err})}
			defer body.Close()
			data, err := io.ReadAll(body)
			want := tc.want
			if want == io.EOF {
				want = nil
			}
			if err != want || string(data) != "partial" {
				t.Fatalf("body boundary changed a typed failure or consumed bytes: data=%q err=%v", data, err)
			}
		})
	}
}
