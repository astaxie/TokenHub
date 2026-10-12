package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMediaProxyFailureDoesNotPenalizeProvider(t *testing.T) {
	for _, stage := range []string{"connect", "config", "auth", "timeout"} {
		t.Run(stage, func(t *testing.T) {
			server, store, key := classificationGateway(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			calls := 0
			failure := newProviderProxyTransportError(stage, errors.New("fixture proxy failure"))
			adapter := OpenAICompatibleAdapter{Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, failure
			})}}
			server.adapterRegistry.Register(ProviderOpenAICompatible, adapter, AdapterCapabilityMedia)
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "classified-model", "input": "fixture"}, key)
			want := AsHTTPError(failure)
			if response.Code != want.Status || !strings.Contains(response.Body, want.Code) || calls != 1 {
				t.Fatalf("proxy failure changed or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			for _, resource := range []string{"rsrc_classified_0", "rsrc_classified_1"} {
				if failures := resourceFailureCount(t, store, resource); failures != 0 {
					t.Errorf("shared egress failure penalized %s: failures=%d", resource, failures)
				}
			}
		})
	}
}

func TestMediaReadFailureRetainsCompleteUsage(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		wantTokens              int64
	}{
		{"complete JSON", "application/json", `{"text":"fixture","usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`, 10},
		{"incomplete JSON", "application/json", `{"text":"fixture","usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}`, 0},
		{"complete SSE", "text/event-stream", "event: transcript.text.done\ndata: {\"usage\":{\"input_tokens\":3,\"output_tokens\":7,\"total_tokens\":10}}\n\n", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Content-Length", "1024")
				w.Header().Set("X-Request-ID", "fixture-upstream-request")
				_, _ = io.WriteString(w, tc.body)
			}, "audio")
			store.AddRoute(ModelRoute{ID: "fallback-media-read", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "Unable to read the complete media response") || calls != 1 {
				t.Fatalf("read failure changed or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			var tokens int64
			for _, record := range store.ListUsageRecords() {
				tokens += record.TotalTokens
			}
			if tokens != tc.wantTokens {
				t.Errorf("read failure usage = %d, want %d", tokens, tc.wantTokens)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 || attempts[0].TotalTokens != tc.wantTokens || attempts[0].ServedModel != "vendor-media" || attempts[0].UpstreamRequestID != "fixture-upstream-request" || attempts[0].Transport != "http_media" {
				t.Fatalf("read failure lost attempt usage or metadata: %+v", attempts)
			}
		})
	}
}

func TestMediaCancellationTakesPrecedenceOverEgressFailure(t *testing.T) {
	err := &ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorEgress}
	classified := uncertainMediaSubmission(err)
	if providerErrorDisposition(classified) != ProviderErrorClient || providerAttemptOutcome(classified) != AttemptNeutral || shouldFailoverRoutedError(classified, false) {
		t.Fatalf("media cancellation classification changed: %v", classified)
	}
}
