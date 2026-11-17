package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestJevRuncrateCatalogAdmitsOutputBudgetAndSampling(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var models []ProviderCatalogModel
	for _, entry := range entries {
		if entry.ID == "runcrate" {
			models = entry.Models
		}
	}
	if len(models) == 0 {
		t.Fatal("runcrate catalog models missing")
	}
	for _, catalogModel := range models {
		id := catalogModel.ID
		t.Run(id, func(t *testing.T) {
			server, _, _, policy := jevFixture(t)
			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
					return
				}
				if got["model"] != id || got["max_tokens"] != float64(128) || got["temperature"] != .5 {
					t.Errorf("upstream request lost the controls: %v", got)
				}
				hits.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_runcrate", "model": id, "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
			}))
			defer upstream.Close()
			store := server.store.(*GormStore)
			for i, candidate := range policy.SemanticRouting.Candidates {
				imported := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-models/import", ProviderModelImportRequest{ProviderID: candidate.ProviderID, Models: []ProviderCatalogModel{catalogModel}}, "")
				if imported.Code != 200 && imported.Code != 201 {
					t.Fatalf("catalog import: %d %s", imported.Code, imported.Body)
				}
				if err := store.db.Model(&Provider{}).Where("id = ?", candidate.ProviderID).Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
					t.Fatal(err)
				}
				if err := store.db.Model(&ModelRoute{}).Where("id = ?", policy.Routes[i].RouteID).Update("provider_model", id).Error; err != nil {
					t.Fatal(err)
				}
				policy.SemanticRouting.Candidates[i].ProviderModel = id
			}
			if _, err := store.UpdateModelRoutePolicy("auto-chat", policy); err != nil {
				t.Fatal(err)
			}
			server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
				return semanticDecision{Choice: "choice_1", Confidence: .9}, nil
			})
			body := map[string]any{"model": "auto-chat", "messages": []any{map[string]any{"role": "user", "content": "task"}}, "max_tokens": 128, "temperature": .5}
			result := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", body, "thk_semantic_test")
			if result.Code != http.StatusOK || hits.Load() != 1 {
				t.Fatalf("runcrate admission: %d %s hits=%d", result.Code, result.Body, hits.Load())
			}
		})
	}
}
