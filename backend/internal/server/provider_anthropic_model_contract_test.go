package server

import "testing"

func TestCurrentAnthropicModelsPreserveEffort(t *testing.T) {
	for _, model := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "anthropic.claude-opus-5-5"} {
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			request := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, ReasoningEffort: &effort}
			payload, err := (AnthropicAdapter{}).buildRequest(model, request)
			if err != nil {
				t.Fatalf("%s/%s: %v", model, effort, err)
			}
			config, _ := payload["output_config"].(map[string]any)
			if config["effort"] != effort {
				t.Fatalf("%s dropped %s: %+v", model, effort, payload)
			}
		}
		invalid := "none"
		if _, err := (AnthropicAdapter{}).buildRequest(model, ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}, ReasoningEffort: &invalid}); err == nil {
			t.Fatalf("%s silently enabled adaptive thinking for an unsupported effort", model)
		}
	}
}

func TestCurrentAnthropicForcedToolsRespectModelContract(t *testing.T) {
	for _, model := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-sonnet-4-6"} {
		for _, choice := range []string{"required", "auto", "none"} {
			request := ChatCompletionRequest{
				Messages: []ChatMessage{{Role: "user", Content: "hello"}}, ToolChoice: choice,
				Tools: []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}},
			}
			_, err := (AnthropicAdapter{}).buildRequest(model, request)
			wantError := choice == "required" && model != "claude-haiku-5-5" && model != "claude-sonnet-4-6"
			if (err != nil) != wantError {
				t.Fatalf("%s/%s: error=%v, want rejection=%v", model, choice, err, wantError)
			}
		}
	}
}
