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

func TestSubscriptionNativeResponsesRejectNonSSEErrorBodies(t *testing.T) {
	for _, providerType := range []string{ProviderChatGPTSubscription, ProviderGitHubCopilotSubscription} {
		for _, contentType := range []string{"", "text/plain", "text/event-stream"} {
			for _, body := range []string{
				`{"error":{"message":"direct-token github-secret session-secret","type":"rate_limit_error"}}`,
				`{"error":{"message":"direct-token github-secret session-secret`,
				"<html>direct-token github-secret session-secret</html>",
				"Authentication failed: direct-token github-secret session-secret",
			} {
				t.Run(fmt.Sprintf("%s/%s/%c", providerType, contentType, body[0]), func(t *testing.T) {
					handler := func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/token" && providerType == ProviderGitHubCopilotSubscription {
							_, _ = fmt.Fprintf(w, `{"token":"session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
							return
						}
						if contentType != "" {
							w.Header().Set("Content-Type", contentType)
						}
						_, _ = io.WriteString(w, body)
					}
					var opened *http.Response
					var provider Provider
					var err error
					if providerType == ProviderChatGPTSubscription {
						adapter, fixtureProvider := chatGPTSubscriptionFixture(t, handler)
						provider = fixtureProvider
						opened, err = adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
					} else {
						adapter, fixtureProvider := copilotSubscriptionFixture(t, handler)
						provider = fixtureProvider
						opened, err = adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
					}
					var output bytes.Buffer
					if err == nil {
						defer opened.Body.Close()
						_, _, _, err = consumeRoutedResponsesStream(CallContext{}, provider, opened.Body, &output)
					}
					if strings.Contains(output.String()+fmt.Sprint(err), "secret") || strings.Contains(output.String()+fmt.Sprint(err), "direct-token") {
						t.Fatal("non-SSE error body exposed an account credential")
					}
					if err == nil || output.Len() != 0 {
						t.Fatalf("non-SSE body was forwarded before failing: bytes=%d error=%v", output.Len(), err)
					}
				})
			}
		}
	}
}

func TestSubscriptionErrorStreamPreservesStandardFramingAndOutput(t *testing.T) {
	const stream = ": keepalive\r\n\r\nid: event-1\r\nretry: 1000\r\nevent: response.output_text.delta\r\nx-extension: value\r\ndata: {\"type\":\"response.output_text.delta\",\r\ndata: \"delta\":\"account-secret\"}\r\n\r\nid: event-2\n\ndata: [DONE]\n\n"
	reader := newSubscriptionErrorStream(io.NopCloser(strings.NewReader(stream)), func() Provider { return Provider{APIKey: "account-secret"} }, 0)
	defer func() { _ = reader.Close() }()
	output, err := io.ReadAll(reader)
	expected := strings.ReplaceAll(stream, "x-extension: value\r\n", "")
	if err != nil || string(output) != expected {
		t.Fatalf("valid framing or normal output changed: %q, %v", output, err)
	}
}

func TestSubscriptionErrorStreamRejectsUnframedPendingFailure(t *testing.T) {
	failure := errors.New("upstream connection interrupted")
	body := io.NopCloser(&failingReader{data: []byte(`{"error":{"message":"account-secret`), err: failure})
	reader := newSubscriptionErrorStream(body, func() Provider { return Provider{APIKey: "account-secret"} }, 0)
	defer func() { _ = reader.Close() }()
	output, err := io.ReadAll(reader)
	if !errors.Is(err, failure) || len(output) != 0 {
		t.Fatalf("unframed pending failure was forwarded: bytes=%d, %v", len(output), err)
	}
}

func TestSubscriptionErrorStreamIgnoresUnframedBytesAroundKnownFields(t *testing.T) {
	for _, stream := range []string{
		": keepalive\n{\"error\":\"account-secret\"}\n\n",
		"{\"error\":\"account-secret\"}\n: keepalive\n\n",
		"event: error\n{\"error\":\"account-secret\"}\n\n",
		"id: event-1\n{\"error\":\"account-secret\"}\n\n",
	} {
		reader := newSubscriptionErrorStream(io.NopCloser(strings.NewReader(stream)), func() Provider { return Provider{APIKey: "account-secret"} }, 0)
		output, _ := io.ReadAll(reader)
		_ = reader.Close()
		if bytes.Contains(output, []byte("account-secret")) || bytes.Contains(output, []byte("{\"error\"")) {
			t.Fatal("known SSE fields allowed an unframed error body through")
		}
	}
}
