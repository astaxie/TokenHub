package server

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"tokenhub/backend/internal/metering"
)

func TestModelPricePatchRejectsNegativeBasePrices(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close test store: %v", err)
		}
	})
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	original := store.ListModels()[0]
	original.Modality = "chat"
	original.InputPriceUSDPer1M = 10
	original.CacheReadPriceUSDPer1M = 2
	if _, err := store.UpdateModel(original.Name, original); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, field := range []string{"input_price_usd_per_1m", "output_price_usd_per_1m", "embedding_price_usd_per_1m", "cache_read_price_usd_per_1m", "cache_write_price_usd_per_1m", "cache_write_5m_price_usd_per_1m", "cache_write_1h_price_usd_per_1m"} {
		t.Run(field, func(t *testing.T) {
			response := doJSON(t, app, "PATCH", "/api/admin/models/"+url.PathEscape(original.Name), map[string]any{field: -1}, "")
			if response.Code != 400 {
				t.Fatalf("negative price accepted: %d %s", response.Code, response.Body)
			}
		})
	}
	for _, model := range store.ListModels() {
		if model.Name == original.Name && (model.CacheReadPriceUSDPer1M != 2 || model.InputPriceUSDPer1M != 10) {
			t.Fatalf("rejected patch changed prices: %+v", model)
		}
	}
	// Existing invalid metadata must not turn a legacy negative value into a credit.
	usage := priceUsage(Model{InputPriceUSDPer1M: 10, CacheReadPriceUSDPer1M: -1, Metadata: map[string]string{cacheReadConfiguredKey: "true"}}, Usage{PromptTokens: 1000000, CachedInputTokens: 1000000})
	if usage.CostUSD < 0 {
		t.Fatalf("negative cost: %v", usage.CostUSD)
	}
}

func TestMeteringDiscoveredPricesRemainUnknownUntilExplicit(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{})
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close test store: %v", err)
		}
	})
	provider := providerModelFromCatalog("provider", ProviderCatalogModel{ID: "unpriced"})
	provider.ID = "pm_unpriced"
	if err := store.db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	for _, explicit := range []bool{false, true} {
		if explicit {
			_, err := store.PublishMeteringCard(meteringRateCard{Kind: "provider", Target: "provider:unpriced", Currency: "USD", Source: "explicit free fixture", Rates: metering.Rates{Input: "0"}})
			if err != nil {
				t.Fatal(err)
			}
		}
		call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
		if err != nil {
			t.Fatal(err)
		}
		route, err := store.PrepareMeteringAttempt(call.RequestID, 1, RouteSelection{Provider: Provider{ID: "provider"}, ProviderModel: "unpriced"}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		usage := Usage{PromptTokens: 1000000}
		call.RouteAttempts = []RouteAttempt{{Selection: route, Invoked: true, Status: 200, Usage: priceUsage(call.Model, usage), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}}
		store.FinishCall(call, route, usage, 200, "", "", "")
		rows, err := store.MeteringEvidence(call.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		var settled struct {
			Attempts []meteringAttemptCharge `json:"attempts"`
		}
		for _, row := range rows {
			if row.Kind == "shadow_settlement" {
				if err := json.Unmarshal(row.Data, &settled); err != nil {
					t.Fatal(err)
				}
			}
		}
		if len(settled.Attempts) != 1 {
			t.Fatalf("missing settlement: %+v", rows)
		}
		charge := settled.Attempts[0].Charge
		if charge.LegacyUSD != "" {
			t.Fatalf("missing legacy price became known: %+v", charge)
		}
		if explicit {
			if charge.Charge == nil || charge.Charge.Amount != "0.000000000000" {
				t.Fatalf("explicit free price lost: %+v", charge)
			}
		} else if charge.Status != "pending" || charge.Charge != nil {
			t.Fatalf("missing price became free: %+v", charge)
		}
	}
}

func TestMeteringProviderLegacyZerosRequireEvidencePerBucket(t *testing.T) {
	zero := 0.0
	for _, tc := range []struct {
		name    string
		model   Model
		usage   Usage
		pending bool
	}{
		{"unconfigured_cache", Model{InputPriceUSDPer1M: 2}, Usage{PromptTokens: 100, CachedInputTokens: 100}, true},
		{"unconfigured_output", Model{InputPriceUSDPer1M: 2}, Usage{PromptTokens: 100, CompletionTokens: 1}, true},
		{"inherited_write_unknown", Model{}, Usage{PromptTokens: 100, CacheWriteInputTokens: 100}, true},
		{"inherited_write_known", Model{InputPriceUSDPer1M: 2}, Usage{PromptTokens: 100, CacheWriteInputTokens: 100}, false},
		{"configured_free_write", Model{CacheWritePriceConfiguration: CacheWritePriceConfiguration{CacheWritePriceConfigured: true}}, Usage{PromptTokens: 100, CacheWriteInputTokens: 100}, false},
		{"explicit_free_period", Model{PricingPeriods: []ModelPricingPeriod{{InputPriceUSDPer1M: &zero, OutputPriceUSDPer1M: &zero, CacheReadPriceUSDPer1M: &zero}}}, Usage{PromptTokens: 100, CachedInputTokens: 50, CompletionTokens: 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price := legacyMeteringPrice(tc.model, time.Now().UTC(), true)
			charge := shadowPrice(&price, tc.usage, 0)
			if (charge.Status == "pending") != tc.pending {
				t.Fatalf("unexpected pricing evidence: %+v", charge)
			}
		})
	}
}
