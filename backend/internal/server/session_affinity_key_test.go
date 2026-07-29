package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicSessionIdentifierPrecedence(t *testing.T) {
	raw := map[string]any{
		"model":    "claude-test",
		"metadata": map[string]any{"user_id": "metadata-user"},
	}
	headers := make(http.Header)
	headers.Set("x-tokenhub-session-id", "tokenhub-session")
	headers.Set("session-id", "plain-session")

	identifier, scope := anthropicSessionIdentifier(headers, raw)
	if identifier != "tokenhub-session" || scope != sessionScopeSession {
		t.Fatalf("expected x-tokenhub-session-id priority, got %q scope=%d", identifier, scope)
	}

	headers.Del("x-tokenhub-session-id")
	identifier, scope = anthropicSessionIdentifier(headers, raw)
	if identifier != "plain-session" || scope != sessionScopeSession {
		t.Fatalf("expected session-id priority, got %q scope=%d", identifier, scope)
	}

	// metadata.user_id names a user rather than a session, so it must be reported
	// at user scope and left for the caller to accept or reject.
	headers.Del("session-id")
	identifier, scope = anthropicSessionIdentifier(headers, raw)
	if identifier != "metadata-user" || scope != sessionScopeUser {
		t.Fatalf("expected metadata.user_id to report user scope, got %q scope=%d", identifier, scope)
	}

	if _, scope := anthropicSessionIdentifier(make(http.Header), map[string]any{"model": "claude-test"}); scope != sessionScopeNone {
		t.Fatalf("expected no identifier when every source is absent, got scope=%d", scope)
	}

	// A present metadata block with a non-string user_id must neither panic nor
	// yield an identifier.
	malformed := map[string]any{"metadata": map[string]any{"user_id": 42}}
	if _, scope := anthropicSessionIdentifier(make(http.Header), malformed); scope != sessionScopeNone {
		t.Fatalf("expected non-string user_id to be ignored, got scope=%d", scope)
	}
}

func TestChatCompletionSessionIdentifierPrecedence(t *testing.T) {
	request := ChatCompletionRequest{
		Model:          "chat-test",
		PromptCacheKey: "cache-key",
		User:           "user-field",
	}
	headers := make(http.Header)
	headers.Set("x-tokenhub-session-id", "tokenhub-session")
	headers.Set("session-id", "plain-session")

	identifier, scope := chatCompletionSessionIdentifier(headers, request)
	if identifier != "tokenhub-session" || scope != sessionScopeSession {
		t.Fatalf("expected x-tokenhub-session-id priority, got %q scope=%d", identifier, scope)
	}

	headers.Del("x-tokenhub-session-id")
	identifier, scope = chatCompletionSessionIdentifier(headers, request)
	if identifier != "plain-session" || scope != sessionScopeSession {
		t.Fatalf("expected session-id priority, got %q scope=%d", identifier, scope)
	}

	headers.Del("session-id")
	identifier, scope = chatCompletionSessionIdentifier(headers, request)
	if identifier != "cache-key" || scope != sessionScopeSession {
		t.Fatalf("expected prompt_cache_key priority over user, got %q scope=%d", identifier, scope)
	}

	request.PromptCacheKey = nil
	identifier, scope = chatCompletionSessionIdentifier(headers, request)
	if identifier != "user-field" || scope != sessionScopeUser {
		t.Fatalf("expected user fallback at user scope, got %q scope=%d", identifier, scope)
	}

	request.User = nil
	if _, scope := chatCompletionSessionIdentifier(headers, request); scope != sessionScopeNone {
		t.Fatalf("expected no identifier when every source is absent, got scope=%d", scope)
	}

	// Non-string values take no part in derivation but must not break extraction.
	request.PromptCacheKey = 12345
	request.User = map[string]any{"id": "x"}
	if _, scope := chatCompletionSessionIdentifier(headers, request); scope != sessionScopeNone {
		t.Fatalf("expected non-string values to be ignored, got scope=%d", scope)
	}
}

