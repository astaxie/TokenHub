package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestCatalogAnnotationsSurviveNormalizationAndImport(t *testing.T) {
	raw := map[string]any{
		"id": "vendor-model", "type": "chat",
		"metadata": map[string]any{
			"source": "untrusted-override", "availability": "preview",
			"lifecycle_status": "deprecated", "shutdown_at": "2026-10-21T10:00:00+08:00",
			"replacement_model": "next-model", "pricing_status": "unverified",
			"upstream_source": "https://vendor.example/models", "unexpected_object": map[string]any{"nested": true},
		},
	}
	model := providerModelFromCatalog("provider", normalizeProviderCatalogModel(raw))
	for key, want := range map[string]string{
		"source": "local-provider-catalog", "availability": "preview",
		"lifecycle_status": "deprecated", "shutdown_at": "2026-10-21T10:00:00+08:00",
		"replacement_model": "next-model", "pricing_status": "unverified",
	} {
		if model.Metadata[key] != want {
			t.Fatalf("annotation %s = %q, want %q", key, model.Metadata[key], want)
		}
	}
	if _, ok := model.Metadata["unexpected_object"]; ok {
		t.Fatal("non-string catalog annotation was retained")
	}
}

func TestCatalogRetirementUsesExactCutoffAndKeepsPreviewAvailable(t *testing.T) {
	cutoff := time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC)
	metadata := map[string]string{"lifecycle_status": "deprecated", "shutdown_at": "2026-10-21T10:00:00+08:00"}
	if catalogModelRetired(metadata, cutoff.Add(-time.Second)) || !catalogModelRetired(metadata, cutoff) {
		t.Fatal("retirement did not honor the published timezone and cutoff")
	}
	for _, metadata := range []map[string]string{
		{"availability": "preview"}, {"availability": "restricted"},
		{"lifecycle_status": "redirected", "shutdown_at": "2020-01-01T00:00:00Z"},
		{"lifecycle_status": "deprecated", "shutdown_at": "not-before-2026-10-15"},
	} {
		if err := catalogModelPublicationError(metadata, cutoff); err != nil {
			t.Fatalf("non-retired offer was blocked: %v", err)
		}
	}
}

func TestCatalogPolicyBlocksNewRoutesWithoutChangingExistingTraffic(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true, Options: map[string]string{"catalog_id": "official"}})
	store.AddModel(Model{Name: "public", Modality: "chat", Status: StatusActive})
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "same-model", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 9})
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	if err := store.SaveProviderCatalogSnapshot([]ProviderCatalogEntry{{ID: "official", Models: []ProviderCatalogModel{{ID: "same-model", Metadata: map[string]string{"lifecycle_status": "retired", "replacement_model": "next-model"}}}}}, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	route := ModelRoute{ID: "existing", ModelName: "public", ProviderID: provider.ID, ProviderModel: "same-model", Status: StatusActive, Weight: 100}
	if err := app.validateRetrievalRoute(route, nil, provider); AsHTTPError(err).Code != "provider_model_retired" {
		t.Fatalf("new retired route was not rejected: %v", err)
	}
	store.AddRoute(route)
	route.Priority = 2
	if err := app.validateRetrievalRoute(route, nil, provider); err != nil {
		t.Fatalf("unrelated edit to an existing route was blocked: %v", err)
	}
	thirdParty := store.AddProvider(Provider{ID: "third-party", Type: ProviderOpenAICompatible, Status: StatusActive, Options: map[string]string{"catalog_id": "independent"}})
	route.ID, route.ProviderID = "new-third-party", thirdParty.ID
	if err := app.validateRetrievalRoute(route, nil, thirdParty); err != nil {
		t.Fatalf("same ID on an independent provider inherited retirement: %v", err)
	}
	if saved := store.ListProviderModels()[0]; saved.InputPriceUSDPer1M != 9 || saved.Metadata["lifecycle_status"] != "" || saved.Status != StatusActive {
		t.Fatal("catalog policy mutated existing inventory")
	}
}

