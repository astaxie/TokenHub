package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankAttributionSettlesValidatedUsageOnDeliveryFailure(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, tc := range []struct {
			name         string
			patch        string
			hookError    bool
			invalidFinal bool
			wantStatus   int
			wantTokens   int64
		}{
			{name: "hook failure", hookError: true, wantStatus: 502, wantTokens: 200},
			{name: "malformed nested patch", patch: `{"retrieval_evidence":{"quantity":-1,"source":"plugin"},"total_tokens":"invalid"}`, wantStatus: 502, wantTokens: 200},
			{name: "negative quantity", patch: `{"retrieval_evidence":{"quantity":-1,"source":"plugin"}}`, wantStatus: 502, wantTokens: 200},
			{name: "wrong billing unit", patch: `{"retrieval_evidence":{"unit":"search_unit","quantity":3,"source":"plugin"}}`, wantStatus: 502, wantTokens: 200},
			{name: "valid attribution", patch: `{"prompt_tokens":400,"total_tokens":400,"retrieval_evidence":{"quantity":400,"source":"plugin"}}`, wantStatus: 200, wantTokens: 400},
			{name: "explicit zero attribution", patch: `{"prompt_tokens":0,"total_tokens":0,"retrieval_evidence":{"quantity":0,"source":"plugin"}}`, wantStatus: 200},
			{name: "valid attribution before invalid final response", patch: `{"prompt_tokens":400,"total_tokens":400,"retrieval_evidence":{"quantity":400,"source":"plugin"}}`, invalidFinal: true, wantStatus: 502, wantTokens: 400},
		} {
			t.Run(tc.name+map[bool]string{false: "/native", true: "/cache"}[cached], func(t *testing.T) {
				upstreamCalls := 0
				app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					upstreamCalls++
					writeJSON(w, http.StatusOK, map[string]any{
						"results": []any{map[string]any{"index": 0, "relevance_score": .9}},
						"usage":   map[string]any{"prompt_tokens": 200, "total_tokens": 200},
					})
				})
				if err := store.db.Model(&APIKey{}).Where("name = ?", "rerank completion").Updates(map[string]any{"limit_daily_tokens": 200, "token_limit_tpm": 1_000_000}).Error; err != nil {
					t.Fatal(err)
				}
				if cached {
					registerRerankAttributionCache(t, app)
				}
				registerRerankAttributionPatch(t, app, tc.patch, tc.hookError)
				if tc.invalidFinal {
					hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-attribution", HookID: "invalid-final", Stage: pluginmeta.StageResponsePost, Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
					registerRerankAttributionHook(t, app, hook, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						return pluginmeta.GatewayHookResult{Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"model":"wrong","results":[{"index":0,"relevance_score":0.9}]}`)}}}, nil
					})
				}
				payload := map[string]any{"model": "rank-complete", "query": "q", "documents": []string{"d"}}
				response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", payload, key)
				if response.Code != tc.wantStatus || upstreamCalls != map[bool]int{false: 1, true: 0}[cached] {
					t.Fatalf("status=%d upstream calls=%d body=%s", response.Code, upstreamCalls, response.Body)
				}
				assertRerankAttributedSettlement(t, store, cached, tc.wantTokens)
				if tc.wantTokens > 0 {
					response = doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", payload, key)
					if response.Code != http.StatusTooManyRequests || upstreamCalls != map[bool]int{false: 1, true: 0}[cached] {
						t.Fatalf("exhausted daily quota was bypassed: status=%d upstream calls=%d body=%s", response.Code, upstreamCalls, response.Body)
					}
				}
			})
		}
	}
}

func assertRerankAttributedSettlement(t *testing.T, store *MemoryStore, cached bool, wantTokens int64) {
	t.Helper()
	var records []UsageRecord
	if err := store.db.Where("model_name = ?", "rank-complete").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	wantCost := float64(wantTokens) / 1_000_000
	wantProviderCost := wantCost
	if cached {
		wantProviderCost = 0
	}
	if len(records) != 1 || records[0].InputTokens != wantTokens || records[0].TotalTokens != wantTokens || records[0].CostUSD != wantCost || records[0].ProviderCostUSD != wantProviderCost {
		t.Fatalf("request settlement differs from accepted usage: %+v", records)
	}
	var counters []QuotaBucket
	if err := store.db.Where("key_id = ?", records[0].APIKeyID).Find(&counters).Error; err != nil {
		t.Fatal(err)
	}
	if len(counters) != 3 {
		t.Fatalf("quota bucket count=%d", len(counters))
	}
	for _, counter := range counters {
		want := wantTokens
		if counter.Scope == "minute" && !cached {
			// TPM retains the aggregate upstream tokens, including failover attempts.
			want = 200
		}
		if counter.TotalTokens != want || counter.Scope != "minute" && counter.CostUSD != wantCost {
			t.Fatalf("%s quota tokens=%d cost=%g, want tokens=%d cost=%g", counter.Scope, counter.TotalTokens, counter.CostUSD, want, wantCost)
		}
	}
	var entry meteringEntry
	if err := store.db.Where("kind = ? AND scope = ?", "shadow_settlement", records[0].RequestID).First(&entry).Error; err != nil {
		t.Fatal(err)
	}
	var settlement struct {
		Tenant   meteringShadowCharge    `json:"tenant"`
		Attempts []meteringAttemptCharge `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(entry.Payload), &settlement); err != nil {
		t.Fatal(err)
	}
	check := func(charge meteringShadowCharge) {
		t.Helper()
		if charge.DocumentCount != 1 || charge.Evidence == nil || charge.Evidence.Quantity == nil || *charge.Evidence.Quantity != wantTokens || charge.Units.Input != wantTokens || charge.LegacyUSD != strconv.FormatFloat(wantCost, 'f', -1, 64) {
			t.Fatalf("durable evidence differs from accepted usage: %s", entry.Payload)
		}
	}
	check(settlement.Tenant)
	if cached {
		if len(settlement.Attempts) != 0 {
			t.Fatalf("cache hit invented supplier attempts: %s", entry.Payload)
		}
		return
	}
	if len(settlement.Attempts) != 1 {
		t.Fatalf("supplier attempt count=%d", len(settlement.Attempts))
	}
	check(settlement.Attempts[0].Charge)
}

func registerRerankAttributionHook(t *testing.T, app *Server, hook pluginmeta.GatewayHookDescriptor, handler pluginmeta.GatewayHookHandlerFunc) {
	t.Helper()
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := app.gatewayHooks.RegisterHandler(hook, handler); err != nil {
		t.Fatal(err)
	}
}

func registerRerankAttributionPatch(t *testing.T, app *Server, patch string, fail bool) {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-attribution", HookID: "attribute", Stage: pluginmeta.StageUsageAttribution, Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	registerRerankAttributionHook(t, app, hook, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if fail {
			return pluginmeta.GatewayHookResult{}, NewHTTPError(http.StatusBadGateway, "fixture_failure", "Fixture attribution failure")
		}
		return pluginmeta.GatewayHookResult{Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataUsage: {Value: json.RawMessage(patch)}}}, nil
	})
}

