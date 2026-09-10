package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	providerResourceSupportedModelsOption = "provider_resource_supported_models"
	providerResourceModelsFetchedAtOption = "provider_resource_models_fetched_at"
	providerResourceModelsETagOption      = "provider_resource_models_etag"
	providerResourceModelCatalogOption    = "provider_resource_model_catalog"
)

func (s *Server) queryProviderResourceModels(ctx context.Context, resourceID string) (ProviderCatalogEntry, error) {
	entry, supported, err := s.queryProviderResourceModelsForCatalog(ctx, "", resourceID)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	if !supported {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadRequest, "provider_resource_models_unsupported", "Provider resource models are not available for this provider")
	}
	return entry, nil
}

func (s *Server) queryProviderResourceModelsForCatalog(ctx context.Context, catalogProviderType string, resourceID string) (ProviderCatalogEntry, bool, error) {
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
	descriptor, ok := s.adapterRegistry.Describe(provider.Type)
	if !ok || !adapterSupports(descriptor, AdapterCapabilityModels) {
		return ProviderCatalogEntry{}, false, nil
	}
	adapter, err := s.adapterRegistry.Resolve(provider.Type)
	if err != nil {
		return ProviderCatalogEntry{}, true, err
	}
	modeler, ok := adapter.(ProviderResourceModelCataloger)
	if !ok {
		return ProviderCatalogEntry{}, false, nil
	}
	etag := ""
	if resource.Options != nil {
		etag = providerResourceModelsETag(&resource)
	}
	catalog, status, err := modeler.ResourceModels(ctx, provider, resource, etag)
	if err == nil && status == http.StatusNotModified {
		pluginCatalog, _ := s.pluginProviderCatalogCapabilityEntryForType(provider.Type)
		if cached, ok := providerResourceCachedCatalog(provider, &resource, pluginCatalog); ok {
			return cached, true, nil
		}
		catalog, _, err = modeler.ResourceModels(ctx, provider, resource, "")
	}
	if err == nil {
		if persistErr := s.persistProviderResourceModels(resourceID, catalog.Models, time.Now().UTC()); persistErr != nil {
			return ProviderCatalogEntry{}, true, persistErr
		}
	}
	return catalog, true, err
}

func providerResourceCachedCatalog(provider Provider, resource *ProviderResource, catalogEntries ...ProviderCatalogEntry) (ProviderCatalogEntry, bool) {
	if resource == nil || resource.Options == nil {
		return ProviderCatalogEntry{}, false
	}
	var models []ProviderCatalogModel
	if json.Unmarshal([]byte(providerResourceModelCatalogJSON(resource)), &models) != nil || len(models) == 0 {
		return ProviderCatalogEntry{}, false
	}
	categories, counts := catalogCategorySummary(models)
	metadata := providerResourceCachedCatalogIdentityFor(provider, resource, catalogEntries...)
	return ProviderCatalogEntry{
		ID:             metadata.id,
		Name:           metadata.name,
		DisplayName:    metadata.displayName,
		Type:           metadata.providerType,
		BaseURL:        metadata.baseURL,
		DocURL:         metadata.docURL,
		Categories:     categories,
		CategoryCounts: counts,
		ModelsCount:    len(models),
		Source:         metadata.source,
		ETag:           providerResourceModelsETag(resource),
		Models:         models,
	}, true
}

type providerResourceCachedCatalogIdentity struct {
	id           string
	name         string
	displayName  string
	providerType string
	baseURL      string
	docURL       string
	source       string
}

func providerResourceCachedCatalogIdentityFor(provider Provider, resource *ProviderResource, catalogEntries ...ProviderCatalogEntry) providerResourceCachedCatalogIdentity {
	for _, catalog := range catalogEntries {
		if strings.TrimSpace(catalog.Type) == "" {
			continue
		}
		name := firstNonEmpty(strings.TrimSpace(catalog.Name), strings.TrimSpace(catalog.DisplayName), strings.TrimSpace(provider.Name), strings.TrimSpace(provider.Type))
		return providerResourceCachedCatalogIdentity{
			id:           firstNonEmpty(strings.TrimSpace(catalog.ID), provider.Type),
			name:         name,
			displayName:  firstNonEmpty(strings.TrimSpace(catalog.DisplayName), name),
			providerType: firstNonEmpty(strings.TrimSpace(catalog.Type), provider.Type),
			baseURL:      firstNonEmpty(strings.TrimSpace(catalog.BaseURL), provider.BaseURL),
			docURL:       strings.TrimSpace(catalog.DocURL),
			source:       cachedProviderCatalogSource(catalog),
		}
	}
	name := firstNonEmpty(strings.TrimSpace(provider.Name), strings.TrimSpace(provider.Type))
	return providerResourceCachedCatalogIdentity{
		id:           provider.Type,
		name:         name,
		displayName:  name,
		providerType: provider.Type,
		baseURL:      provider.BaseURL,
		source:       "provider-resource-cache",
	}
}

