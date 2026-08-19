package server

import (
	"reflect"
	"strings"
	"testing"
)

func TestAnthropicEndpointURLNormalizesVersionPrefix(t *testing.T) {
	tests := []struct{ baseURL, endpoint, want string }{
		{"https://api.anthropic.com", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com/v1", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"https://api.minimaxi.com/anthropic", "/v1/models", "https://api.minimaxi.com/anthropic/v1/models"},
		{"https://api.minimaxi.com/anthropic/v1/", "/v1/models", "https://api.minimaxi.com/anthropic/v1/models"},
	}

	for _, tt := range tests {
		if got := anthropicEndpointURL(tt.baseURL, tt.endpoint); got != tt.want {
			t.Errorf("anthropicEndpointURL(%q, %q) = %q, want %q", tt.baseURL, tt.endpoint, got, tt.want)
		}
	}
}

func TestUsageFromMapExtractsCachedInputTokens(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want Usage
	}{
		{
			name: "chat completions details",
			body: map[string]any{"usage": map[string]any{
				"prompt_tokens":     float64(2006),
				"completion_tokens": float64(300),
				"total_tokens":      float64(2306),
				"prompt_tokens_details": map[string]any{
					"cached_tokens": float64(1920),
				},
				"completion_tokens_details": map[string]any{
					"reasoning_tokens":           float64(60),
					"audio_tokens":               float64(10),
					"accepted_prediction_tokens": float64(5),
					"rejected_prediction_tokens": float64(2),
				},
			}},
			want: Usage{
				PromptTokens:             2006,
				CachedInputTokens:        1920,
				CompletionTokens:         300,
				ReasoningOutputTokens:    60,
				OutputAudioTokens:        10,
				AcceptedPredictionTokens: 5,
				RejectedPredictionTokens: 2,
				TotalTokens:              2306,
			},
		},
		{
			name: "responses details",
			body: map[string]any{"usage": map[string]any{
				"input_tokens":  float64(100),
				"output_tokens": float64(40),
				"total_tokens":  float64(140),
				"input_tokens_details": map[string]any{
					"cached_tokens": float64(40),
					"audio_tokens":  float64(5),
				},
				"output_tokens_details": map[string]any{
					"reasoning_tokens":           float64(10),
					"audio_tokens":               float64(2),
					"accepted_prediction_tokens": float64(3),
					"rejected_prediction_tokens": float64(4),
				},
			}},
			want: Usage{
				PromptTokens:             100,
				CachedInputTokens:        40,
				InputAudioTokens:         5,
				CompletionTokens:         40,
				ReasoningOutputTokens:    10,
				OutputAudioTokens:        2,
				AcceptedPredictionTokens: 3,
				RejectedPredictionTokens: 4,
				TotalTokens:              140,
			},
		},
		{
			name: "deepseek cache hit",
			body: map[string]any{"usage": map[string]any{
				"prompt_tokens":           float64(80),
				"prompt_cache_hit_tokens": float64(30),
				"completion_tokens":       float64(20),
				"total_tokens":            float64(100),
			}},
			want: Usage{PromptTokens: 80, CachedInputTokens: 30, CompletionTokens: 20, TotalTokens: 100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usageFromMap(tt.body)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("usageFromMap() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestProviderSpecificUsageIncludesCachedInputTokens(t *testing.T) {
	anthropic := anthropicUsage(map[string]any{"usage": map[string]any{
		"input_tokens":                float64(100),
		"cache_creation_input_tokens": float64(200),
		"cache_read_input_tokens":     float64(300),
		"output_tokens":               float64(50),
	}})
	if !reflect.DeepEqual(anthropic, Usage{PromptTokens: 600, CachedInputTokens: 300, CacheWriteInputTokens: 200, CompletionTokens: 50, TotalTokens: 650}) {
		t.Fatalf("unexpected Anthropic usage: %+v", anthropic)
	}

	gemini := geminiUsage(map[string]any{"usageMetadata": map[string]any{
		"promptTokenCount":        float64(500),
		"cachedContentTokenCount": float64(350),
		"candidatesTokenCount":    float64(25),
		"thoughtsTokenCount":      float64(15),
		"totalTokenCount":         float64(540),
	}})
	if !reflect.DeepEqual(gemini, Usage{PromptTokens: 500, CachedInputTokens: 350, CompletionTokens: 40, ReasoningOutputTokens: 15, TotalTokens: 540}) {
		t.Fatalf("unexpected Gemini usage: %+v", gemini)
	}
}

func TestIncludeOpenAIStreamUsagePreservesOtherOptions(t *testing.T) {
	original := ChatCompletionRequest{
		StreamOptions: map[string]any{
			"include_usage":       false,
			"include_obfuscation": false,
		},
	}
	updated := includeOpenAIStreamUsage(original)

	if updated.StreamOptions["include_usage"] != true {
		t.Fatalf("include_usage = %v, want true", updated.StreamOptions["include_usage"])
	}
	if updated.StreamOptions["include_obfuscation"] != false {
		t.Fatalf("include_obfuscation = %v, want false", updated.StreamOptions["include_obfuscation"])
	}
	if original.StreamOptions["include_usage"] != false {
		t.Fatal("original stream options were mutated")
	}
}

func TestCopyOpenAIStreamAndUsagePreservesStreamAndReturnsUsage(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120,\"prompt_tokens_details\":{\"cached_tokens\":64}}}\n\n" +
		"data: [DONE]\n\n"
	var output strings.Builder

	usage, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != stream {
		t.Fatalf("stream changed during copy:\n%s", output.String())
	}
	want := Usage{PromptTokens: 100, CachedInputTokens: 64, CompletionTokens: 20, TotalTokens: 120}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("stream usage = %+v, want %+v", usage, want)
	}
}

// Cache-creation tokens bill above base input, so they must reach usage on their own
// field rather than only being folded into the prompt total.
func TestAnthropicUsageRecordsCacheWriteTokens(t *testing.T) {
	usage := anthropicUsage(map[string]any{"usage": map[string]any{
		"input_tokens":                float64(10),
		"cache_creation_input_tokens": float64(40),
		"cache_read_input_tokens":     float64(50),
		"output_tokens":               float64(5),
	}})

	if usage.CacheWriteInputTokens != 40 {
		t.Fatalf("cache write tokens = %d, want 40", usage.CacheWriteInputTokens)
	}
	// Prompt tokens keep their existing definition so cost accounting is unchanged.
	if usage.PromptTokens != 100 {
		t.Fatalf("prompt tokens = %d, want 100", usage.PromptTokens)
	}
	if usage.CachedInputTokens != 50 {
		t.Fatalf("cached input tokens = %d, want 50", usage.CachedInputTokens)
	}
	if got := anthropicUsageObject(usage); got["input_tokens"] != int64(10) || got["cache_creation_input_tokens"] != int64(40) || got["cache_read_input_tokens"] != int64(50) {
		t.Fatalf("Anthropic cache usage = %#v", got)
	}
	if got := anthropicUsageObject(Usage{PromptTokens: 10, CachedInputTokens: 9, CacheWriteInputTokens: 9}); got["input_tokens"] != int64(0) || got["cache_creation_input_tokens"] != int64(1) || got["cache_read_input_tokens"] != int64(9) {
		t.Fatalf("Anthropic bounded cache usage = %#v", got)
	}
	if usage.TotalTokens != 105 {
		t.Fatalf("total tokens = %d, want 105", usage.TotalTokens)
	}
}

func TestAnthropicUsageWithoutCacheWriteReportsZero(t *testing.T) {
	usage := anthropicUsage(map[string]any{"usage": map[string]any{
		"input_tokens":  float64(10),
		"output_tokens": float64(5),
	}})

	if usage.CacheWriteInputTokens != 0 {
		t.Fatalf("cache write tokens = %d, want 0", usage.CacheWriteInputTokens)
	}
	if usage.PromptTokens != 10 {
		t.Fatalf("prompt tokens = %d, want 10", usage.PromptTokens)
	}
}
