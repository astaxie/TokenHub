package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"tokenhub/backend/internal/metering"
)

func TestMeteringRequestKeepsPublishedPriceAndSettlesShadowOnce(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{})
	old := meteringRateCard{Kind: "tenant", Target: "user-quota-model", Currency: "USD", Source: "test", Rates: metering.Rates{Input: "2", CacheRead: "0.5", CacheWrite: "0", CacheWrite5m: "0", CacheWrite1h: "0", Output: "6"}}
	first, err := store.PublishMeteringCard(old)
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	old.Rates.Input = "20"
	if _, err := store.PublishMeteringCard(old); err != nil {
		t.Fatal(err)
	}
	usage := Usage{PromptTokens: 1000000, CachedInputTokens: 800000, CompletionTokens: 10000}
	for range 2 {
		store.FinishCall(call, RouteSelection{}, usage, 200, "", "", "")
	}
	var entries []meteringEntry
	if err := store.db.Where("scope = ?", call.RequestID).Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected one admission and one settlement, got %d", len(entries))
	}
	var outcome struct {
		Tenant meteringShadowCharge `json:"tenant"`
	}
	for _, entry := range entries {
		if entry.Kind == "shadow_settlement" {
			if err := json.Unmarshal([]byte(entry.Payload), &outcome); err != nil {
				t.Fatal(err)
			}
		}
	}
	if outcome.Tenant.Price == nil || outcome.Tenant.Price.Version != first.ID || outcome.Tenant.Charge == nil || outcome.Tenant.Charge.Amount != "0.860000000000" {
		t.Fatalf("price snapshot changed: %+v", outcome)
	}
}

func TestMeteringProviderSnapshotSurvivesPriceChange(t *testing.T) {
	store := NewMemoryStore()
	provider := ProviderModel{ID: "pm_snapshot", ProviderID: "provider", UpstreamModel: "upstream", Modality: "chat", InputPriceUSDPer1M: 2}
	if err := store.db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	route, err := store.PrepareMeteringAttempt("snapshot-request", 1, RouteSelection{Provider: Provider{ID: "provider"}, ProviderModel: "upstream"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&provider).Update("input_price_usd_per1_m", 20).Error; err != nil {
		t.Fatal(err)
	}
	if cost := store.providerCostUSDAt(route, Usage{PromptTokens: 1000000}, time.Now()); cost != 2 {
		t.Fatalf("snapshot cost = %v", cost)
	}
	var prepared meteringEntry
	if err := store.db.First(&prepared, "id = ?", "snapshot-request:attempt:1").Error; err != nil {
		t.Fatal(err)
	}
	var snapshot meteringAttemptSnapshot
	if err := json.Unmarshal([]byte(prepared.Payload), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "possibly_sent" || snapshot.Price.Rates.Input != "2" {
		t.Fatalf("invalid durable preparation: %+v", snapshot)
	}
}

func TestMeteringRejectsContradictoryUsageAfterLegacyClamp(t *testing.T) {
	usage := priceUsage(Model{}, Usage{PromptTokens: 10, CachedInputTokens: 80})
	price := legacyMeteringPrice(Model{}, time.Now(), false)
	charge := shadowPrice(&price, usage, 0)
	if charge.Status != "pending" || charge.Reason != "inconsistent_usage" {
		t.Fatalf("clamp hid invalid evidence: %+v", charge)
	}
}

func TestMeteringFXAndAttemptEvidenceRemainIndependent(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{})
	fx, err := store.PublishMeteringExchangeRate(meteringExchangeRate{Currency: "CNY", Rate: "0.14", Source: "test fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishMeteringCard(meteringRateCard{Kind: "provider", Target: "provider:upstream", Currency: "CNY", Source: "test fixture", Rates: metering.Rates{Input: "1.5", CacheRead: "0.05", Output: "4.5"}}); err != nil {
		t.Fatal(err)
	}
	provider := ProviderModel{ID: "pm_attempt", ProviderID: "provider", UpstreamModel: "upstream", Modality: "chat", InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 6, CacheReadPriceUSDPer1M: 0.5}
	if err := store.db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	route, err := store.PrepareMeteringAttempt(call.RequestID, 2, RouteSelection{Provider: Provider{ID: "provider"}, ProviderModel: "upstream"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishMeteringExchangeRate(meteringExchangeRate{Currency: "CNY", Rate: "0.28", Source: "changed fixture"}); err != nil {
		t.Fatal(err)
	}
	usage := Usage{PromptTokens: 1000000, CachedInputTokens: 800000, CompletionTokens: 10000, UpstreamRequestID: "upstream-id"}
	call.RouteAttempts = []RouteAttempt{{Selection: route, Invoked: true, Status: 500, Usage: priceUsage(call.Model, usage), StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}}
	store.FinishCall(call, route, Usage{}, 500, "provider_error", "", "")
	rows, err := store.MeteringEvidence(call.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Attempts []meteringAttemptCharge `json:"attempts"`
	}
	for _, row := range rows {
		if row.Kind == "shadow_settlement" {
			if err := json.Unmarshal(row.Data, &result); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(result.Attempts) != 1 {
		t.Fatalf("missing attempt: %+v", result)
	}
	attempt := result.Attempts[0]
	if attempt.Number != 2 || attempt.UpstreamRequestID != "upstream-id" || attempt.Charge.Price.ExchangeRateVersion != fx.ID || attempt.Charge.Charge.USD != "0.053900000000" || attempt.Charge.LegacyUSD != "0.8600000000000001" && attempt.Charge.LegacyUSD != "0.86" {
		t.Fatalf("attempt evidence: %+v", attempt)
	}
}

func TestMeteringEndpointsRejectOrdinaryUsers(t *testing.T) {
	store, _, _, _ := setupUserQuotaTest(t, map[string]any{})
	_, token, err := store.CreateAdminSession("usr_user_quota", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/admin/billing/rate-cards"}, {"POST", "/api/admin/billing/rate-cards"}, {"POST", "/api/admin/billing/preview"}, {"POST", "/api/admin/billing/exchange-rates"}, {"GET", "/api/admin/billing/evidence/request"},
	} {
		response := doJSON(t, app, tc.method, tc.path, map[string]any{}, token.Token)
		if response.Code != 403 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
		}
	}
}
