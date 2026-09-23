package server

import (
	"reflect"
	"testing"
)

func TestAnthropicUsageFromRawMap(t *testing.T) {
	// Anthropic reports the three input classes disjointly, so prompt tokens are
	// their sum, and cache-creation tokens keep their own field because they bill
	// at a premium over base input.
	usage := anthropicUsageFromRawMap(map[string]any{
		"input_tokens":                int64(10),
		"cache_creation_input_tokens": int64(4),
		"cache_creation": map[string]any{
			"ephemeral_5m_input_tokens": int64(3),
			"ephemeral_1h_input_tokens": int64(1),
		},
		"cache_read_input_tokens": int64(6),
		"output_tokens":           int64(5),
	})
	want := Usage{
		PromptTokens:            20,
		CachedInputTokens:       6,
		CacheWriteInputTokens:   4,
		CacheWrite5mInputTokens: 3,
		CacheWrite1hInputTokens: 1,
		CompletionTokens:        5,
		TotalTokens:             25,
	}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("anthropicUsageFromRawMap() = %+v, want %+v", usage, want)
	}

	if empty := anthropicUsageFromRawMap(nil); !reflect.DeepEqual(empty, Usage{}) {
		t.Fatalf("anthropicUsageFromRawMap(nil) = %+v, want a zero usage", empty)
	}
}

func TestAnthropicUsageObjectDerivesCacheWriteTotalFromDurationDetails(t *testing.T) {
	result := anthropicUsageObject(Usage{
		PromptTokens:            20,
		CacheWrite5mInputTokens: 3,
		CacheWrite1hInputTokens: 4,
		CompletionTokens:        5,
	})
	if result["input_tokens"] != int64(13) ||
		result["cache_creation_input_tokens"] != int64(7) ||
		result["output_tokens"] != int64(5) {
		t.Fatalf("Anthropic usage object did not derive cache-write totals: %+v", result)
	}
	if _, ok := result["cache_creation"]; ok {
		t.Fatalf("Anthropic usage object should not expose duration-specific cache creation details: %+v", result)
	}
}

// A stream splits usage across message_start and message_delta. A frame only
// overwrites what it carries, and the three input classes move as one group: a
// frame that restates any of them replaces the whole input side.
func TestMergeAnthropicStreamUsageOnlyPositiveValuesOverwrite(t *testing.T) {
	start := mergeAnthropicStreamUsage(Usage{}, map[string]any{
		"input_tokens":            int64(30),
		"cache_read_input_tokens": int64(10),
		"output_tokens":           int64(1),
	})
	if start.PromptTokens != 40 || start.CachedInputTokens != 10 || start.CompletionTokens != 1 || start.TotalTokens != 41 {
		t.Fatalf("message_start merge = %+v", start)
	}

	// message_delta reports only the final output count; the input fields it
	// omits must not reset the totals the first frame established.
	final := mergeAnthropicStreamUsage(start, map[string]any{"output_tokens": int64(120)})
	if final.PromptTokens != 40 || final.CachedInputTokens != 10 || final.CompletionTokens != 120 || final.TotalTokens != 160 {
		t.Fatalf("message_delta merge = %+v", final)
	}

	// An explicit zero is treated the same as an omission.
	zeroed := mergeAnthropicStreamUsage(final, map[string]any{
		"input_tokens":            int64(0),
		"cache_read_input_tokens": int64(0),
		"output_tokens":           int64(0),
	})
	if !reflect.DeepEqual(zeroed, final) {
		t.Fatalf("zero-valued merge = %+v, want %+v", zeroed, final)
	}

	created := mergeAnthropicStreamUsage(final, map[string]any{
		"cache_creation_input_tokens": int64(7),
		"cache_creation": map[string]any{
			"ephemeral_5m_input_tokens": int64(2),
			"ephemeral_1h_input_tokens": int64(5),
		},
	})
	if created.PromptTokens != 7 || created.CachedInputTokens != 0 || created.CompletionTokens != 120 {
		t.Fatalf("cache-creation merge = %+v", created)
	}
	if created.CacheWriteInputTokens != 7 || created.CacheWrite5mInputTokens != 2 || created.CacheWrite1hInputTokens != 5 {
		t.Fatalf("stream merge cache-write tokens = %+v", created)
	}
}
