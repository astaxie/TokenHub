package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRetrievalConfirmedFreeProviderSnapshot(t *testing.T) {
	for _, modality := range []string{"embedding", "rerank"} {
		model := Model{Modality: modality, Metadata: map[string]string{"retrieval_pricing_confirmed": "true"}}
		price := legacyMeteringPrice(model, time.Now(), true)
		usage := retrievalUsage(map[string]any{"usage": map[string]any{"total_tokens": 5}}, false)
		result := shadowPrice(&price, usage, 0)
		if result.Charge == nil || result.Charge.USD != "0.000000000000" {
			t.Fatalf("%s: %+v", modality, result)
		}
		model.Metadata = nil
		price = legacyMeteringPrice(model, time.Now(), true)
		if shadowPrice(&price, usage, 0).Charge != nil {
			t.Fatal("unconfirmed zero became free")
		}
	}
}

func TestRetrievalNativeTenantStatementUsesRecordedPrice(t *testing.T) {
	quantity := int64(2)
	price := meteringPriceSnapshot{Currency: "USD", Source: "legacy_float_configuration", SearchUnitPrice: "0.003"}
	evidence := statementEvidence{Admission: &meteringRequestSnapshot{RequestID: "native", LegacyPrice: &price}}
	raw, _ := json.Marshal(map[string]any{"tenant": meteringShadowCharge{Status: "estimated", LegacyUSD: "0.006", Evidence: &RetrievalUsageEvidence{Unit: "search_unit", Quantity: &quantity, Source: "upstream"}}})
	if err := json.Unmarshal(raw, &evidence.Settlement); err != nil {
		t.Fatal(err)
	}
	out := statementResult{}
	appendTenantStatement(&out, &evidence, "rerank")
	row := out.Rows[0]
	if row.Status != "estimated" || row.Price == nil || len(row.Lines) == 0 {
		t.Fatalf("native statement lost evidence: %+v", row)
	}
	price.SearchUnitPrice = "0.004"
	out.Rows = nil
	appendTenantStatement(&out, &evidence, "rerank")
	if out.Rows[0].Status != "legacy_incomplete" {
		t.Fatal("mismatched recorded charge trusted")
	}
}

func TestRetrievalRouteCanBeDisabledWithoutRepricing(t *testing.T) {
	store := NewMemoryStore()
	model := Model{Name: "old-embedding", Modality: "embedding"}
	route := ModelRoute{ID: "old", ModelName: model.Name, ProviderID: "p", ProviderModel: "m", Status: StatusActive}
	provider := Provider{ID: "p", Type: ProviderOpenAICompatible, Options: map[string]string{"embedding_protocol": "invalid"}}
	store.AddProvider(provider)
	store.AddRoute(route)
	app := New(store)
	route.Status = StatusDisabled
	if err := app.validateRetrievalRoute(route, &model, provider); err != nil {
		t.Fatal(err)
	}
}

func TestInitialEmbeddingRoutesValidateCombinedSpaces(t *testing.T) {
	store := NewMemoryStore()
	routes := []ModelRoute{}
	for _, id := range []string{"a", "b"} {
		store.AddProvider(Provider{ID: id, Type: ProviderOpenAICompatible, Status: StatusActive})
		routes = append(routes, ModelRoute{ModelName: "public", ProviderID: id, ProviderModel: "m", Status: StatusActive})
	}
	app := New(store)
	model := Model{Name: "public", Modality: "embedding"}
	if err := app.validateInitialEmbeddingRoutes(context.Background(), model, routes); err == nil {
		t.Fatal("incompatible batch passed")
	}
	for _, id := range []string{"a", "b"} {
		provider, _ := store.GetProvider(id)
		provider.Options = map[string]string{"embedding_spaces": `{"m":"shared"}`}
		if _, err := store.UpdateProvider(id, provider); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.validateInitialEmbeddingRoutes(context.Background(), model, routes); err != nil {
		t.Fatal(err)
	}
}