func registerRerankAttributionCache(t *testing.T, app *Server) {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-attribution", HookID: "cache", Stage: pluginmeta.StageCacheLookup, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey, pluginmeta.DataProviderResponse, pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
	registerRerankAttributionHook(t, app, hook, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataCacheKey:         {Value: input.Data[pluginmeta.DataCacheKey]},
			pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"model":"rank-complete","results":[{"index":0,"relevance_score":0.9}]}`)},
			pluginmeta.DataUsage:            {Value: json.RawMessage(`{"prompt_tokens":200,"total_tokens":200,"retrieval_evidence":{"unit":"token","quantity":200,"source":"plugin"}}`)},
		}}, nil
	})
}

func TestRerankInvalidAttributionPreservesNativeEvidence(t *testing.T) {
	for _, patch := range []string{
		`{"retrieval_evidence":{"quantity":-1,"source":"plugin"},"total_tokens":"invalid"}`,
		`{"retrieval_evidence":{"quantity":-1,"source":"plugin"}}`,
		`{"retrieval_evidence":{"unit":"token","quantity":0,"source":"plugin"}}`,
	} {
		t.Run(patch, func(t *testing.T) {
			app := New(NewMemoryStore())
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
			registerRerankAttributionPatch(t, app, patch, false)
			quantity := int64(3)
			usage := Usage{DocumentCount: 1, RetrievalEvidence: &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &quantity, Source: "upstream"}, RateLimitTokens: 200}
			call := gatewayPluginTestCall()
			call.Model = Model{Modality: "rerank", Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.1"}}
			route := RouteSelection{Provider: Provider{Options: map[string]string{"rerank_protocol": "cohere"}}}
			got, err := app.attributeRerankUsage(context.Background(), call, route, map[string]any{}, usage)
			if err == nil || usage.RetrievalEvidence.Source != "upstream" || quantity != 3 || got.RetrievalEvidence != usage.RetrievalEvidence || got.RateLimitTokens != 200 || priceUsage(call.Model, got).CostUSD != .3 {
				t.Fatalf("invalid override lost native evidence: got=%+v original=%+v quantity=%d error=%v", got, usage, quantity, err)
			}
		})
	}
}
