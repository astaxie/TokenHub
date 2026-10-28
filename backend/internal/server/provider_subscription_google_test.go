package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func googleSubscriptionTestAdapter(server *httptest.Server, providerType string) *GoogleSubscriptionAdapter {
	return &GoogleSubscriptionAdapter{ProviderType: providerType, Client: server.Client(), StreamClient: server.Client(), BaseURL: server.URL, LoadURL: server.URL, TokenURL: server.URL + "/token", OnboardPoll: time.Millisecond, StreamIdleTimeout: time.Second, accountState: &googleSubscriptionAccountState{}}
}

func googleSubscriptionTestProject(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/v1internal:loadCodeAssist" {
		return false
	}
	writeJSON(w, http.StatusOK, map[string]any{"cloudaicompanionProject": "project", "currentTier": map[string]any{"id": "standard-tier"}})
	return true
}

func TestGoogleSubscriptionChatUsesOAuthProjectAndEnvelope(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			var loaded, generated atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-access" || r.URL.Query().Has("key") {
					t.Error("expected bearer credentials without API key query")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case "/v1internal:loadCodeAssist":
					loaded.Add(1)
					writeJSON(w, 200, map[string]any{"cloudaicompanionProject": map[string]any{"id": "project-test"}, "currentTier": map[string]any{"id": "standard-tier"}})
				case "/v1internal:streamGenerateContent":
					generated.Add(1)
					if body["project"] != "project-test" || body["model"] != "gemini-test" {
						t.Errorf("unexpected inference envelope: %v", body)
					}
					request, _ := body["request"].(map[string]any)
					if request["systemInstruction"] == nil || request["tools"] == nil {
						t.Error("system instructions and tools were not forwarded")
					}
					if providerType == providerAntigravitySubscription {
						if body["userAgent"] != "antigravity" || body["requestType"] != "agent" || request["sessionId"] == nil {
							t.Error("missing Antigravity request metadata")
						}
					} else if body["user_prompt_id"] == nil {
						t.Error("missing Gemini prompt ID")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					data, _ := json.Marshal(map[string]any{"response": map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "answer"}}}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 12, "candidatesTokenCount": 3, "thoughtsTokenCount": 2, "totalTokenCount": 17}}})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerType)
			request := ChatCompletionRequest{Model: "public-model", Messages: []ChatMessage{{Role: "system", Content: "Be concise."}, {Role: "user", Content: "Hello"}}, Tools: []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}}}}}
			response, usage, err := adapter.Chat(context.Background(), Provider{Type: providerType, APIKey: "test-access"}, "gemini-test", request)
			if err != nil {
				t.Fatal(err)
			}
			result := response.(map[string]any)
			if choiceText(result) != "answer" || result["model"] != "public-model" || usage.PromptTokens != 12 || usage.CompletionTokens != 5 || usage.TotalTokens != 17 {
				t.Fatalf("incorrect response/usage: %v %+v", result, usage)
			}
			if loaded.Load() != 1 || generated.Load() != 1 {
				t.Fatal("expected project bootstrap followed by inference")
			}
		})
	}
}

func TestGoogleSubscriptionStreamConvertsToolsUsageAndRejectsTruncation(t *testing.T) {
	for _, finish := range []bool{true, false} {
		t.Run(fmt.Sprint(finish), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if googleSubscriptionTestProject(w, r) {
					return
				}
				if r.URL.Path != "/v1internal:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
					t.Errorf("unexpected path: %s", r.URL.String())
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"lookup\",\"args\":{\"q\":\"test\"}},\"thoughtSignature\":\"signed\"}]}}]}}\n\n")
				if finish {
					_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":7,\"candidatesTokenCount\":2,\"totalTokenCount\":9}}}\n\n")
				}
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
			var output bytes.Buffer
			usage, err := adapter.ChatStream(context.Background(), Provider{APIKey: "test-access", Options: map[string]string{"organization_id": "project"}}, "gemini-test", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Find it."}}, StreamOptions: map[string]any{"include_usage": true}}, &output)
			if !finish {
				if err == nil || strings.Contains(output.String(), "[DONE]") {
					t.Fatalf("truncated stream accepted: %v %s", err, output.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"tool_calls", "lookup", "thought_signature", "[DONE]"} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("stream missing %s: %s", want, output.String())
				}
			}
			if usage.TotalTokens != 9 {
				t.Fatalf("usage=%+v", usage)
			}
		})
	}
}

