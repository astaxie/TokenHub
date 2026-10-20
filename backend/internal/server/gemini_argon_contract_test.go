package server

import "testing"

func TestGeminiArgonUsesVerifiedSynchronousContract(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high"} {
		payload, err := buildGeminiRequest("gemini-4-argon", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, MaxTokens: 65536, ReasoningEffort: &effort})
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
