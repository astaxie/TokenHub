package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/metering"
)

func pricingSourceFixture(t *testing.T) (*GormStore, ProviderModel) {
	t.Helper()
	store := NewMemoryStore()
	store.AddProvider(Provider{ID: "pricing-supplier", Name: "Pricing supplier", Type: ProviderOpenAICompatible, Status: StatusActive})
	source := store.AddProviderModel(ProviderModel{ProviderID: "pricing-supplier", UpstreamModel: "wholesale-chat", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 6, Metadata: map[string]string{"pricing_status": "configured"}})
	return store, source
}

func linkedPricingModel(source ProviderModel) Model {
	return Model{Name: "linked-model", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 20, OutputPriceUSDPer1M: 60, Metadata: map[string]string{modelPricingModeKey: "provider", modelPricingProviderKey: source.ID}}
}

func TestModelPricingSourceValidatesCreateAndPatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*GormStore, *ProviderModel, *Model)
	}{
		{"invalid_mode", func(_ *GormStore, _ *ProviderModel, model *Model) { model.Metadata[modelPricingModeKey] = "automatic" }},
		{"missing_reference", func(_ *GormStore, _ *ProviderModel, model *Model) { delete(model.Metadata, modelPricingProviderKey) }},
		{"unknown_reference", func(_ *GormStore, _ *ProviderModel, model *Model) {
			model.Metadata[modelPricingProviderKey] = "missing"
		}},
		{"unsupported_model", func(_ *GormStore, _ *ProviderModel, model *Model) { model.Modality = "audio" }},
		{"incompatible_reference", func(store *GormStore, source *ProviderModel, _ *Model) {
			source.Modality = "embedding"
			store.AddProviderModel(*source)
		}},
		{"unconfirmed_reference", func(store *GormStore, source *ProviderModel, _ *Model) {
			source.Metadata["pricing_status"] = "unverified"
			store.AddProviderModel(*source)
		}},
		{"disabled_reference", func(store *GormStore, source *ProviderModel, _ *Model) {
			source.Status = StatusDisabled
			store.AddProviderModel(*source)
		}},
		{"disabled_provider", func(store *GormStore, _ *ProviderModel, _ *Model) {
			store.AddProvider(Provider{ID: "pricing-supplier", Type: ProviderOpenAICompatible, Status: StatusDisabled})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, source := pricingSourceFixture(t)
			model := linkedPricingModel(source)
			test.mutate(store, &source, &model)
			app := New(store).Handler()
			response := doJSON(t, app, http.MethodPost, "/api/admin/models", model, "")
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_model_pricing_source") {
				t.Fatalf("create returned %d: %s", response.Code, response.Body)
			}
			if len(store.ListModels()) != 0 {
				t.Fatal("invalid create persisted a model")
			}
			store.AddModel(Model{Name: model.Name, Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 7})
			response = doJSON(t, app, http.MethodPatch, "/api/admin/models/"+model.Name, model, "")
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_model_pricing_source") {
				t.Fatalf("patch returned %d: %s", response.Code, response.Body)
			}
			if stored := store.ListModels()[0]; stored.InputPriceUSDPer1M != 7 || stored.Metadata[modelPricingModeKey] != "" {
				t.Fatalf("invalid patch changed model: %+v", stored)
			}
		})
	}
}

