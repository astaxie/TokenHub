package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGoogleSubscriptionPreservesGuardedEgressFailures(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		for _, operation := range []string{"bootstrap", "chat", "stream", "refresh", "authorization", "identity"} {
			for _, stage := range []string{"auth", "timeout"} {
				t.Run(providerType+"/"+operation+"/"+stage, func(t *testing.T) {
					failure := newProviderProxyTransportError(stage, errors.New("sensitive-proxy-details"))
					requests := 0
					client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(req *http.Request) (*http.Response, error) {
						requests++
						if operation != "bootstrap" && req.URL.Path == "/v1internal:loadCodeAssist" {
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"cloudaicompanionProject":"project","currentTier":{"id":"standard-tier"}}`)), Request: req}, nil
						}
						return nil, failure
					})}
					adapter := &GoogleSubscriptionAdapter{ProviderType: providerType, Client: client, StreamClient: client}
					credentials := ProviderResourceCredentials{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", ClientID: "synthetic-client", ClientSecret: "synthetic-secret"}
					provider := Provider{Type: providerType, APIKey: credentials.AccessToken}
					request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
					var err error
					switch operation {
					case "bootstrap", "chat":
						_, _, err = adapter.Chat(context.Background(), provider, "gemini-test", request)
					case "stream":
						_, err = adapter.ChatStream(context.Background(), provider, "gemini-test", request, io.Discard)
					case "refresh":
						_, err = adapter.refresh(context.Background(), credentials)
					case "authorization":
						_, err = adapter.ExchangeAuthorization(context.Background(), "synthetic-code", strings.Repeat("v", 43), adapter.DefaultOAuthRedirectURI(), credentials.ClientID, credentials.ClientSecret)
					case "identity":
						_, err = adapter.oauthIdentity(context.Background(), credentials)
					}
					if requests == 0 || !errors.Is(err, failure) || providerErrorDisposition(err) != ProviderErrorEgress {
						t.Fatalf("guarded egress failure was replaced: requests=%d error=%v disposition=%s", requests, err, providerErrorDisposition(err))
					}
					if providerAttemptOutcome(err) != AttemptNeutral || shouldFailoverRoutedError(err, false) {
						t.Fatal("shared egress failure penalized or failed over the provider account")
					}
					if AsHTTPError(err).Status != AsHTTPError(failure).Status || AsHTTPError(err).Code != AsHTTPError(failure).Code || strings.Contains(err.Error(), "sensitive-proxy-details") {
						t.Fatal("egress diagnostics lost their status/code or exposed private details")
					}
				})
			}
		}
	}
}
