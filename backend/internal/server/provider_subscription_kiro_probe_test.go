package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKiroProbePreservesBundledOutput(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, selectedModel := range []string{"", "auto"} {
		t.Run("selected-model="+selectedModel, func(t *testing.T) {
			var inferenceCalls atomic.Int32
			frames := append(kiroProbeEventFrame(t, "assistantResponseEvent", map[string]any{"content": "OK."}),
				kiroProbeEventFrame(t, "metadataEvent", map[string]any{"tokenUsage": map[string]any{"inputTokens": 2, "outputTokens": 3}})...)
			client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
				headers := http.Header{}
				for name, values := range request.Header {
					for _, value := range values {
						headers.Add(name, value)
					}
				}
				if headers.Get("Authorization") != "Bearer synthetic-kiro-access" {
					t.Error("Kiro request lost its account credentials")
					return nil, fmt.Errorf("Kiro request lost its account credentials")
				}
				contentType := "application/json"
				var body io.Reader
				switch request.URL.Path {
				case "/List-Available-Models":
					body = strings.NewReader(`{"models":[{"modelId":"auto","modelName":"Auto","supportedInputTypes":["TEXT"]}],"defaultModel":{"modelId":"auto"}}`)
				case "/generateAssistantResponse":
					var payload struct {
						ConversationState struct {
							CurrentMessage struct {
								UserInputMessage struct {
									ModelID string `json:"modelId"`
									Content string `json:"content"`
								} `json:"userInputMessage"`
							} `json:"currentMessage"`
						} `json:"conversationState"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						return nil, err
					}
					message := payload.ConversationState.CurrentMessage.UserInputMessage
					if message.ModelID != "auto" || message.Content != "Reply with OK." {
						t.Errorf("unexpected Kiro input message: %#v", message)
						return nil, fmt.Errorf("Kiro probe changed the selected model or prompt")
					}
					inferenceCalls.Add(1)
					contentType, body = "application/vnd.amazon.eventstream", bytes.NewReader(frames)
				default:
					t.Errorf("unexpected Kiro upstream path: %s", request.URL.Path)
					return nil, fmt.Errorf("unexpected Kiro upstream path: %s", request.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(body), Request: request}, nil
			})}
			pool := newSubscriptionWorkerPool(client)
			defer pool.Close()
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"kiro", "Kiro"}, Pool: pool,
				RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
					return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-kiro-access","refresh":"synthetic-kiro-refresh","expires":4102444800000,"profileArn":"arn:aws:codewhisperer:us-east-1:000000000000:profile/fixture"}`}, nil
				}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
			provider := Provider{ID: "provider-fixture", Type: adapter.Definition.providerType()}
			resource := ProviderResource{ID: "account-fixture", ResourceType: adapter.Definition.resourceType()}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := adapter.Probe(ctx, provider, resource, ProviderProbeRequest{Model: selectedModel})
			if err != nil {
				t.Fatal(err)
			}
			if result.OutputText != "OK." || choiceText(result.Response) != result.OutputText {
				t.Fatalf("probe lost converted Kiro output: text=%q response=%#v", result.OutputText, result.Response)
			}
			if result.ResourceID != resource.ID || result.Model != "auto" || inferenceCalls.Load() != 1 {
				t.Fatalf("probe changed the resource/model or repeated inference: %#v; calls=%d", result, inferenceCalls.Load())
			}
			if result.Usage.PromptTokens != 2 || result.Usage.CompletionTokens != 3 || result.Usage.TotalTokens != 5 {
				t.Fatalf("probe lost Kiro usage: %#v", result.Usage)
			}
		})
	}
}

func kiroProbeEventFrame(t *testing.T, event string, payload map[string]any) []byte {
	t.Helper()
	var headers []byte
	for _, header := range [][2]string{{":message-type", "event"}, {":event-type", event}} {
		headers = append(headers, byte(len(header[0])))
		headers = append(headers, header[0]...)
		headers = append(headers, 7)
		headers = binary.BigEndian.AppendUint16(headers, uint16(len(header[1])))
		headers = append(headers, header[1]...)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 12, 16+len(headers)+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(16+len(headers)+len(body)))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headers)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	frame = append(frame, headers...)
	frame = append(frame, body...)
	return binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
}

func TestSubscriptionWorkerProbeExtractsProtocolOutput(t *testing.T) {
	for _, protocol := range []string{"chat", "anthropic", "gemini", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			pool := newSubscriptionWorkerPool(&http.Client{})
			defer pool.Close()
			pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					switch command["operation"] {
					case "models":
						return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"test-model","protocol":"` + protocol + `"}]}`)}}
					case "fetch":
						contentType := "application/json"
						if protocol == "responses" {
							contentType = "text/event-stream"
						}
						return []subscriptionWorkerEvent{{Event: "response", Status: http.StatusOK, Headers: http.Header{"Content-Type": {contentType}}},
							{Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(subscriptionProtocolFixture(protocol, false)))}, {Event: "end"}}
					}
					return nil
				}), nil
			}
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"kiro", "Kiro"}, Pool: pool,
				RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
					return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"synthetic-key"}`}, nil
				}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
			result, err := adapter.Probe(context.Background(), Provider{Type: adapter.Definition.providerType()}, ProviderResource{ID: "account-fixture"}, ProviderProbeRequest{Model: "test-model"})
			if err != nil {
				t.Fatal(err)
			}
			if result.OutputText != "Hello" {
				t.Fatalf("probe omitted %s output: %q", protocol, result.OutputText)
			}
		})
	}
}
