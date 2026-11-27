package server

import "testing"

func TestGeminiArgonUsesVerifiedSynchronousContract(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high"} {
		payload, err := buildGeminiRequest("gemini-4-argon", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, MaxTokens: 32768, ReasoningEffort: &effort})
		if err != nil {
			t.Fatal(err)
		}
		config, _ := payload["generationConfig"].(map[string]any)
		thinking, _ := config["thinkingConfig"].(map[string]any)
		if thinking["thinkingLevel"] != effort {
			t.Fatalf("lost Argon thinking level %s: %+v", effort, config)
		}
	}
	for _, effort := range []string{"none", "minimal", "xhigh", "max"} {
		if _, err := buildGeminiRequest("gemini-4-argon", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, ReasoningEffort: &effort}); err == nil {
			t.Fatalf("unsupported Argon effort %s silently changed", effort)
		}
	}
	if _, err := buildGeminiRequest("gemini-4-argon", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, MaxTokens: 65537}); err == nil {
		t.Fatal("unsupported continuation output was advertised as a single synchronous response")
	}
	if _, err := buildGeminiRequest("gemini-3.8-flash", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, MaxTokens: 65537}); err != nil {
		t.Fatalf("Argon-only guard leaked to another model: %v", err)
	}
}

func TestGemini4NewModelsRejectInvalidAdapterRequests(t *testing.T) {
	for _, tc := range []struct {
		model, effort  string
		rejectedBudget int
	}{
		{"gemini-4-carbon", "xhigh", 32769},
		{"gemini-4-hydrogen", "none", 65537},
		{"gemini-4-neon", "minimal", 65537},
	} {
		t.Run(tc.model, func(t *testing.T) {
			req := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, ReasoningEffort: &tc.effort}
			if _, err := buildGeminiRequest(tc.model, req); err == nil || AsHTTPError(err).Code != "unsupported_reasoning_effort" {
				t.Fatalf("expected rejection of %q, got %v", tc.effort, err)
			}
			req.ReasoningEffort = nil
			req.MaxTokens = tc.rejectedBudget
			if _, err := buildGeminiRequest(tc.model, req); err == nil || AsHTTPError(err).Code != "model_output_limit_exceeded" {
				t.Fatalf("expected rejection of a continuation-sized output budget, got %v", err)
			}
		})
	}
}
