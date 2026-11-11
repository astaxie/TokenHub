package server

import (
	"fmt"
	"reflect"
	"testing"

	"gorm.io/gorm"
)

func TestModelPricingSourceBatchUsesTwoQueriesForSharedReferences(t *testing.T) {
	store, source := pricingSourceFixture(t)
	other := source
	other.ID, other.UpstreamModel = "other-source", "other-upstream"
	other.InputPriceUSDPer1M = 3
	if err := store.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	models := make([]Model, 100)
	for i := range models {
		models[i] = linkedPricingModel(source)
		models[i].Name = fmt.Sprintf("alias-%d", i)
		if i%2 == 1 {
			models[i].Metadata[modelPricingProviderKey] = other.ID
		}
	}
	queries := 0
	const callback = "test:count_pricing_projection_queries"
	if err := store.db.Callback().Query().Before("gorm:query").Register(callback, func(*gorm.DB) { queries++ }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callback) })
	projected := store.publicPricedModels(models)
	if queries != 2 {
		t.Fatalf("100 aliases issued %d queries; expected one source and one provider query", queries)
	}
	for i, model := range projected {
		want := float64(2 + i%2)
		if model.Name != models[i].Name || model.InputPriceUSDPer1M != want || model.pricingUnavailable {
			t.Fatalf("alias %d has incorrect projection: %+v", i, model)
		}
		if model.Metadata[modelPricingProviderKey] != "" || model.pricingReference != nil {
			t.Fatalf("alias %d leaked pricing source identity", i)
		}
		if models[i].InputPriceUSDPer1M != 20 || models[i].Metadata[modelPricingProviderKey] == "" {
			t.Fatalf("alias %d projection changed the stored custom configuration", i)
		}
	}
	queries = 0
	store.publicPricedModels([]Model{{Name: "custom"}})
	if queries != 0 {
		t.Fatalf("custom-only projection issued %d pricing queries", queries)
	}
}

func TestModelPricingSourceBatchMatchesAdmissionValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*GormStore, *ProviderModel, *Model) error
	}{
		{"available", func(*GormStore, *ProviderModel, *Model) error { return nil }},
		{"missing_source", func(store *GormStore, source *ProviderModel, _ *Model) error {
			return store.db.Delete(source).Error
		}},
		{"missing_provider", func(store *GormStore, source *ProviderModel, _ *Model) error {
			return store.db.Delete(&Provider{}, "id = ?", source.ProviderID).Error
		}},
		{"disabled_provider", func(store *GormStore, source *ProviderModel, _ *Model) error {
			return store.db.Model(&Provider{}).Where("id = ?", source.ProviderID).UpdateColumn("status", StatusDisabled).Error
		}},
		{"disabled_source", func(store *GormStore, source *ProviderModel, _ *Model) error {
			return store.db.Model(source).UpdateColumn("status", StatusDisabled).Error
		}},
		{"unconfirmed_source", func(store *GormStore, source *ProviderModel, _ *Model) error {
			source.Metadata["pricing_status"] = "unverified"
			return store.db.Save(source).Error
		}},
		{"corrupt_prices_without_timestamp_change", func(store *GormStore, source *ProviderModel, _ *Model) error {
			return store.db.Model(source).UpdateColumn("input_price_usd_per1_m", -1).Error
		}},
		{"wrong_source_modality", func(store *GormStore, source *ProviderModel, _ *Model) error {
			return store.db.Model(source).UpdateColumn("modality", "embedding").Error
		}},
		{"wrong_model_modality", func(_ *GormStore, _ *ProviderModel, model *Model) error {
			model.Modality = "embedding"
			return nil
		}},
		{"missing_reference", func(_ *GormStore, _ *ProviderModel, model *Model) error {
			delete(model.Metadata, modelPricingProviderKey)
			return nil
		}},
		{"unknown_mode", func(_ *GormStore, _ *ProviderModel, model *Model) error {
			model.Metadata[modelPricingModeKey] = "unknown"
			return nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, source := pricingSourceFixture(t)
			model := linkedPricingModel(source)
			// Prime validation before simulating an external configuration change.
			if _, err := resolveModelPricingSource(store.db, model); err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(store, &source, &model); err != nil {
				t.Fatal(err)
			}
			resolved, err := resolveModelPricingSource(store.db, model)
			projected := store.publicPricedModels([]Model{model})
			if len(projected) != 1 || projected[0].pricingUnavailable != (err != nil) {
				t.Fatalf("batch availability disagrees with admission: %+v, %v", projected, err)
			}
			if test.name != "available" && err == nil {
				t.Fatal("invalid source was accepted")
			}
			if err == nil && !reflect.DeepEqual(projected[0], publicModelPricingMetadata(resolved)) {
				t.Fatalf("batch price differs from admission: %+v != %+v", projected[0], resolved)
			}
		})
	}
}
