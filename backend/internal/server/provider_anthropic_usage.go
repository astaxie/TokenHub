package server

// anthropicUsageFromRawMap reads one Anthropic usage snapshot. Anthropic reports
// the three input classes disjointly, so prompt tokens are their sum.
// Cache-creation tokens are also surfaced on their own field: they bill at a
// premium over base input, and folding them into the total alone would lose the
// distinction. A nil snapshot yields a zero usage.
func anthropicUsageFromRawMap(raw map[string]any) Usage {
	uncachedInputTokens := int64FromAny(raw["input_tokens"])
	cacheCreationInputTokens := int64FromAny(raw["cache_creation_input_tokens"])
	cachedInputTokens := int64FromAny(raw["cache_read_input_tokens"])
	usage := Usage{
		PromptTokens:          uncachedInputTokens + cacheCreationInputTokens + cachedInputTokens,
		CachedInputTokens:     cachedInputTokens,
		CacheWriteInputTokens: cacheCreationInputTokens,
		CompletionTokens:      int64FromAny(raw["output_tokens"]),
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

// anthropicUsage reads the snapshot off a complete non-streaming response body.
func anthropicUsage(body map[string]any) Usage {
	usageMap, _ := body["usage"].(map[string]any)
	return anthropicUsageFromRawMap(usageMap)
}
