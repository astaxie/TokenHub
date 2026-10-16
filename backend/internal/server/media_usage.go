package server

// Seedream reports output_tokens alongside a zero completion_tokens alias.
// Normalize only media accounting; leave response bytes and text APIs unchanged.
func mediaUsageFromMap(body map[string]any) Usage {
	usage := captureAudioDuration(usageFromMap(body), body)
	reported, _ := body["usage"].(map[string]any)
	output := int64FromAny(reported["output_tokens"])
	if usage.CompletionTokens == 0 && output > 0 {
		usage.CompletionTokens = output
		if int64FromAny(reported["total_tokens"]) == 0 {
			usage.TotalTokens = saturatingAddNonNegative(usage.PromptTokens, usage.CompletionTokens)
		}
	}
	return usage
}

// Parse only the usage object so large encoded media is not decoded again.
func mediaUsageFromProbe(probe providerStreamEventProbe, allowVerbose ...bool) (Usage, bool) {
	var reported map[string]any
	_ = decodeResponsesJSON(probe["usage"], &reported)
	var duration any
	_, hasDuration := probe["duration"]
	hasDuration = hasDuration && len(allowVerbose) > 0 && allowVerbose[0]
	_ = decodeResponsesJSON(probe["duration"], &duration)
	if len(reported) == 0 && !hasDuration {
		return Usage{}, false
	}
	body := map[string]any{"usage": reported}
	if hasDuration {
		body["duration"] = duration
	}
	return captureAudioDuration(mediaUsageFromMap(body), body, hasDuration), true
}
