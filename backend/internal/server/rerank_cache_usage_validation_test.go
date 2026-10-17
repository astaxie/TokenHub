package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankCacheRejectsInvalidUsageBeforeAttributionOrSettlement(t *testing.T) {
	for _, patch := range []string{
		`{"retrieval_evidence":{"unit":"search_unit","quantity":3,"source":"plugin"}}`,
		`{"prompt_tokens":200,"total_tokens":200,"retrieval_evidence":{"unit":"token","quantity":300,"source":"plugin"}}`,
		`{"prompt_tokens":200,"total_tokens":200,"retrieval_evidence":{"unit":"token","quantity":200,"source":"unsupported"}}`,
	} {
		t.Run(patch, func(t *testing.T) {
			app, store, key := newRerankCompletionFixture(t, func(http.ResponseWriter, *http.Request) {
				t.Error("cache hit reached upstream")
			})
			if _, err := store.UpdateModel("rank-complete", Model{Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.1", "retrieval_pricing_confirmed": "true"}}); err != nil {
				t.Fatal(err)
			}
			cache := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-cache-usage", HookID: "hit", Stage: pluginmeta.StageCacheLookup, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey, pluginmeta.DataProviderResponse, pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
			registerRerankAttributionHook(t, app, cache, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
					pluginmeta.DataCacheKey:         {Value: input.Data[pluginmeta.DataCacheKey]},
					pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"model":"rank-complete","results":[{"index":0,"relevance_score":0.9}]}`)},
					pluginmeta.DataUsage:            {Value: json.RawMessage(patch)},
				}}, nil
			})
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": "q", "documents": []string{"d"}}, key)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "invalid_provider_usage") {
				t.Fatalf("invalid cache usage was accepted: status=%d body=%s", response.Code, response.Body)
			}
			var record UsageRecord
			if err := store.db.Where("model_name = ?", "rank-complete").First(&record).Error; err != nil {
				t.Fatal(err)
			}
			if record.CostUSD != 0 || record.ProviderCostUSD != 0 {
				t.Fatalf("rejected cache usage was billed: %+v", record)
			}
			var entry meteringEntry
			if err := store.db.Where("kind = ? AND scope = ?", "shadow_settlement", record.RequestID).First(&entry).Error; err != nil {
				t.Fatal(err)
			}
			var settlement struct {
				Tenant   meteringShadowCharge    `json:"tenant"`
				Attempts []meteringAttemptCharge `json:"attempts"`
			}
			if err := json.Unmarshal([]byte(entry.Payload), &settlement); err != nil {
				t.Fatal(err)
			}
			if settlement.Tenant.Charge != nil || settlement.Tenant.Evidence == nil || settlement.Tenant.Evidence.Source != "invalid" || settlement.Tenant.DocumentCount != 1 || len(settlement.Attempts) != 0 {
				t.Fatalf("invalid cache usage was retained as valid evidence: %s", entry.Payload)
			}
		})
	}
}
