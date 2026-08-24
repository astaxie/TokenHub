package guardrails

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQwenDetectorUsesOpenAICompatibleChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("authorization") != "Bearer local-secret" {
			t.Fatalf("unexpected request: method=%s authorization=%q", r.Method, r.Header.Get("authorization"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["model"] != "guard-model" {
			t.Fatalf("model = %#v", request["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "Safety: Unsafe\nCategories: Privacy, Malware"}}},
		})
	}))
	defer server.Close()

	detector := NewQwenDetector(QwenDetectorConfig{URL: server.URL, APIKey: "local-secret", Model: "guard-model"})
	result, err := detector.Detect(context.Background(), "classify this")
	if err != nil {
		t.Fatal(err)
	}
	if result.Safety != "unsafe" || len(result.Categories) != 2 || result.Categories[0] != "Privacy" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestQwenDetectorRejectsMalformedClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not a classification"}}]}`))
	}))
	defer server.Close()

	_, err := NewQwenDetector(QwenDetectorConfig{URL: server.URL}).Detect(context.Background(), "classify this")
	if err == nil {
		t.Fatal("expected malformed classification to fail")
	}
}
