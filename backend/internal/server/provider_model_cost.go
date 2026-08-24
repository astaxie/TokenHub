package server

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

var providerModelCostFields = []string{
	"input_price_usd_per_1m",
	"cache_read_price_usd_per_1m",
	"cache_write_price_usd_per_1m",
	"cache_write_5m_price_usd_per_1m",
	"cache_write_1h_price_usd_per_1m",
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
	if err := validateModelPricingPeriods(model.PricingPeriods); err != nil {
		return err
	}
	costs := []struct {
		name  string
		value float64
	}{
		{name: "input_price_usd_per_1m", value: model.InputPriceUSDPer1M},
		{name: "cache_read_price_usd_per_1m", value: model.CacheReadPriceUSDPer1M},
		{name: "cache_write_price_usd_per_1m", value: model.CacheWritePriceUSDPer1M},
		{name: "cache_write_5m_price_usd_per_1m", value: model.CacheWrite5mPriceUSDPer1M},
		{name: "cache_write_1h_price_usd_per_1m", value: model.CacheWrite1hPriceUSDPer1M},
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
	return s.providerCostUSDAt(route, usage, time.Now().UTC())
}

func (s *GormStore) providerCostUSDAt(route RouteSelection, usage Usage, requestStartedAt time.Time) float64 {
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
	usage = clampBillableInputTokens(usage)
	providerModel = modelPriceAt(providerModelCostModel(providerModel), requestStartedAt).providerModelCost(providerModel)
	if providerModel.Modality == "embedding" {
		return float64(usage.TotalTokens) * providerModel.InputPriceUSDPer1M / 1_000_000
	}
	cacheWrite5mTokens, cacheWrite1hTokens, cacheWriteOtherTokens := cacheWriteTokenParts(usage)
	uncachedInputTokens := maxInt64(usage.PromptTokens-usage.CachedInputTokens-usage.CacheWriteInputTokens, 0)
	return float64(uncachedInputTokens)*providerModel.InputPriceUSDPer1M/1_000_000 +
		float64(usage.CachedInputTokens)*providerModel.CacheReadPriceUSDPer1M/1_000_000 +
		float64(cacheWriteOtherTokens)*effectiveProviderCacheWritePriceUSDPer1M(providerModel)/1_000_000 +
		float64(cacheWrite5mTokens)*effectiveProviderCacheWrite5mPriceUSDPer1M(providerModel)/1_000_000 +
		float64(cacheWrite1hTokens)*effectiveProviderCacheWrite1hPriceUSDPer1M(providerModel)/1_000_000 +
		float64(usage.CompletionTokens)*providerModel.OutputPriceUSDPer1M/1_000_000
}

func providerModelCostModel(providerModel ProviderModel) Model {
	return Model{
		Modality:                  providerModel.Modality,
		InputPriceUSDPer1M:        providerModel.InputPriceUSDPer1M,
		CacheReadPriceUSDPer1M:    providerModel.CacheReadPriceUSDPer1M,
		CacheWritePriceUSDPer1M:   providerModel.CacheWritePriceUSDPer1M,
		CacheWrite5mPriceUSDPer1M: providerModel.CacheWrite5mPriceUSDPer1M,
		CacheWrite1hPriceUSDPer1M: providerModel.CacheWrite1hPriceUSDPer1M,
		OutputPriceUSDPer1M:       providerModel.OutputPriceUSDPer1M,
		CacheWritePriceConfiguration: CacheWritePriceConfiguration{
			CacheWritePriceConfigured:   providerModel.CacheWritePriceConfigured,
			CacheWrite5mPriceConfigured: providerModel.CacheWrite5mPriceConfigured,
			CacheWrite1hPriceConfigured: providerModel.CacheWrite1hPriceConfigured,
		},
		PricingPeriods: providerModel.PricingPeriods,
	}
}

func (model Model) providerModelCost(providerModel ProviderModel) ProviderModel {
	providerModel.InputPriceUSDPer1M = model.InputPriceUSDPer1M
	providerModel.CacheReadPriceUSDPer1M = model.CacheReadPriceUSDPer1M
	providerModel.CacheWritePriceUSDPer1M = model.CacheWritePriceUSDPer1M
	providerModel.CacheWritePriceConfigured = model.CacheWritePriceConfigured
	providerModel.CacheWrite5mPriceUSDPer1M = model.CacheWrite5mPriceUSDPer1M
	providerModel.CacheWrite5mPriceConfigured = model.CacheWrite5mPriceConfigured
	providerModel.CacheWrite1hPriceUSDPer1M = model.CacheWrite1hPriceUSDPer1M
	providerModel.CacheWrite1hPriceConfigured = model.CacheWrite1hPriceConfigured
	providerModel.OutputPriceUSDPer1M = model.OutputPriceUSDPer1M
	return providerModel
}

func effectiveProviderCacheWritePriceUSDPer1M(model ProviderModel) float64 {
	if model.CacheWritePriceConfigured {
		return model.CacheWritePriceUSDPer1M
	}
	return model.InputPriceUSDPer1M
}

func effectiveProviderCacheWrite5mPriceUSDPer1M(model ProviderModel) float64 {
	if model.CacheWrite5mPriceConfigured {
		return model.CacheWrite5mPriceUSDPer1M
	}
	return effectiveProviderCacheWritePriceUSDPer1M(model)
}

func effectiveProviderCacheWrite1hPriceUSDPer1M(model ProviderModel) float64 {
	if model.CacheWrite1hPriceConfigured {
		return model.CacheWrite1hPriceUSDPer1M
	}
	return effectiveProviderCacheWritePriceUSDPer1M(model)
}
