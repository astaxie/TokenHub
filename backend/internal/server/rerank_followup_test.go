package server

import (
	"encoding/json"
	"testing"
)

func TestImportedRerankerOverridesStaleChatFallback(t *testing.T) {
	for _, id := range []string{"voyage/rerank-2.5", "nvidia/rerank-qa-mistral-4b"} {
		if model := providerModelFromCatalog("p", ProviderCatalogModel{ID: id, Type: "chat"}); model.Modality != "rerank" {
			t.Fatalf("stale type won: %+v", model)
		}
	}
	if got := importedRetrievalModality("image", "rerank-alias"); got != "image" {
		t.Fatal("explicit non-chat operation overwritten")
	}
}

func TestRerankFinalValidationRejectsUnsortedScores(t *testing.T) {
	request := RerankRequest{Model: "m", Query: "q", Documents: []string{"private-one", "private-two"}, ReturnDocuments: true}
	bad := map[string]any{"model": "m", "results": []any{map[string]any{"index": 0, "relevance_score": 0.1, "document": map[string]any{"text": "redacted"}}, map[string]any{"index": 1, "relevance_score": 0.9}}}
	if _, err := validateRerankResult(bad, request); err == nil {
		t.Fatal("ascending ranking accepted")
	}
	good := map[string]any{"model": "m", "results": []any{map[string]any{"index": 1, "relevance_score": 0.9}, map[string]any{"index": 0, "relevance_score": 0.1, "document": map[string]any{"text": "redacted"}}}}
	result, err := validateRerankResult(good, request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	original, _ := json.Marshal(good)
	if string(encoded) != string(original) {
		t.Fatal("validation changed safety output")
	}
}
