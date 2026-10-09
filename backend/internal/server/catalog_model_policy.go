package server

import (
	"net/http"
	"strings"
	"time"
)

// Catalog annotations describe an upstream offer. They never change an existing
// route's status, an operator's configured costs, or another provider's offer.
func catalogModelMetadata(raw map[string]any) map[string]string {
	metadata := map[string]string{}
	for key, value := range catalogObjectField(raw, "metadata") {
		if text, ok := value.(string); ok && key != "source" && strings.TrimSpace(text) != "" {
			metadata[key] = text
		}
	}
	metadata["source"] = "local-provider-catalog"
	return metadata
}

func catalogModelRetired(metadata map[string]string, now time.Time) bool {
	if metadata["lifecycle_status"] == "retired" {
		return true
	}
	if metadata["lifecycle_status"] != "deprecated" {
		return false
	}
	shutdown, err := time.Parse(time.RFC3339, metadata["shutdown_at"])
	return err == nil && !now.Before(shutdown)
}

func catalogModelPublicationError(metadata map[string]string, now time.Time) error {
	if catalogModelRetired(metadata, now) {
		return NewHTTPError(http.StatusBadRequest, "provider_model_retired", "This upstream model has retired; choose a current model before publishing a new route")
	}
	if metadata["call_support"] == "unsupported" {
		return NewHTTPError(http.StatusBadRequest, "model_operation_unsupported", "This model is catalog-only; its endpoint or request contract is not supported by this gateway")
	}
	return nil
}

var catalogAdvisoryMetadataKeys = []string{
	"availability", "lifecycle_status", "shutdown_at", "replacement_model",
	"lifecycle_source", "verified_at", "call_support", "support_note",
}

type catalogAdvisoryIndex map[string]map[string]map[string]string

func (s *Server) catalogAdvisories() (catalogAdvisoryIndex, error) {
	index := catalogAdvisoryIndex{}
	if s.providerCatalog == nil {
		return index, nil
	}
	entries, _, _, err := s.providerCatalog.loadStored(true)
	if err != nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "provider_catalog_unavailable", "Provider catalog policy could not be loaded; retry before publishing a new route")
	}
	for _, entry := range entries {
		models := map[string]map[string]string{}
		for _, model := range entry.Models {
			models[model.ID] = model.Metadata
		}
		index[entry.ID] = models
	}
	return index, nil
}

func (index catalogAdvisoryIndex) apply(provider Provider, model ProviderModel) ProviderModel {
	catalogID := strings.TrimSpace(provider.Options["catalog_id"])
	// Standard templates retain the source offer's provenance. Its retirement
	// notice does not govern an independently hosted catalog or deployment ID.
	sourceCatalogID := strings.TrimSpace(model.Metadata["provider_catalog_id"])
	sourceModelID := strings.TrimSpace(model.Metadata["provider_model_id"])
	if sourceCatalogID != "" && (sourceCatalogID != catalogID || sourceModelID != "" && sourceModelID != model.UpstreamModel) {
		model.Metadata = cloneStringMap(model.Metadata)
		for _, key := range []string{"availability", "lifecycle_status", "shutdown_at", "replacement_model", "lifecycle_source"} {
			delete(model.Metadata, key)
		}
	}
	current, ok := index[catalogID][model.UpstreamModel]
	if !ok {
		return model
	}
	model.Metadata = cloneStringMap(model.Metadata)
	if model.Metadata == nil {
		model.Metadata = map[string]string{}
	}
	for _, key := range catalogAdvisoryMetadataKeys {
		if value := current[key]; value != "" {
			model.Metadata[key] = value
		}
	}
	// A legacy zero without an explicit confirmation is still unknown. Add the
	// advisory to the response, without persisting it or changing live billing.
	if current["pricing_status"] == "unverified" && model.Metadata["pricing_status"] == "" && model.InputPriceUSDPer1M == 0 && model.OutputPriceUSDPer1M == 0 && model.Metadata["retrieval_pricing_confirmed"] != "true" {
		model.Metadata["pricing_status"] = "unverified"
	}
	return model
}

func (s *Server) validateCatalogModelPublication(provider Provider, route ModelRoute) error {
	index, err := s.catalogAdvisories()
	if err != nil {
		return err
	}
	for _, model := range s.store.ListProviderModels() {
		if model.ProviderID == provider.ID && model.UpstreamModel == route.ProviderModel {
			model = index.apply(provider, model)
			if err := catalogModelPublicationError(model.Metadata, time.Now()); err != nil {
				return err
			}
			return catalogModelCostConfigurationError(model)
		}
	}
	// Custom routes can name an upstream before importing its inventory. Apply
	// the same exact catalog/provider rule, without matching a model globally.
	metadata := index[strings.TrimSpace(provider.Options["catalog_id"])][route.ProviderModel]
	if err := catalogModelPublicationError(metadata, time.Now()); err != nil {
		return err
	}
	return catalogModelCostConfigurationError(ProviderModel{Metadata: metadata})
}

func catalogModelCostConfigurationError(model ProviderModel) error {
	unconfirmed := model.InputPriceUSDPer1M <= 0 && model.OutputPriceUSDPer1M <= 0 || model.Metadata["catalog_price_reference"] == "true"
	if model.Metadata["pricing_status"] == "unverified" && unconfirmed && model.Metadata["retrieval_pricing_confirmed"] != "true" {
		return NewHTTPError(http.StatusBadRequest, "provider_model_price_required", "Confirm and save upstream model costs before publishing a new route; an unverified catalog price is not free usage")
	}
	return nil
}
