package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"tokenhub/backend/internal/metering"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestChatPricingIgnoresRetrievalClaims(t *testing.T) {
	quantity := int64(1)
	for _, claim := range []struct {
		name     string
		evidence *RetrievalUsageEvidence
	}{
		{"unreported", &RetrievalUsageEvidence{Unit: "token", Source: "unreported"}},
		{"search_unit", &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &quantity, Source: "plugin"}},
	} {
		for _, mode := range []string{"provider", "custom"} {
			t.Run(mode+"/"+claim.name, func(t *testing.T) {
				store, source := pricingSourceFixture(t)
				model := linkedPricingModel(source)
				model.Metadata[modelPricingModeKey] = mode
				model.Metadata[retrievalSearchUnitPriceKey] = "99"
				store.AddModel(model)
				project := store.CreateProject(Project{Name: "Chat retrieval evidence", Status: StatusActive})
				_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Chat evidence key", Status: StatusActive}, "chat-retrieval-evidence-key")
				if err != nil {
					t.Fatal(err)
				}
				store.AddProvider(Provider{ID: "serving", Name: "Serving provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
				store.AddProviderModel(ProviderModel{ProviderID: "serving", UpstreamModel: "served-chat", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 13, Metadata: map[string]string{"pricing_status": "configured", retrievalSearchUnitPriceKey: "77"}})
				store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: "serving", ProviderModel: "served-chat", Status: StatusActive, Weight: 100})
				card, err := store.PublishMeteringCard(meteringRateCard{Kind: "tenant", Target: model.Name, Currency: "USD", Source: "independent-shadow", Rates: metering.Rates{Input: "50", CacheRead: "0", CacheWrite: "0", CacheWrite5m: "0", CacheWrite1h: "0", Output: "0"}})
				if err != nil {
					t.Fatal(err)
				}
				app := New(store)
				t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.chat-retrieval-evidence", HookID: "provider", Stage: pluginmeta.StageProviderCall, Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := app.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					response := map[string]any{"id": "chat-evidence-response", "object": "chat.completion", "model": model.Name, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Synthetic response"}, "finish_reason": "stop"}}}
					return rawProviderCallResult(t, response, Usage{PromptTokens: 1000000, TotalTokens: 1000000, CostUSD: 99, InputCostUSD: 99, RetrievalEvidence: claim.evidence}), nil
				})); err != nil {
					t.Fatal(err)
				}
				response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{"model": model.Name, "messages": []ChatMessage{{Role: "user", Content: "Synthetic request"}}}, secret)
				if response.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", response.Code, response.Body)
				}
				var rows []UsageRecord
				if err := store.db.Where("model_name = ?", model.Name).Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				if len(rows) != 1 {
					t.Fatalf("usage records=%d, want 1", len(rows))
				}
				want := 2.0
				if mode == "custom" {
					want = 99
				}
				row := rows[0]
				if row.CostUSD != want || row.InputCostUSD != want || row.ProviderCostUSD != 13 || row.InputTokens != 1000000 || row.TotalTokens != 1000000 {
					t.Fatalf("unexpected persisted chat charge or token evidence: %+v", row)
				}
				var admission, settlement meteringEntry
				if err := store.db.First(&admission, "id = ?", row.RequestID+":admission").Error; err != nil {
					t.Fatal(err)
				}
				var snapshot meteringRequestSnapshot
				if err := json.Unmarshal([]byte(admission.Payload), &snapshot); err != nil {
					t.Fatal(err)
				}
				if snapshot.Price.Modality != "chat" || snapshot.Price.Version != card.ID {
					t.Fatalf("unexpected shadow snapshot: %+v", snapshot.Price)
				}
				if mode == "provider" && (snapshot.LegacyPrice == nil || snapshot.LegacyPrice.Rates.Input != "2" || snapshot.LegacyPrice.ProviderReference == nil || snapshot.LegacyPrice.ProviderReference.ProviderModelID != source.ID) {
					t.Fatalf("linked source snapshot changed: %+v", snapshot.LegacyPrice)
				}
				if err := store.db.First(&settlement, "id = ?", row.RequestID+":shadow").Error; err != nil {
					t.Fatal(err)
				}
				evidence := statementEvidence{Admission: &snapshot}
				if err := json.Unmarshal([]byte(settlement.Payload), &evidence.Settlement); err != nil {
					t.Fatal(err)
				}
				comparison := evidence.Settlement
				if comparison == nil || len(comparison.Attempts) != 1 {
					t.Fatalf("unexpected shadow attempts: %+v", comparison)
				}
				for _, expected := range []struct {
					charge meteringShadowCharge
					legacy string
					amount string
				}{
					{comparison.Tenant, strconv.FormatFloat(want, 'f', -1, 64), "50.000000000000"},
					{comparison.Attempts[0].Charge, "13", "13.000000000000"},
				} {
					charge := expected.charge
					if charge.Status != "estimated" || charge.LegacyUSD != expected.legacy || charge.Charge == nil || charge.Charge.USD != expected.amount || charge.Units.Input != 1000000 || !reflect.DeepEqual(charge.Evidence, claim.evidence) {
						t.Fatalf("unexpected shadow charge or raw evidence: %+v", charge)
					}
				}
				if mode == "provider" {
					var statement statementResult
					appendTenantStatement(&statement, &evidence, model.Name)
					if len(statement.Rows) != 1 || statement.Rows[0].Status != "estimated" || statement.Rows[0].Price == nil || statement.Rows[0].Price.Rates.Input != "2" {
						t.Fatalf("retrieval evidence diverted the chat statement: %+v", statement.Rows)
					}
				}
			})
		}
	}
}

func TestChatRetrievalEvidenceDoesNotProveTokenPresence(t *testing.T) {
	quantity := int64(1)
	evidence := &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &quantity, Source: "plugin"}
	price := legacyMeteringPrice(Model{Modality: "chat", InputPriceUSDPer1M: 2, Metadata: map[string]string{retrievalSearchUnitPriceKey: "99"}}, time.Time{}, false)
	charge := shadowPrice(&price, Usage{RetrievalEvidence: evidence}, 0)
	if charge.Status != "pending" || charge.Reason != "usage_presence_unknown" || charge.Charge != nil || charge.Evidence != evidence {
		t.Fatalf("retrieval evidence proved missing chat token usage: %+v", charge)
	}
}
