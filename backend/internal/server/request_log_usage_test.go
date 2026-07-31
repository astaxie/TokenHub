package server

import "testing"

func TestEnrichRequestLogsWithUsage(t *testing.T) {
	logs := []RequestLog{{RequestID: "req_visible"}, {RequestID: "req_without_usage"}}
	records := []UsageRecord{
		{
			RequestID:                "req_visible",
			InputTokens:              100,
			CachedInputTokens:        40,
			CacheWriteTokens:         5,
			InputAudioTokens:         3,
			OutputTokens:             30,
			ReasoningTokens:          10,
			OutputAudioTokens:        2,
			AcceptedPredictionTokens: 4,
			RejectedPredictionTokens: 1,
			TotalTokens:              130,
			CostUSD:                  0.00123,
		},
		{RequestID: "req_visible", InputTokens: 1, OutputTokens: 2, TotalTokens: 3, CostUSD: 0.00001},
		{RequestID: "req_not_visible", InputTokens: 999, TotalTokens: 999},
	}

	got := enrichRequestLogsWithUsage(logs, records)
	if len(got) != 2 {
		t.Fatalf("logs = %d, want 2", len(got))
	}
	usage := got[0]
	if usage.InputTokens != 101 || usage.CachedInputTokens != 40 || usage.CacheWriteTokens != 5 || usage.InputAudioTokens != 3 {
		t.Fatalf("input usage = %+v", usage)
	}
	if usage.OutputTokens != 32 || usage.ReasoningTokens != 10 || usage.OutputAudioTokens != 2 ||
		usage.AcceptedPredictionTokens != 4 || usage.RejectedPredictionTokens != 1 {
		t.Fatalf("output usage = %+v", usage)
	}
	if usage.TotalTokens != 133 || usage.EstimatedCostUSD != 0.00124 || usage.UsageRecordCount != 2 {
		t.Fatalf("usage totals = %+v", usage)
	}
	if got[1].UsageRecordCount != 0 || got[1].TotalTokens != 0 {
		t.Fatalf("unmatched log was enriched: %+v", got[1])
	}
}
