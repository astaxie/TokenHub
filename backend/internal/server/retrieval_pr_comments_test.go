package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPRRetrievalLegacyInventoryReconciliation(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: "legacy-alias", Modality: "chat", InputPriceUSDPer1M: 9})
	store.AddRoute(ModelRoute{ModelName: "legacy-alias", ProviderID: "p", ProviderModel: "bge-reranker-v2-m3", Status: StatusActive})
	original := store.AddProviderModel(ProviderModel{ProviderID: "p", UpstreamModel: "bge-reranker-v2-m3", Modality: "chat", InputPriceUSDPer1M: 7, Metadata: map[string]string{"custom": "keep"}})
	backfillProviderModelsFromRoutes(store)
	got := store.ListProviderModels()[0]
	if model := store.ListModels()[0]; model.Modality != "rerank" || model.InputPriceUSDPer1M != 9 {
		t.Fatalf("legacy public alias not reconciled safely: %+v", model)
	}
	if got.Modality != "rerank" || got.InputPriceUSDPer1M != 7 || got.Metadata["custom"] != "keep" {
		t.Fatalf("legacy row not reconciled safely: %+v", got)
	}
	backfillProviderModelsFromRoutes(store)
	if len(store.ListProviderModels()) != 1 || store.ListProviderModels()[0].ID != original.ID {
		t.Fatal("reconciliation not idempotent")
	}
	inferred := providerModelFromRoute(ModelRoute{ProviderModel: "bge-reranker-v2-m3", ModelName: "legacy"}, []Model{{Name: "legacy", Modality: "chat"}})
	if inferred.Modality != "rerank" {
		t.Fatal("legacy public chat overrode rerank inference")
	}
	imported := providerModelFromCatalog("p", ProviderCatalogModel{ID: "bce-reranker", Type: "reranker"})
	if imported.Modality != "rerank" {
		t.Fatal("catalog alias not normalized")
	}
}

func TestPRRetrievalBuiltinAliases(t *testing.T) {
	app := New(NewMemoryStore())
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	for _, kind := range []string{"qwen", "local"} {
		p := Provider{Type: kind, Options: map[string]string{"rerank_protocol": "jina"}}
		if !app.providerRetrievalSupport(p, "rerank") {
			t.Fatalf("%s valid protocol blocked", kind)
		}
		p.Options["rerank_protocol"] = "invalid"
		if app.providerRetrievalSupport(p, "rerank") {
			t.Fatalf("%s invalid protocol admitted", kind)
		}
	}
}

func TestPRRetrievalQuotaRetainsUnknownTokenReservation(t *testing.T) {
	quantity := int64(1)
	for _, evidence := range []*RetrievalUsageEvidence{{Unit: "search_unit", Quantity: &quantity, Source: "upstream"}, {Unit: "token", Source: "unreported"}} {
		if got := quotaActualTokens(CallContext{ReservedTokens: 40}, Usage{RetrievalEvidence: evidence}); got != 40 {
			t.Fatalf("reservation refunded: %d", got)
		}
	}
	zero := int64(0)
	if got := quotaActualTokens(CallContext{ReservedTokens: 40}, Usage{RetrievalEvidence: &RetrievalUsageEvidence{Unit: "token", Quantity: &zero, Source: "upstream"}}); got != 0 {
		t.Fatalf("measured zero not preserved: %d", got)
	}
	req := RerankRequest{Query: "a", Documents: []string{"b"}}
	base := requestTokenReservation(req)
	req.Instruction = strings.Repeat("instruction ", 100)
	if requestTokenReservation(req) <= base {
		t.Fatal("instruction not reserved")
	}
}

