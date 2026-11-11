package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"

	"tokenhub/backend/internal/metering"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestModelPricingSourceOverridesProviderReportedCharges(t *testing.T) {
	for _, initialPrice := range []float64{2, 0} {
		name := "paid_source"
		if initialPrice == 0 {
			name = "free_source"
		}
		t.Run(name, func(t *testing.T) {
			store, source := pricingSourceFixture(t)
			source.InputPriceUSDPer1M = initialPrice
			source.OutputPriceUSDPer1M = initialPrice * 3
			store.AddProviderModel(source)
			model := linkedPricingModel(source)
			store.AddModel(model)
			project := store.CreateProject(Project{Name: "Linked price authority", Status: StatusActive})
			_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Linked price key", Status: StatusActive}, "linked-price-authority-key")
			if err != nil {
				t.Fatal(err)
			}
			store.AddProvider(Provider{ID: "serving-provider", Name: "Serving provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
			store.AddProviderModel(ProviderModel{ProviderID: "serving-provider", UpstreamModel: "served-chat", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 13, Metadata: map[string]string{"pricing_status": "configured"}})
			store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: "serving-provider", ProviderModel: "served-chat", Status: StatusActive, Weight: 100})
			card, err := store.PublishMeteringCard(meteringRateCard{Kind: "tenant", Target: model.Name, Currency: "USD", Source: "independent-shadow", Rates: metering.Rates{Input: "50", CacheRead: "0", CacheWrite: "0", CacheWrite5m: "0", CacheWrite1h: "0", Output: "0"}})
			if err != nil {
				t.Fatal(err)
			}
			app := New(store)
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.linked-pricing", HookID: "provider", Stage: pluginmeta.StageProviderCall, Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := app.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				// An edit after admission must affect only the next request.
				source.InputPriceUSDPer1M = 8
				store.AddProviderModel(source)
				response := map[string]any{"id": "linked-price-response", "object": "chat.completion", "model": model.Name, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Synthetic response"}, "finish_reason": "stop"}}}
				return rawProviderCallResult(t, response, Usage{PromptTokens: 1000000, TotalTokens: 1000000, InputCostUSD: 99, CostUSD: 99}), nil
			})); err != nil {
				t.Fatal(err)
			}
			for index, want := range []float64{initialPrice, 8} {
				response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{"model": model.Name, "messages": []ChatMessage{{Role: "user", Content: "Synthetic request"}}}, secret)
				if response.Code != http.StatusOK {
					t.Fatalf("request %d: status=%d body=%s", index, response.Code, response.Body)
				}
				var rows []UsageRecord
				if err := store.db.Where("model_name = ?", model.Name).Order("created_at asc").Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				if len(rows) != index+1 {
					t.Fatalf("usage records=%d, want %d", len(rows), index+1)
				}
				row := rows[index]
				if row.CostUSD != want || row.InputCostUSD != want || row.ProviderCostUSD != 13 {
					t.Fatalf("request %d: tenant=%v input=%v provider=%v, want %v/%v/13", index, row.CostUSD, row.InputCostUSD, row.ProviderCostUSD, want, want)
				}
				var admission, settlement meteringEntry
				if err := store.db.First(&admission, "id = ?", row.RequestID+":admission").Error; err != nil {
					t.Fatal(err)
				}
				var snapshot meteringRequestSnapshot
				if err := json.Unmarshal([]byte(admission.Payload), &snapshot); err != nil {
					t.Fatal(err)
				}
				wantRate := strconv.FormatFloat(want, 'f', -1, 64)
				if snapshot.LegacyPrice == nil || snapshot.LegacyPrice.Rates.Input != wantRate || snapshot.LegacyPrice.ProviderReference == nil || snapshot.LegacyPrice.ProviderReference.ProviderModelID != source.ID || snapshot.Price.Version != card.ID {
					t.Fatalf("request %d: unexpected frozen pricing evidence: %+v", index, snapshot)
				}
				if err := store.db.First(&settlement, "id = ?", row.RequestID+":shadow").Error; err != nil {
					t.Fatal(err)
				}
				var comparison struct {
					Tenant meteringShadowCharge `json:"tenant"`
				}
				if err := json.Unmarshal([]byte(settlement.Payload), &comparison); err != nil {
					t.Fatal(err)
				}
				if comparison.Tenant.LegacyUSD != wantRate || comparison.Tenant.Charge == nil || comparison.Tenant.Charge.Amount != "50.000000000000" {
					t.Fatalf("request %d: actual or independent shadow charge changed: %+v", index, comparison.Tenant)
				}
			}
		})
	}
}

