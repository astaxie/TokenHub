package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelPricingSourceUnavailableKeepsDiscoveryWithoutPriceOrIdentity(t *testing.T) {
	store, source := pricingSourceFixture(t)
	model := linkedPricingModel(source)
	store.AddModel(model)
	store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: source.ProviderID, ProviderModel: source.UpstreamModel, Status: StatusActive})
	project := store.CreateProject(Project{Name: "Discovery pricing", Status: StatusActive})
	_, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Discovery key", Status: StatusActive}, "pricing-discovery-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProviderModel(source.ID); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	for _, path := range []string{"/v1/models", "/v1/models/" + model.Name} {
		response := doJSON(t, server.Handler(), http.MethodGet, path, nil, "pricing-discovery-key")
		if response.Code != http.StatusOK || !strings.Contains(response.Body, `"id":"linked-model"`) || !strings.Contains(response.Body, `"pricing_available":false`) {
			t.Fatalf("discovery membership changed: %d %s", response.Code, response.Body)
		}
		for _, private := range []string{source.ID, source.ProviderID, source.UpstreamModel, "input_token_price_per_m", "output_token_price_per_m"} {
			if strings.Contains(response.Body, private) {
				t.Fatalf("unavailable model exposed stale price or source: %s", response.Body)
			}
		}
	}
	recorder := httptest.NewRecorder()
	server.serveAdminModelsGet(recorder, AdminUser{Role: "user"})
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"name":"linked-model"`) || !strings.Contains(body, `"pricing_available":false`) {
		t.Fatalf("scoped discovery membership changed: %d %s", recorder.Code, body)
	}
	for _, private := range []string{source.ID, source.ProviderID, source.UpstreamModel, "input_price_usd_per_1m", "output_price_usd_per_1m", "cache_read_price_usd_per_1m", "pricing_periods"} {
		if strings.Contains(body, private) {
			t.Fatalf("scoped unavailable model exposed stale price or source: %s", body)
		}
	}
	stored := store.ListModels()[0]
	if stored.pricingUnavailable || stored.Metadata[modelPricingProviderKey] != source.ID || stored.InputPriceUSDPer1M != 20 {
		t.Fatalf("projection modified persisted custom data: %+v", stored)
	}
}

func TestModelPricingSourceOrdinaryProjectionPreservesJSON(t *testing.T) {
	model := Model{Name: "custom", Modality: "chat", InputPriceUSDPer1M: 2, Metadata: map[string]string{"display_name": "Custom"}}
	original, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := json.Marshal(modelPricingView{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, projected) {
		t.Fatalf("ordinary model representation changed: %s != %s", original, projected)
	}
	item := buildModelListItem(model)
	type originalModelListItem modelListItem
	original, err = json.Marshal(originalModelListItem(item))
	if err != nil {
		t.Fatal(err)
	}
	projected, err = json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, projected) {
		t.Fatalf("ordinary API model representation changed: %s != %s", original, projected)
	}
	payload := struct {
		Model
		Routes []ModelRoute `json:"routes"`
	}{Model: model, Routes: []ModelRoute{{ProviderID: "provider", ProviderModel: "upstream"}}}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"routes":[`) {
		t.Fatalf("embedded model suppressed sibling create fields: %s", encoded)
	}
}

func TestModelPricingSourceTenantStatementOmitsProcurementReference(t *testing.T) {
	store := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	statementFixture(t, store, "linked-statement", "project-a", at)
	var entry meteringEntry
	if err := store.db.First(&entry, "id = ?", "linked-statement:admission").Error; err != nil {
		t.Fatal(err)
	}
	var admission meteringRequestSnapshot
	if err := json.Unmarshal([]byte(entry.Payload), &admission); err != nil {
		t.Fatal(err)
	}
	reference := &modelPricingReference{ProviderID: "private-provider", ProviderModelID: "private-provider-model", UpstreamModel: "private-upstream-model"}
	admission.LegacyPrice.ProviderReference = reference
	admission.Price.ProviderReference = reference
	payload, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&entry).Update("payload", string(payload)).Error; err != nil {
		t.Fatal(err)
	}
	statement, err := store.BillingStatement(context.Background(), statementTestQuery("tenant"))
	if err != nil {
		t.Fatal(err)
	}
	if len(statement.Rows) != 1 || statement.Rows[0].Price == nil || statement.Rows[0].Price.Rates.Input != "10" {
		t.Fatalf("customer price evidence was lost: %+v", statement.Rows)
	}
	exported, err := json.Marshal(statement)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"provider_reference", reference.ProviderID, reference.ProviderModelID, reference.UpstreamModel} {
		if strings.Contains(string(exported), private) {
			t.Fatalf("customer statement leaks %q: %s", private, exported)
		}
	}
	if err := store.db.First(&entry, "id = ?", "linked-statement:admission").Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.Payload, reference.ProviderModelID) {
		t.Fatal("customer projection rewrote original procurement provenance")
	}
}
