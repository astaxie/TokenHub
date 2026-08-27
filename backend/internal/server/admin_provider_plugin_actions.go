package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *Server) executeProviderProbeAction(ctx context.Context, user AdminUser, providerID string) (any, bool, error) {
	provider, ok := s.providerByID(providerID)
	if !ok {
		return nil, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	result, handled, err := s.executeProviderCapabilityAction(ctx, user, provider.Type, AdapterCapabilityProbe, "provider.probe.run", map[string]any{
		"provider_id": providerID,
	}, providerPluginActionOptions{})
	if err != nil {
		return nil, true, err
	}
	if !handled {
		return nil, false, nil
	}
	return result.Data, true, nil
}

func (s *Server) executeProviderResourceModelsAction(ctx context.Context, user AdminUser, resourceID string) (ProviderCatalogEntry, bool, error) {
	return s.executeProviderResourceModelsActionForCatalog(ctx, user, "", resourceID)
}

func (s *Server) executeProviderResourceModelsActionForCatalog(ctx context.Context, user AdminUser, catalogProviderType string, resourceID string) (ProviderCatalogEntry, bool, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if catalogProviderType = strings.TrimSpace(catalogProviderType); catalogProviderType != "" && provider.Type != catalogProviderType {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusBadRequest, "provider_resource_catalog_mismatch", "Provider resource does not belong to this provider catalog")
	}
	result, handled, err := s.executeProviderCapabilityAction(ctx, user, provider.Type, AdapterCapabilityModels, "models.read", map[string]any{
		"resource_id": resourceID,
	}, providerPluginActionOptions{ResourceType: resource.ResourceType})
	if err != nil {
		return ProviderCatalogEntry{}, true, err
	}
	if !handled {
		return ProviderCatalogEntry{}, false, nil
	}
	catalog, ok := providerCatalogEntryFromActionData(result.Data)
	if !ok {
		return ProviderCatalogEntry{}, true, NewHTTPError(http.StatusInternalServerError, "provider_models_invalid_result", "Provider models action returned an invalid result")
	}
	if err := s.persistProviderResourceModels(resourceID, catalog.Models, time.Now().UTC()); err != nil {
		return ProviderCatalogEntry{}, true, err
	}
	return catalog, true, nil
}

func (s *Server) executeProviderCredentialModelsAction(ctx context.Context, user AdminUser, providerType string, credentials ProviderResourceCredentials) (ProviderCatalogEntry, bool, error) {
	return s.executeProviderModelsPreviewAction(ctx, user, providerType, credentials)
}

func (s *Server) executeProviderCreateRequestModelsAction(ctx context.Context, user AdminUser, req ProviderCreateRequest) (ProviderCatalogEntry, bool, error) {
	providerType := strings.TrimSpace(req.Type)
	if providerType == "" {
		return ProviderCatalogEntry{}, false, nil
	}
	return s.executeProviderModelsPreviewAction(ctx, user, providerType, req)
}

func (s *Server) executeProviderModelsPreviewAction(ctx context.Context, user AdminUser, providerType string, payload any) (ProviderCatalogEntry, bool, error) {
	result, handled, err := s.executeProviderCapabilityAction(ctx, user, providerType, AdapterCapabilityModels, "models.preview", payload, providerPluginActionOptions{})
	if err != nil {
		return ProviderCatalogEntry{}, true, err
	}
	if !handled {
		return ProviderCatalogEntry{}, false, nil
	}
	catalog, ok := providerCatalogEntryFromActionData(result.Data)
	if !ok {
		return ProviderCatalogEntry{}, true, NewHTTPError(http.StatusInternalServerError, "provider_models_invalid_result", "Provider models preview action returned an invalid result")
	}
	return catalog, true, nil
}

func (s *Server) discoverProviderCatalogFromCreateRequest(ctx context.Context, user AdminUser, req ProviderCreateRequest) (ProviderCatalogEntry, error) {
	catalog, supported, err := s.executeProviderCreateRequestModelsAction(ctx, user, req)
	if err != nil || supported {
		return catalog, err
	}
	descriptor, _ := s.adapterRegistry.Describe(req.Type)
	return CustomProviderCatalogFromUpstreamWithDescriptor(ctx, s.upstreamClient, req, descriptor)
}

func providerCatalogEntryFromActionData(data any) (ProviderCatalogEntry, bool) {
	if result, ok := data.(ProviderCatalogEntry); ok {
		return result, strings.TrimSpace(result.ID) != ""
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ProviderCatalogEntry{}, false
	}
	var result ProviderCatalogEntry
	if err := json.Unmarshal(raw, &result); err != nil {
		return ProviderCatalogEntry{}, false
	}
	return result, strings.TrimSpace(result.ID) != ""
}
