package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCopilotSubscriptionResponsesGatewayRetainsFailedOpeningUsage(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			account := strings.TrimPrefix(r.Header.Get("Authorization"), "token ")
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "session-" + account, "expires_at": time.Now().Add(time.Hour).Unix()})
			return
		}
		if r.URL.Path != "/responses" {
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.Contains(r.Header.Get("Authorization"), "rsrc_classified_0") {
			primaryCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"session-rsrc_classified_0-account-secret rsrc_classified_0-account-secret"},"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
			return
		}
		fallbackCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-fallback\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"fallback answer\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n")
	}))
	defer upstream.Close()
	server, store, key := classificationGateway(t, copilotSubscriptionBaseURL, copilotSubscriptionBaseURL, ProviderGitHubCopilotSubscription)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	for _, resource := range store.ListProviderResources() {
		resource.ResourceType = ProviderResourceGitHubCopilotSubscription
		resource.Credentials = &ProviderResourceCredentials{AccessToken: resource.ID + "-account-secret"}
		if _, err := store.UpdateProviderResource(resource.ID, resource); err != nil {
			t.Fatal(err)
		}
	}
	adapter := &CopilotSubscriptionAdapter{
		Client: upstream.Client(), StreamClient: upstream.Client(), CredentialRefreshClient: upstream.Client(),
		APIBaseURL: upstream.URL, TokenURL: upstream.URL + "/token",
		RefreshCredentials: store.RefreshProviderResourceCredentials,
	}
	registerTestAdapter(server, ProviderGitHubCopilotSubscription, adapter)
	emitter := &recordingTraceEmitter{}
	server.traceEmitter = emitter
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "classified-model", "input": "hello", "stream": true}, key)
	if response.Code != http.StatusOK || !strings.Contains(response.Body, "fallback answer") || primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatalf("public Responses did not fail over before output: status=%d primary=%d fallback=%d body=%s", response.Code, primaryCalls.Load(), fallbackCalls.Load(), response.Body)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Order("attempt_index ASC").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].StatusCode != http.StatusTooManyRequests || attempts[0].UpstreamStatus != http.StatusOK || attempts[0].InputTokens != 4 || attempts[0].OutputTokens != 2 || attempts[0].TotalTokens != 6 || attempts[1].StatusCode != http.StatusOK || attempts[1].TotalTokens != 4 {
		t.Fatalf("public Responses lost failed opening usage: %+v", attempts)
	}
	records := store.ListUsageRecords()
	// The customer usage record covers the winning response; failed provider
	// work remains in attempt accounting and the cumulative rate-limit charge.
	if len(records) != 1 || records[0].TotalTokens != 4 || records[0].ProviderResourceID != "rsrc_classified_1" {
		t.Fatalf("winning response settlement changed: %+v", records)
	}
	completions := emitter.take()
	if len(completions) != 1 || completions[0].Usage.RateLimitTokens != 10 || resourceFailureCount(t, store, "rsrc_classified_0") != 1 {
		t.Fatalf("failed opening lost quota accounting or resource health: %+v", completions)
	}
	audit, err := json.Marshal(map[string]any{"attempts": attempts, "requests": store.ListRequestLogs()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body, "account-secret") || strings.Contains(string(audit), "account-secret") {
		t.Fatal("public Responses failure exposed account credentials")
	}
}