func TestGoogleSubscriptionStreamHonorsCancellationAndIdleTimeout(t *testing.T) {
	for _, cancelRequest := range []bool{true, false} {
		t.Run(fmt.Sprint(cancelRequest), func(t *testing.T) {
			opened := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if googleSubscriptionTestProject(w, r) {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				close(opened)
				<-r.Context().Done()
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
			adapter.StreamIdleTimeout = 30 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var output bytes.Buffer
				_, err := adapter.ChatStream(ctx, Provider{APIKey: "test-access", Options: map[string]string{"organization_id": "project"}}, "gemini-test", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}, &output)
				done <- err
			}()
			<-opened
			if cancelRequest {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected canceled or idle stream failure")
				}
			case <-time.After(time.Second):
				t.Fatal("stream did not terminate")
			}
		})
	}
}

func TestGoogleSubscriptionRefreshPreservesIdentityAndRotatesCredentials(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("client_secret") != "synthetic-client-secret" || r.Form.Get("client_id") != "synthetic-client-id" {
					t.Error("invalid refresh form")
				}
				writeJSON(w, 200, map[string]any{"access_token": "fresh-access", "refresh_token": "fresh-refresh", "expires_in": 3600, "token_type": "Bearer"})
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerType)
			old := ProviderResourceCredentials{ClientID: "synthetic-client-id", ClientSecret: "synthetic-client-secret", AccessToken: "old-access", RefreshToken: "old-refresh", OrganizationID: "project", Email: "test@example.invalid"}
			got, err := adapter.ProviderResourceCredentialRefreshHandlers()[0].Refresh(context.Background(), old)
			if err != nil {
				t.Fatal(err)
			}
			if got.AccessToken != "fresh-access" || got.RefreshToken != "fresh-refresh" || got.OrganizationID != "project" || got.Email != old.Email || got.ClientID != old.ClientID || got.ClientSecret != old.ClientSecret {
				t.Fatalf("credential identity changed: %+v", got)
			}
			expiry, ok := parseCredentialExpiry(got.ExpiresAt)
			if !ok || time.Until(expiry) < 59*time.Minute {
				t.Fatal("invalid expiry")
			}
		})
	}
}

