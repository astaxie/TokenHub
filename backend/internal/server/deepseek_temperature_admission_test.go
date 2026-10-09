package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestDeepSeekTemperatureRequestsRemainEligible(t *testing.T) {
	local, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../../data/builtin-plugins/providers/deepseek/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	packaged := []ProviderCatalogEntry{normalizeProviderCatalogEntry("deepseek", raw)}
	standard, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	for _, model := range standard {
		if model.Name == "deepseek-flash" || model.Name == "deepseek-v4-flash" || model.Name == "deepseek-v4-pro" {
			store.AddModel(model)
		}
	}
	templates := (&Server{store: store}).providerCatalogEntryWithSelectedStandardModels(ProviderCatalogEntry{ID: "deepseek"}, []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-pro"}, "")
	if len(templates.Models) != 3 {
		t.Fatal("standard catalog regression must exercise all three DeepSeek models")
	}
	for source, entries := range map[string][]ProviderCatalogEntry{
		"source": local, "package": packaged, "builtin": {deepSeekBuiltinCatalogEntry()}, "standard": {templates},
	} {
		for _, entry := range entries {
			if entry.ID != "deepseek" {
				continue
			}
			for _, catalogModel := range entry.Models {
				if catalogModel.ID != "deepseek-flash" && catalogModel.ID != "deepseek-v4-flash" && catalogModel.ID != "deepseek-v4-pro" {
					continue
				}
				t.Run(source+"/"+catalogModel.ID, func(t *testing.T) {
					if catalogModel.Metadata["temperature_note"] == "" {
						t.Fatal("thinking-mode temperature qualification was lost")
					}
					server, routed, request, _ := jevFixture(t)
					for _, route := range routed.Routes {
						inventory := providerModelFromCatalog(route.Provider.ID, catalogModel)
						inventory.UpstreamModel = route.ProviderModel
						server.store.AddProviderModel(inventory)
					}
					server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, candidates []semanticCandidate, _ string) (semanticDecision, error) {
						if len(candidates) != 3 {
							t.Fatalf("temperature request lost candidates: %+v", candidates)
						}
						return semanticDecision{Choice: "choice_0", Confidence: 0.9}, nil
					})
					temperature := 0.2
					request.Temperature = &temperature
					if err := server.applySemanticRouting(context.Background(), &routed, request, nil); err != nil {
						t.Fatalf("accepted temperature request was rejected: %v", err)
					}
					if request.Temperature == nil || *request.Temperature != temperature {
						t.Fatal("routing changed the accepted temperature parameter")
					}
				})
			}
		}
	}
}
