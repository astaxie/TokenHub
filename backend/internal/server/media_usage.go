package server

// Seedream reports output_tokens alongside a zero completion_tokens alias.
// Normalize only media accounting; leave response bytes and text APIs unchanged.
func mediaUsageFromMap(body map[string]any) Usage {
	usage := usageFromMap(body)
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
func mediaUsageFromProbe(probe providerStreamEventProbe) (Usage, bool) {
	var reported map[string]any
	if decodeResponsesJSON(probe["usage"], &reported) != nil || len(reported) == 0 {
		return Usage{}, false
	}
	return mediaUsageFromMap(map[string]any{"usage": reported}), true
}