func TestModelPricingSourceRepricesAllBucketsAtAdmissionBoundary(t *testing.T) {
	store, source := pricingSourceFixture(t)
	zero, one, two, three, four := 0.0, 1.0, 2.0, 3.0, 4.0
	source.InputPriceUSDPer1M, source.CacheReadPriceUSDPer1M, source.OutputPriceUSDPer1M = 2, 0.25, 8
	source.CacheWritePriceUSDPer1M, source.CacheWrite5mPriceUSDPer1M, source.CacheWrite1hPriceUSDPer1M = 4, 5, 6
	source.PricingPeriods = []ModelPricingPeriod{{Name: "discount", Timezone: "UTC", StartTime: "08:00", EndTime: "09:00", InputPriceUSDPer1M: &one, CacheReadPriceUSDPer1M: &zero, CacheWritePriceUSDPer1M: &two, CacheWrite5mPriceUSDPer1M: &three, CacheWrite1hPriceUSDPer1M: &four, OutputPriceUSDPer1M: &four}}
	store.AddProviderModel(source)
	resolved, err := resolveModelPricingSource(store.db, linkedPricingModel(source))
	if err != nil {
		t.Fatal(err)
	}
	usage := Usage{PromptTokens: 1000000, CachedInputTokens: 200000, CacheWriteInputTokens: 300000, CacheWrite5mInputTokens: 100000, CacheWrite1hInputTokens: 100000, CompletionTokens: 500000, CostUSD: 99, InputCostUSD: 99, CacheReadCostUSD: 99, CacheWriteCostUSD: 99, OutputCostUSD: 99}
	for _, tc := range []struct {
		at                         string
		input, read, write, output float64
	}{
		{"2026-11-11T07:59:59Z", 1, 0.05, 1.5, 4},
		{"2026-11-11T08:00:00Z", 0.5, 0, 0.9, 2},
		{"2026-11-11T08:59:59Z", 0.5, 0, 0.9, 2},
		{"2026-11-11T09:00:00Z", 1, 0.05, 1.5, 4},
	} {
		t.Run(tc.at, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			priced := priceUsageAt(resolved, usage, at)
			for _, pair := range [][2]float64{{priced.InputCostUSD, tc.input}, {priced.CacheReadCostUSD, tc.read}, {priced.CacheWriteCostUSD, tc.write}, {priced.OutputCostUSD, tc.output}, {priced.CostUSD, tc.input + tc.read + tc.write + tc.output}} {
				if math.Abs(pair[0]-pair[1]) > 1e-12 {
					t.Fatalf("price=%v, want %v; usage=%+v", pair[0], pair[1], priced)
				}
			}
			repriced := priceUsageAt(resolved, priced, at)
			if repriced.CostUSD != priced.CostUSD || repriced.MeteringRaw != priced.MeteringRaw || repriced.TotalTokens != priced.TotalTokens {
				t.Fatalf("repeated pricing changed charge or usage evidence: once=%+v twice=%+v", priced, repriced)
			}
		})
	}
}

func TestCustomModelPricingKeepsExplicitPluginCharge(t *testing.T) {
	model := Model{Modality: "chat", InputPriceUSDPer1M: 2, Metadata: map[string]string{modelPricingModeKey: "custom"}}
	usage := priceUsage(model, Usage{PromptTokens: 1000000, CostUSD: 99, InputCostUSD: 99})
	if usage.CostUSD != 99 || usage.InputCostUSD != 99 {
		t.Fatalf("custom model explicit charge changed: %+v", usage)
	}
}
