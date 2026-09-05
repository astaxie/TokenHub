package server

import (
	"encoding/json"
	"math"
	"net/url"
	"testing"
	"time"
)

func TestTimeCachePricingUsesWeekdaysAndEveryInputBucket(t *testing.T) {
	var model Model
	err := json.Unmarshal([]byte(`{
		"modality":"chat", "input_price_usd_per_1m":1.5,
		"cache_read_price_usd_per_1m":0.05, "output_price_usd_per_1m":4.5,
		"pricing_periods":[{"name":"peak","timezone":"Asia/Shanghai",
		"weekdays":[1,2,3,4,5],"start_time":"09:00","end_time":"12:00",
		"input_price_usd_per_1m":3,"cache_read_price_usd_per_1m":0.1,
		"output_price_usd_per_1m":9}]
	}`), &model)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		at   string
		want float64
	}{
		{"2026-09-04T09:00:00+08:00", .77},
		{"2026-09-04T12:00:00+08:00", .385},
		{"2026-09-05T09:00:00+08:00", .385},
	} {
		at, err := time.Parse(time.RFC3339, test.at)
		if err != nil {
			t.Fatal(err)
		}
		got := priceUsageAt(model, Usage{PromptTokens: 1000000, CachedInputTokens: 800000, CompletionTokens: 10000}, at)
		if math.Abs(got.CostUSD-test.want) > 1e-12 {
			t.Errorf("at %s: cost=%v, want %v", test.at, got.CostUSD, test.want)
		}
	}
}

func TestTimeCachePricingValidatesAmbiguousSchedules(t *testing.T) {
	for _, raw := range []string{
		`[{"weekdays":[7]}]`,
		`[{"weekdays":[1,1]}]`,
		`[{"effective_from":"2026-09-06T00:00:00Z","effective_until":"2026-09-05T00:00:00Z"}]`,
		`[{"weekdays":[5],"start_time":"23:00","end_time":"02:00"},{"weekdays":[6],"start_time":"01:00","end_time":"03:00"}]`,
		`[{"cache_read_price_usd_per_1m":-1}]`,
	} {
		var periods []ModelPricingPeriod
		if err := json.Unmarshal([]byte(raw), &periods); err != nil {
			t.Fatal(err)
		}
		if err := validateModelPricingPeriods(periods); err == nil {
			t.Errorf("accepted invalid periods: %s", raw)
		}
	}
}

func TestTimeCachePricingCrossMidnightAndFreeOverrides(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{"input_price_usd_per_1m":10,"pricing_periods":[{"weekdays":[5],"timezone":"Asia/Shanghai","start_time":"23:00","end_time":"02:00","cache_read_price_usd_per_1m":0,"cache_write_price_usd_per_1m":2,"cache_write_5m_price_usd_per_1m":3,"cache_write_1h_price_usd_per_1m":4}]}`), &model); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at   string
		want float64
	}{
		{"2026-09-05T01:59:59+08:00", 200.0 / 1e6},
		{"2026-09-05T02:00:00+08:00", 640.0 / 1e6},
	} {
		at, _ := time.Parse(time.RFC3339, tc.at)
		got := priceUsageAt(model, Usage{PromptTokens: 100, CachedInputTokens: 40, CacheWriteInputTokens: 60, CacheWrite5mInputTokens: 20, CacheWrite1hInputTokens: 30}, at)
		if math.Abs(got.CostUSD-tc.want) > 1e-12 {
			t.Errorf("%s: got %v want %v", tc.at, got.CostUSD, tc.want)
		}
	}
}

func TestModelPricePatchPreservesOmittedFieldsAndExplicitFreeCache(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	models := store.ListModels()
	if len(models) == 0 {
		t.Fatal("missing seeded models")
	}
	original := models[0]
	original.InputPriceUSDPer1M = 10
	original.OutputPriceUSDPer1M = 20
	original.CacheReadPriceUSDPer1M = 2
	if _, err := store.UpdateModel(original.Name, original); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	resp := doJSON(t, app, "PATCH", "/api/admin/models/"+url.PathEscape(original.Name), map[string]any{"cache_read_price_usd_per_1m": 0, "metadata": map[string]string{"display_name": "Free cache model"}}, "")
	if resp.Code != 200 {
		t.Fatalf("patch: %d %s", resp.Code, resp.Body)
	}
	var updated Model
	if err := json.Unmarshal([]byte(resp.Body), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.InputPriceUSDPer1M != 10 || updated.OutputPriceUSDPer1M != 20 {
		t.Fatalf("omitted prices changed: %+v", updated)
	}
	resp = doJSON(t, app, "PATCH", "/api/admin/models/"+url.PathEscape(original.Name), map[string]any{"metadata": map[string]string{"display_name": "Renamed free cache model"}}, "")
	if resp.Code != 200 {
		t.Fatalf("metadata-only patch: %d %s", resp.Code, resp.Body)
	}
	for _, model := range store.ListModels() {
		if model.Name == original.Name {
			got := priceUsage(model, Usage{PromptTokens: 90, CachedInputTokens: 80})
			if math.Abs(got.CostUSD-0.0001) > 1e-12 {
				t.Fatalf("free cache did not survive persistence: %+v", got)
			}
		}
	}
	resp = doJSON(t, app, "PATCH", "/api/admin/models/"+url.PathEscape(original.Name), map[string]any{"cache_read_price_usd_per_1m": nil}, "")
	if resp.Code != 200 {
		t.Fatal(resp.Body)
	}
	updated = Model{}
	if err := json.Unmarshal([]byte(resp.Body), &updated); err != nil {
		t.Fatal(err)
	}
	if got := effectiveCacheReadPriceUSDPer1M(updated); got != 1 {
		t.Fatalf("clear cache override: got %v", got)
	}
}
