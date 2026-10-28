package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoogleSubscriptionRedirectsFailWithoutReadingOrFollowing(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
			for _, body := range []string{"", "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"synthetic-private\"}]},\"finishReason\":\"STOP\"}]}}\n\n"} {
				t.Run(fmt.Sprintf("%s/%d/body-%t", providerType, status, body != ""), func(t *testing.T) {
					closed := make(chan struct{})
					var followed atomic.Int32
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/redirect" {
							followed.Add(1)
							w.WriteHeader(http.StatusNoContent)
							return
						}
						w.Header().Set("Location", "/redirect")
						w.Header().Set("Content-Type", "text/event-stream")
						w.Header().Set("Retry-After", "90")
						w.Header().Set("X-Private-Token", "synthetic-private")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, body)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						close(closed)
					}))
					defer upstream.Close()
					adapter := googleSubscriptionTestAdapter(upstream, providerType)
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					response, err := adapter.request(ctx, upstream.URL, "streamGenerateContent?alt=sse", ProviderResourceCredentials{AccessToken: "synthetic-access"}, map[string]any{}, true)
					if response != nil {
						_ = response.Body.Close()
						t.Fatal("Redirect was accepted as a successful inference response")
					}
					failure := AsHTTPError(err)
					if err == nil || ctx.Err() != nil || failure.Status != http.StatusBadGateway || failure.UpstreamStatus != status ||
						providerErrorDisposition(err) != ProviderErrorTransientSame || providerErrorRetryAfter(err) != 90*time.Second {
						t.Fatalf("Redirect waited for its body or lost routing metadata: %v", err)
					}
					if followed.Load() != 0 || strings.Contains(err.Error(), "synthetic-") {
						t.Fatal("Redirect forwarded account credentials or exposed private diagnostics")
					}
					select {
					case <-closed:
					case <-time.After(time.Second):
						t.Fatal("Rejected redirect left its upstream body open")
					}
				})
			}
		}
	}
}
