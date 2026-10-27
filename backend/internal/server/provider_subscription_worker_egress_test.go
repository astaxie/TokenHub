package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionWorkerPreservesGuardedEgressFailures(t *testing.T) {
	for _, stage := range []string{"config", "connect", "auth", "timeout"} {
		for _, operation := range []string{"fetch", "refresh"} {
			t.Run(stage+"/"+operation, func(t *testing.T) {
				failure := newProviderProxyTransportError(stage, errors.New("private-proxy-details"))
				client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(*http.Request) (*http.Response, error) {
					return nil, failure
				})}
				process, err := startSubscriptionWorker(client, `{"type":"oauth","access":"synthetic-access","refresh":"synthetic-refresh","expires":9999999999999}`, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { process.cancel(); <-process.done })
				_, err = process.invoke(context.Background(), map[string]any{
					"operation": operation, "provider": "grok", "resource_id": "synthetic-account",
					"model": "grok-4.7", "protocol": "responses", "body": map[string]any{"input": "Hello"},
				}, nil)
				if err == nil || providerErrorDisposition(err) != ProviderErrorEgress {
					t.Fatalf("guarded egress failure was replaced: error=%v disposition=%s", err, providerErrorDisposition(err))
				}
				if providerAttemptOutcome(err) != AttemptNeutral || shouldFailoverRoutedError(err, false) {
					t.Fatal("shared egress failure penalized or failed over the account")
				}
				if AsHTTPError(err).Status != AsHTTPError(failure).Status || AsHTTPError(err).Code != AsHTTPError(failure).Code || strings.Contains(err.Error(), "private-proxy-details") {
					t.Fatal("egress diagnostics lost their status/code or exposed private details")
				}
			})
		}
	}
}

func TestSubscriptionWorkerRetainsEgressFailureCaughtByAdapter(t *testing.T) {
	for _, stage := range []string{"config", "connect", "auth", "timeout"} {
		for _, stream := range []bool{false, true} {
			name := stage + "/collected"
			if stream {
				name = stage + "/streamed"
			}
			t.Run(name, func(t *testing.T) {
				failure := newProviderProxyTransportError(stage, errors.New("private-proxy-details"))
				var calls atomic.Int32
				adapter, provider := subscriptionVariantAdapter(t, "kiro", subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == "/List-Available-Models" {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"models":[{"modelId":"auto","modelName":"Auto"}]}`)), Request: request}, nil
					}
					if request.URL.Path != "/generateAssistantResponse" {
						t.Errorf("unexpected upstream request: %s", request.URL)
					}
					if calls.Add(1) == 1 {
						return nil, failure
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(kiroProbeEventFrame(t, "assistantResponseEvent", map[string]any{"content": "Recovered"}))), Request: request}, nil
				}))
				ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), subscriptionCallerKey{}, "fixture-caller"), 10*time.Second)
				defer cancel()
				call := func() error {
					request := ChatCompletionRequest{Model: "auto", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
					if stream {
						_, err := adapter.ChatStream(ctx, provider, "auto", request, &bytes.Buffer{})
						return err
					}
					_, _, err := adapter.Chat(ctx, provider, "auto", request)
					return err
				}
				err := call()
				if err == nil || providerErrorDisposition(err) != ProviderErrorEgress || providerAttemptOutcome(err) != AttemptNeutral || shouldFailoverRoutedError(err, false) {
					t.Fatalf("adapter converted a shared egress failure into an account failure: %v", err)
				}
				if AsHTTPError(err).Code != AsHTTPError(failure).Code || strings.Contains(err.Error(), "private-proxy-details") {
					t.Fatalf("egress code or redaction was lost: %v", err)
				}
				if err := call(); err != nil {
					t.Fatalf("a previous command's egress failure prevented recovery: %v", err)
				}
				if calls.Load() != 2 {
					t.Fatalf("unexpected inference request count: %d", calls.Load())
				}
			})
		}
	}
}
