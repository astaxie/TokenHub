package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func invokeCopilotAutoForTest(ctx context.Context, adapter *CopilotSubscriptionAdapter, provider Provider, operation string) error {
	request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
	switch operation {
	case "chat":
		_, _, err := adapter.Chat(ctx, provider, "auto", request)
		return err
	case "stream":
		_, err := adapter.ChatStream(ctx, provider, "auto", request, io.Discard)
		return err
	case "responses":
		_, _, err := adapter.Responses(ctx, provider, "auto", ResponsesRequest{Input: "Hello"})
		return err
	default:
		response, err := adapter.OpenResponses(ctx, provider, "auto", ResponsesRequest{Input: "Hello"}, nil)
		if response != nil {
			_ = response.Body.Close()
		}
		return err
	}
}

func TestCopilotSubscriptionAutoRetainsChooserFailures(t *testing.T) {
	for _, operation := range []string{"chat", "stream", "responses", "responses-stream"} {
		for _, statuses := range [][2]int{{429, 429}, {429, 404}, {503, 404}, {404, 503}, {402, 404}} {
			t.Run(fmt.Sprintf("%s/%d/%d", operation, statuses[0], statuses[1]), func(t *testing.T) {
				var inferred atomic.Int32
				adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					case "/models":
						_, _ = fmt.Fprint(w, `{"data":[]}`)
					case "/auto", "/models/session":
						status := statuses[0]
						if r.URL.Path == "/models/session" {
							status = statuses[1]
						}
						w.Header().Set("Retry-After", "7")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, "private diagnostic github-secret")
					default:
						inferred.Add(1)
						w.WriteHeader(500)
					}
				})
				err := invokeCopilotAutoForTest(context.Background(), adapter, provider, operation)
				want := statuses[0]
				if statuses[1] != 404 {
					want = statuses[1]
				}
				failure := AsHTTPError(err)
				if err == nil || failure.UpstreamStatus != want || failure.Status != classifyProviderStatus(want).status || providerErrorDisposition(err) != classifyProviderStatus(want).disposition || providerErrorRetryAfter(err) != 7*time.Second {
					t.Fatalf("Chooser failure lost its routing metadata: error=%v disposition=%s delay=%v", err, providerErrorDisposition(err), providerErrorRetryAfter(err))
				}
				if inferred.Load() != 0 || strings.Contains(err.Error(), "private diagnostic") || strings.Contains(err.Error(), "github-secret") {
					t.Fatal("Failed selection invoked inference or exposed private diagnostics")
				}
			})
		}
	}
}

func TestCopilotSubscriptionAutoFallbackSurvivesChooserOutage(t *testing.T) {
	for _, fallback := range []string{"legacy", "manual"} {
		t.Run(fallback, func(t *testing.T) {
			var inferred atomic.Int32
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/models":
					if fallback == "manual" {
						_, _ = fmt.Fprint(w, `{"data":[{"id":"fallback-model"}]}`)
					} else {
						_, _ = fmt.Fprint(w, `{"data":[]}`)
					}
				case "/auto":
					w.WriteHeader(503)
				case "/models/session":
					if fallback == "manual" {
						w.WriteHeader(404)
					} else {
						_, _ = fmt.Fprint(w, `{"session_token":"legacy-session","selected_model":"fallback-model"}`)
					}
				case "/chat/completions":
					inferred.Add(1)
					_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
				default:
					t.Errorf("Unexpected endpoint: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			if err := invokeCopilotAutoForTest(context.Background(), adapter, provider, "chat"); err != nil || inferred.Load() != 1 {
				t.Fatalf("Available fallback failed: calls=%d error=%v", inferred.Load(), err)
			}
		})
	}
}

func TestCopilotSubscriptionAutoPreservesEgressFailures(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, path := range []string{"/auto", "/models/session"} {
			t.Run(fmt.Sprintf("manual_%t/%s", manual, path), func(t *testing.T) {
				var failed atomic.Int32
				adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					case "/models":
						if manual {
							_, _ = fmt.Fprint(w, `{"data":[{"id":"fallback-model"}]}`)
						} else {
							_, _ = fmt.Fprint(w, `{"data":[]}`)
						}
					case "/auto":
						w.WriteHeader(404)
					default:
						t.Errorf("Egress failure reached fallback: %s", r.URL.Path)
						w.WriteHeader(500)
					}
				})
				transport := adapter.Client.Transport
				failure := newProviderProxyTransportError("auth", errors.New("private proxy credential"))
				adapter.Client.Transport = subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == path {
						failed.Add(1)
						return nil, failure
					}
					return transport.RoundTrip(request)
				})
				err := invokeCopilotAutoForTest(context.Background(), adapter, provider, "chat")
				if !errors.Is(err, failure) || failed.Load() != 1 || providerErrorDisposition(err) != ProviderErrorEgress || providerAttemptOutcome(err) != AttemptNeutral || shouldFailoverRoutedError(err, false) {
					t.Fatalf("Egress failure was lost or penalized account: %v", err)
				}
			})
		}
	}
}

func TestCopilotSubscriptionAutoPreservesNetworkFailureWithoutFallback(t *testing.T) {
	adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "/models":
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		default:
			w.WriteHeader(404)
		}
	})
	transport := adapter.Client.Transport
	adapter.Client.Transport = subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/auto" {
			return nil, errors.New("private network error")
		}
		return transport.RoundTrip(request)
	})
	err := invokeCopilotAutoForTest(context.Background(), adapter, provider, "chat")
	if err == nil || AsHTTPError(err).Code != "subscription_request_failed" || AsHTTPError(err).Status != 502 || strings.Contains(err.Error(), "private network error") {
		t.Fatalf("Network failure became account unavailability: %v", err)
	}
}

func TestCopilotSubscriptionAutoCancellationStopsFallback(t *testing.T) {
	entered := make(chan struct{})
	adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"fallback-model"}]}`)
		case "/auto":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(entered)
			<-r.Context().Done()
		default:
			t.Errorf("Canceled selection reached fallback: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- invokeCopilotAutoForTest(ctx, adapter, provider, "chat") }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Chooser did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Cancellation changed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Canceled chooser did not finish")
	}
}
