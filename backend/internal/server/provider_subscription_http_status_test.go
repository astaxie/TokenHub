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

func TestNativeSubscriptionRejectsStatusWithoutWaitingForErrorBody(t *testing.T) {
	for _, copilot := range []bool{false, true} {
		for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("copilot_%t/status_%d", copilot, status), func(t *testing.T) {
				closed := make(chan struct{})
				open := nativeSubscriptionStatusFixture(t, copilot, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Retry-After", "7")
					w.WriteHeader(status)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					close(closed)
				})
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				started := time.Now()
				response, err := open(ctx)
				if response != nil {
					_ = response.Body.Close()
					t.Fatal("Failed status returned a response")
				}
				if time.Since(started) > 500*time.Millisecond || err == nil || AsHTTPError(err).UpstreamStatus != status {
					t.Fatalf("Status classification waited for the discarded body: elapsed=%v error=%v", time.Since(started), err)
				}
				var failure *ProviderInvocationError
				if !errors.As(err, &failure) {
					t.Fatalf("Failure lost its routing classification: %v", err)
				}
				disposition := ProviderErrorTransientSame
				if status >= http.StatusBadRequest {
					disposition = classifyProviderStatus(status).disposition
				}
				if failure.Disposition != disposition || failure.RetryAfter != 7*time.Second {
					t.Fatalf("Failure lost its status or retry delay: %+v", failure)
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("Discarded error body left its upstream request running")
				}
			})
		}
	}
}

func TestNativeSubscriptionUnauthorizedBodyDoesNotBlockCredentialRetry(t *testing.T) {
	for _, copilot := range []bool{false, true} {
		t.Run(fmt.Sprintf("copilot_%t", copilot), func(t *testing.T) {
			var calls atomic.Int32
			closed := make(chan struct{})
			open := nativeSubscriptionStatusFixture(t, copilot, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					close(closed)
					return
				}
				if !strings.Contains(r.Header.Get("Authorization"), "refreshed-token") {
					t.Error("Retry did not use refreshed credentials")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{\"total_tokens\":6}}}\n\n")
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			started := time.Now()
			response, err := open(ctx)
			if err != nil {
				t.Fatalf("Unauthorized body blocked credential retry: %v", err)
			}
			defer response.Body.Close()
			_, _, usage, err := consumeSubscriptionResponsesStream(response.Body, nil)
			if err != nil || usage.TotalTokens != 6 || calls.Load() != 2 || time.Since(started) > 500*time.Millisecond {
				t.Fatalf("Credential retry changed: calls=%d usage=%+v elapsed=%v error=%v", calls.Load(), usage, time.Since(started), err)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("Retried request did not cancel its previous error body")
			}
		})
	}
}

// Both native adapters use the same status-only HTTP failure boundary. Keep
// actual HTTP bodies here so closing them must cancel the server-side request.
func nativeSubscriptionStatusFixture(t *testing.T, copilot bool, inference http.HandlerFunc) func(context.Context) (*http.Response, error) {
	t.Helper()
	if !copilot {
		adapter, provider := chatGPTSubscriptionFixture(t, inference)
		adapter.StreamIdleTimeout = 20 * time.Millisecond
		adapter.RefreshCredentials = func(_ context.Context, _ string, force bool) (ProviderResourceCredentials, error) {
			token := "initial-token"
			if force {
				token = "refreshed-token"
			}
			return ProviderResourceCredentials{AccessToken: token}, nil
		}
		return func(ctx context.Context) (*http.Response, error) {
			return adapter.OpenResponses(ctx, provider, "fixture-model", ResponsesRequest{Input: "Hello"}, nil)
		}
	}
	var exchanges atomic.Int32
	adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			inference(w, r)
			return
		}
		token := "initial-token"
		if exchanges.Add(1) > 1 {
			token = "refreshed-token"
		}
		_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%d,"endpoints":{"api":"https://api.githubcopilot.com"}}`, token, time.Now().Add(time.Hour).Unix())
	})
	adapter.StreamIdleTimeout = 20 * time.Millisecond
	return func(ctx context.Context) (*http.Response, error) {
		return adapter.OpenResponses(ctx, provider, "fixture-model", ResponsesRequest{Input: "Hello"}, nil)
	}
}