func TestGoogleSubscriptionRefreshErrorsDoNotExposeSecrets(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"error":"invalid_grant","error_description":"refresh-secret access-secret"}`)
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
			_, err := adapter.refresh(context.Background(), ProviderResourceCredentials{ClientID: "synthetic-client-id", ClientSecret: "synthetic-client-secret", RefreshToken: "refresh-secret", AccessToken: "access-secret"})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe refresh error: %v", err)
			}
			if (status == 400 || status == 401) && AsHTTPError(err).Code != "provider_resource_reauthorization_required" {
				t.Fatalf("expected reauthorization error: %v", err)
			}
		})
	}
}

func TestGoogleSubscriptionOnboardingAndRequiredProject(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			var polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1internal:loadCodeAssist":
					writeJSON(w, 200, map[string]any{"allowedTiers": []any{map[string]any{"id": "free-tier", "isDefault": true}}})
				case "/v1internal:onboardUser":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					field := "tierId"
					if providerType == providerAntigravitySubscription {
						field = "tier_id"
					}
					if body[field] != "free-tier" {
						t.Errorf("missing tier field %s", field)
					}
					done := polls.Add(1) > 1
					writeJSON(w, 200, map[string]any{"done": done, "response": map[string]any{"cloudaicompanionProject": "new-project"}})
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerType)
			project, err := adapter.project(context.Background(), Provider{}, ProviderResourceCredentials{AccessToken: "test-access"})
			if err != nil || project.ID != "new-project" || polls.Load() != 2 {
				t.Fatalf("project=%+v polls=%d err=%v", project, polls.Load(), err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"currentTier": map[string]any{"id": "standard-tier"}})
	}))
	defer server.Close()
	adapter := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
	_, err := adapter.project(context.Background(), Provider{}, ProviderResourceCredentials{AccessToken: "test-access"})
	if err == nil || AsHTTPError(err).Code != "google_subscription_project_required" {
		t.Fatalf("expected missing project error, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.project(ctx, Provider{}, ProviderResourceCredentials{AccessToken: "test-access"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestGoogleSubscriptionLiveModelsFilterDisabledAndDeduplicate(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if googleSubscriptionTestProject(w, r) {
					return
				}
				if r.URL.Path == "/v1internal:listExperiments" {
					writeJSON(w, 200, map[string]any{"flags": []any{}})
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["project"] != "project" {
					t.Errorf("unexpected project: %v", body)
				}
				if providerType == providerAntigravitySubscription {
					if r.URL.Path != "/v1internal:fetchAvailableModels" {
						t.Error("wrong model discovery method")
					}
					writeJSON(w, 200, map[string]any{"models": map[string]any{"claude-live": map[string]any{"displayName": "Claude Live", "maxTokens": 250000, "maxOutputTokens": 32000, "supportsImages": true}, "disabled": map[string]any{"disabled": true}, "chat_internal": map[string]any{}, "tab_internal": map[string]any{}}})
				} else {
					if r.URL.Path != "/v1internal:retrieveUserQuota" {
						t.Error("wrong model discovery method")
					}
					writeJSON(w, 200, map[string]any{"buckets": []any{map[string]any{"modelId": "gemini-live"}, map[string]any{"modelId": "gemini-live"}, map[string]any{"modelId": "gemini-hidden_vertex"}}})
				}
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerType)
			catalog, err := adapter.ModelsWithCredentials(context.Background(), ProviderResourceCredentials{AccessToken: "test-access", OrganizationID: "project"})
			if err != nil {
				t.Fatal(err)
			}
			wantModels := 1
			if providerType == providerGeminiCLISubscription {
				wantModels = 5
			}
			if len(catalog.Models) != wantModels || catalog.ModelsCount != wantModels || catalog.Type != providerType {
				t.Fatalf("unexpected catalog: %+v", catalog)
			}
			if providerType == providerAntigravitySubscription && (catalog.Models[0].ContextWindow != 250000 || catalog.Models[0].MaxOutputTokens != 32000 || catalog.Models[0].Category != "claude") {
				t.Fatalf("incorrect model metadata: %+v", catalog.Models[0])
			}
		})
	}
}

func TestGoogleSubscriptionRejectsMissingEnvelopesAndRedactsHTTPFailure(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(403)
				_, _ = fmt.Fprint(w, `{"error":{"message":"test-secret denied"}}`)
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
			p := Provider{APIKey: "test-secret", Options: map[string]string{"organization_id": "project"}}
			request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}
			var err error
			if stream {
				var out bytes.Buffer
				_, err = adapter.ChatStream(context.Background(), p, "gemini-test", request, &out)
			} else {
				_, _, err = adapter.Chat(context.Background(), p, "gemini-test", request)
			}
			if err == nil || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("unsafe inference error: %v", err)
			}
		})
	}
	if _, err := googleSubscriptionUnwrap(map[string]any{"candidates": []any{}}); err == nil {
		t.Fatal("accepted missing envelope")
	}
	if _, err := googleSubscriptionUnwrap(map[string]any{"response": map[string]any{"error": "test-secret"}}); err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal("accepted or exposed upstream error")
	}
}

func TestGoogleSubscriptionNonStreamCollectsToolsAndRequiresFinish(t *testing.T) {
	first := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"Thinking","thought":true},{"functionCall":{"name":"lookup","args":{"x":1}},"thoughtSignature":"signed"}]}}],"usageMetadata":{"promptTokenCount":4}}}` + "\n\n"
	last := `data: {"response":{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"candidatesTokenCount":2,"totalTokenCount":6}}}` + "\n\n"
	result, usage, err := collectGoogleSubscriptionChat(strings.NewReader(first+last), "public")
	if err != nil || usage.PromptTokens != 4 || usage.TotalTokens != 6 {
		t.Fatalf("result=%v usage=%+v err=%v", result, usage, err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tool_calls", "lookup", "thought_signature", "reasoning_content"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("completion missing %s: %s", want, data)
		}
	}
	if _, _, err = collectGoogleSubscriptionChat(strings.NewReader(first), "public"); err == nil {
		t.Fatal("accepted truncated non-stream response")
	}
}

