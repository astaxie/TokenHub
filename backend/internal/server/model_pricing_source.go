package server

import (
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

const (
	modelPricingModeKey     = "pricing_mode"
	modelPricingProviderKey = "pricing_provider_model_id"
)

type modelPricingReference struct {
	ProviderID      string `json:"provider_id"`
	ProviderModelID string `json:"provider_model_id"`
	UpstreamModel   string `json:"upstream_model"`
}

// Pricing source keys survive unrelated metadata edits. Switching explicitly to
// custom pricing clears the reference while retaining the stored custom prices.
func preserveModelPricingSourceMetadata(current, patch map[string]string) map[string]string {
	if mode, present := patch[modelPricingModeKey]; present && (strings.TrimSpace(mode) == "" || strings.TrimSpace(mode) == "custom") {
		return patch
	}
	if current[modelPricingModeKey] == "" && current[modelPricingProviderKey] == "" {
		return patch
	}
	result := make(map[string]string, len(patch)+2)
	for key, value := range patch {
		result[key] = value
	}
	for _, key := range []string{modelPricingModeKey, modelPricingProviderKey} {
		if _, present := patch[key]; !present && current[key] != "" {
			result[key] = current[key]
		}
	}
	return result
}

func validateModelPricingSource(db *gorm.DB, model *Model) error {
	mode := strings.TrimSpace(model.Metadata[modelPricingModeKey])
	if model.Metadata[modelPricingModeKey] != mode || model.Metadata[modelPricingProviderKey] != strings.TrimSpace(model.Metadata[modelPricingProviderKey]) {
		model.Metadata = cloneStringMap(model.Metadata)
		model.Metadata[modelPricingModeKey] = mode
		model.Metadata[modelPricingProviderKey] = strings.TrimSpace(model.Metadata[modelPricingProviderKey])
	}
	if mode == "" || mode == "custom" {
		if _, present := model.Metadata[modelPricingProviderKey]; present {
			model.Metadata = cloneStringMap(model.Metadata)
			delete(model.Metadata, modelPricingProviderKey)
		}
		return nil
	}
	if mode != "provider" {
		return invalidModelPricingSource("pricing_mode must be custom or provider")
	}
	_, err := loadModelPricingSource(db, *model)
	return err
}

func validateUpdatedModelPricingSource(db *gorm.DB, previous Model, model *Model) error {
	// Operators must be able to take a broken linked model offline. Re-enabling
	// it or changing its source still requires an available, confirmed policy.
	if model.Status == StatusDisabled && previous.Metadata[modelPricingModeKey] == "provider" &&
		model.Metadata[modelPricingModeKey] == previous.Metadata[modelPricingModeKey] &&
		model.Metadata[modelPricingProviderKey] == previous.Metadata[modelPricingProviderKey] && model.Modality == previous.Modality {
		return nil
	}
	return validateModelPricingSource(db, model)
}

func loadModelPricingSource(db *gorm.DB, model Model) (ProviderModel, error) {
	if model.Modality != "chat" {
		return ProviderModel{}, invalidModelPricingSource("Provider pricing references are supported only for chat models")
	}
	id := strings.TrimSpace(model.Metadata[modelPricingProviderKey])
	if id == "" {
		return ProviderModel{}, invalidModelPricingSource("Select a provider model as the pricing reference")
	}
	var source ProviderModel
	if err := db.First(&source, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return source, invalidModelPricingSource("The pricing reference provider model no longer exists")
		}
		return source, err
	}
	if source.Modality != "chat" || source.Status != StatusActive {
		return source, invalidModelPricingSource("The pricing reference must be an active chat provider model")
	}
	if source.Metadata["pricing_status"] != "configured" {
		return source, invalidModelPricingSource("Confirm and save the provider model costs before using them as a pricing reference")
	}
	var provider Provider
	if err := db.First(&provider, "id = ?", source.ProviderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return source, invalidModelPricingSource("The pricing reference provider no longer exists")
		}
		return source, err
	}
	if provider.Status != StatusActive {
		return source, invalidModelPricingSource("The pricing reference provider must be active")
	}
	if err := validateProviderModelCosts(source); err != nil {
		return source, invalidModelPricingSource("The pricing reference contains invalid prices or time periods")
	}
	return source, nil
}

func invalidModelPricingSource(message string) error {
	return NewHTTPError(http.StatusBadRequest, "invalid_model_pricing_source", message)
}

func resolveModelPricingSource(db *gorm.DB, model Model) (Model, error) {
	mode := strings.TrimSpace(model.Metadata[modelPricingModeKey])
	if mode == "" || mode == "custom" {
		return model, nil
	}
	if mode != "provider" {
		return model, unavailableModelPricingSource()
	}
	source, err := loadModelPricingSource(db, model)
	if err != nil {
		return model, unavailableModelPricingSource()
	}
	prices := providerModelCostModel(source)
	model.InputPriceUSDPer1M = prices.InputPriceUSDPer1M
	model.OutputPriceUSDPer1M = prices.OutputPriceUSDPer1M
	model.EmbeddingPriceUSDPer1M = 0
	model.CacheReadPriceUSDPer1M = prices.CacheReadPriceUSDPer1M
	model.CacheWritePriceUSDPer1M = prices.CacheWritePriceUSDPer1M
	model.CacheWrite5mPriceUSDPer1M = prices.CacheWrite5mPriceUSDPer1M
	model.CacheWrite1hPriceUSDPer1M = prices.CacheWrite1hPriceUSDPer1M
	model.CacheWritePriceConfiguration = prices.CacheWritePriceConfiguration
	model.PricingPeriods = prices.PricingPeriods
	model.Metadata = withConfiguredCacheReadPrice(model.Metadata, true)
	model.pricingReference = &modelPricingReference{ProviderID: source.ProviderID, ProviderModelID: source.ID, UpstreamModel: source.UpstreamModel}
	return model, nil
}

func unavailableModelPricingSource() error {
	return NewHTTPError(http.StatusServiceUnavailable, "model_pricing_source_unavailable", "The model pricing reference is unavailable or unconfirmed; contact an administrator")
}

func (s *GormStore) ResolveModelPricingSource(model Model) (Model, error) {
	return resolveModelPricingSource(s.db, model)
}

func resolvePlaygroundModelPricing(store Store, model Model) (Model, error) {
	if model.Metadata[modelPricingModeKey] == "" || model.Metadata[modelPricingModeKey] == "custom" {
		return model, nil
	}
	if resolver, ok := store.(interface{ ResolveModelPricingSource(Model) (Model, error) }); ok {
		return resolver.ResolveModelPricingSource(model)
	}
	return model, unavailableModelPricingSource()
}

func publicModelPricingMetadata(model Model) Model {
	model.Metadata = cloneStringMap(model.Metadata)
	delete(model.Metadata, modelPricingProviderKey)
	model.pricingReference = nil
	return model
}

func (s *GormStore) publicPricedModels(models []Model) []Model {
	items := make([]Model, 0, len(models))
	for _, model := range models {
		resolved, err := resolveModelPricingSource(s.db, model)
		if err != nil {
			resolved.pricingUnavailable = true
		}
		items = append(items, publicModelPricingMetadata(resolved))
	}
	return items
}
