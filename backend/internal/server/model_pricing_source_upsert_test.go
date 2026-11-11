package server

import (
	"net/http"
	"reflect"
	"testing"

	"gorm.io/gorm"
)

func TestModelPricingSourceUpsertMergesPartialMetadata(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata map[string]string
		wantMode string
		wantID   string
	}{
		{"source_only", map[string]string{modelPricingProviderKey: "replacement-source"}, "provider", "replacement-source"},
		{"mode_only", map[string]string{modelPricingModeKey: "provider"}, "provider", "original-source"},
		{"unrelated", map[string]string{"display_name": "Updated label"}, "provider", "original-source"},
		{"custom", map[string]string{modelPricingModeKey: "custom"}, "custom", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, original, _ := pricingSourceUpsertFixture(t)
			model := store.AddModel(linkedPricingModel(original))
			model.Metadata = test.metadata
			response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/models", model, "")
			if response.Code != http.StatusCreated {
				t.Fatalf("upsert returned %d: %s", response.Code, response.Body)
			}
			saved := store.ListModels()[0]
			if saved.Metadata[modelPricingModeKey] != test.wantMode || saved.Metadata[modelPricingProviderKey] != test.wantID {
				t.Fatalf("upsert lost pricing intent: %+v", saved.Metadata)
			}
			if saved.InputPriceUSDPer1M != 20 || saved.OutputPriceUSDPer1M != 60 {
				t.Fatalf("upsert changed retained custom prices: %+v", saved)
			}
			resolved, err := store.ResolveModelPricingSource(saved)
			if err != nil {
				t.Fatal(err)
			}
			wantInput := map[string]float64{"original-source": 2, "replacement-source": 7, "": 20}[test.wantID]
			if resolved.InputPriceUSDPer1M != wantInput {
				t.Fatalf("resolved input price = %v, want %v", resolved.InputPriceUSDPer1M, wantInput)
			}
		})
	}
}

func TestModelPricingSourceUpsertRejectsInvalidMergedMetadata(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata map[string]string
	}{
		{"missing_source", map[string]string{modelPricingProviderKey: "missing-source"}},
		{"blank_source", map[string]string{modelPricingProviderKey: ""}},
		{"invalid_mode", map[string]string{modelPricingModeKey: "invalid"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, original, _ := pricingSourceUpsertFixture(t)
			model := store.AddModel(linkedPricingModel(original))
			before := store.ListModels()
			model.Metadata = test.metadata
			model.InputPriceUSDPer1M = 99
			response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/models", model, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid upsert returned %d: %s", response.Code, response.Body)
			}
			if !reflect.DeepEqual(before, store.ListModels()) {
				t.Fatal("invalid upsert changed persisted model")
			}
		})
	}
}

func TestModelPricingSourceNewInsertKeepsExplicitModeSemantics(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata map[string]string
		wantCode int
		wantMode string
		wantID   string
	}{
		{"source_only", map[string]string{modelPricingProviderKey: "replacement-source"}, http.StatusCreated, "", ""},
		{"explicit_custom", map[string]string{modelPricingModeKey: "custom", modelPricingProviderKey: "replacement-source"}, http.StatusCreated, "custom", ""},
		{"explicit_provider", map[string]string{modelPricingModeKey: "provider", modelPricingProviderKey: "replacement-source"}, http.StatusCreated, "provider", "replacement-source"},
		{"missing_source", map[string]string{modelPricingModeKey: "provider"}, http.StatusBadRequest, "", ""},
		{"invalid_source", map[string]string{modelPricingModeKey: "provider", modelPricingProviderKey: "missing-source"}, http.StatusBadRequest, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, original, _ := pricingSourceUpsertFixture(t)
			model := linkedPricingModel(original)
			model.Metadata = test.metadata
			response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/models", model, "")
			if response.Code != test.wantCode {
				t.Fatalf("create returned %d, want %d: %s", response.Code, test.wantCode, response.Body)
			}
			models := store.ListModels()
			if test.wantCode != http.StatusCreated {
				if len(models) != 0 {
					t.Fatal("invalid creation persisted a model")
				}
				return
			}
			if len(models) != 1 || models[0].Metadata[modelPricingModeKey] != test.wantMode || models[0].Metadata[modelPricingProviderKey] != test.wantID {
				t.Fatalf("unexpected creation pricing: %+v", models)
			}
		})
	}
}

func TestModelPricingSourceUpsertRetainsIntentAfterInsertConflict(t *testing.T) {
	store, original, replacement := pricingSourceUpsertFixture(t)
	winner := linkedPricingModel(original)
	winner.ID = winner.Name
	insertedWinner := false
	callback := "test:pricing-source-insert-conflict"
	if err := store.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "models" || insertedWinner {
			return
		}
		// Insert the competing row after the absent lookup, so the candidate's
		// real ON CONFLICT path must reload and merge with the winning policy.
		insertedWinner = true
		if err := tx.Session(&gorm.Session{NewDB: true}).Create(&winner).Error; err != nil {
			_ = tx.AddError(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Create().Remove(callback) })
	model := linkedPricingModel(original)
	model.Metadata = map[string]string{modelPricingProviderKey: replacement.ID}
	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/models", model, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("upsert returned %d: %s", response.Code, response.Body)
	}
	models := store.ListModels()
	if !insertedWinner || len(models) != 1 || models[0].Metadata[modelPricingModeKey] != "provider" || models[0].Metadata[modelPricingProviderKey] != replacement.ID {
		t.Fatalf("insert conflict lost the requested source: winner=%v models=%+v", insertedWinner, models)
	}
}

func pricingSourceUpsertFixture(t *testing.T) (*GormStore, ProviderModel, ProviderModel) {
	t.Helper()
	store, source := pricingSourceFixture(t)
	if err := store.DeleteProviderModel(source.ID); err != nil {
		t.Fatal(err)
	}
	source.ID = "original-source"
	source = store.AddProviderModel(source)
	replacement := source
	replacement.ID = "replacement-source"
	replacement.UpstreamModel = "replacement-chat"
	replacement.InputPriceUSDPer1M = 7
	replacement = store.AddProviderModel(replacement)
	return store, source, replacement
}
