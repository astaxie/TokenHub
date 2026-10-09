package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestProviderConfirmedZeroPricesAreKnownInMetering(t *testing.T) {
	usage := Usage{PromptTokens: 10, CachedInputTokens: 2, CompletionTokens: 3, TotalTokens: 13}
	for _, status := range []string{"", "reference", "unverified", "configured"} {
		t.Run("status_"+status, func(t *testing.T) {
			model := providerModelCostModel(ProviderModel{Modality: "chat", Metadata: map[string]string{"pricing_status": status}})
			at := time.Now().UTC()
			price := legacyMeteringPrice(model, at, true)
			charge := shadowPrice(&price, usage, 0)
			known := providerLegacyMeteringKnown(&meteringAttemptSnapshot{LegacyModel: &model, At: at}, usage)
			if status == "configured" {
				if price.Rates.Input != "0" || price.Rates.Output != "0" || price.Rates.CacheRead != "0" || !known || charge.Charge == nil || charge.Charge.USD != "0.000000000000" {
					t.Fatalf("confirmed free rates lost: rates=%+v known=%v charge=%+v", price.Rates, known, charge)
				}
			} else if price.Rates.Input != "" || price.Rates.Output != "" || price.Rates.CacheRead != "" || known || charge.Charge != nil {
				t.Fatalf("unconfirmed zero became a free price: rates=%+v known=%v charge=%+v", price.Rates, known, charge)
			}
		})
	}
}

func TestProviderPriceConfirmationKeepsCacheWriteAndUsageContracts(t *testing.T) {
	model := Model{
		Modality: "chat", InputPriceUSDPer1M: 2, CacheWritePriceUSDPer1M: 3,
		CacheWritePriceConfiguration: CacheWritePriceConfiguration{CacheWritePriceConfigured: true, CacheWrite5mPriceConfigured: true},
		Metadata:                     map[string]string{"pricing_status": "configured"},
	}
	price := legacyMeteringPrice(model, time.Now(), true)
	if price.Rates.Input != "2" || price.Rates.Output != "0" || price.Rates.CacheRead != "0" || price.Rates.CacheWrite != "3" || price.Rates.CacheWrite5m != "0" || price.Rates.CacheWrite1h != "3" {
		t.Fatalf("confirmation changed configured or inherited cache-write rates: %+v", price.Rates)
	}
	mixed := shadowPrice(&price, Usage{PromptTokens: 1000000, CachedInputTokens: 200000, CacheWriteInputTokens: 100000, CacheWrite5mInputTokens: 50000, CompletionTokens: 300000}, 0)
	if mixed.Charge == nil || mixed.Charge.USD != "1.550000000000" {
		t.Fatalf("mixed free and paid components were not priced correctly: %+v", mixed)
	}
	for _, usage := range []Usage{{}, {PromptTokens: 1, CachedInputTokens: 2}} {
		if charge := shadowPrice(&price, usage, 0); charge.Charge != nil {
			t.Fatalf("price confirmation fabricated missing or inconsistent usage: %+v", charge)
		}
	}
}

func TestInventoryConfirmationDoesNotConfirmHiddenRetrievalRates(t *testing.T) {
	for _, modality := range []string{"embedding", "rerank"} {
		model := Model{Modality: modality, Metadata: map[string]string{"pricing_status": "configured"}}
		price := legacyMeteringPrice(model, time.Now(), true)
		if price.Rates.Input != "" {
			t.Fatalf("%s bypassed its explicit retrieval confirmation", modality)
		}
		model.Metadata["retrieval_pricing_confirmed"] = "true"
		price = legacyMeteringPrice(model, time.Now(), true)
		if price.Rates.Input != "0" || price.Rates.Output != "" || price.Rates.CacheRead != "" {
			t.Fatalf("%s confirmed hidden prices: %+v", modality, price.Rates)
		}
	}
}

func TestProviderZeroCostSavePublishesAndPersistsKnownFreeSnapshot(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "confirmed-price", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true})
	inventory := store.AddProviderModel(ProviderModel{
		ProviderID: provider.ID, UpstreamModel: "synthetic-chat", Modality: "chat", Status: StatusActive,
		Metadata: map[string]string{"pricing_status": "unverified", "custom_note": "preserve"},
	})
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	selection := RouteSelection{Provider: provider, ProviderModel: inventory.UpstreamModel}
	at := time.Now().UTC()
	before, err := store.PrepareMeteringAttempt("unconfirmed-request", 1, selection, at)
	if err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app.Handler(), http.MethodPatch, "/api/admin/provider-models/"+inventory.ID, map[string]any{
		"metadata":               map[string]string{"pricing_status": "configured", "custom_note": "preserve"},
		"input_price_usd_per_1m": 0, "output_price_usd_per_1m": 0, "cache_read_price_usd_per_1m": 0,
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("save confirmed zero prices: %d %s", response.Code, response.Body)
	}
	response = doJSON(t, app.Handler(), http.MethodPost, "/api/admin/models", map[string]any{
		"name": "confirmed-free-chat", "family": "synthetic", "modality": "chat", "status": StatusActive,
		"input_price_usd_per_1m": 1, "output_price_usd_per_1m": 2,
		"routes": []ModelRoute{{ProviderID: provider.ID, ProviderModel: inventory.UpstreamModel, Status: StatusActive, Priority: 1, Weight: 100}},
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("publish confirmed free model: %d %s", response.Code, response.Body)
	}
	after, err := store.PrepareMeteringAttempt("confirmed-request", 1, selection, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	usage := Usage{PromptTokens: 10, CachedInputTokens: 2, CompletionTokens: 3, TotalTokens: 13}
	if providerLegacyMeteringKnown(before.MeteringSnapshot, usage) || !providerLegacyMeteringKnown(after.MeteringSnapshot, usage) {
		t.Fatal("confirmation must price new attempts without rewriting an earlier unknown snapshot")
	}
	var entry meteringEntry
	if err := store.db.First(&entry, "id = ?", "confirmed-request:attempt:1").Error; err != nil {
		t.Fatal(err)
	}
	var persisted meteringAttemptSnapshot
	if err := json.Unmarshal([]byte(entry.Payload), &persisted); err != nil {
		t.Fatal(err)
	}
	charge := shadowPrice(persisted.Price, usage, 0)
	if charge.Charge == nil || charge.Charge.USD != "0.000000000000" || charge.Status != "estimated" {
		t.Fatalf("persisted snapshot did not retain confirmed free prices: %+v", charge)
	}
}
