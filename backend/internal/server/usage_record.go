package server

import (
	"strings"
	"time"
)

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
		InputAudioTokens:         usage.InputAudioTokens,
		OutputTokens:             usage.CompletionTokens,
		ReasoningTokens:          usage.ReasoningOutputTokens,
		OutputAudioTokens:        usage.OutputAudioTokens,
		AcceptedPredictionTokens: usage.AcceptedPredictionTokens,
		RejectedPredictionTokens: usage.RejectedPredictionTokens,
		TotalTokens:              usage.TotalTokens,
		CostUSD:                  usage.CostUSD,
		ProviderCostUSD:          usage.ProviderCostUSD,
		CreatedAt:                createdAt,
	}
}
