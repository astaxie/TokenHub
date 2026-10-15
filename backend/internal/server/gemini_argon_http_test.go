package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGeminiArgonHTTPChatAndStreamContract(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, effort := range []string{"low", "medium", "high"} {
			t.Run(fmt.Sprintf("stream_%t_%s", stream, effort), func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					action := ":generateContent"
					if stream {
						action = ":streamGenerateContent"
					}
					if r.Method != http.MethodPost || r.URL.Path != "/v1beta/models/gemini-4-argon"+action {
						t.Errorf("unexpected native request: %s %s", r.Method, r.URL.Path)
					}
					if (r.URL.Query().Get("alt") == "sse") != stream {
						t.Error("incorrect native streaming query")
					}
					var body struct {
						Contents []struct {
							Role  string `json:"role"`
							Parts []struct {
								Text string `json:"text"`
							} `json:"parts"`
						} `json:"contents"`
						GenerationConfig struct {
							MaxOutputTokens int `json:"maxOutputTokens"`
							ThinkingConfig  struct {
								ThinkingLevel string `json:"thinkingLevel"`
							} `json:"thinkingConfig"`
						} `json:"generationConfig"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if len(body.Contents) != 1 || body.Contents[0].Role != "user" || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0].Text != "synthetic task" {
						t.Errorf("lost prompt: %+v", body.Contents)
					}
					if body.GenerationConfig.MaxOutputTokens != 65536 || body.GenerationConfig.ThinkingConfig.ThinkingLevel != effort {
						t.Errorf("lost budget or effort: %+v", body.GenerationConfig)
					}
					reply := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Synthetic answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"cachedContentTokenCount":7,"candidatesTokenCount":5,"thoughtsTokenCount":3,"totalTokenCount":28}}`
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: %s\n\n", reply)
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(reply))
					}
				}))
				defer upstream.Close()
				adapter := GeminiAdapter{Client: upstream.Client()}
				provider := Provider{BaseURL: upstream.URL + "/v1beta"}
				req := ChatCompletionRequest{Model: "public-argon", Messages: []ChatMessage{{Role: "user", Content: "synthetic task"}}, MaxTokens: 65536, ReasoningEffort: &effort}
				var usage Usage
				var err error
				if stream {
					var out bytes.Buffer
					usage, err = adapter.ChatStream(context.Background(), provider, "gemini-4-argon", req, &out)
					if !strings.Contains(out.String(), "Synthetic answer") || !strings.Contains(out.String(), "[DONE]") || !strings.Contains(out.String(), "public-argon") {
						t.Errorf("invalid OpenAI stream: %s", out.String())
					}
				} else {
					var result any
					result, usage, err = adapter.Chat(context.Background(), provider, "gemini-4-argon", req)
					body, ok := result.(map[string]any)
					if !ok || choiceText(body) != "Synthetic answer" || body["model"] != "public-argon" {
						t.Errorf("invalid OpenAI response: %#v", result)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 {
					t.Fatalf("expected one native call, got %d", calls.Load())
				}
				if usage.PromptTokens != 20 || usage.CachedInputTokens != 7 || usage.CompletionTokens != 8 || usage.ReasoningOutputTokens != 3 || usage.TotalTokens != 28 {
					t.Fatalf("incorrect billable usage (reasoning must count once): %+v", usage)
				}
			})
		}
	}
}

func TestGeminiArgonRejectsInvalidContractBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	adapter := GeminiAdapter{Client: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL}
	for _, stream := range []bool{false, true} {
		for _, effort := range []string{"none", "minimal", "xhigh", "max", ""} {
			req := ChatCompletionRequest{Model: "public-argon", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
			want := "unsupported_reasoning_effort"
			if effort == "" {
				req.MaxTokens = 65537
				want = "model_output_limit_exceeded"
			} else {
				req.ReasoningEffort = &effort
			}
			var err error
			if stream {
				var out bytes.Buffer
				_, err = adapter.ChatStream(context.Background(), provider, "gemini-4-argon", req, &out)
				if out.Len() != 0 {
					t.Fatal("rejected stream emitted output")
				}
			} else {
				_, _, err = adapter.Chat(context.Background(), provider, "gemini-4-argon", req)
			}
			if err == nil || AsHTTPError(err).Status != 400 || AsHTTPError(err).Code != want {
				t.Fatalf("stream=%t effort=%q: wanted %s, got %v", stream, effort, want, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid contract reached upstream %d times", calls.Load())
	}
}
