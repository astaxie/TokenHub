package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionWorkerAnthropicReasoningVariants(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, test := range []struct {
		provider, model, effort, thinking, outputEffort string
	}{
		{"minimax-code", "MiniMax-M3.1-Flash-Preview", "high", "adaptive", "high"},
		{"minimax-code-global", "MiniMax-M3.1-Flash-Preview", "max", "adaptive", "max"},
		{"minimax-code", "MiniMax-M3", "none", "disabled", ""},
		{"minimax-code", "MiniMax-M3", "high", "adaptive", ""},
		{"minimax-code", "MiniMax-M3.1-Flash-Preview", "ultra", "", ""},
	} {
		for _, stream := range []bool{false, true} {
			name := test.provider + "/" + test.model + "/" + test.effort + "/collected"
			if stream {
				name = strings.TrimSuffix(name, "collected") + "streamed"
			}
			t.Run(name, func(t *testing.T) {
				adapter, provider := subscriptionVariantAdapter(t, test.provider, subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
					status, body, contentType := http.StatusOK, "", "application/json"
					switch request.URL.Path {
					case "/mavis/api/v1/models":
						status, body = http.StatusServiceUnavailable, `{}`
					case "/mavis/api/v1/llm/v1/messages":
						if test.effort == "ultra" {
							t.Error("unsupported effort reached inference")
						}
						var payload struct {
							Thinking     struct{ Type string }   `json:"thinking"`
							OutputConfig struct{ Effort string } `json:"output_config"`
						}
						if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
							return nil, err
						}
						if payload.Thinking.Type != test.thinking || payload.OutputConfig.Effort != test.outputEffort {
							t.Errorf("reasoning variant was not applied: %#v", payload)
						}
						body = subscriptionProtocolFixture("anthropic", stream)
						if stream {
							contentType = "text/event-stream"
						}
					default:
						t.Errorf("unexpected upstream request: %s", request.URL)
						status, body = http.StatusNotFound, `{}`
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				}))
				request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}, ReasoningEffort: &test.effort}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var err error
				if stream {
					_, err = adapter.ChatStream(ctx, provider, test.model, request, &bytes.Buffer{})
				} else {
					_, _, err = adapter.Chat(ctx, provider, test.model, request)
				}
				if test.effort == "ultra" {
					if err == nil || AsHTTPError(err).Status != http.StatusBadRequest || AsHTTPError(err).Code != "unsupported_reasoning_effort" {
						t.Fatalf("unsupported effort was not rejected: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestSubscriptionWorkerKiroReasoningBudget(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, effort := range []string{"low", "high"} {
		t.Run(effort, func(t *testing.T) {
			adapter, provider := subscriptionVariantAdapter(t, "kiro", subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
				contentType, body := "application/json", []byte(`{"models":[{"modelId":"auto","modelName":"Auto"}]}`)
				switch request.URL.Path {
				case "/List-Available-Models":
				case "/generateAssistantResponse":
					input, err := io.ReadAll(request.Body)
					if err != nil {
						return nil, err
					}
					budget := "10000"
					if effort == "high" {
						budget = "30000"
					}
					if !bytes.Contains(input, []byte("<thinking_mode>enabled</thinking_mode><max_thinking_length>"+budget+"</max_thinking_length>")) {
						t.Errorf("Kiro reasoning budget was not applied: %s", input)
					}
					contentType = "application/vnd.amazon.eventstream"
					body = kiroProbeEventFrame(t, "assistantResponseEvent", map[string]any{"content": "OK."})
				default:
					t.Errorf("unexpected upstream request: %s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, err := adapter.Chat(ctx, provider, "auto", ChatCompletionRequest{Model: "auto", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}, ReasoningEffort: &effort})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func subscriptionVariantAdapter(t *testing.T, id string, transport http.RoundTripper) (*SubscriptionWorkerAdapter, Provider) {
	t.Helper()
	pool := newSubscriptionWorkerPool(&http.Client{Transport: transport})
	t.Cleanup(pool.Close)
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{id, id}, Pool: pool,
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-access","refresh":"synthetic-refresh","expires":4102444800000,"profileArn":"arn:aws:codewhisperer:us-east-1:000000000000:profile/fixture"}`}, nil
		}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
	return adapter, Provider{Options: map[string]string{"resource_id": "account"}}
}
