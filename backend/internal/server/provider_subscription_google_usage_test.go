package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoogleSubscriptionPreservesUsageWhenInferenceStreamFails(t *testing.T) {
	for _, ending := range []string{
		`{"error":{"code":429,"message":"quota exhausted"}}`,
		`{"response":{"error":{"message":"generation failed"}}}`,
		`{"response":{"promptFeedback":{"blockReason":"SAFETY"}}}`,
		`{"unexpected":"envelope"}`,
		`invalid-json`,
	} {
		t.Run(ending, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"totalTokenCount\":6}}}\n\n")
				_, _ = fmt.Fprintf(w, "data: %s\n\n", ending)
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
			provider := Provider{APIKey: "test-access", Options: map[string]string{"organization_id": "project"}}
			request := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
			_, usage, err := adapter.Chat(context.Background(), provider, "gemini-test", request)
			if err == nil || usage.PromptTokens != 4 || usage.CompletionTokens != 2 || usage.TotalTokens != 6 {
				t.Errorf("non-stream failure lost reported usage: %+v, %v", usage, err)
			}
			var output bytes.Buffer
			streamUsage, streamErr := adapter.ChatStream(context.Background(), provider, "gemini-test", request, &output)
			if streamErr == nil || streamUsage.PromptTokens != 4 || streamUsage.CompletionTokens != 2 || streamUsage.TotalTokens != 6 {
				t.Errorf("stream failure lost reported usage: %+v, %v", streamUsage, streamErr)
			}
		})
	}
}
