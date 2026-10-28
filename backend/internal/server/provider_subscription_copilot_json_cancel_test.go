package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestCopilotSubscriptionJSONReadRetainsCancellationAndIdleTimeout(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(fmt.Sprintf("idle_%t", idle), func(t *testing.T) {
			started := make(chan struct{})
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = fmt.Fprintf(w, `{"token":"session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{`)
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
			})
			if idle {
				adapter.StreamIdleTimeout = 20 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := adapter.OpenResponses(ctx, provider, "model", ResponsesRequest{Input: "hello"}, nil)
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("JSON request did not start")
			}
			if !idle {
				cancel()
			}
			select {
			case err := <-result:
				if idle {
					if failure := AsHTTPError(err); failure == nil || failure.Status != http.StatusGatewayTimeout || failure.Code != "subscription_stream_idle" {
						t.Fatalf("JSON read lost the stream idle timeout: %v", err)
					}
				} else if !errors.Is(err, context.Canceled) {
					t.Fatalf("JSON read lost caller cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("JSON read did not stop")
			}
		})
	}
}
