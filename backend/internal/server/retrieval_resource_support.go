package server

import "strings"

// Configured resources participate even while unhealthy: recovery must not
// silently change a route's protocol or billing requirements.
func (s *Server) retrievalRouteProviders(provider Provider, route ModelRoute, modality string, resources []ProviderResource) ([]Provider, error) {
	if route.ProviderResourceID != "" {
		for _, resource := range resources {
			if resource.ID == route.ProviderResourceID && resource.ProviderID == provider.ID {
				return []Provider{effectiveProviderResourceConfig(provider, &resource)}, nil
			}
		}
		return nil, NewHTTPError(400, "route_resource_mismatch", "Resource must belong to Provider")
	}
	result := []Provider{}
	required := providerRouteRequiresResource(provider)
	for _, resource := range resources {
		if resource.ProviderID != provider.ID {
			continue
		}
		if s.store.IsProviderAccountResourceType(provider.Type, resource.ResourceType) {
			required = true
		}
		if resource.Status != StatusActive || (strings.TrimSpace(route.ResourceGroup) != "" && resource.Group != strings.TrimSpace(route.ResourceGroup)) {
			continue
		}
		result = append(result, effectiveProviderResourceConfig(provider, &resource))
	}
	if !required && (len(result) == 0 || s.providerRetrievalSupport(provider, modality)) {
		result = append(result, provider)
	}
	if len(result) == 0 {
		return nil, NewHTTPError(400, "provider_resource_missing", "No configured resource can execute this route")
	}
	return result, nil
}

func (s *Server) providerInventoryRetrievalSupport(provider Provider, model ProviderModel, resources []ProviderResource) bool {
	if !retrievalTextInputSupported(model) {
		return false
	}
	if model.Modality != "embedding" && canonicalProviderModality(model.Modality) != "rerank" {
		return s.providerRetrievalSupport(provider, model.Modality)
	}
	configs, err := s.retrievalRouteProviders(provider, ModelRoute{}, canonicalProviderModality(model.Modality), resources)
	if err != nil {
		return false
	}
	for _, config := range configs {
		if s.providerRetrievalSupport(config, canonicalProviderModality(model.Modality)) {
			return true
		}
	}
	return false
}

func (s *Server) validateRetrievalProviderPrice(model Model, route ModelRoute, provider Provider) error {
	if !s.providerRetrievalSupport(provider, model.Modality) {
		return NewHTTPError(400, "model_operation_unsupported", "Provider cannot execute this model operation; retain it in the catalog without publishing a route")
	}
	if model.Modality != "embedding" && model.Modality != "rerank" {
		return nil
	}
	if provider.Type == ProviderMock {
		return nil
	}
	search := model.Modality == "rerank" && providerRerankProtocol(provider) == "cohere"
	if !retrievalPriceConfigured(model, search, false) {
		return NewHTTPError(400, "retrieval_price_required", "Configure the tenant price or explicitly confirm a free token price before publishing")
	}
	for _, upstream := range s.store.ListProviderModels() {
		if upstream.ProviderID != provider.ID || upstream.UpstreamModel != route.ProviderModel {
			continue
		}
		if !retrievalTextInputSupported(upstream) || (upstream.Modality != "" && canonicalProviderModality(upstream.Modality) != model.Modality) {
			return NewHTTPError(400, "model_operation_mismatch", "Upstream and public model operations differ")
		}
		if !retrievalPriceConfigured(providerModelCostModel(upstream), search, true) {
			return NewHTTPError(400, "retrieval_price_required", "Configure the provider price or explicitly confirm a free token price before publishing")
		}
		return nil
	}
	return NewHTTPError(400, "provider_model_required", "Import and configure the upstream model before publishing")
}