func TestPRRetrievalResourcePublicationAndInventory(t *testing.T) {
	store := NewMemoryStore()
	// A named native catalog still requires a protocol; local auto defaults must
	// not hide a resource-only capability or invent a different parent protocol.
	provider := store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true, Options: map[string]string{"catalog_id": "qwen"}})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: "p", Name: "only-resource", ResourceType: "api_key", Group: "retrieval", Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "cohere"}})
	if err != nil {
		t.Fatal(err)
	}
	upstream := store.AddProviderModel(ProviderModel{ProviderID: "p", UpstreamModel: "reranker", Modality: "rerank", Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.002"}})
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	model := Model{Name: "public", Modality: "rerank", Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.003"}}
	route := ModelRoute{ModelName: model.Name, ProviderID: "p", ProviderModel: "reranker", ResourceGroup: "retrieval", Status: StatusActive}
	if !app.providerInventoryRetrievalSupport(provider, upstream, store.ListProviderResources()) {
		t.Fatal("resource-only capability hidden")
	}
	if err := app.validateRetrievalRoute(route, &model, provider); err != nil {
		t.Fatal(err)
	}
	// A custom auto parent has a Jina fallback, so a search-unit-only tenant
	// price must not authorize that token-metered fallback at publication.
	provider.Options = nil
	if err := app.validateRetrievalRoute(route, &model, provider); err == nil {
		t.Fatal("automatic token fallback accepted without tenant token price")
	}
	// A token-priced parent cannot mask a search-unit resource's missing prices.
	provider.Options = map[string]string{"rerank_protocol": "jina"}
	model.Metadata = nil
	model.InputPriceUSDPer1M = 1
	if err := app.validateRetrievalRoute(route, &model, provider); err == nil {
		t.Fatal("implicit resource pricing bypassed")
	}
	route.ProviderResourceID = resource.ID
	if err := app.validateRetrievalRoute(route, &model, provider); err == nil {
		t.Fatal("explicit resource pricing bypassed")
	}
}

func TestPRRetrievalSequentialTPM(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "search-unit", false: "unreported"}[native], func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				response := map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.8}}}
				if native {
					response["meta"] = map[string]any{"billed_units": map[string]any{"search_units": 1}}
				}
				writeJSON(w, 200, response)
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "TPM", Status: StatusActive})
			profile := "jina"
			if native {
				profile = "cohere"
			}
			store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, BaseURL: upstream.URL, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": profile}})
			metadata := map[string]string{retrievalSearchUnitPriceKey: "0.003", "retrieval_pricing_confirmed": "true"}
			store.AddModel(Model{Name: "m", Modality: "rerank", Status: StatusActive, Metadata: metadata})
			store.AddProviderModel(ProviderModel{ProviderID: "p", UpstreamModel: "u", Modality: "rerank", Metadata: metadata})
			store.AddRoute(ModelRoute{ModelName: "m", ProviderID: "p", ProviderModel: "u", Status: StatusActive, Weight: 100})
			request := RerankRequest{Model: "m", Query: strings.Repeat("q", 120), Documents: []string{strings.Repeat("d", 120)}}
			tpm := requestTokenReservation(request)
			_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "limited", Allowed: []string{"m"}, TokenLimitTPM: &tpm, Status: StatusActive}, "thk_retrieval_tpm")
			if err != nil {
				t.Fatal(err)
			}
			app := New(store)
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
			for index, want := range []int{200, 429} {
				result := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", request, secret)
				if result.Code != want {
					t.Fatalf("request %d status=%d body=%s", index, result.Code, result.Body)
				}
			}
			if calls != 1 {
				t.Fatalf("quota bypass: %d calls", calls)
			}
		})
	}
}

type retrievalCountingStore struct {
	Store
	providerReads int
}

func (s *retrievalCountingStore) ListProviders() []Provider {
	s.providerReads++
	return s.Store.ListProviders()
}

func TestPRRetrievalInventoryReadsProvidersOnce(t *testing.T) {
	store := NewMemoryStore()
	store.AddProvider(Provider{ID: "p", Type: ProviderMock, Status: StatusActive})
	for _, name := range []string{"first", "second", "third"} {
		store.AddProviderModel(ProviderModel{ProviderID: "p", UpstreamModel: name, Modality: "embedding"})
	}
	counted := &retrievalCountingStore{Store: store}
	app := New(counted)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	counted.providerReads = 0
	response := doJSON(t, app.Handler(), http.MethodGet, "/api/admin/provider-models", nil, "")
	if response.Code != 200 || counted.providerReads != 1 {
		t.Fatalf("status=%d full provider reads=%d", response.Code, counted.providerReads)
	}
	for _, modality := range []string{"video", "ocr", "audio", "image"} {
		if got := providerModelFromCatalog("p", ProviderCatalogModel{ID: "special", Type: modality}); got.Modality != modality {
			t.Fatalf("unrelated modality rewritten: %s", got.Modality)
		}
	}
}
