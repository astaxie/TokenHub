package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCopilotSubscriptionRedactsExchangedSessionFromStreamErrors(t *testing.T) {
	for _, endpoint := range []string{"/chat/completions", "/v1/messages", "/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"exchanged-session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/models":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "model", "supported_endpoints": []string{endpoint}}}})
				default:
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"message\":\"Bearer exchanged-session-secret github-secret\"}}\n\n")
				}
			})
			provider.APIKey = "github-secret"
			var output bytes.Buffer
			_, err := adapter.ChatStream(context.Background(), provider, "model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}, &output)
			if err == nil {
				t.Fatal("upstream stream error was not reported")
			}
			for _, secret := range []string{"exchanged-session-secret", "github-secret"} {
				if strings.Contains(output.String(), secret) || strings.Contains(err.Error(), secret) {
					t.Errorf("stream exposed %q in output or returned error", secret)
				}
			}
			if endpoint == "/responses" {
				response, err := adapter.OpenResponses(context.Background(), provider, "model", ResponsesRequest{Input: "hello"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"exchanged-session-secret", "github-secret"} {
					if bytes.Contains(data, []byte(secret)) {
						t.Errorf("public Responses stream exposed %q", secret)
					}
				}
			}
		})
	}
}

func TestCopilotSubscriptionStreamPreservesFramesAndBoundsFailures(t *testing.T) {
	const normal = ": keepalive\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"session-secret\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n"
	reader := newCopilotSubscriptionStream(io.NopCloser(strings.NewReader(normal)), Provider{}, "github-secret", "session-secret", 0)
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != normal {
		t.Fatalf("ordinary stream framing was altered: %v", err)
	}
	for _, malformed := range []bool{false, true} {
		frame := "data: {\"error\":{\"message\":\"session\\u002dsecret github-secret\"}}\n"
		if malformed {
			frame = "data: {\"error\":\"session-secret github-secret\n"
		}
		failure := errors.New("upstream connection interrupted")
		reader = newCopilotSubscriptionStream(io.NopCloser(&failingReader{data: []byte(frame), err: failure}), Provider{}, "github-secret", "session-secret", 0)
		data, err = io.ReadAll(reader)
		if !errors.Is(err, failure) || bytes.Contains(data, []byte("secret")) {
			t.Fatalf("incomplete error frame was not safely preserved: %q, %v", data, err)
		}
	}
	for _, frame := range []string{
		`data: {"error":"session-secret github-secret`,
		"data: {\"error\":\"session-secret github-secret\n\n",
	} {
		reader = newCopilotSubscriptionStream(io.NopCloser(strings.NewReader(frame)), Provider{}, "github-secret", "session-secret", 0)
		data, err = io.ReadAll(reader)
		if err == nil || bytes.Contains(data, []byte("secret")) {
			t.Fatalf("malformed error frame at EOF was not safely rejected: %q, %v", data, err)
		}
	}
	reader = newCopilotSubscriptionStream(io.NopCloser(strings.NewReader("data: "+strings.Repeat("x", 65))), Provider{}, "github-secret", "session-secret", 64)
	if data, err = io.ReadAll(reader); err == nil || len(data) != 0 {
		t.Fatalf("oversized frame was forwarded: %d bytes, %v", len(data), err)
	}
}