func TestModelPricingSourcePreservesCustomPricesAndMetadataEdits(t *testing.T) {
	store, source := pricingSourceFixture(t)
	custom := linkedPricingModel(source)
	custom.CacheReadPriceUSDPer1M = 4
	custom.PricingPeriods = []ModelPricingPeriod{{Name: "custom-policy", Timezone: "UTC", StartTime: "08:00", EndTime: "09:00"}}
	app := New(store).Handler()
	response := doJSON(t, app, http.MethodPost, "/api/admin/models", custom, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	response = doJSON(t, app, http.MethodPatch, "/api/admin/models/linked-model", map[string]any{"metadata": map[string]string{"display_name": "Renamed"}}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", response.Code, response.Body)
	}
	saved := store.ListModels()[0]
	if saved.Metadata[modelPricingModeKey] != "provider" || saved.Metadata[modelPricingProviderKey] != source.ID || saved.InputPriceUSDPer1M != 20 || !reflect.DeepEqual(saved.PricingPeriods, custom.PricingPeriods) {
		t.Fatalf("unrelated edit changed pricing: %+v", saved)
	}
	refreshed := saved
	refreshed.Metadata = map[string]string{"source": "tokenhub-standard-catalog"}
	if _, err := store.CreateModelWithRoutes(refreshed, nil); err != nil {
		t.Fatal(err)
	}
	saved = store.ListModels()[0]
	if saved.Metadata[modelPricingModeKey] != "provider" || saved.Metadata[modelPricingProviderKey] != source.ID {
		t.Fatalf("catalog refresh removed pricing policy: %+v", saved.Metadata)
	}
	response = doJSON(t, app, http.MethodPatch, "/api/admin/models/linked-model", map[string]any{"metadata": map[string]string{modelPricingModeKey: "custom"}}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("custom: %d %s", response.Code, response.Body)
	}
	saved = store.ListModels()[0]
	if saved.Metadata[modelPricingProviderKey] != "" || saved.InputPriceUSDPer1M != 20 || saved.OutputPriceUSDPer1M != 60 || saved.CacheReadPriceUSDPer1M != 4 || !reflect.DeepEqual(saved.PricingPeriods, custom.PricingPeriods) {
		t.Fatalf("custom pricing was not retained: %+v", saved)
	}
}

func TestModelPricingSourceValidatesMergedUpsertAndPreservesFailedCatalogRefresh(t *testing.T) {
	store, source := pricingSourceFixture(t)
	store.AddModel(linkedPricingModel(source))
	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/models", map[string]any{"name": "linked-model", "modality": "embedding"}, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_model_pricing_source") {
		t.Fatalf("merged upsert accepted unsupported pricing mode: %d %s", response.Code, response.Body)
	}
	before := store.ListModels()[0]
	if before.Modality != "chat" || before.Metadata[modelPricingProviderKey] != source.ID {
		t.Fatalf("invalid merged update changed existing model: %+v", before)
	}
	if err := store.DeleteProviderModel(source.ID); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(t.TempDir(), "model-catalog.yaml")
	if err := os.WriteFile(catalog, []byte("version: 1\nmodels:\n  - name: linked-model\n    category: fixture\n    family: fixture\n    modality: chat\n    context_window: 1000\n    input_modalities: [text]\n    output_modalities: [text]\n    capabilities: [chat]\n    supported_parameters: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedDefaultModelCatalog(store, catalog); err != nil {
		t.Fatalf("unavailable source blocked catalog refresh: %v", err)
	}
	if after := store.ListModels()[0]; !reflect.DeepEqual(after, before) {
		t.Fatalf("catalog refresh changed unavailable linked model: before=%+v after=%+v", before, after)
	}
}

func TestModelPricingSourceUnavailableModelCanBeDisabled(t *testing.T) {
	store, source := pricingSourceFixture(t)
	store.AddModel(linkedPricingModel(source))
	if err := store.DeleteProviderModel(source.ID); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	response := doJSON(t, app, http.MethodPatch, "/api/admin/models/linked-model", map[string]string{"status": StatusDisabled}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("cannot disable broken linked model: %d %s", response.Code, response.Body)
	}
	saved := store.ListModels()[0]
	if saved.Status != StatusDisabled || saved.Metadata[modelPricingProviderKey] != source.ID {
		t.Fatalf("offlining removed pricing policy: %+v", saved)
	}
	for _, patch := range []map[string]any{
		{"status": StatusActive},
		{"modality": "embedding"},
		{"metadata": map[string]string{modelPricingProviderKey: "different-missing-model"}},
	} {
		response = doJSON(t, app, http.MethodPatch, "/api/admin/models/linked-model", patch, "")
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_model_pricing_source") {
			t.Fatalf("disabled model bypassed changed policy validation: %d %s", response.Code, response.Body)
		}
	}
	if after := store.ListModels()[0]; !reflect.DeepEqual(after, saved) {
		t.Fatalf("rejected edit changed disabled model: %+v", after)
	}
}