// The Codex affinity key is a durable contract: if derivation drifts, every live
// session is redistributed and the upstream cache is invalidated wholesale.
// These golden vectors pin the values down.
func TestCodexAffinityKeyGoldenVector(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		secret   string
		apiKeyID string
		session  string
		expected string
	}{
		{
			name:     "configured secret",
			secret:   "affinity-secret",
			apiKeyID: "key_alpha",
			session:  "session-root",
			expected: deriveSessionAffinityKey("affinity-secret", "key_alpha", "session-root"),
		},
		{
			name:     "empty secret falls back to api key derivation",
			secret:   "",
			apiKeyID: "key_alpha",
			session:  "session-root",
			expected: deriveSessionAffinityKey("", "key_alpha", "session-root"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var request ResponsesRequest
			if err := json.Unmarshal([]byte(`{"model":"gpt-test","input":[]}`), &request); err != nil {
				t.Fatal(err)
			}
			headers := make(http.Header)
			headers.Set("session-id", testCase.session)
			affinity, err := resolveCodexSessionAffinity(testCase.secret, testCase.apiKeyID, headers, request)
			if err != nil {
				t.Fatal(err)
			}
			if affinity == nil {
				t.Fatal("expected affinity to be resolved")
			}
			if affinity.KeyHash != testCase.expected {
				t.Fatalf("codex affinity key drifted: got %q want %q", affinity.KeyHash, testCase.expected)
			}
			if affinity.AdapterType != ProviderOpenAICodex || affinity.Kind != AffinityKindCodexSession {
				t.Fatalf("codex affinity metadata changed: %+v", affinity)
			}
			if len(affinity.KeyHash) != 64 {
				t.Fatalf("expected hex-encoded sha256 output, got %d chars", len(affinity.KeyHash))
			}
		})
	}
}

// Codex's external error code is part of the API contract and must survive
// refactoring unchanged.
func TestCodexSessionIdentifierErrorCodeUnchanged(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-test","input":[]}`), &request); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.Repeat("a", 513), "bad\x01id"} {
		headers := make(http.Header)
		headers.Set("session-id", invalid)
		_, err := resolveCodexSessionAffinity("secret", "key_alpha", headers, request)
		if err == nil {
			t.Fatalf("expected rejection for %q", invalid)
		}
		httpErr := AsHTTPError(err)
		if httpErr.Code != "codex_session_id_invalid" {
			t.Fatalf("codex error code changed: %q", httpErr.Code)
		}
		if httpErr.Status != http.StatusBadRequest {
			t.Fatalf("codex error status changed: %d", httpErr.Status)
		}
	}
}

func TestSessionAffinityKeyIsolatedByAPIKey(t *testing.T) {
	first, err := sessionAffinityKey("shared-secret", "key_alpha", "same-session")
	if err != nil {
		t.Fatal(err)
	}
	second, err := sessionAffinityKey("shared-secret", "key_beta", "same-session")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected different API keys to derive different affinity keys")
	}

	repeat, err := sessionAffinityKey("shared-secret", "key_alpha", "same-session")
	if err != nil {
		t.Fatal(err)
	}
	if repeat != first {
		t.Fatal("expected affinity key derivation to be deterministic")
	}
}

func TestSessionAffinityKeyDerivesWithoutConfiguredSecret(t *testing.T) {
	key, err := sessionAffinityKey("", "key_alpha", "same-session")
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Fatal("expected a derived key when no secret is configured")
	}
	other, err := sessionAffinityKey("", "key_beta", "same-session")
	if err != nil {
		t.Fatal(err)
	}
	if key == other {
		t.Fatal("expected tenant isolation to hold without a configured secret")
	}
}

func TestSessionAffinityKeyRejectsInvalidIdentifier(t *testing.T) {
	if _, err := sessionAffinityKey("secret", "key_alpha", strings.Repeat("a", 513)); err == nil {
		t.Fatal("expected oversized identifier to be rejected")
	}
	if _, err := sessionAffinityKey("secret", "key_alpha", "bad\x01identifier"); err == nil {
		t.Fatal("expected control characters to be rejected")
	}
	if _, err := sessionAffinityKey("secret", "key_alpha", "bad\x7fidentifier"); err == nil {
		t.Fatal("expected DEL character to be rejected")
	}
	if _, err := sessionAffinityKey("secret", "key_alpha", strings.Repeat("a", 512)); err != nil {
		t.Fatalf("expected identifier at the size limit to be accepted: %v", err)
	}
}

