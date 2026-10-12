package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRerankProtocolsAndUsage(t *testing.T) {
	for _, tc := range []struct{ profile, path, response string }{
		{"jina", "/rerank", `{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}],"usage":{"total_tokens":7}}`},
		{"cohere", "/rerank", `{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}],"meta":{"billed_units":{"search_units":2}}}`},
		{"voyage", "/rerank", `{"data":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}],"usage":{"total_tokens":7}}`},
		{"qwen", "/reranks", `{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}],"usage":{"total_tokens":7}}`},
		{"dashscope", "/services/rerank/text-rerank/text-rerank", `{"output":{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}]},"usage":{"total_tokens":7}}`},
		{"tei", "/rerank", `[{"index":1,"score":0.9},{"index":0,"score":0.2}]`},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path=%s", r.URL.Path)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if tc.profile == "voyage" {
					if payload["top_k"] != float64(2) || payload["top_n"] != nil {
						t.Errorf("invalid Voyage request %v", payload)
					}
				}
				if tc.profile == "tei" && payload["texts"] == nil {
					t.Error("missing TEI texts")
				}
				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Error(err)
				}
			}))
			defer upstream.Close()
			top := 2
			req := RerankRequest{Model: "public-rerank", Query: "question", Documents: []string{"duplicate", "duplicate"}, TopN: &top, ReturnDocuments: true}
			resp, usage, err := (OpenAICompatibleAdapter{Client: upstream.Client()}).Rerank(context.Background(), Provider{BaseURL: upstream.URL, Options: map[string]string{"rerank_protocol": tc.profile}}, "upstream", req)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(resp)
			if !strings.Contains(string(encoded), `"index":1`) || !strings.Contains(string(encoded), `"document":{"text":"duplicate"}`) {
				t.Fatalf("invalid result %s", encoded)
			}
			if tc.profile == "cohere" && (usage.TotalTokens != 0 || usage.RetrievalEvidence.Unit != "search_unit" || *usage.RetrievalEvidence.Quantity != 2) {
				t.Fatalf("invalid units %+v", usage)
			}
			if tc.profile == "tei" && usage.RetrievalEvidence.Quantity != nil {
				t.Fatal("missing usage converted to zero")
			}
		})
	}
}
func TestRerankRejectsInvalidRequestsAndResults(t *testing.T) {
	for _, r := range []RerankRequest{{Model: "m", Query: "q"}, {Model: "m", Query: "q", Documents: []string{""}}, {Model: "m", Documents: []string{"doc"}}} {
		if validateRerankRequest(r) == nil {
			t.Fatal("accepted invalid request")
		}
	}
	for _, raw := range []string{`{"results":[]}`, `{"results":[{"index":1,"relevance_score":0.5}]}`, `{"results":[{"index":0,"relevance_score":null}]}`} {
		var body any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatal(err)
		}
		if _, err := normalizeRerankResponse(body, "jina", RerankRequest{Model: "m", Query: "q", Documents: []string{"doc"}}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if normalizeModelModality("BAAI/bge-reranker-v2-m3") != "rerank" || normalizeModelModality("rerank") != "rerank" {
		t.Fatal("rerank misclassified")
	}
	models := customProviderModelsFromPayloadWithDefinitions(map[string]any{"data": []any{map[string]any{"id": "opaque-model", "type": "rerank"}}}, nil)
	if len(models) != 1 || models[0].Type != "rerank" {
		t.Fatal("explicit type lost")
	}
}
func TestRerankGatewayAuthenticatesRoutesAndCharges(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.8}}, "meta": map[string]any{"billed_units": map[string]any{"search_units": 1}}})
	}))
	defer upstream.Close()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddProvider(Provider{ID: "rerank-provider", Name: "Rerank fixture", Type: ProviderOpenAICompatible, BaseURL: upstream.URL, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "cohere"}})
	store.AddModel(Model{Name: "public-rerank", Modality: "rerank", Status: StatusActive, Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.003"}})
	store.AddProviderModel(ProviderModel{ProviderID: "rerank-provider", UpstreamModel: "upstream-rerank", Modality: "rerank", Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.001"}})
	store.AddRoute(ModelRoute{ModelName: "public-rerank", ProviderID: "rerank-provider", ProviderModel: "upstream-rerank", Status: StatusActive, Weight: 100})
	_, secret, err := store.CreateAPIKey("prj_demo", APIKey{Name: "rerank-test", Allowed: []string{"public-rerank"}, Status: StatusActive}, "thk_rerank_test")
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	payload := map[string]any{"model": "public-rerank", "query": "q", "documents": []string{"doc"}}
	denied := doJSON(t, server.Handler(), http.MethodPost, "/v1/rerank", payload, "invalid")
	if denied.Code != 401 {
		t.Fatalf("authentication bypassed: %d", denied.Code)
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/rerank", payload, secret)
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var rows []UsageRecord
	if err := store.db.Where("model_name = ?", "public-rerank").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CostUSD != 0.003 {
		t.Fatalf("charge=%+v", rows)
	}
}

func TestRetrievalPublicationPreservesExistingRoutes(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	provider := store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, Status: StatusActive, Options: map[string]string{"rerank_protocol": "jina"}})
	model := store.AddModel(Model{Name: "m", Modality: "rerank", Status: StatusActive})
	upstream := store.AddProviderModel(ProviderModel{ProviderID: "p", UpstreamModel: "u", Modality: "rerank"})
	route := ModelRoute{ID: "new", ModelName: "m", ProviderID: "p", ProviderModel: "u", Status: StatusActive}
	if err := server.validateRetrievalRoute(route, &model, provider); err == nil {
		t.Fatal("unpriced new route accepted")
	}
	model.Metadata = map[string]string{"retrieval_pricing_confirmed": "true"}
	if err := server.validateRetrievalRoute(route, &model, provider); err == nil {
		t.Fatal("missing provider price accepted")
	}
	upstream.Metadata = map[string]string{"retrieval_pricing_confirmed": "true"}
	store.AddProviderModel(upstream)
	if err := server.validateRetrievalRoute(route, &model, provider); err != nil {
		t.Fatal(err)
	}
	store.AddRoute(route)
	model.Metadata = nil
	provider.Options = nil
	if err := server.validateRetrievalRoute(route, &model, provider); err != nil {
		t.Fatalf("existing route changed implicitly: %v", err)
	}
	route.ID = "newer"
	if err := server.validateRetrievalRoute(route, &model, provider); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	if gatewayRequestProtocol("/v1/rerank") != "rerank" {
		t.Fatal("rerank was classified as chat")
	}
}

func TestAdminRerankPriceMetadataPersistsWithoutAffectingTokenPrices(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "price-provider", Type: ProviderOpenAICompatible, Status: StatusActive})
	model := store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "reranker", Modality: "rerank", InputPriceUSDPer1M: 2})
	app := New(store).Handler()
	response := doJSON(t, app, http.MethodPatch, "/api/admin/provider-models/"+model.ID, map[string]any{"metadata": map[string]string{retrievalSearchUnitPriceKey: "0.002", "retrieval_pricing_confirmed": "true"}}, "")
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	for _, stored := range store.ListProviderModels() {
		if stored.ID == model.ID {
			if stored.Metadata[retrievalSearchUnitPriceKey] != "0.002" || stored.InputPriceUSDPer1M != 2 {
				t.Fatalf("lost independent pricing: %+v", stored)
			}
			return
		}
	}
	t.Fatal("provider model disappeared")
}
