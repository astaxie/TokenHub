package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGoogleSubscriptionMetadataEgressStopsFallbackAndRecovers(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		for _, operation := range []string{"chat", "stream", "models"} {
			for _, stage := range []string{"auth", "timeout"} {
				t.Run(providerType+"/"+operation+"/"+stage, func(t *testing.T) {
					failure := newProviderProxyTransportError(stage, errors.New("private proxy credential"))
					failing := true
					metadataCalls, inferenceCalls := 0, 0
					metadataPath := "/v1internal:listExperiments"
					if providerType == providerAntigravitySubscription {
						metadataPath = "/v1internal:loadCodeAssist"
					}
					client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(req *http.Request) (*http.Response, error) {
						if req.URL.Path == metadataPath {
							metadataCalls++
							if failing {
								return nil, failure
							}
						}
						status, body := http.StatusOK, ""
						switch req.URL.Path {
						case "/v1internal:loadCodeAssist":
							body = `{"cloudaicompanionProject":"explicit-project","currentTier":{"id":"standard-tier"}}`
						case "/v1internal:listExperiments":
							body = `{"flags":[{"flagId":45842815,"boolValue":true}]}`
						case "/v1internal:retrieveUserQuota":
							body = `{"buckets":[]}`
						case "/v1internal:fetchAvailableModels":
							body = `{"models":{"gemini-3.8-flash":{}}}`
						case "/v1internal:streamGenerateContent":
							inferenceCalls++
							if failing {
								status = http.StatusUnauthorized
								break
							}
							var payload struct {
								Model string `json:"model"`
							}
							if json.NewDecoder(req.Body).Decode(&payload) != nil {
								t.Fatal("Invalid inference request")
							}
							if providerType == providerGeminiCLISubscription && payload.Model != "gemini-3.8-flash" {
								t.Errorf("Recovered experiments did not map the model: %s", payload.Model)
							}
							body = "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}}\n\n"
						default:
							t.Errorf("Unexpected endpoint: %s", req.URL.Path)
							status = http.StatusNotFound
						}
						return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
					})}
					adapter := &GoogleSubscriptionAdapter{ProviderType: providerType, Client: client, StreamClient: client, accountState: &googleSubscriptionAccountState{}}
					credentials := ProviderResourceCredentials{AccessToken: "synthetic-access", OrganizationID: "explicit-project"}
					provider := Provider{Type: providerType, APIKey: credentials.AccessToken, Options: map[string]string{"organization_id": credentials.OrganizationID}}
					invoke := func() error {
						request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
						switch operation {
						case "chat":
							_, _, err := adapter.Chat(context.Background(), provider, "gemini-3.5-flash", request)
							return err
						case "stream":
							_, err := adapter.ChatStream(context.Background(), provider, "gemini-3.5-flash", request, io.Discard)
							return err
						default:
							catalog, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ResourceType: adapter.resourceType(), Credentials: &credentials}, "")
							if err == nil && !failing {
								found := false
								for _, model := range catalog.Models {
									found = found || model.ID == "gemini-3.8-flash"
								}
								if !found {
									t.Error("Recovered catalog omitted the account model")
								}
							}
							return err
						}
					}
					err := invoke()
					if !errors.Is(err, failure) || providerErrorDisposition(err) != ProviderErrorEgress || providerAttemptOutcome(err) != AttemptNeutral || shouldFailoverRoutedError(err, false) {
						t.Fatalf("Metadata egress failure was lost or penalized the account: %v", err)
					}
					if metadataCalls != 1 || inferenceCalls != 0 || len(adapter.accountState.flags) != 0 || providerType == providerAntigravitySubscription && len(adapter.accountState.projects) != 0 {
						t.Fatalf("Egress failure reached inference or cached fallback: metadata=%d inference=%d projects=%d flags=%d", metadataCalls, inferenceCalls, len(adapter.accountState.projects), len(adapter.accountState.flags))
					}
					if AsHTTPError(err).Code != AsHTTPError(failure).Code || AsHTTPError(err).Status != AsHTTPError(failure).Status || strings.Contains(err.Error(), "private proxy credential") {
						t.Fatal("Egress diagnostics lost status/code or exposed private details")
					}
					failing = false
					for range 2 {
						if err := invoke(); err != nil {
							t.Fatalf("Account did not recover after egress repair: %v", err)
						}
					}
					if metadataCalls != 2 {
						t.Fatalf("Metadata was not retried once and cached after recovery: %d", metadataCalls)
					}
				})
			}
		}
	}
}

func TestAntigravityExplicitProjectStillSupportsUpstreamFallback(t *testing.T) {
	adapter := &GoogleSubscriptionAdapter{ProviderType: providerAntigravitySubscription, accountState: &googleSubscriptionAccountState{}}
	calls := 0
	adapter.Client = &http.Client{Transport: subscriptionWorkerRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	for range 2 {
		project, err := adapter.project(context.Background(), Provider{}, ProviderResourceCredentials{AccessToken: "synthetic-access", OrganizationID: "explicit-project"})
		if err != nil || project.ID != "explicit-project" {
			t.Fatalf("Ordinary upstream failure lost explicit project fallback: %+v %v", project, err)
		}
	}
	if calls != 1 {
		t.Fatalf("Successful explicit project fallback was not cached: %d", calls)
	}
}
