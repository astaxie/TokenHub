package server

import (
	"log"
	"strings"
)

// Reconcile only retrieval aliases and the historical reranker-as-chat inference.
// Preserve routing, pricing, credentials and unrelated model configuration.
func reconcileLegacyRetrievalInventory(store Store) {
	for _, model := range store.ListProviderModels() {
		modality := strings.ToLower(strings.TrimSpace(model.Modality))
		next := modality
		if modality == "reranker" || modality == "reranking" || ((modality == "chat" || modality == "") && normalizeModelModality(model.UpstreamModel) == "rerank") {
			next = "rerank"
		}
		if next != modality {
			model.Modality = next
			if _, err := store.UpdateProviderModel(model.ID, model); err != nil {
				log.Printf("[tokenhub] retrieval inventory reconciliation failed: %v", err)
			}
		}
	}
	// A public alias is safe to correct only when all of its configured upstreams
	// are recognizable rerankers. Mixed-purpose aliases remain administrator-owned.
	routes := store.ListRoutes()
	for _, model := range store.ListModels() {
		if model.Modality != "chat" && model.Modality != "reranker" {
			continue
		}
		matched, onlyRerank := false, true
		for _, route := range routes {
			if route.ModelName != model.Name {
				continue
			}
			matched = true
			if normalizeModelModality(route.ProviderModel) != "rerank" {
				onlyRerank = false
			}
		}
		if model.Modality == "reranker" || (matched && onlyRerank) {
			model.Modality = "rerank"
			if _, err := store.UpdateModel(model.Name, model); err != nil {
				log.Printf("[tokenhub] retrieval model reconciliation failed: %v", err)
			}
		}
	}

}

func usesCompatibleRetrievalProtocol(kind string) bool {
	switch kind {
	case ProviderOpenAICompatible, "qwen", "local", "deepseek", ProviderOpenAI:
		return true
	}
	return false
}

func retrievalTokenQuantityUnknown(usage Usage) bool {
	evidence := usage.RetrievalEvidence
	return evidence != nil && (evidence.Unit != "token" || evidence.Quantity == nil || evidence.Source == "invalid") && meteredTokens(usage) == 0
}

func retrievalAttemptQuotaTokens(call CallContext, usage Usage) int64 {
	if retrievalTokenQuantityUnknown(usage) {
		return call.ReservedTokens
	}
	return meteredTokens(usage)
}

func canonicalProviderModality(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "reranker", "reranking":
		return "rerank"
	case "embeddings":
		return "embedding"
	default:
		return value
	}
}

func importedRetrievalModality(declared, id string) string {
	modality := canonicalProviderModality(declared)
	inferred := normalizeModelModality(id)
	if modality == "" || (modality == "chat" && inferred == "rerank") {
		return inferred
	}
	return modality
}
