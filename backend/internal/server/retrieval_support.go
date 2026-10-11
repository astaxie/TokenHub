package server

import (
	"context"
	"net/http"
	"strings"
	"tokenhub/backend/internal/metering"
)

func validRerankProtocol(profile string) bool {
	switch profile {
	case "jina", "cohere", "voyage", "qwen", "dashscope", "tei":
		return true
	}
	return false
}
func (s *Server) providerRetrievalSupport(provider Provider, modality string) bool {
	descriptor, ok := s.adapterRegistry.Describe(provider.Type)
	if !ok {
		return false
	}
	switch modality {
	case "embedding":
		if !adapterSupports(descriptor, AdapterCapabilityEmbeddings) {
			return false
		}
		if usesCompatibleRetrievalProtocol(provider.Type) {
			switch providerEmbeddingProtocol(provider) {
			case "openai", "cohere", "jina", "voyage", "dashscope", "tei":
				return true
			default:
				return false
			}
		}
		return true
	case "rerank":
		return adapterSupports(descriptor, AdapterCapabilityRerank) && (!usesCompatibleRetrievalProtocol(provider.Type) || validRerankProtocol(providerRerankProtocol(provider)))
	case "video", "audio", "ocr":
		return false
	default:
		return true
	}
}
func retrievalPriceConfigured(model Model, search bool, provider bool) bool {
	if search {
		_, err := metering.Decimal(model.Metadata[retrievalSearchUnitPriceKey])
		return err == nil
	}
	price := model.InputPriceUSDPer1M
	if !provider && model.Modality == "embedding" {
		price = model.EmbeddingPriceUSDPer1M
	}
	return price > 0 || model.Metadata["retrieval_pricing_confirmed"] == "true"
}
func (s *Server) validateRetrievalRoute(route ModelRoute, pending *Model, provider Provider) error {
	if route.Status == StatusDisabled {
		return nil
	}
	// Preserve already-published routes during unrelated edits. Their runtime
	// capability checks still apply; no upgrade silently disables old traffic.
	for _, old := range s.store.ListRoutes() {
		if old.ID == route.ID && old.ModelName == route.ModelName && old.ProviderID == route.ProviderID && old.ProviderModel == route.ProviderModel && old.ProviderResourceID == route.ProviderResourceID && strings.TrimSpace(old.ResourceGroup) == strings.TrimSpace(route.ResourceGroup) && old.Status == route.Status {
			return nil
		}
	}
	if err := s.validateCatalogModelPublication(provider, route); err != nil {
		return err
	}
	var model Model
	found := false
	if pending != nil {
		model = *pending
		found = true
	} else {
		for _, candidate := range s.store.ListModels() {
			if candidate.Name == route.ModelName {
				model = candidate
				found = true
				break
			}
		}
	}
	if !found {
		return nil
	}
	if model.Modality != "embedding" && model.Modality != "rerank" {
		return s.validateRetrievalProviderPrice(model, route, provider)
	}
	configs, err := s.retrievalRouteProviders(provider, route, model.Modality, s.store.ListProviderResources())
	if err != nil {
		return err
	}
	for _, config := range configs {
		if err := s.validateRetrievalProviderPrice(model, route, config); err != nil {
			return err
		}
	}
	if model.Modality == "embedding" {
		_, err := s.embeddingSpaceContract(context.Background(), model.Name, &route)
		return err
	}
	return nil
}
func (s *Server) pricedRerankRoutes(call CallContext, routes []RouteSelection) []RouteSelection {
	return s.pricedRetrievalRoutes(call, routes, "rerank", providerRouteProtocolRerank)
}
func (s *Server) handleAdminRerankTest(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	var req struct {
		ProviderID string        `json:"provider_id"`
		ResourceID string        `json:"resource_id,omitempty"`
		Request    RerankRequest `json:"request"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := validateRerankRequest(req.Request); err != nil {
		writeError(w, r, err)
		return
	}
	loader, ok := s.store.(interface {
		LoadProviderTestRoute(context.Context, string, string, string) (RouteSelection, error)
	})
	if !ok {
		writeError(w, r, NewHTTPError(501, "provider_test_unavailable", "Store cannot load provider execution settings"))
		return
	}
	selection, err := loader.LoadProviderTestRoute(r.Context(), strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.ResourceID), req.Request.Model)
	if err != nil {
		writeError(w, r, err)
		return
	}
	selection, err = s.prepareRouteForUpstream(r.Context(), selection)
	if err != nil {
		writeError(w, r, err)
		return
	}
	adapter, err := s.adapterForRoute(selection)
	if err != nil {
		writeError(w, r, err)
		return
	}
	reranker, ok := adapter.(ProviderReranker)
	if !ok {
		writeError(w, r, NewHTTPError(501, "provider_capability_not_supported", "Provider does not support rerank"))
		return
	}
	response, usage, err := reranker.Rerank(r.Context(), selection.Provider, selection.ProviderModel, req.Request)
	s.recordAdminAudit(r, user, "test", "provider_rerank", selection.Provider.ID, "", map[string]any{"model": req.Request.Model, "usage_evidence": usage.RetrievalEvidence, "success": err == nil})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"response": response, "usage_evidence": usage.RetrievalEvidence, "pricing_status": "unverified"})
}

func validateRetrievalPriceMetadata(metadata map[string]string) error {
	if value := metadata[retrievalSearchUnitPriceKey]; value != "" {
		if _, err := metering.Decimal(value); err != nil {
			return NewHTTPError(400, "invalid_retrieval_price", "search_unit_price_usd must be a non-negative decimal")
		}
	}
	return nil
}

func retrievalTextInputSupported(model ProviderModel) bool {
	if model.Modality != "embedding" && model.Modality != "rerank" {
		return true
	}
	if len(model.InputModalities) == 0 {
		return true
	}
	for _, modality := range model.InputModalities {
		if modality == "text" {
			return true
		}
	}
	return false
}