func TestCatalogAdvisoriesOnlyOverlayOfferMetadata(t *testing.T) {
	index := catalogAdvisoryIndex{"official": {"model": {
		"lifecycle_status": "deprecated", "replacement_model": "next", "call_support": "unsupported", "pricing_status": "unverified",
	}}}
	model := ProviderModel{UpstreamModel: "model", InputPriceUSDPer1M: 3, Metadata: map[string]string{"pricing_status": "configured", "custom": "keep"}}
	updated := index.apply(Provider{Options: map[string]string{"catalog_id": "official"}}, model)
	if updated.Metadata["replacement_model"] != "next" || updated.Metadata["pricing_status"] != "configured" || updated.Metadata["custom"] != "keep" || updated.InputPriceUSDPer1M != 3 {
		t.Fatalf("unexpected advisory overlay: %+v", updated)
	}
	if model.Metadata["replacement_model"] != "" {
		t.Fatal("advisory overlay mutated stored metadata")
	}
	if err := catalogModelPublicationError(updated.Metadata, time.Now()); AsHTTPError(err).Status != http.StatusBadRequest {
		t.Fatalf("unsupported operation was not blocked: %v", err)
	}
}

func TestLegacyZeroInventoryRequiresNewRouteCostConfirmation(t *testing.T) {
	index := catalogAdvisoryIndex{"official": {"model": {"pricing_status": "unverified"}}}
	provider := Provider{Options: map[string]string{"catalog_id": "official"}}
	legacy := ProviderModel{UpstreamModel: "model"}
	effective := index.apply(provider, legacy)
	if err := catalogModelCostConfigurationError(effective); AsHTTPError(err).Code != "provider_model_price_required" {
		t.Fatalf("unconfirmed legacy zero was treated as free: %v", err)
	}
	if legacy.Metadata != nil {
		t.Fatal("advisory persisted metadata into legacy inventory")
	}
	for _, model := range []ProviderModel{
		{UpstreamModel: "model", InputPriceUSDPer1M: 2},
		{UpstreamModel: "model", Metadata: map[string]string{"pricing_status": "configured"}},
		{UpstreamModel: "model", Metadata: map[string]string{"retrieval_pricing_confirmed": "true"}},
	} {
		if err := catalogModelCostConfigurationError(index.apply(provider, model)); err != nil {
			t.Fatalf("configured cost was overwritten: %v", err)
		}
	}
}

func TestReviewedModelsSurviveRefreshWithoutFreezingOtherModels(t *testing.T) {
	local := []ProviderCatalogEntry{{ID: "official", Models: []ProviderCatalogModel{
		{ID: "reviewed", ContextWindow: 1000000, Metadata: map[string]string{"catalog_reviewed_at": "2026-10-10", "lifecycle_status": "retired"}},
		{ID: "added", Metadata: map[string]string{"catalog_reviewed_at": "2026-10-10"}},
		{ID: "unreviewed", ContextWindow: 10},
	}}}
	upstream := []ProviderCatalogEntry{{ID: "official", Models: []ProviderCatalogModel{
		{ID: "reviewed", ContextWindow: 1000}, {ID: "unreviewed", ContextWindow: 2000}, {ID: "upstream-new"},
	}}, {ID: "third-party", Models: []ProviderCatalogModel{{ID: "reviewed", ContextWindow: 3000}}}}
	merged := mergeCuratedProviderCatalogEntries(upstream, local)
	models := map[string]ProviderCatalogModel{}
	for _, model := range merged[0].Models {
		models[model.ID] = model
	}
	if len(models) != 4 || models["reviewed"].ContextWindow != 1000000 || models["reviewed"].Metadata["lifecycle_status"] != "retired" || models["unreviewed"].ContextWindow != 2000 {
		t.Fatalf("refresh lost reviewed entries or froze unreviewed ones: %+v", models)
	}
	if merged[1].Models[0].ContextWindow != 3000 {
		t.Fatal("curation affected an independent provider")
	}
	if got := mergeCuratedProviderCatalogEntries(nil, local); len(got) != 1 || len(got[0].Models) != 3 {
		t.Fatal("new reviewed provider was lost when absent upstream")
	}
}
