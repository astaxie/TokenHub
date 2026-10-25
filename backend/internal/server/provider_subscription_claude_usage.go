package server

// CLI assistant blocks repeat a message's initial usage snapshot. Stream deltas
// update that same snapshot, while the final result covers the entire process.
func claudeSubscriptionUsageDelta(snapshot, reported Usage) Usage {
	usage := Usage{
		CompletionTokens:        max(0, snapshot.CompletionTokens-reported.CompletionTokens),
		CachedInputTokens:       max(0, snapshot.CachedInputTokens-reported.CachedInputTokens),
		CacheWriteInputTokens:   max(0, snapshot.CacheWriteInputTokens-reported.CacheWriteInputTokens),
		CacheWrite5mInputTokens: max(0, snapshot.CacheWrite5mInputTokens-reported.CacheWrite5mInputTokens),
		CacheWrite1hInputTokens: max(0, snapshot.CacheWrite1hInputTokens-reported.CacheWrite1hInputTokens),
	}
	uncached := snapshot.PromptTokens - snapshot.CachedInputTokens - snapshot.CacheWriteInputTokens
	previousUncached := reported.PromptTokens - reported.CachedInputTokens - reported.CacheWriteInputTokens
	usage.PromptTokens = max(0, uncached-previousUncached) + usage.CachedInputTokens + usage.CacheWriteInputTokens
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}
