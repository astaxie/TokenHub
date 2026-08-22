package server

import "testing"

func TestOpenAIChatUsageObjectIncludesStandardDetailsAndLegacyAliases(t *testing.T) {
	usage := completeOpenAIUsageFixture()
	result := openAIChatUsageObject(usage)

	if result["prompt_tokens"] != usage.PromptTokens ||
		result["completion_tokens"] != usage.CompletionTokens ||
		result["total_tokens"] != usage.TotalTokens {
		t.Fatalf("Chat token totals = %#v", result)
	}
	input := usageMapField(t, result, "prompt_tokens_details")
	if input["cached_tokens"] != usage.CachedInputTokens ||
		input["cache_write_tokens"] != usage.CacheWriteInputTokens ||
		input["audio_tokens"] != usage.InputAudioTokens {
		t.Fatalf("Chat input details = %#v", input)
	}
	assertOpenAIOutputDetails(t, usageMapField(t, result, "completion_tokens_details"), usage)
	assertLegacyUsageAliases(t, result, usage)
}

func TestOpenAIResponsesUsageObjectIncludesStandardDetailsAndLegacyAliases(t *testing.T) {
	usage := completeOpenAIUsageFixture()
	result := openAIResponsesUsageObject(usage)

	if result["input_tokens"] != usage.PromptTokens ||
		result["output_tokens"] != usage.CompletionTokens ||
		result["prompt_tokens"] != usage.PromptTokens ||
		result["completion_tokens"] != usage.CompletionTokens ||
		result["total_tokens"] != usage.TotalTokens {
		t.Fatalf("Responses token totals = %#v", result)
	}
	input := usageMapField(t, result, "input_tokens_details")
	if input["cached_tokens"] != usage.CachedInputTokens ||
		input["cache_write_tokens"] != usage.CacheWriteInputTokens ||
		input["audio_tokens"] != usage.InputAudioTokens {
		t.Fatalf("Responses input details = %#v", input)
	}
	assertOpenAIOutputDetails(t, usageMapField(t, result, "output_tokens_details"), usage)
	assertLegacyUsageAliases(t, result, usage)
}

func TestOpenAIUsageObjectsOmitEmptyDetailObjects(t *testing.T) {
	usage := Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}
	chat := openAIChatUsageObject(usage)
	responses := openAIResponsesUsageObject(usage)
	if _, exists := chat["prompt_tokens_details"]; exists {
		t.Fatalf("empty Chat input details were emitted: %#v", chat)
	}
	if _, exists := chat["completion_tokens_details"]; exists {
		t.Fatalf("empty Chat output details were emitted: %#v", chat)
	}
	if _, exists := responses["input_tokens_details"]; exists {
		t.Fatalf("empty Responses input details were emitted: %#v", responses)
	}
	if _, exists := responses["output_tokens_details"]; exists {
		t.Fatalf("empty Responses output details were emitted: %#v", responses)
	}
}

func completeOpenAIUsageFixture() Usage {
	return Usage{
		PromptTokens:             100,
		CachedInputTokens:        60,
		CacheWriteInputTokens:    10,
		InputAudioTokens:         5,
		CompletionTokens:         40,
		ReasoningOutputTokens:    20,
		OutputAudioTokens:        4,
		AcceptedPredictionTokens: 3,
		RejectedPredictionTokens: 2,
		TotalTokens:              140,
		CostUSD:                  0.25,
		UpstreamRequestID:        "req_upstream",
		ServedModel:              "served-model",
		ModelETag:                "model-etag",
		Transport:                "websocket",
	}
}

func usageMapField(t *testing.T, result map[string]any, name string) map[string]any {
	t.Helper()
	value, ok := result[name].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v", name, result[name])
	}
	return value
}

func assertOpenAIOutputDetails(t *testing.T, details map[string]any, usage Usage) {
	t.Helper()
	if details["reasoning_tokens"] != usage.ReasoningOutputTokens ||
		details["audio_tokens"] != usage.OutputAudioTokens ||
		details["accepted_prediction_tokens"] != usage.AcceptedPredictionTokens ||
		details["rejected_prediction_tokens"] != usage.RejectedPredictionTokens {
		t.Fatalf("output details = %#v", details)
	}
}

func assertLegacyUsageAliases(t *testing.T, result map[string]any, usage Usage) {
	t.Helper()
	if result["cached_input_tokens"] != usage.CachedInputTokens ||
		result["cache_write_input_tokens"] != usage.CacheWriteInputTokens ||
		result["reasoning_output_tokens"] != usage.ReasoningOutputTokens ||
		result["estimated_cost_usd"] != usage.CostUSD ||
		result["upstream_request_id"] != usage.UpstreamRequestID ||
		result["served_model"] != usage.ServedModel ||
		result["model_etag"] != usage.ModelETag ||
		result["transport"] != usage.Transport {
		t.Fatalf("legacy aliases = %#v", result)
	}
}
