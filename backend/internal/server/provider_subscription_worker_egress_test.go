package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
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
