package server

import "testing"

func TestRetrievalUsageRejectsContradictoryZero(t *testing.T) {
	for _, raw := range []map[string]any{{"total_tokens": 0, "prompt_tokens": 5}, {"total_tokens": 2, "prompt_tokens": 1, "input_tokens": 2}, {"total_tokens": 1.5}, {"prompt_tokens": -1}} {
		usage := retrievalUsage(map[string]any{"usage": raw}, false)
		if validateRetrievalUsageResult(usage) == nil || usage.RetrievalEvidence.Source != "invalid" {
			t.Fatalf("invalid evidence accepted: %+v", usage)
		}
		priced := priceUsage(Model{Modality: "embedding", EmbeddingPriceUSDPer1M: 1}, usage)
		if priced.CostUSD != 0 {
			t.Fatalf("invalid usage was charged: %+v", priced)
		}
	}
	zero := retrievalUsage(map[string]any{"usage": map[string]any{"total_tokens": 0, "prompt_tokens": 0}}, false)
	if zero.RetrievalEvidence.Quantity == nil || *zero.RetrievalEvidence.Quantity != 0 || validateRetrievalUsageResult(zero) != nil {
		t.Fatal("explicit zero lost")
	}
	missing := retrievalUsage(map[string]any{}, false)
	if missing.RetrievalEvidence.Quantity != nil || missing.RetrievalEvidence.Source != "unreported" {
		t.Fatal("missing usage changed into zero")
	}
}
func TestPluginRetrievalUsageHandlesNativeUnitsAndLegacyCounters(t *testing.T) {
	body := map[string]any{"meta": map[string]any{"billed_units": map[string]any{"search_units": 2}}}
	usage, err := pluginRetrievalUsage(body, Usage{}, true)
	if err != nil || usage.RetrievalEvidence.Quantity == nil || *usage.RetrievalEvidence.Quantity != 2 {
		t.Fatalf("native parse failed: %+v %v", usage, err)
	}
	if got := priceUsage(Model{Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.003"}}, usage); got.CostUSD != 0.006 {
		t.Fatalf("native usage uncharged: %+v", got)
	}
	if _, err := pluginRetrievalUsage(body, Usage{}, false); err == nil {
		t.Fatal("unexpected billing unit accepted")
	}
	legacy, err := pluginRetrievalUsage(map[string]any{}, Usage{PromptTokens: 7, TotalTokens: 7}, false)
	if err != nil || legacy.RetrievalEvidence.Quantity == nil || *legacy.RetrievalEvidence.Quantity != 7 {
		t.Fatal("legacy usage lost")
	}
	missing, err := pluginRetrievalUsage(map[string]any{}, Usage{}, false)
	if err != nil || missing.RetrievalEvidence.Quantity != nil {
		t.Fatal("legacy omission became measured zero")
	}
	zero := int64(0)
	declared, err := pluginRetrievalUsage(map[string]any{}, Usage{RetrievalEvidence: &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &zero}}, true)
	if err != nil || declared.RetrievalEvidence.Quantity == nil {
		t.Fatalf("declared zero lost: %v", err)
	}
	bad := int64(-1)
	if _, err := pluginRetrievalUsage(map[string]any{}, Usage{RetrievalEvidence: &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &bad, Source: "upstream"}}, true); err == nil {
		t.Fatal("negative native quantity accepted")
	}
}
