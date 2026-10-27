package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoogleSubscriptionHTTPFailureDoesNotWaitForUnusedBody(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			closed := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "90")
				w.Header().Set("X-Private-Token", "private-header-secret")
				w.WriteHeader(status)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			t.Cleanup(upstream.Close)
			adapter := googleSubscriptionTestAdapter(upstream, providerAntigravitySubscription)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := adapter.request(ctx, upstream.URL, "streamGenerateContent?alt=sse", ProviderResourceCredentials{AccessToken: "private-access-token"}, map[string]any{}, true)
			if ctx.Err() != nil {
				t.Fatal("known HTTP failure waited for the unfinished error body")
			}
			failure := AsHTTPError(err)
			class := classifyProviderStatus(status)
			if failure.Status != class.status || failure.UpstreamStatus != status || providerErrorDisposition(err) != class.disposition || providerErrorRetryAfter(err) != 90*time.Second {
				t.Fatalf("HTTP failure lost routing or retry metadata: %+v", failure)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatal("HTTP failure exposed private diagnostics")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("failed upstream request was left open")
			}
		})
	}
}

func TestGoogleSubscriptionForbiddenInspectionIsBounded(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			closed := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "90")
				w.WriteHeader(http.StatusForbidden)
				body := googleSubscriptionLocationFixture
				if !complete {
					body = body[:len(body)-1]
				}
				_, _ = fmt.Fprint(w, body)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			t.Cleanup(upstream.Close)
			adapter := googleSubscriptionTestAdapter(upstream, providerGeminiCLISubscription)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			start := time.Now()
			_, err := adapter.request(ctx, upstream.URL, "streamGenerateContent?alt=sse", ProviderResourceCredentials{AccessToken: "private-access-token"}, map[string]any{}, true)
			if ctx.Err() != nil {
				t.Fatal("403 inspection exceeded its independent deadline")
			}
			if complete {
				assertGoogleSubscriptionLocationError(t, err, http.StatusForbidden)
				if time.Since(start) >= time.Second {
					t.Fatal("complete eligibility diagnostic waited for EOF")
				}
			} else if failure := AsHTTPError(err); failure.UpstreamStatus != http.StatusForbidden || providerErrorDisposition(err) != ProviderErrorAuthBroken || providerErrorRetryAfter(err) != 90*time.Second {
				t.Fatalf("incomplete diagnostic lost HTTP classification: %+v", failure)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("failed upstream request was left open")
			}
		})
	}
}
