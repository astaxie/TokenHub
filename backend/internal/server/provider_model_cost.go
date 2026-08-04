package server

import (
	"fmt"
	"math"
	"net/http"
	"strings"
)

var providerModelCostFields = []string{
	"input_price_usd_per_1m",
	"cache_read_price_usd_per_1m",
	"output_price_usd_per_1m",
}

func providerModelCostDecodeError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, field := range providerModelCostFields {
		if strings.Contains(message, field) {
			return NewHTTPError(
				http.StatusBadRequest,
				"invalid_provider_model_cost",
				fmt.Sprintf("%s must be a finite non-negative number", field),
			)
		}
	}
	return nil
}

func validateProviderModelCosts(model ProviderModel) error {
	costs := []struct {
		name  string
		value float64
	}{
		{name: "input_price_usd_per_1m", value: model.InputPriceUSDPer1M},
		{name: "cache_read_price_usd_per_1m", value: model.CacheReadPriceUSDPer1M},
		{name: "output_price_usd_per_1m", value: model.OutputPriceUSDPer1M},
	}
	for _, cost := range costs {
		if cost.value < 0 || math.IsNaN(cost.value) || math.IsInf(cost.value, 0) {
			return NewHTTPError(
				http.StatusBadRequest,
				"invalid_provider_model_cost",
				fmt.Sprintf("%s must be a finite non-negative number", cost.name),
			)
		}
	}
	return nil
}

func (s *GormStore) providerCostUSD(route RouteSelection, usage Usage) float64 {
	providerID := strings.TrimSpace(route.Provider.ID)
	upstreamModel := strings.TrimSpace(route.ProviderModel)
	if providerID == "" || upstreamModel == "" {
		return 0
	}
	var providerModel ProviderModel
	if err := s.db.Where("provider_id = ? AND upstream_model = ?", providerID, upstreamModel).First(&providerModel).Error; err != nil {
		return 0
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	usage.CachedInputTokens = minInt64(maxInt64(usage.CachedInputTokens, 0), usage.PromptTokens)
	if providerModel.Modality == "embedding" {
		return float64(usage.TotalTokens) * providerModel.InputPriceUSDPer1M / 1_000_000
	}
	uncachedInputTokens := usage.PromptTokens - usage.CachedInputTokens
	return float64(uncachedInputTokens)*providerModel.InputPriceUSDPer1M/1_000_000 +
		float64(usage.CachedInputTokens)*providerModel.CacheReadPriceUSDPer1M/1_000_000 +
		float64(usage.CompletionTokens)*providerModel.OutputPriceUSDPer1M/1_000_000
}
