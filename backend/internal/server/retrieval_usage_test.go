package server

import (
	"testing"
	"time"
)

func TestRetrievalUsagePreservesPresenceBeforePricing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  map[string]any
		known bool
	}{
		{"missing", map[string]any{}, false}, {"zero", map[string]any{"usage": map[string]any{"total_tokens": 0}}, true}, {"reported", map[string]any{"usage": map[string]any{"total_tokens": 5}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := retrievalUsage(tc.body, false)
			if (u.RetrievalEvidence.Quantity != nil) != tc.known {
				t.Fatal("lost presence")
			}
			charge := shadowPrice(nil, u, 0)
			if charge.Evidence == nil || (charge.Evidence.Quantity != nil) != tc.known {
				t.Fatal("missing price erased evidence")
			}
		})
	}
	u := retrievalUsage(map[string]any{"meta": map[string]any{"billed_units": map[string]any{"search_units": 2}}}, true)
	if u.TotalTokens != 0 || u.RetrievalEvidence.Unit != "search_unit" || *u.RetrievalEvidence.Quantity != 2 {
		t.Fatal("search units were converted to tokens")
	}
}

func TestNativeRetrievalPricesRemainIndependent(t *testing.T) {
	quantity := int64(2)
	usage := Usage{RetrievalEvidence: &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &quantity, Source: "upstream"}}
	tenant := Model{Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.003"}}
	provider := Model{Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.001"}}
	if got := priceUsage(tenant, usage); got.CostUSD != 0.006 || got.TotalTokens != 0 {
		t.Fatalf("tenant=%+v", got)
	}
	if cost, known := nativeRetrievalCost(provider, usage); !known || cost != 0.002 {
		t.Fatalf("provider cost=%v known=%v", cost, known)
	}
	price := legacyMeteringPrice(provider, time.Now(), true)
	if got := shadowPrice(&price, usage, 0.002); got.Charge == nil || got.Charge.USD != "0.002000000000" {
		t.Fatalf("evidence=%+v", got)
	}
}
