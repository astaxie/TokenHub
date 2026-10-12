package server

import "testing"

func TestEmbeddingDiscoveryRecognizesEstablishedModelFamilies(t *testing.T) {
	for _, name := range []string{"BAAI/bge-m3", "thenlper/gte-large", "intfloat/multilingual-e5-large", "voyage-3-large", "sentence-transformers/all-MiniLM-L6-v2"} {
		if got := normalizeModelModality(name); got != "embedding" {
			t.Errorf("%s classified as %s", name, got)
		}
	}
	for _, name := range []string{"BAAI/bge-reranker-v2-m3", "gte-rerank-v2", "voyage-rerank-2"} {
		if got := normalizeModelModality(name); got != "rerank" {
			t.Errorf("reranker classified as %s", got)
		}
	}
	payload := map[string]any{"data": []any{map[string]any{"id": "opaque-local-model", "model_type": "embedding"}, map[string]any{"id": "bge-custom-chat", "type": "chat"}}}
	models := customProviderModelsFromPayloadWithDefinitions(payload, nil)
	for _, model := range models {
		if model.ID == "opaque-local-model" && model.Type != "embedding" {
			t.Fatal("explicit model_type lost")
		}
		if model.ID == "bge-custom-chat" && model.Type != "chat" {
			t.Fatal("explicit type overridden by name")
		}
	}
}
