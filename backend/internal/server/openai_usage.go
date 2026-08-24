package server

// openAIChatUsageObject renders TokenHub's internal accounting in the public
// Chat Completions shape. The legacy top-level detail fields remain available
// for existing TokenHub clients, while standard clients read the nested detail
// objects.
func openAIChatUsageObject(usage Usage) map[string]any {
	result := tokenHubUsageAliases(usage)
	result["prompt_tokens"] = usage.PromptTokens
	result["completion_tokens"] = usage.CompletionTokens
	result["total_tokens"] = usage.TotalTokens

	inputDetails := map[string]any{}
	setNonZeroInt64(inputDetails, "cached_tokens", usage.CachedInputTokens)
	setNonZeroInt64(inputDetails, "cache_write_tokens", usage.CacheWriteInputTokens)
	setNonZeroInt64(inputDetails, "audio_tokens", usage.InputAudioTokens)
	if len(inputDetails) > 0 {
		result["prompt_tokens_details"] = inputDetails
	}

	outputDetails := openAIOutputTokenDetails(usage)
	if len(outputDetails) > 0 {
		result["completion_tokens_details"] = outputDetails
	}
	return result
}

// openAIResponsesUsageObject renders TokenHub's internal accounting in the
// public Responses shape while retaining TokenHub's compatibility aliases.
func openAIResponsesUsageObject(usage Usage) map[string]any {
	result := tokenHubUsageAliases(usage)
	result["input_tokens"] = usage.PromptTokens
	result["output_tokens"] = usage.CompletionTokens
	result["prompt_tokens"] = usage.PromptTokens
	result["completion_tokens"] = usage.CompletionTokens
	result["total_tokens"] = usage.TotalTokens

	inputDetails := map[string]any{}
	setNonZeroInt64(inputDetails, "cached_tokens", usage.CachedInputTokens)
	setNonZeroInt64(inputDetails, "cache_write_tokens", usage.CacheWriteInputTokens)
	setNonZeroInt64(inputDetails, "audio_tokens", usage.InputAudioTokens)
	if len(inputDetails) > 0 {
		result["input_tokens_details"] = inputDetails
	}

	outputDetails := openAIOutputTokenDetails(usage)
	if len(outputDetails) > 0 {
		result["output_tokens_details"] = outputDetails
	}
	return result
}

func tokenHubUsageAliases(usage Usage) map[string]any {
	result := map[string]any{}
	setNonZeroInt64(result, "cached_input_tokens", usage.CachedInputTokens)
	setNonZeroInt64(result, "cache_write_input_tokens", usage.CacheWriteInputTokens)
	setNonZeroInt64(result, "input_audio_tokens", usage.InputAudioTokens)
	setNonZeroInt64(result, "reasoning_output_tokens", usage.ReasoningOutputTokens)
	setNonZeroInt64(result, "output_audio_tokens", usage.OutputAudioTokens)
	setNonZeroInt64(result, "accepted_prediction_tokens", usage.AcceptedPredictionTokens)
	setNonZeroInt64(result, "rejected_prediction_tokens", usage.RejectedPredictionTokens)
	if usage.CostUSD != 0 {
		result["estimated_cost_usd"] = usage.CostUSD
	}
	if usage.UpstreamRequestID != "" {
		result["upstream_request_id"] = usage.UpstreamRequestID
	}
	if usage.ServedModel != "" {
		result["served_model"] = usage.ServedModel
	}
	if usage.ModelETag != "" {
		result["model_etag"] = usage.ModelETag
	}
	if usage.Transport != "" {
		result["transport"] = usage.Transport
	}
	return result
}

func openAIOutputTokenDetails(usage Usage) map[string]any {
	details := map[string]any{}
	setNonZeroInt64(details, "reasoning_tokens", usage.ReasoningOutputTokens)
	setNonZeroInt64(details, "audio_tokens", usage.OutputAudioTokens)
	setNonZeroInt64(details, "accepted_prediction_tokens", usage.AcceptedPredictionTokens)
	setNonZeroInt64(details, "rejected_prediction_tokens", usage.RejectedPredictionTokens)
	return details
}

func setNonZeroInt64(target map[string]any, key string, value int64) {
	if value != 0 {
		target[key] = value
	}
}
