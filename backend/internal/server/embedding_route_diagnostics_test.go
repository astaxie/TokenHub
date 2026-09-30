package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEmbeddingRouteDiagnosticsAggregateWithoutLeakingConfiguration(t *testing.T) {
	store := NewMemoryStore()
	app := New(store)
	defer func() { _ = app.Shutdown(context.Background()) }()
	provider := Provider{ID: "private-provider-id", Type: ProviderOpenAICompatible, BaseURL: "https://private.example/v1", APIKey: "private-credential"}
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "private-chat-model", Modality: "chat", InputPriceUSDPer1M: 1})
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "private-valid-model", Modality: "embedding", InputPriceUSDPer1M: 1})
	call := CallContext{Model: Model{Modality: "embedding", EmbeddingPriceUSDPer1M: 1}}
	routes := []RouteSelection{
		{Provider: provider, ProviderModel: "private-missing-model"},
		{Provider: provider, ProviderModel: "private-chat-model"},
		{Provider: provider, ProviderModel: "private-missing-model"},
	}
	filtered, reasons := app.retrievalRoutesWithDiagnostics(call, routes, "embedding", providerRouteProtocolEmbeddings)
	if len(filtered) != 0 || !reflect.DeepEqual(reasons, map[string]int{"upstream_model_inventory_missing": 2, "upstream_model_modality_mismatch": 1}) {
		t.Fatalf("unexpected filtering: %v %v", filtered, reasons)
	}
	err := embeddingRouteSelectionError(reasons)
	raw, marshalErr := json.Marshal(err.Details)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(raw)+err.Message, "private-") || strings.Contains(string(raw)+err.Message, "private.example") {
		t.Fatal("diagnostic disclosed provider configuration")
	}
	reverse := []RouteSelection{routes[2], routes[1], routes[0]}
	_, reversedReasons := app.retrievalRoutesWithDiagnostics(call, reverse, "embedding", providerRouteProtocolEmbeddings)
	reversedErr := embeddingRouteSelectionError(reversedReasons)
	if err.Message != reversedErr.Message || !reflect.DeepEqual(err.Details, reversedErr.Details) {
		t.Fatal("diagnostic depends on routing order")
	}
	valid := RouteSelection{Provider: provider, ProviderModel: "private-valid-model"}
	routes = append(routes, valid)
	filtered, _ = app.retrievalRoutesWithDiagnostics(call, routes, "embedding", providerRouteProtocolEmbeddings)
	if !reflect.DeepEqual(filtered, []RouteSelection{valid}) {
		t.Fatal("invalid route blocked eligible fallback")
	}
}

func TestEmbeddingRouteDiagnosticsNoCandidates(t *testing.T) {
	err := embeddingRouteSelectionError(nil)
	if err.Status != 501 || err.Code != "provider_capability_not_supported" || !strings.Contains(err.Message, "check route availability") {
		t.Fatalf("unexpected error: %+v", err)
	}
	raw, marshalErr := json.Marshal(err.Details)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if !strings.Contains(string(raw), `"reasons":[]`) {
		t.Fatalf("expected empty reason list: %s", raw)
	}
}
