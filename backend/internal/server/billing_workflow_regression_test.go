package server

import (
	"fmt"
	"testing"
	"time"
)

func TestBillingReconciliationScopesBeforeStatementLimit(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	at := time.Now().UTC().Add(-time.Minute)
	rows := make([]UsageRecord, 10001)
	for i := range rows {
		rows[i] = UsageRecord{ID: fmt.Sprintf("unrelated-%d", i), RequestID: fmt.Sprintf("unrelated-%d", i), ProviderID: "other", ModelName: "model", ProviderCostUSD: 1, CreatedAt: at}
	}
	if err := store.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"chosen", "other"} {
		row := UsageRecord{ID: resource, RequestID: resource, ProviderID: "selected", ProviderResourceID: resource, ModelName: "model", ProviderCostUSD: 2, CreatedAt: at}
		if err := store.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	bridge := &reconciliationStoreBridge{store: store}
	usages, err := bridge.ListScopedUsages(at.Add(-time.Second), at.Add(time.Second), 0, "selected", "chosen")
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 1 || usages[0].ID != "chosen" || !usages[0].ProviderCostKnown || usages[0].ProviderCostUSD != 2 {
		t.Fatalf("scoped costs: %+v", usages)
	}
	for _, scope := range []struct{ provider, resource string }{
		{"irrelevant", " chosen,other,chosen "},
		{"selected, absent, selected", ""},
	} {
		items, err := bridge.ListScopedUsages(at.Add(-time.Second), at.Add(time.Second), 0, scope.provider, scope.resource)
		if err != nil || len(items) != 2 {
			t.Fatalf("multi-scope %v: %+v %v", scope, items, err)
		}
	}
}

func TestBillingProviderReimportPreservesSavedPrices(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	original := store.AddProviderModel(ProviderModel{ID: "custom-id", ProviderID: "provider", UpstreamModel: "model", InputPriceUSDPer1M: 3, OutputPriceUSDPer1M: 4})
	zero := 0.0
	patch := providerModelPatchRequest{InputPriceUSDPer1M: &zero, OutputPriceUSDPer1M: &zero, CacheReadPriceUSDPer1M: &zero}
	edited, err := store.UpdateProviderModel(original.ID, patch.withCurrentCosts(original))
	if err != nil {
		t.Fatal(err)
	}
	imported := store.AddProviderModel(ProviderModel{ProviderID: "provider", UpstreamModel: "model", DisplayName: "Refreshed name", InputPriceUSDPer1M: 10, OutputPriceUSDPer1M: 20})
	if imported.ID != edited.ID || imported.InputPriceUSDPer1M != 0 || imported.OutputPriceUSDPer1M != 0 || imported.DisplayName != "Refreshed name" {
		t.Fatalf("reimport replaced saved prices: %+v", imported)
	}
	for _, key := range []string{"input_price_configured", "output_price_configured", cacheReadConfiguredKey} {
		if imported.Metadata[key] != "true" {
			t.Fatalf("free price lost presence for %s", key)
		}
	}
	imported.InputPriceUSDPer1M = 7
	if _, err := store.UpdateProviderModel(imported.ID, imported); err != nil {
		t.Fatal(err)
	}
	again := store.AddProviderModel(ProviderModel{ProviderID: "provider", UpstreamModel: "model"})
	if again.InputPriceUSDPer1M != 7 {
		t.Fatal("reimport lost the explicit price edit")
	}
}
