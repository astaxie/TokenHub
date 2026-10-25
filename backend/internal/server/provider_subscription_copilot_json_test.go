package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCopilotSubscriptionRejectsEmbeddedJSONErrors(t *testing.T) {
	for _, endpoint := range []string{"/chat/completions", "/v1/messages", "/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"exchanged-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/models":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "model", "supported_endpoints": []string{endpoint}}}})
				default:
					_, _ = fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"exchanged-secret github-secret"},"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
				}
			})
			body, usage, err := adapter.Chat(context.Background(), provider, "model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}})
			encoded, _ := json.Marshal(body)
			if err == nil {
				t.Errorf("embedded error was accepted as a successful completion")
			}
			if usage.PromptTokens != 4 || usage.CompletionTokens != 2 || usage.TotalTokens != 6 {
				t.Errorf("embedded error lost reported usage: %+v", usage)
			}
			if err != nil && (providerErrorDisposition(err) != ProviderErrorTransientSame || AsHTTPError(err).Status != http.StatusTooManyRequests) {
				t.Errorf("embedded rate limit error was not classified as a retryable rate limit: %v", err)
			}
			for _, secret := range []string{"exchanged-secret", "github-secret"} {
				if strings.Contains(string(encoded), secret) || err != nil && strings.Contains(err.Error(), secret) {
					t.Errorf("embedded error exposed account credentials")
				}
			}
			if _, err := adapter.Probe(context.Background(), provider, ProviderResource{ID: "account"}, ProviderProbeRequest{Model: "model"}); err == nil {
				t.Error("probe accepted an embedded inference error")
			}
		})
	}
}

func TestCopilotSubscriptionRejectsEmbeddedModelErrors(t *testing.T) {
	for _, method := range []string{"models", "probe"} {
		t.Run(method, func(t *testing.T) {
			var inference atomic.Int32
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"exchanged-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/models":
					_, _ = fmt.Fprint(w, `{"error":{"message":"exchanged-secret github-secret"}}`)
				default:
					inference.Add(1)
					_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"unexpected"}}]}`)
				}
			})
			var err error
			if method == "models" {
				_, _, err = adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
			} else {
				_, err = adapter.Probe(context.Background(), provider, ProviderResource{ID: "account"}, ProviderProbeRequest{Model: "model"})
			}
			if err == nil || strings.Contains(err.Error(), "secret") || inference.Load() != 0 {
				t.Fatalf("model discovery did not safely reject the error: inference=%d err=%v", inference.Load(), err)
			}
			if len(adapter.modelRoutes) != 0 {
				t.Fatal("model error populated the route cache")
			}
		})
	}
}

func TestCopilotSubscriptionJSONPreservesNormalModelOutput(t *testing.T) {
	const normal = "{\n \"choices\": [{\"message\":{\"content\":\"github-secret exchanged-secret\"}}], \"error\": null\n}"
	response := &http.Response{Body: io.NopCloser(strings.NewReader(normal))}
	if err := checkCopilotSubscriptionJSON(response, Provider{}, "github-secret", "exchanged-secret"); err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || !bytes.Equal(data, []byte(normal)) {
		t.Fatalf("ordinary model JSON was altered: %q, %v", data, err)
	}
}

func TestCopilotSubscriptionRejectsEmbeddedCLIIdentityErrors(t *testing.T) {
	adapter, _ := copilotSubscriptionFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"error":{"type":"authentication_error","message":"github-secret"}}`)
	})
	session, err := adapter.session(context.Background(), ProviderResourceCredentials{AccessToken: "github-secret", TokenType: "copilot_cli"}, false)
	if err == nil || strings.Contains(err.Error(), "github-secret") || session.Token != "" || len(adapter.sessions) != 0 {
		t.Fatalf("CLI identity error was cached as an authorized session: tokenPresent=%v err=%v", session.Token != "", err)
	}
}

func TestCopilotSubscriptionRejectsJSONErrorsFromStreamRequests(t *testing.T) {
	for _, endpoint := range []string{"/chat/completions", "/v1/messages", "/responses"} {
		for _, contentType := range []string{"application/json; charset=utf-8", "application/problem+json"} {
			t.Run(endpoint+"/"+contentType, func(t *testing.T) {
				adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						_, _ = fmt.Fprintf(w, `{"token":"exchanged-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					case "/models":
						_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "model", "supported_endpoints": []string{endpoint}}}})
					default:
						w.Header().Set("Content-Type", contentType)
						_, _ = fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"exchanged-secret github-secret"},"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
					}
				})
				var output bytes.Buffer
				usage, err := adapter.ChatStream(context.Background(), provider, "model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}, &output)
				if err == nil || output.Len() != 0 || usage.PromptTokens != 4 || usage.CompletionTokens != 2 || usage.TotalTokens != 6 {
					t.Errorf("stream JSON error was not rejected before output with usage retained: bytes=%d usage=%+v err=%v", output.Len(), usage, err)
				}
				if err != nil && (strings.Contains(err.Error(), "secret") || AsHTTPError(err).Status != http.StatusTooManyRequests) {
					t.Errorf("stream JSON error was not safely classified: %v", err)
				}
				if endpoint == "/responses" {
					opened, openErr := adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
					if opened != nil {
						_ = opened.Body.Close()
					}
					openUsage := subscriptionJSONFailureUsage(openErr)
					if openErr == nil || opened != nil || openUsage.TotalTokens != 6 || strings.Contains(openErr.Error(), "secret") {
						t.Errorf("public Responses opening lost JSON failure or usage: responsePresent=%v usage=%+v err=%v", opened != nil, openUsage, openErr)
					}
				}
			})
		}
	}
}
