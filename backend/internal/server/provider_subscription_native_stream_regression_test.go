package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCopilotSubscriptionRoutedResponsesStreamSemantics(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stream    string
		wantError bool
		wantUsage int64
	}{
		{
			name:      "failed response usage",
			stream:    "data: {\"type\":\"response.failed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6},\"error\":{\"code\":\"insufficient_quota\",\"message\":\"github-secret session-secret\"}}}\n\n",
			wantError: true,
			wantUsage: 6,
		},
		{
			name:      "missing terminal response",
			stream:    "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\"}}\n\ndata: {\"type\":\"response.completed\"}\n\n",
			wantError: true,
		},
		{
			name:      "multiline terminal event",
			stream:    "event: response.completed\r\ndata: {\"response\":{\"id\":\"response-1\",\r\ndata: \"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\r\n\r\n",
			wantUsage: 6,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/responses":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, tc.stream)
				default:
					t.Errorf("unexpected upstream endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			opened, err := adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Body.Close()
			var output bytes.Buffer
			_, _, usage, err := consumeRoutedResponsesStream(CallContext{}, provider, opened.Body, &output)
			if (err != nil) != tc.wantError || usage.TotalTokens != tc.wantUsage {
				t.Fatalf("stream outcome changed: usage=%+v error=%v", usage, err)
			}
			if !tc.wantError && output.String() != tc.stream {
				t.Fatal("valid SSE framing changed")
			}
			for _, secret := range []string{"github-secret", "session-secret"} {
				if strings.Contains(output.String()+fmt.Sprint(err), secret) {
					t.Fatal("stream failure exposed account credentials")
				}
			}
		})
	}
}

func TestChatGPTSubscriptionBufferedFailureRetainsDisposition(t *testing.T) {
	for _, tc := range []struct {
		name        string
		code        string
		disposition ProviderErrorDisposition
		failover    bool
	}{
		{"policy", "content_policy_violation", ProviderErrorPolicy, false},
		{"quota", "insufficient_quota", ProviderErrorQuotaExhausted, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/responses" {
					t.Errorf("unexpected upstream endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.failed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6},\"error\":{\"code\":%q,\"message\":\"direct-token\"}}}\n\n", tc.code)
			})
			_, usage, err := adapter.Responses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"})
			if err == nil || providerErrorDisposition(err) != tc.disposition || usage.TotalTokens != 6 {
				t.Fatalf("buffered failure lost classification or usage: disposition=%q usage=%+v error=%v", providerErrorDisposition(err), usage, err)
			}
			if shouldFailoverRoutedError(err, false) != tc.failover {
				t.Fatal("buffered failure changed route fallback behavior")
			}
			if strings.Contains(err.Error(), "direct-token") {
				t.Fatal("buffered failure exposed account credentials")
			}
		})
	}
}

func TestChatGPTSubscriptionBufferedReadFailureHidesTransportDetails(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.openai.com" || r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected upstream endpoint: %s", r.URL)
		}
		body := &failingReader{
			data: []byte("data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n"),
			err:  errors.New("stream read failed: direct-token transport-secret"),
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(body)}, nil
	})}
	adapter := &ChatGPTSubscriptionAdapter{Client: client, StreamClient: client,
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{AccessToken: "direct-token"}, nil
		}}
	provider := Provider{Type: ProviderChatGPTSubscription, Options: map[string]string{"resource_id": "account"}}
	_, usage, err := adapter.Responses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"})
	if err == nil || AsHTTPError(err).Status != http.StatusBadGateway || AsHTTPError(err).Code != "subscription_stream_failed" || usage.TotalTokens != 6 {
		t.Fatalf("read failure lost safe status or usage: usage=%+v error=%v", usage, err)
	}
	for _, secret := range []string{"direct-token", "transport-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("read failure exposed transport details")
		}
	}
}