func TestGoogleSubscriptionAntigravityFreeTierAndEntitlementFallback(t *testing.T) {
	for _, tier := range []string{"free-tier", "standard-tier"} {
		t.Run(tier, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1internal:loadCodeAssist" {
					writeJSON(w, 200, map[string]any{"currentTier": map[string]any{"id": tier}})
					return
				}
				requests.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if _, present := body["entitlement"]; present {
					if tier == "free-tier" {
						t.Error("free tier must not be sent as entitlement")
					}
					w.WriteHeader(400)
					_, _ = fmt.Fprint(w, `{"error":{"message":"FAILED_PRECONDITION"}}`)
					return
				}
				writeJSON(w, 200, map[string]any{"models": map[string]any{"gemini-live": map[string]any{}}})
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerAntigravitySubscription)
			catalog, err := adapter.ModelsWithCredentials(context.Background(), ProviderResourceCredentials{AccessToken: "test-access", OrganizationID: "project", PlanType: tier})
			if err != nil || catalog.ModelsCount != 1 {
				t.Fatalf("catalog=%+v err=%v", catalog, err)
			}
			expected := int32(1)
			if tier == "standard-tier" {
				expected = 2
			}
			if requests.Load() != expected {
				t.Fatalf("requests=%d want=%d", requests.Load(), expected)
			}
		})
	}
}

func TestGoogleSubscriptionToolContinuationKeepsIDsAndSignatures(t *testing.T) {
	adapter := &GoogleSubscriptionAdapter{ProviderType: providerAntigravitySubscription}
	request := ChatCompletionRequest{Messages: []ChatMessage{
		{Role: "user", Content: "Find it"},
		{Role: "assistant", ToolCalls: []any{map[string]any{"id": "tool/a", "type": "function", "function": map[string]any{"name": "lookup", "arguments": "{}"}}}},
		{Role: "tool", ToolCallID: "tool/a", Content: "found"},
	}}
	payload, err := buildGeminiRequest("claude-live", request)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.preparePayload("claude-live", payload); err != nil {
		t.Fatal(err)
	}
	contents := payload["contents"].([]map[string]any)
	callPart := contents[1]["parts"].([]map[string]any)[0]
	resultPart := contents[2]["parts"].([]map[string]any)[0]
	callID := callPart["functionCall"].(map[string]any)["id"]
	resultID := resultPart["functionResponse"].(map[string]any)["id"]
	if callID != resultID || callID == "tool/a" {
		t.Fatal("tool continuation IDs were not normalized consistently")
	}
	if callPart["thoughtSignature"] != "skip_thought_signature_validator" {
		t.Fatal("foreign tool call has no Code Assist compatibility signature")
	}
	session := googleSubscriptionSessionID(payload)
	if _, err := strconv.ParseInt(session, 10, 64); err != nil || !strings.HasPrefix(session, "-") {
		t.Fatalf("invalid session ID: %s", session)
	}
	if session != googleSubscriptionSessionID(payload) {
		t.Fatal("session ID changed across turns")
	}
}

func TestGoogleSubscriptionConfiguredClientsRetainSSRFGuard(t *testing.T) {
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "strict")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "false")
	client, stream, idle := newUpstreamClients(Config{})
	adapter := newGoogleSubscriptionAdapter(providerGeminiCLISubscription, builtinProviderRuntimeDependencies{Client: client, StreamClient: stream, StreamIdleTimeout: idle})
	adapter.BaseURL = server.URL
	_, _, err := adapter.Chat(context.Background(), Provider{BaseURL: server.URL, APIKey: "test-access", Options: map[string]string{"organization_id": "project"}}, "gemini-test", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}})
	if err == nil || called.Load() != 0 {
		t.Fatalf("SSRF guard did not stop local request: calls=%d err=%v", called.Load(), err)
	}
}