func TestModelPricingSourceResolvesTimeAndAllCacheBuckets(t *testing.T) {
	store, source := pricingSourceFixture(t)
	zero, two, three, four := 0.0, 2.0, 3.0, 4.0
	source.InputPriceUSDPer1M = 10
	source.CacheReadPriceUSDPer1M = 0
	source.PricingPeriods = []ModelPricingPeriod{{Name: "overnight", Timezone: "Asia/Shanghai", Weekdays: []int{5}, StartTime: "23:00", EndTime: "02:00", CacheReadPriceUSDPer1M: &zero, CacheWritePriceUSDPer1M: &two, CacheWrite5mPriceUSDPer1M: &three, CacheWrite1hPriceUSDPer1M: &four}}
	store.AddProviderModel(source)
	resolved, err := resolveModelPricingSource(store.db, linkedPricingModel(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		at   string
		want float64
	}{
		{"2026-09-05T01:59:59+08:00", 200.0 / 1e6},
		{"2026-09-05T02:00:00+08:00", 600.0 / 1e6},
	} {
		at, _ := time.Parse(time.RFC3339, test.at)
		usage := priceUsageAt(resolved, Usage{PromptTokens: 100, CachedInputTokens: 40, CacheWriteInputTokens: 60, CacheWrite5mInputTokens: 20, CacheWrite1hInputTokens: 30}, at)
		if math.Abs(usage.CostUSD-test.want) > 1e-12 {
			t.Errorf("at %s: cost = %v, want %v", test.at, usage.CostUSD, test.want)
		}
	}
	if got := priceUsage(resolved, Usage{PromptTokens: 1000000, CachedInputTokens: 1000000}); got.CostUSD != 0 {
		t.Fatalf("explicit zero cache read became estimated: %+v", got)
	}
}

func TestModelPricingSourceAdmissionFreezesRatesAndKeepsShadowIndependent(t *testing.T) {
	store, source := pricingSourceFixture(t)
	model := linkedPricingModel(source)
	store.AddModel(model)
	project := store.CreateProject(Project{Name: "Pricing project", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Pricing key", Status: StatusActive}, "pricing-key")
	if err != nil {
		t.Fatal(err)
	}
	card, err := store.PublishMeteringCard(meteringRateCard{Kind: "tenant", Target: model.Name, Currency: "USD", Source: "independent-shadow", Rates: metering.Rates{Input: "50", CacheRead: "0", CacheWrite: "0", CacheWrite5m: "0", CacheWrite1h: "0", Output: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.StartCall(context.Background(), project, key, model.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	source.InputPriceUSDPer1M = 8
	store.AddProviderModel(source)
	second, err := store.StartCall(context.Background(), project, key, model.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if old, next := priceUsage(first.Model, Usage{PromptTokens: 1000000}).CostUSD, priceUsage(second.Model, Usage{PromptTokens: 1000000}).CostUSD; old != 2 || next != 8 {
		t.Fatalf("admission snapshots changed: first=%v second=%v", old, next)
	}
	for _, call := range []CallContext{first, second} {
		store.FinishCall(call, RouteSelection{}, Usage{PromptTokens: 1000000}, http.StatusOK, "", "", "")
	}
	var entry meteringEntry
	if err := store.db.First(&entry, "id = ?", first.RequestID+":admission").Error; err != nil {
		t.Fatal(err)
	}
	var snapshot meteringRequestSnapshot
	if err := json.Unmarshal([]byte(entry.Payload), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.LegacyPrice == nil || snapshot.LegacyPrice.Rates.Input != "2" || snapshot.LegacyPrice.ProviderReference == nil || snapshot.LegacyPrice.ProviderReference.ProviderModelID != source.ID || snapshot.LegacyPrice.ProviderReference.ProviderID != source.ProviderID || snapshot.LegacyPrice.ProviderReference.UpstreamModel != source.UpstreamModel {
		t.Fatalf("missing frozen price provenance: %+v", snapshot.LegacyPrice)
	}
	if snapshot.Price.Version != card.ID || snapshot.Price.Rates.Input != "50" || snapshot.Price.ProviderReference != nil {
		t.Fatalf("linked actual price replaced independent shadow: %+v", snapshot.Price)
	}
	var settled meteringEntry
	if err := store.db.First(&settled, "id = ?", first.RequestID+":shadow").Error; err != nil {
		t.Fatal(err)
	}
	var result struct {
		Tenant meteringShadowCharge `json:"tenant"`
	}
	if err := json.Unmarshal([]byte(settled.Payload), &result); err != nil {
		t.Fatal(err)
	}
	if result.Tenant.LegacyUSD != "2" || result.Tenant.Charge == nil || result.Tenant.Charge.Amount != "50.000000000000" {
		t.Fatalf("actual and shadow charges not independent: %+v", result.Tenant)
	}
	if stored := store.ListModels()[0]; stored.InputPriceUSDPer1M != 20 {
		t.Fatalf("resolution rewrote custom rates: %+v", stored)
	}
}

func TestModelPricingSourceUnavailableFailsBeforeQuota(t *testing.T) {
	for _, state := range []string{"missing_model", "disabled_model", "unconfirmed_model", "missing_provider", "disabled_provider"} {
		t.Run(state, func(t *testing.T) {
			store, source := pricingSourceFixture(t)
			store.AddModel(linkedPricingModel(source))
			project := store.CreateProject(Project{Name: "Pricing project", Status: StatusActive})
			key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Pricing key", Status: StatusActive, Limits: QuotaLimits{DailyRequests: 1}}, "pricing-key")
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "missing_model":
				err = store.DeleteProviderModel(source.ID)
			case "disabled_model":
				source.Status = StatusDisabled
				store.AddProviderModel(source)
			case "unconfirmed_model":
				source.Metadata["pricing_status"] = "unverified"
				store.AddProviderModel(source)
			case "missing_provider":
				err = store.db.Delete(&Provider{}, "id = ?", source.ProviderID).Error
			case "disabled_provider":
				err = store.db.Model(&Provider{}).Where("id = ?", source.ProviderID).Update("status", StatusDisabled).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.StartCall(context.Background(), project, key, "linked-model", 100)
			if err == nil || AsHTTPError(err).Status != http.StatusServiceUnavailable || AsHTTPError(err).Code != "model_pricing_source_unavailable" {
				t.Fatalf("admission did not fail closed: %v", err)
			}
			var quotaCount, entryCount int64
			if err := store.db.Model(&QuotaBucket{}).Count(&quotaCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Model(&meteringEntry{}).Count(&entryCount).Error; err != nil {
				t.Fatal(err)
			}
			if quotaCount != 0 || entryCount != 0 {
				t.Fatalf("failed admission wrote counters or evidence: quota=%d entries=%d", quotaCount, entryCount)
			}
		})
	}
}

func TestModelPricingSourceDoesNotExposeReferenceToScopedUsers(t *testing.T) {
	store, source := pricingSourceFixture(t)
	model := linkedPricingModel(source)
	store.AddModel(model)
	store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: source.ProviderID, ProviderModel: source.UpstreamModel, Status: StatusActive})
	server := New(store)
	models := server.accessibleModelsForAdminUser(AdminUser{Role: "user"})
	if len(models) != 1 || models[0].Metadata[modelPricingProviderKey] != "" || models[0].InputPriceUSDPer1M != 2 {
		t.Fatalf("scoped model pricing not sanitized: %+v", models)
	}
	data, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), source.ID) || strings.Contains(string(data), source.ProviderID) || strings.Contains(string(data), source.UpstreamModel) {
		t.Fatalf("scoped model leaked procurement identity: %s", data)
	}
	if store.ListModels()[0].Metadata[modelPricingProviderKey] != source.ID {
		t.Fatal("sanitization erased persisted reference")
	}
	public := buildModelListItem(models[0])
	if public.InputTokenPricePerM != modelTokenPricePerM(2) {
		t.Fatalf("public quote did not use source prices: %+v", public)
	}
}
