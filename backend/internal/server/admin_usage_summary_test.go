package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestAdminUsageSummaryIncludesCacheWriteAndReasoningTokens(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()

	call := CallContext{
		RequestID: "req_summary_tokens",
		Project:   Project{ID: "project_summary_tokens"},
		Key:       APIKey{ID: "key_summary_tokens", OwnerUserID: "user_summary_tokens"},
		Model:     Model{Name: "summary-chat", Modality: "chat"},
		StartedAt: time.Now(),
	}
	route := RouteSelection{Provider: Provider{ID: "provider_summary_tokens"}}
	store.FinishCall(call, route, Usage{
		PromptTokens:          1000,
		CachedInputTokens:     400,
		CacheWriteInputTokens: 25,
		CompletionTokens:      100,
		ReasoningOutputTokens: 30,
	}, http.StatusOK, "", "127.0.0.1", "summary-test")

	response := doJSON(t, app, http.MethodGet, "/api/admin/usage/summary", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("usage summary status = %d, want 200: %s", response.Code, response.Body)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(response.Body), &summary); err != nil {
		t.Fatalf("decode usage summary: %v", err)
	}

	want := map[string]float64{
		"input_tokens":             1000,
		"cached_input_tokens":      400,
		"cache_write_input_tokens": 25,
		"output_tokens":            100,
		"reasoning_output_tokens":  30,
	}
	for key, expected := range want {
		value, ok := summary[key].(float64)
		if !ok {
			t.Fatalf("usage summary %q = %#v, want number %v", key, summary[key], expected)
		}
		if value != expected {
			t.Fatalf("usage summary %q = %v, want %v", key, value, expected)
		}
	}
}