func cachedProviderCatalogSource(catalog ProviderCatalogEntry) string {
	source := strings.TrimSpace(catalog.Source)
	if strings.HasSuffix(source, "-live") {
		return strings.TrimSuffix(source, "-live") + "-cache"
	}
	return "provider-resource-cache"
}

func (s *Server) persistProviderResourceModels(resourceID string, models []ProviderCatalogModel, fetchedAt time.Time) error {
	modelIDs := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		modelID := strings.TrimSpace(model.ID)
		lookupName := normalizeModelLookupName(modelID)
		if modelID == "" {
			continue
		}
		if _, ok := seen[lookupName]; ok {
			continue
		}
		seen[lookupName] = struct{}{}
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	encoded, err := json.Marshal(modelIDs)
	if err != nil {
		return err
	}
	catalogEncoded, err := json.Marshal(models)
	if err != nil {
		return err
	}
	_, err = s.store.UpdateProviderResourceOptions(resourceID, map[string]string{
		providerResourceSupportedModelsOption: string(encoded),
		providerResourceModelsFetchedAtOption: fetchedAt.UTC().Format(time.RFC3339Nano),
		providerResourceModelsETagOption:      firstNonEmptyModelETag(models),
		providerResourceModelCatalogOption:    string(catalogEncoded),
	})
	return err
}

func firstNonEmptyModelETag(models []ProviderCatalogModel) string {
	for _, model := range models {
		if value := strings.TrimSpace(model.Metadata["models_etag"]); value != "" {
			return value
		}
	}
	return ""
}

func providerResourceCachedModels(resource *ProviderResource) ([]string, time.Time, bool) {
	if resource == nil || resource.Options == nil {
		return nil, time.Time{}, false
	}
	encoded := providerResourceSupportedModelsJSON(resource)
	if strings.TrimSpace(encoded) == "" {
		return nil, time.Time{}, false
	}
	var models []string
	if err := json.Unmarshal([]byte(encoded), &models); err != nil {
		return nil, time.Time{}, false
	}
	fetchedAt, err := time.Parse(time.RFC3339Nano, providerResourceModelsFetchedAt(resource))
	if err != nil {
		fetchedAt = time.Time{}
	}
	return models, fetchedAt, true
}

func providerResourceSupportedModelsJSON(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceSupportedModelsOption)
}

func providerResourceModelsFetchedAt(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceModelsFetchedAtOption)
}

func providerResourceModelsETag(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceModelsETagOption)
}

func providerResourceModelCatalogJSON(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceModelCatalogOption)
}

func providerResourceModelOption(resource *ProviderResource, key string) string {
	if resource == nil || resource.Options == nil {
		return ""
	}
	if value := strings.TrimSpace(resource.Options[key]); value != "" {
		return value
	}
	for _, legacyKey := range providerResourceModelOptionLegacyKeys(key) {
		if value := strings.TrimSpace(resource.Options[legacyKey]); value != "" {
			return value
		}
	}
	return ""
}

func providerResourceModelInList(modelName string, models []string) bool {
	lookupName := normalizeModelLookupName(modelName)
	for _, candidate := range models {
		if normalizeModelLookupName(candidate) == lookupName {
			return true
		}
	}
	return false
}

func (s *Server) filterProviderAccountRoutesByModel(modelName string, routes []RouteSelection) ([]RouteSelection, error) {
	filtered := make([]RouteSelection, 0, len(routes))

	for _, route := range routes {
		if route.Resource == nil || !s.store.IsProviderAccountResourceType(route.Provider.Type, route.Resource.ResourceType) {
			filtered = append(filtered, route)
			continue
		}
		cachedModels, _, cached := providerResourceCachedModels(route.Resource)
		if !cached {
			// Model discovery is a control-plane operation. If no snapshot exists
			// yet, keep the route and let the upstream return a precise result.
			filtered = append(filtered, route)
			continue
		}
		upstreamModel := firstNonEmpty(route.ProviderModel, modelName)
		if providerResourceModelInList(upstreamModel, cachedModels) {
			filtered = append(filtered, route)
		}
	}

	if len(filtered) > 0 {
		return filtered, nil
	}
	return nil, NewHTTPError(
		http.StatusServiceUnavailable,
		"provider_resource_model_unavailable",
		fmt.Sprintf("No connected provider account supports model %q", strings.TrimSpace(modelName)),
	)
}

func (s *Server) removeProviderResourceModel(resourceID string, modelName string) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return
	}
	models, _, cached := providerResourceCachedModels(&resource)
	if !cached {
		return
	}
	filtered := make([]string, 0, len(models))
	for _, candidate := range models {
		if normalizeModelLookupName(candidate) != normalizeModelLookupName(modelName) {
			filtered = append(filtered, candidate)
		}
	}
	catalogModels := make([]ProviderCatalogModel, 0, len(filtered))
	for _, modelID := range filtered {
		catalogModels = append(catalogModels, ProviderCatalogModel{ID: modelID})
	}
	_ = s.persistProviderResourceModels(resourceID, catalogModels, time.Now().UTC())
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
