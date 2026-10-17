package server

type audioTokenObservation struct {
	target *int64
	value  any
}

// Match usageFromMap's auxiliary aliases while retaining field presence. A
// later event may report only one detail; absent details do not mean zero.
func audioAuxiliaryTokenObservations(usage *Usage, reported map[string]any) []audioTokenObservation {
	input, _ := firstNonNil(reported["prompt_tokens_details"], reported["input_tokens_details"], reported["input_token_details"]).(map[string]any)
	output, _ := firstNonNil(reported["completion_tokens_details"], reported["output_tokens_details"]).(map[string]any)
	return []audioTokenObservation{
		{&usage.CachedInputTokens, firstNonNil(input["cached_tokens"], reported["prompt_cache_hit_tokens"], reported["cached_input_tokens"], reported["cached_tokens"], reported["total_cached_tokens"])},
		{&usage.CacheWriteInputTokens, firstNonNil(reported["cache_write_input_tokens"], input["cache_write_tokens"])},
		{&usage.CacheWrite5mInputTokens, firstNonNil(reported["cache_write_5m_input_tokens"], input["cache_write_5m_tokens"], input["ephemeral_5m_input_tokens"])},
		{&usage.CacheWrite1hInputTokens, firstNonNil(reported["cache_write_1h_input_tokens"], input["cache_write_1h_tokens"], input["ephemeral_1h_input_tokens"])},
		{&usage.InputAudioTokens, firstNonNil(input["audio_tokens"], reported["input_audio_tokens"])},
		{&usage.ReasoningOutputTokens, firstNonNil(reported["reasoning_output_tokens"], output["reasoning_tokens"])},
		{&usage.OutputAudioTokens, firstNonNil(output["audio_tokens"], reported["output_audio_tokens"])},
		{&usage.AcceptedPredictionTokens, firstNonNil(output["accepted_prediction_tokens"], reported["accepted_prediction_tokens"], reported["output_accepted_prediction_tokens"])},
		{&usage.RejectedPredictionTokens, firstNonNil(output["rejected_prediction_tokens"], reported["rejected_prediction_tokens"], reported["output_rejected_prediction_tokens"])},
	}
}
