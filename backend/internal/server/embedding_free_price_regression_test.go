package server

import (
	"testing"
	"time"
)

func TestConfirmedFreeEmbeddingOverridesLegacyAndReportedTenantCosts(t *testing.T) {
	quantity := int64(1000000)
	for _, reported := range []float64{0, 9} {
		model := Model{Modality: "embedding", InputPriceUSDPer1M: 2, EmbeddingPriceUSDPer1M: 0, Metadata: map[string]string{"retrieval_pricing_confirmed": "true"}}
		usage := Usage{PromptTokens: quantity, TotalTokens: quantity, CostUSD: reported, InputCostUSD: reported, ProviderCostUSD: 0.2, RetrievalEvidence: &RetrievalUsageEvidence{Unit: "token", Quantity: &quantity, Source: "upstream"}}
		priced := priceUsage(model, usage)
		if priced.CostUSD != 0 || priced.InputCostUSD != 0 || priced.ProviderCostUSD != 0.2 || priced.TotalTokens != quantity {
			t.Fatalf("confirmed free charged or procurement lost: %+v", priced)
		}
		snapshot := legacyMeteringPrice(model, time.Now(), false)
		shadow := shadowPrice(&snapshot, priced, priced.CostUSD)
		if shadow.Charge == nil || shadow.Charge.USD != "0.000000000000" {
			t.Fatalf("shadow disagrees: %+v", shadow)
		}
	}
	legacy := priceUsage(Model{Modality: "embedding", InputPriceUSDPer1M: 2}, Usage{PromptTokens: quantity, TotalTokens: quantity})
	if legacy.CostUSD != 2 {
		t.Fatalf("unconfirmed legacy fallback changed: %+v", legacy)
	}
	paid := priceUsage(Model{Modality: "embedding", InputPriceUSDPer1M: 2, EmbeddingPriceUSDPer1M: 0.5}, Usage{PromptTokens: quantity, TotalTokens: quantity})
	if paid.CostUSD != 0.5 {
		t.Fatalf("paid embedding changed: %+v", paid)
	}
}
