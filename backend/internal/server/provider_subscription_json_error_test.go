package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSubscriptionJSONErrorClassification(t *testing.T) {
	for _, test := range []struct {
		name, body  string
		status      int
		disposition ProviderErrorDisposition
	}{
		{"authentication", `{"error":{"type":"authentication_error"}}`, 502, ProviderErrorAuthBroken},
		{"rate limit", `{"error":{"type":"rate_limit_error"}}`, 429, ProviderErrorTransientSame},
		{"quota", `{"error":{"code":"insufficient_quota"}}`, 502, ProviderErrorQuotaExhausted},
		{"bad request", `{"error":{"type":"invalid_request_error"}}`, 400, ProviderErrorClient},
		{"Google code", `{"error":{"code":403,"status":"PERMISSION_DENIED"}}`, 502, ProviderErrorAuthBroken},
		{"Google status", `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, 429, ProviderErrorTransientSame},
		{"Responses failure", `{"object":"response","status":"failed","error":{"code":"invalid_api_key"}}`, 502, ProviderErrorAuthBroken},
		{"nested Responses failure", `{"response":{"status":"failed","error":{"type":"overloaded_error"}}}`, 503, ProviderErrorTransientSame},
		{"unknown error", `{"error":{"message":"unavailable"}}`, 502, ProviderErrorTransientSame},
		{"failed without detail", `{"object":"response","status":"failed"}`, 502, ProviderErrorTransientSame},
		{"null error", `{"error":null,"choices":[]}`, 0, ""},
		{"ordinary output", `{"choices":[{"message":{"content":"error","error":"user data"}}]}`, 0, ""},
		{"unrelated status", `{"status":"complete","code":401}`, 0, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := subscriptionJSONError([]byte(test.body), Provider{})
			if test.status == 0 {
				if err != nil {
					t.Fatalf("Ordinary output was rejected: %v", err)
				}
				return
			}
			if err == nil || AsHTTPError(err).Status != test.status || providerErrorDisposition(err) != test.disposition {
				t.Fatalf("Unexpected classification: %v, %s", err, providerErrorDisposition(err))
			}
			if AsHTTPError(err).UpstreamStatus != http.StatusOK {
				t.Fatal("Attempt diagnostics lost the actual HTTP 200 status")
			}
		})
	}
}

func TestSubscriptionJSONErrorPreservesUsageAndBoundsDiagnostics(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"rate_limit_error","message":"synthetic-secret"},"usage":{"prompt_tokens":9,"completion_tokens":3}}`,
		`{"error":{"type":"rate_limit_error"},"usage":{"input_tokens":7,"cache_read_input_tokens":2,"output_tokens":3}}`,
		`{"error":{"code":429},"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":3}}`,
		`{"response":{"status":"failed","error":{"code":"rate_limit_exceeded"},"usage":{"input_tokens":9,"output_tokens":3}}}`,
		`{"response":{"error":{"code":"rate_limit_exceeded"}},"usage":{"input_tokens":9,"output_tokens":3}}`,
	} {
		usage, err := subscriptionJSONError([]byte(body), Provider{APIKey: "synthetic-secret"})
		retained := subscriptionJSONFailureUsage(err)
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") || usage.PromptTokens != 9 || usage.CompletionTokens != 3 || retained.PromptTokens != 9 || retained.CompletionTokens != 3 {
			t.Fatalf("Embedded error did not retain safe usage: %+v, %v", usage, err)
		}
	}
	data, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "rate_limit_error", "message": strings.Repeat("synthetic-secret", 1000)}, "usage": map[string]int{"prompt_tokens": 9}})
	usage, err := subscriptionJSONError(data, Provider{APIKey: "synthetic-secret"})
	if err == nil || len(err.Error()) > providerErrorBodyPrefix || strings.Contains(err.Error(), "synthetic-secret") || usage.PromptTokens != 9 {
		t.Fatal("Large embedded error lost usage or exceeded the diagnostic limit")
	}
	data, _ = json.Marshal(map[string]any{"error": map[string]any{"message": strings.Repeat("x", 1000)}})
	_, err = subscriptionJSONError(data, Provider{APIKey: "x"})
	if err == nil || len(err.Error()) > providerErrorBodyPrefix {
		t.Fatal("Credential replacement expanded diagnostics beyond their limit")
	}
}
