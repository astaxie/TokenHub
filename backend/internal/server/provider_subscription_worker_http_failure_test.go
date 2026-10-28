package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionWorkerHTTPFailureDoesNotWaitForDiscardedBody(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("status-%d/stream-%t", status, stream), func(t *testing.T) {
				process := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent {
					// Failure headers are final, but the upstream body never ends.
					return []subscriptionWorkerEvent{{Event: "response", Status: status, Headers: http.Header{
						"content-type": {"application/json"}, "retry-after": {"90"}, "x-private": {"synthetic-private"},
					}}}
				})
				defer process.cancel()
				transport := &subscriptionWorkerTransport{adapter: &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}},
					process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}, stream: stream}
				client := &http.Client{Transport: transport}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
				started := time.Now()
				response, err := sendUpstream(client, client, time.Second, request, stream)
				if response != nil {
					err = checkProviderResponse(response)
				}
				if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
					t.Errorf("Failure waited for a discarded body: %s", elapsed)
				}
				class := classifyProviderStatus(status)
				if err == nil || AsHTTPError(err).Status != class.status || AsHTTPError(err).UpstreamStatus != status || providerErrorDisposition(err) != class.disposition {
					t.Fatalf("Failure lost HTTP classification: %v", err)
				}
				if providerErrorRetryAfter(err) != 90*time.Second || strings.Contains(err.Error(), "synthetic-private") {
					t.Fatalf("Failure lost retry metadata or leaked private headers: %v", err)
				}
				select {
				case <-process.done:
				case <-time.After(time.Second):
					t.Fatal("Discarded error response left its worker running")
				}
			})
		}
	}
}
