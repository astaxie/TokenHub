package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankProviderHookResponseFailureRetainsValidatedUsage(t *testing.T) {
	quantity := int64(3)
	tests := []struct {
		name            string
		protocol        string
		usage           *Usage
		usageRaw        json.RawMessage
		wantTokens      int64
		wantSearch      int64
		wantCost        float64
		wantAttemptCost float64
		wantQuota       int64
	}{
		{
			name:            "token usage on oversized response",
			protocol:        "jina",
			usage:           &Usage{PromptTokens: 7, TotalTokens: 7},
			wantTokens:      7,
			wantCost:        0.000007,
			wantAttemptCost: 0.000007,
			wantQuota:       7,
		},
		{
			name:            "native search usage on malformed response",
			protocol:        "cohere",
			usage:           &Usage{RetrievalEvidence: &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &quantity, Source: "plugin"}},
			wantSearch:      3,
			wantCost:        0.003,
			wantAttemptCost: 0.009,
			wantQuota:       2, // Native search units reserve the request token bound.
		},
		{
			name:      "missing usage is unpriced",
			protocol:  "jina",
			wantQuota: 2,
		},
		{
			name:     "inconsistent usage is rejected",
			protocol: "jina",
			usageRaw: json.RawMessage(`{"prompt_tokens":7,"total_tokens":8}`),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstreamCalls := 0
			app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls++
				_, _ = io.WriteString(w, `{"results":[]}`)
			})
			provider := store.ListProviders()[0]
			for _, candidate := range store.ListProviders() {
				if candidate.ID == "rank-complete" {
					provider = candidate
				}
			}
			provider.Options["rerank_protocol"] = tc.protocol
			store.AddProvider(provider)
			model := Model{Name: "rank-complete", Modality: "rerank", Status: StatusActive, InputPriceUSDPer1M: 1, Metadata: map[string]string{"retrieval_pricing_confirmed": "true"}}
			providerModel := ProviderModel{ProviderID: "rank-complete", UpstreamModel: "upstream", Modality: "rerank", InputPriceUSDPer1M: 1}
			if tc.protocol == "cohere" {
				model.Metadata[retrievalSearchUnitPriceKey] = "0.003"
				providerModel.Metadata = map[string]string{retrievalSearchUnitPriceKey: "0.001"}
			}
			store.AddModel(model)
			store.AddProviderModel(providerModel)
			var configuredKey APIKey
			if err := store.db.Where("name = ?", "rerank completion").First(&configuredKey).Error; err != nil {
				t.Fatal(err)
			}
			tokenLimit := int64(1_000_000)
			if _, err := store.UpdateAPIKey(configuredKey.ID, APIKey{TokenLimitSet: true, TokenLimitTPM: &tokenLimit}); err != nil {
				t.Fatal(err)
			}

			hook := pluginmeta.GatewayHookDescriptor{
				PluginID: "test.rerank-response-failure-usage", HookID: "output", Stage: pluginmeta.StageProviderCall,
				Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
				FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			}
			if err := app.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			response := `{"results":[{"index":0,"relevance_score":0.9}]}`
			if tc.name == "token usage on oversized response" {
				response += strings.Repeat(" ", maxRerankResponseBytes)
			} else {
				response = `{"results":`
			}
			if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
					pluginmeta.DataProviderResponse: {Value: json.RawMessage(response)},
				}
				if tc.usage != nil {
					raw, err := json.Marshal(tc.usage)
					if err != nil {
						t.Fatal(err)
					}
					writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: raw}
				} else if tc.usageRaw != nil {
					writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: tc.usageRaw}
				}
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
			})); err != nil {
				t.Fatal(err)
			}

			result := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": "q", "documents": []string{"d"}}, key)
			if result.Code != http.StatusBadGateway || upstreamCalls != 0 {
				t.Fatalf("response failure or fallback changed: status=%d upstream_calls=%d body=%s", result.Code, upstreamCalls, result.Body)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Where("provider_id = ?", "rank-complete").Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 || attempts[0].TotalTokens != tc.wantTokens || attempts[0].CostUSD != tc.wantAttemptCost {
				t.Fatalf("attempt usage=%+v want tokens=%d cost=%g", attempts, tc.wantTokens, tc.wantAttemptCost)
			}
			if tc.wantSearch > 0 && attempts[0].InputTokens != 0 {
				t.Fatalf("native search usage became token usage: %+v", attempts[0])
			}
			var entry meteringEntry
			if err := store.db.First(&entry, "id = ?", attempts[0].RequestID+":shadow").Error; err != nil {
				t.Fatal(err)
			}
			var settlement struct {
				Attempts []meteringAttemptCharge `json:"attempts"`
			}
			if err := json.Unmarshal([]byte(entry.Payload), &settlement); err != nil || len(settlement.Attempts) != 1 {
				t.Fatalf("invalid provider settlement: %s, %v", entry.Payload, err)
			}
			if tc.wantCost == 0 {
				if settlement.Attempts[0].Charge.Charge != nil {
					t.Fatalf("unreported or invalid usage received a charge: %+v", settlement.Attempts[0].Charge)
				}
			} else if settlement.Attempts[0].Charge.Charge == nil || settlement.Attempts[0].Charge.Charge.USD != formatReviewRerankCharge(tc.wantCost) {
				t.Fatalf("provider charge=%+v want=%s", settlement.Attempts[0].Charge.Charge, formatReviewRerankCharge(tc.wantCost))
			}
			var counters []QuotaBucket
			var apiKey APIKey
			if err := store.db.Where("name = ?", "rerank completion").First(&apiKey).Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Where("key_id = ? AND scope = ?", apiKey.ID, "minute").Find(&counters).Error; err != nil {
				t.Fatal(err)
			}
			if len(counters) != 1 || counters[0].TotalTokens != tc.wantQuota {
				t.Fatalf("minute quota=%+v want tokens=%d", counters, tc.wantQuota)
			}
		})
	}
}

func formatReviewRerankCharge(value float64) string {
	return strconv.FormatFloat(value, 'f', 12, 64)
}
