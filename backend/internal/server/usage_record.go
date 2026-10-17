package server

import (
	"strings"
	"time"
)

// Transcription details remain useful audit evidence even when no total,
// duration, or billable primary tokens were reported.
func hasTranscriptionTokenUsage(call CallContext, usage Usage) bool {
	return audioTranscriptionEndpoint([]string{call.RouteProtocol}) && meteredTokens(usage) > 0
}

func newUsageRecord(call CallContext, route RouteSelection, usage Usage, createdAt time.Time) *UsageRecord {
	attributedUserID := strings.TrimSpace(call.AttributedUserID)
	if attributedUserID == "" {
		attributedUserID = usageAttributionUserID(call.Key, call.Project)
	}
	return &UsageRecord{
		ID:                       NewID("use"),
		RequestID:                call.RequestID,
		ProjectID:                call.Project.ID,
		APIKeyID:                 call.Key.ID,
		AttributedUserID:         attributedUserID,
		ModelName:                call.Model.Name,
		ProviderID:               route.Provider.ID,
		ProviderResourceID:       routeResourceID(route),
		InputTokens:              usage.PromptTokens,
		CachedInputTokens:        usage.CachedInputTokens,
		CacheWriteTokens:         usage.CacheWriteInputTokens,
		CacheWrite5mTokens:       usage.CacheWrite5mInputTokens,
		CacheWrite1hTokens:       usage.CacheWrite1hInputTokens,
		InputAudioTokens:         usage.InputAudioTokens,
		OutputTokens:             usage.CompletionTokens,
		ReasoningTokens:          usage.ReasoningOutputTokens,
		OutputAudioTokens:        usage.OutputAudioTokens,
		AcceptedPredictionTokens: usage.AcceptedPredictionTokens,
		RejectedPredictionTokens: usage.RejectedPredictionTokens,
		TotalTokens:              usage.TotalTokens,
		InputCostUSD:             usage.InputCostUSD,
		CacheReadCostUSD:         usage.CacheReadCostUSD,
		CacheWriteCostUSD:        usage.CacheWriteCostUSD,
		OutputCostUSD:            usage.OutputCostUSD,
		CostUSD:                  usage.CostUSD,
		ProviderCostUSD:          usage.ProviderCostUSD,
		CreatedAt:                createdAt,
	}
}