// Stage 1 only adds extraction; affinity is not wired into routing yet, so the
// balanced strategy must still spread by RequestID.
//
// This pins the pre-change baseline. It is expected to fail once Stage 3 wires
// affinity in, at which point it should assert convergence instead.
func TestBalancedRoutingStillVariesByRequestID(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: "order-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID:      "prv_affinity_order",
		Name:    "Affinity Order Provider",
		Type:    ProviderOpenAICompatible,
		BaseURL: "https://upstream.invalid",
		Status:  StatusActive,
		Healthy: true,
	})
	for index := 0; index < 4; index++ {
		store.AddRoute(ModelRoute{
			ID:            fmt.Sprintf("route_affinity_order_%d", index),
			ModelName:     "order-model",
			ProviderID:    provider.ID,
			ProviderModel: "upstream-order-model",
			Priority:      1,
			Weight:        100,
			Status:        StatusActive,
			Strategy:      RouteStrategyBalanced,
		})
	}
	server := New(store)
	routes, err := store.SelectRouteCandidates("order-model")
	if err != nil {
		t.Fatal(err)
	}

	firstChoices := map[string]struct{}{}
	for index := 0; index < 40; index++ {
		call := CallContext{RequestID: fmt.Sprintf("req_probe_%d", index)}
		planned := server.planRouteOrder(call, routes)
		if len(planned) == 0 {
			t.Fatal("expected planned routes")
		}
		firstChoices[routeSortID(planned[0])] = struct{}{}
	}
	if len(firstChoices) < 2 {
		t.Fatalf("expected balanced routing to spread across candidates, got %d distinct first choices", len(firstChoices))
	}

	// Ordering for one RequestID must be reproducible, otherwise the failover order
	// would jitter between retries.
	call := CallContext{RequestID: "req_stable_probe"}
	expected := server.planRouteOrder(call, routes)
	for attempt := 0; attempt < 5; attempt++ {
		actual := server.planRouteOrder(call, routes)
		for index := range expected {
			if routeSortID(actual[index]) != routeSortID(expected[index]) {
				t.Fatalf("route order not reproducible at %d: %q vs %q",
					index, routeSortID(actual[index]), routeSortID(expected[index]))
			}
		}
	}
}

func TestChatCompletionForwardsPromptCacheKeyAndUser(t *testing.T) {
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		upstreamRequests = append(upstreamRequests, payload)
		if streaming, _ := payload["stream"].(bool); streaming {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"choices\":[]}\n\ndata: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"model":   "upstream-reasoning-model",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	app := server.Handler()
	for _, stream := range []bool{false, true} {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model":            "reasoning-model",
			"messages":         []map[string]any{{"role": "user", "content": "hello"}},
			"prompt_cache_key": "session-abc",
			"user":             "user-xyz",
			"stream":           stream,
		}, secret)
		if resp.Code != http.StatusOK {
			t.Fatalf("stream=%v expected 200, got %d: %s", stream, resp.Code, resp.Body)
		}
	}

	if len(upstreamRequests) != 2 {
		t.Fatalf("expected two upstream requests, got %d", len(upstreamRequests))
	}
	for _, payload := range upstreamRequests {
		if payload["prompt_cache_key"] != "session-abc" {
			t.Fatalf("prompt_cache_key was not forwarded: %#v", payload["prompt_cache_key"])
		}
		if payload["user"] != "user-xyz" {
			t.Fatalf("user was not forwarded: %#v", payload["user"])
		}
	}
}

// These two fields used to be absent from the struct, so non-string values were
// silently ignored and the request succeeded. Adding the fields must not make
// such requests fail at decode time: the gateway must not be stricter than the
// upstream.
func TestChatCompletionAcceptsNonStringCacheFields(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"model":   "upstream-reasoning-model",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "hello"}},
		"prompt_cache_key": 12345,
		"user":             map[string]any{"id": "structured"},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("non-string cache fields must not be rejected, got %d: %s", resp.Code, resp.Body)
	}
	if value, _ := upstreamRequest["prompt_cache_key"].(float64); value != 12345 {
		t.Fatalf("numeric prompt_cache_key was not forwarded verbatim: %#v", upstreamRequest["prompt_cache_key"])
	}
	if _, ok := upstreamRequest["user"].(map[string]any); !ok {
		t.Fatalf("structured user was not forwarded verbatim: %#v", upstreamRequest["user"])
	}
}

// When unset, no empty field may be injected: the request body the upstream sees
// must not change.
func TestChatCompletionOmitsUnsetCacheFields(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"model":   "upstream-reasoning-model",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "reasoning-model",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if _, exists := upstreamRequest["prompt_cache_key"]; exists {
		t.Fatal("prompt_cache_key must be omitted when unset")
	}
	if _, exists := upstreamRequest["user"]; exists {
		t.Fatal("user must be omitted when unset")
	}
}
