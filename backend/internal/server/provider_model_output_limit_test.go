package server

import (
	"context"
	"reflect"
	"testing"
)

func TestCatalogOutputLimitsSurviveProviderModelImport(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ provider, model string }{{"zai", "glm-5.2"}, {"mistral", "zai-glm-5-3"}} {
		t.Run(target.provider+"/"+target.model, func(t *testing.T) {
			var catalogModel ProviderCatalogModel
			for _, entry := range entries {
				if entry.ID != target.provider {
					continue
				}
				for _, model := range entry.Models {
					if model.ID == target.model {
						catalogModel = model
					}
				}
			}
			if catalogModel.ID == "" || catalogModel.MaxOutputTokens != 128000 || catalogModel.Metadata["max_output_tokens"] != "" {
				t.Fatalf("expected a real catalog entry with only a typed output limit: %+v", catalogModel)
			}
			originalMetadata := cloneStringMap(catalogModel.Metadata)
			store := NewMemoryStore()
			provider := store.AddProvider(Provider{ID: target.provider, Type: ProviderOpenAICompatible, Status: StatusActive})
			app := New(store)
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
			result, err := app.importProviderModels(ProviderModelImportRequest{ProviderID: provider.ID, Models: []ProviderCatalogModel{catalogModel}})
			if err != nil {
				t.Fatal(err)
			}
			if result.ImportedModels != 1 || len(result.ProviderModels) != 1 {
				t.Fatalf("unexpected import result: %+v", result)
			}
			models := store.ListProviderModels()
			if len(models) != 1 || models[0].Metadata["max_output_tokens"] != "128000" {
				t.Fatalf("catalog output limit was lost from persisted inventory: %+v", models)
			}
			if !reflect.DeepEqual(catalogModel.Metadata, originalMetadata) {
				t.Fatal("import mutated the source catalog metadata")
			}
			for _, budget := range []int{127999, 128000, 128001} {
				compatibility := jevChatCompatibility(models[0], ChatCompletionRequest{MaxTokens: budget})
				want := ""
				if budget > 128000 {
					want = "output_limit_exceeded"
				}
				if compatibility.Reason != want {
					t.Errorf("max_tokens=%d: compatibility=%+v, want reason %q", budget, compatibility, want)
				}
			}
		})
	}
}

func TestProviderModelFromCatalogOutputLimitPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int64
		metadata map[string]string
		want     string
	}{
		{"typed limit without metadata", 128000, nil, "128000"},
		{"typed limit overrides duplicate metadata", 128000, map[string]string{"max_output_tokens": "256000", "custom": "keep"}, "128000"},
		{"metadata-only limit", 0, map[string]string{"max_output_tokens": "65536"}, "65536"},
		{"unknown limit", 0, nil, ""},
		{"negative typed limit", -1, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := cloneStringMap(tc.metadata)
			model := providerModelFromCatalog("provider", ProviderCatalogModel{ID: "model", MaxOutputTokens: tc.limit, Metadata: tc.metadata})
			if model.Metadata["max_output_tokens"] != tc.want || model.Metadata["custom"] != tc.metadata["custom"] {
				t.Fatalf("unexpected imported metadata: %+v", model.Metadata)
			}
			if !reflect.DeepEqual(tc.metadata, before) {
				t.Fatal("conversion mutated source metadata")
			}
		})
	}
}
