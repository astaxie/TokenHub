package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCopilotSubscriptionAutoServesAccountsWithoutManualModels(t *testing.T) {
	var selections atomic.Int32
	a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprintf(w, `{"token":"session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "/models":
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		case "/auto":
			selections.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["prompt"] != "Hello" || body["tier"] != "balance" || r.Header.Get("X-GitHub-Api-Version") != "2026-08-01" {
				t.Error("Auto routing request is missing its prompt, tier, or API version")
			}
			_, _ = fmt.Fprint(w, `{"session_token":"auto-secret","selected_model":{"id":"student-model","supported_endpoints":["/chat/completions"]}}`)
		case "/chat/completions":
			var body ChatCompletionRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model != "student-model" || r.Header.Get("Copilot-Session-Token") != "auto-secret" || r.Header.Get("Authorization") != "Bearer session-secret" {
				t.Error("Auto selection was not applied to inference")
			}
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
				return
			}
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	catalog, _, err := a.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].ID != "auto" {
		t.Fatalf("student account has no Auto choice: %+v %v", catalog, err)
	}
	request := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
	_, usage, err := a.Chat(context.Background(), provider, "auto", request)
	if err != nil || usage.TotalTokens != 2 {
		t.Fatalf("Auto completion failed: %+v %v", usage, err)
	}
	var output bytes.Buffer
	if _, err = a.ChatStream(context.Background(), provider, "auto", request, &output); err != nil || !strings.Contains(output.String(), "hello") || selections.Load() != 1 {
		t.Fatalf("Auto session was not reused for streaming: %v, selections=%d", err, selections.Load())
	}
}

func TestCopilotSubscriptionAutoLegacySessionAndRefusedSelection(t *testing.T) {
	for _, manualFallback := range []bool{false, true} {
		t.Run(fmt.Sprint(manualFallback), func(t *testing.T) {
			var calls atomic.Int32
			a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/models":
					_, _ = fmt.Fprint(w, `{"data":[{"id":"premium","model_picker_enabled":true},{"id":"gpt-4.1","model_picker_enabled":true},{"id":"hidden","model_picker_enabled":false},{"id":"exec-agent-internal"}]}`)
				case "/auto":
					w.WriteHeader(http.StatusNotFound)
				case "/models/session":
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["auto_mode"] == nil || r.Header.Get("X-GitHub-Api-Version") != "2025-10-01" {
						t.Error("legacy Auto session request is invalid")
					}
					if manualFallback {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					_, _ = fmt.Fprint(w, `{"session_token":"legacy-secret","available_models":["premium"]}`)
				case "/chat/completions":
					calls.Add(1)
					var body ChatCompletionRequest
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.Model == "premium" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					if body.Model != "gpt-4.1" || r.Header.Get("Copilot-Session-Token") != "" {
						t.Error("manual fallback did not choose the base model without an Auto token")
					}
					_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}]}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			_, _, err := a.Chat(context.Background(), provider, "auto", ChatCompletionRequest{})
			wantCalls := int32(2)
			if manualFallback {
				wantCalls = 1
			}
			if err != nil || calls.Load() != wantCalls {
				t.Fatalf("Auto fallback failed: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestCopilotSubscriptionAutoRedactsErrorsAndDoesNotRetryAfterOutput(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls atomic.Int32
			a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"session-secret","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/models":
					_, _ = fmt.Fprint(w, `{"data":[]}`)
				case "/auto":
					_, _ = fmt.Fprint(w, `{"session_token":"auto-secret","selected_model":"selected"}`)
				case "/chat/completions":
					calls.Add(1)
					failure := `{"error":{"message":"github-secret session-secret auto-secret","status_code":403},"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: "+failure+"\n\n")
					} else {
						_, _ = fmt.Fprint(w, failure)
					}
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			})
			var output bytes.Buffer
			var usage Usage
			var err error
			if stream {
				usage, err = a.ChatStream(context.Background(), provider, "auto", ChatCompletionRequest{}, &output)
			} else {
				_, usage, err = a.Chat(context.Background(), provider, "auto", ChatCompletionRequest{})
			}
			if err == nil || calls.Load() != 1 || usage.TotalTokens != 6 {
				t.Fatalf("failed usage or retry boundary lost: calls=%d usage=%+v err=%v", calls.Load(), usage, err)
			}
			for _, secret := range []string{"github-secret", "session-secret", "auto-secret"} {
				if strings.Contains(err.Error()+output.String(), secret) {
					t.Errorf("error leaked %s", secret)
				}
			}
		})
	}
}

func TestCopilotSubscriptionAutoSupportsResponsesWhenSelectionOffersBoth(t *testing.T) {
	var calls atomic.Int32
	a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "/models":
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		case "/auto":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["prompt"] != "last user text" {
				t.Error("Responses Auto selection lost its latest user prompt")
			}
			_, _ = fmt.Fprint(w, `{"session_token":"auto-secret","selected_model":{"id":"selected","supported_endpoints":["/chat/completions","/responses"]}}`)
		case "/responses":
			calls.Add(1)
			if r.Header.Get("Copilot-Session-Token") != "auto-secret" {
				t.Error("Responses did not use Auto session")
			}
			var body ResponsesRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model != "selected" {
				t.Error("Responses did not use selected model")
			}
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
				return
			}
			_, _ = fmt.Fprint(w, `{"status":"completed","output":[]}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	request := ResponsesRequest{Input: []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "last user text"}}}}}
	if _, _, err := a.Responses(context.Background(), provider, "auto", request); err != nil {
		t.Fatal(err)
	}
	opened, err := a.OpenResponses(context.Background(), provider, "auto", request, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = opened.Body.Close()
	if calls.Load() != 2 {
		t.Fatal("Responses selection was not reused for streaming")
	}
}

func TestCopilotSubscriptionAutoCacheIsolatedAndExpires(t *testing.T) {
	var calls atomic.Int32
	a, _ := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "/models":
			_, _ = fmt.Fprint(w, `{"data":[]}`)
		case "/auto":
			_, _ = fmt.Fprintf(w, `{"session_token":"selection-%d","selected_model":"selected"}`, calls.Add(1))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	a.RefreshCredentials = func(_ context.Context, id string, _ bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{AccessToken: id}, nil
	}
	for index, account := range []string{"first", "second", "first"} {
		ctx, model, err := a.resolveAuto(context.Background(), Provider{Options: map[string]string{"resource_id": account}}, "auto", "hi", nil)
		want := "selection-1"
		if index == 1 {
			want = "selection-2"
		}
		if err != nil || model != "selected" || ctx.Value(copilotSubscriptionAutoKey{}).(copilotSubscriptionAutoSession).Token != want {
			t.Fatalf("Auto session crossed accounts: %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("Auto account session was not cached")
	}
	a.mu.Lock()
	for key, session := range a.autoSessions {
		session.ExpiresAt = time.Now().Add(-time.Second)
		a.autoSessions[key] = session
	}
	a.mu.Unlock()
	if _, _, err := a.resolveAuto(context.Background(), Provider{Options: map[string]string{"resource_id": "first"}}, "auto", "hi", nil); err != nil || calls.Load() != 3 {
		t.Fatal("expired Auto token was reused")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := a.resolveAuto(ctx, Provider{Options: map[string]string{"resource_id": "uncached"}}, "auto", "hi", nil); err != context.Canceled {
		t.Fatalf("Auto ignored cancellation: %v", err)
	}
}
