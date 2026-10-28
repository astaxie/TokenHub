package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestFactoryStandardOnlyExpiredAccountAllowsFallback(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "collected"
		if stream {
			name = "streamed"
		}
		t.Run(name, func(t *testing.T) {
			pool := newSubscriptionWorkerPool(&http.Client{})
			defer pool.Close()
			pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					if command["operation"] != "usage" {
						t.Errorf("expired Standard allowance allowed another operation: %s", command["operation"])
					}
					return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"signIn":"expired","error":"synthetic-secret"}`)}}
				}), nil
			}
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"factory", "Factory"}, Pool: pool,
				RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
					return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-access"}`}, nil
				}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
			provider := Provider{Options: map[string]string{"resource_id": "account", "standard_only": "true"}}
			var err error
			if stream {
				_, err = adapter.ChatStream(context.Background(), provider, "glm-5.3", ChatCompletionRequest{}, io.Discard)
			} else {
				_, _, err = adapter.Chat(context.Background(), provider, "glm-5.3", ChatCompletionRequest{})
			}
			if err == nil || providerErrorDisposition(err) != ProviderErrorAuthBroken || providerAttemptOutcome(err) != AttemptFailed {
				t.Fatalf("expired account was not classified as broken authorization: error=%v disposition=%s", err, providerErrorDisposition(err))
			}
			for _, bound := range []bool{false, true} {
				if !shouldFailoverRoutedError(err, bound) {
					t.Fatalf("expired account prevented fallback: bound=%t error=%v", bound, err)
				}
			}
		})
	}
}
