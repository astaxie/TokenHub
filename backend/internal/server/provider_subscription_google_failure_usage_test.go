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

func TestGoogleSubscriptionErrorFrameRetainsUsageAndDisposition(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("nested=%t/stream=%t", nested, stream), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if googleSubscriptionTestProject(w, r) {
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":7}}}\n\n")
					body := `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"rejected private-access-token"},"usageMetadata":{"candidatesTokenCount":3,"totalTokenCount":10}}`
					if nested {
						body = `{"response":` + body + `}`
					}
					_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
				}))
				defer upstream.Close()
				adapter := googleSubscriptionTestAdapter(upstream, providerGeminiCLISubscription)
				provider := Provider{APIKey: "private-access-token", Options: map[string]string{"organization_id": "project"}}
				request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
				var usage Usage
				var err error
				var output bytes.Buffer
				if stream {
					usage, err = adapter.ChatStream(context.Background(), provider, "gemini-test", request, &output)
				} else {
					_, usage, err = adapter.Chat(context.Background(), provider, "gemini-test", request)
				}
				if usage.PromptTokens != 7 || usage.CompletionTokens != 3 || usage.TotalTokens != 10 {
					t.Errorf("terminal error lost cumulative usage: %+v", usage)
				}
				if providerErrorDisposition(err) != ProviderErrorTransientSame || AsHTTPError(err).Status != http.StatusTooManyRequests {
					t.Errorf("quota error lost retry disposition: %v (disposition=%s)", err, providerErrorDisposition(err))
				}
				if err != nil && strings.Contains(err.Error(), "private-access-token") || strings.Contains(output.String(), "private-access-token") || strings.Contains(output.String(), "[DONE]") {
					t.Errorf("failed stream exposed a secret or reported completion: %v %s", err, output.String())
				}
			})
		}
	}
}
