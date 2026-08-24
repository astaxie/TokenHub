package server

import (
	"context"
	"net/http"
	"strings"
)

func (s *Server) providerForCreate(ctx context.Context, req ProviderCreateRequest) (Provider, ProviderCatalogEntry, string, error) {
	if err := validateProviderRouteCreation(req); err != nil {
		return Provider{}, ProviderCatalogEntry{}, "", err
	}
	provider, catalog, source, err := s.providerFromCreateRequest(ctx, req)
	if err == nil {
		if _, configured := provider.Options[claudeCodeAttributionPolicyOption]; !configured {
			provider.Options[claudeCodeAttributionPolicyOption] = defaultClaudeCodeAttributionPolicy(provider.Type, provider.Options["catalog_id"])
		}
	}
	if err == nil && provider.Name != "" && provider.Type != "" {
		err = validateProviderModelSelection(catalog, req.SelectedModels)
	}
	if err == nil {
		err = validateSelectedProviderModelCosts(catalog, req.SelectedModels)
	}
	return provider, catalog, source, err
}

func validateProviderRouteCreation(req ProviderCreateRequest) error {
	if req.CreateRoutes != nil && *req.CreateRoutes {
		return NewHTTPError(
			http.StatusBadRequest,
			"provider_routes_must_be_configured_separately",
			"Create routes from Model Directory after importing Provider models",
		)
	}
	return nil
}

func validateProviderModelSelection(catalog ProviderCatalogEntry, selectedModels []string) error {
	if catalog.ID == "" || len(catalog.Models) == 0 {
		return nil
	}
	available := make(map[string]bool, len(catalog.Models))
	for _, model := range catalog.Models {
		if modelID := strings.TrimSpace(model.ID); modelID != "" {
			available[modelID] = true
		}
	}
	for _, modelID := range selectedModels {
		if modelID = strings.TrimSpace(modelID); modelID != "" && available[modelID] {
			return nil
		}
	}
	return NewHTTPError(http.StatusBadRequest, "provider_models_required", "Select at least one model from the Provider catalog")
}

func validateSelectedProviderModelCosts(catalog ProviderCatalogEntry, selectedModels []string) error {
	selected := make(map[string]bool, len(selectedModels))
	for _, modelID := range selectedModels {
		if modelID = strings.TrimSpace(modelID); modelID != "" {
			selected[modelID] = true
		}
	}
	for _, model := range catalog.Models {
		if selected[strings.TrimSpace(model.ID)] {
			if err := validateProviderModelCosts(providerModelFromCatalog("", model)); err != nil {
				return err
			}
		}
	}
	return nil
}
