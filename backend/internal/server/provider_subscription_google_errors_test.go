package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const googleSubscriptionLocationFixture = `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"private-access-token private-account@example.invalid","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"UNSUPPORTED_LOCATION","domain":"cloudaicompanion.googleapis.com","metadata":{"private":"private-metadata"}}]}}`

func assertGoogleSubscriptionLocationError(t *testing.T, err error, upstreamStatus int) {
	t.Helper()
	failure := AsHTTPError(err)
	if failure == nil || failure.Status != http.StatusBadGateway || failure.Code != "google_subscription_location_unsupported" || failure.UpstreamStatus != upstreamStatus {
		t.Fatalf("unexpected location error: %+v", failure)
	}
	if providerErrorDisposition(err) != ProviderErrorResourceBroken || providerAttemptOutcome(err) != AttemptFailed || !shouldFailoverRoutedError(err, true) {
		t.Fatalf("incorrect location failure disposition: %v", err)
	}
	if !strings.Contains(failure.Message, "location") || strings.Contains(err.Error(), "private-") {
		t.Fatalf("location diagnostic is missing or exposes upstream data: %v", err)
	}
}

func TestGoogleSubscriptionLocationRestrictionReachesProbeAndStream(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		for _, stage := range []string{"bootstrap", "inference"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", providerType, stage, stream), func(t *testing.T) {
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if stage == "inference" && googleSubscriptionTestProject(w, r) {
							return
						}
						w.WriteHeader(http.StatusForbidden)
						_, _ = fmt.Fprint(w, googleSubscriptionLocationFixture)
					}))
					t.Cleanup(upstream.Close)
					adapter := googleSubscriptionTestAdapter(upstream, providerType)
					provider := Provider{Type: providerType, APIKey: "private-access-token"}
					var err error
					var output bytes.Buffer
					if stream {
						_, err = adapter.ChatStream(context.Background(), provider, "gemini-test", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}, &output)
					} else {
						_, err = adapter.Probe(context.Background(), provider, ProviderResource{ID: "test-account", APIKey: "private-access-token"}, ProviderProbeRequest{Model: "gemini-test"})
					}
					assertGoogleSubscriptionLocationError(t, err, http.StatusForbidden)
					if output.Len() != 0 {
						t.Fatal("HTTP failure committed a stream")
					}
				})
			}
		}
	}
}

func TestGoogleSubscriptionLocationClassificationRequiresStructuredReason(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, googleSubscriptionLocationFixture},
		{"generic_forbidden", 403, `{"error":{"message":"UNSUPPORTED_LOCATION private-access-token"}}`},
		{"wrong_reason", 403, strings.ReplaceAll(googleSubscriptionLocationFixture, "UNSUPPORTED_LOCATION", "ACCESS_DENIED")},
		{"wrong_domain", 403, strings.ReplaceAll(googleSubscriptionLocationFixture, "cloudaicompanion.googleapis.com", "other.googleapis.com")},
		{"wrong_type", 403, strings.ReplaceAll(googleSubscriptionLocationFixture, "google.rpc.ErrorInfo", "google.rpc.OtherInfo")},
		{"malformed", 403, googleSubscriptionLocationFixture[:len(googleSubscriptionLocationFixture)-1]},
		{"oversized", 403, strings.ReplaceAll(googleSubscriptionLocationFixture, "private-metadata", strings.Repeat("x", providerErrorBodyPrefix))},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(scenario.status)
				_, _ = fmt.Fprint(w, scenario.body)
			}))
			t.Cleanup(upstream.Close)
			adapter := googleSubscriptionTestAdapter(upstream, providerAntigravitySubscription)
			_, err := adapter.request(context.Background(), upstream.URL, "loadCodeAssist", ProviderResourceCredentials{AccessToken: "private-access-token"}, map[string]any{}, false)
			failure := AsHTTPError(err)
			if failure == nil || failure.Code != "provider_auth_error" || failure.UpstreamStatus != scenario.status || providerErrorDisposition(err) != ProviderErrorAuthBroken {
				t.Fatalf("unrecognized rejection changed classification: %v", err)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatal("unrecognized rejection exposed upstream data")
			}
		})
	}
}

func TestGoogleSubscriptionLocationErrorFrameRetainsUsage(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("nested=%t/stream=%t", nested, stream), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if googleSubscriptionTestProject(w, r) {
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":7}}}\n\n")
					body := googleSubscriptionLocationFixture[:len(googleSubscriptionLocationFixture)-1] + `,"usageMetadata":{"candidatesTokenCount":3,"totalTokenCount":10}}`
					if nested {
						body = `{"response":` + body + `}`
					}
					_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
				}))
				t.Cleanup(upstream.Close)
				adapter := googleSubscriptionTestAdapter(upstream, providerAntigravitySubscription)
				provider := Provider{APIKey: "private-access-token"}
				request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
				var usage Usage
				var err error
				var output bytes.Buffer
				if stream {
					usage, err = adapter.ChatStream(context.Background(), provider, "gemini-test", request, &output)
				} else {
					_, usage, err = adapter.Chat(context.Background(), provider, "gemini-test", request)
				}
				assertGoogleSubscriptionLocationError(t, err, http.StatusOK)
				if usage.PromptTokens != 7 || usage.CompletionTokens != 3 || usage.TotalTokens != 10 {
					t.Fatalf("location error lost cumulative usage: %+v", usage)
				}
				if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "[DONE]") {
					t.Fatal("failed stream exposed upstream data or reported completion")
				}
			})
		}
	}
}
