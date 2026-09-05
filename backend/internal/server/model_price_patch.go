package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
)

func validateModelBasePrices(model Model) error {
	for field, value := range map[string]float64{
		"input_price_usd_per_1m":          model.InputPriceUSDPer1M,
		"output_price_usd_per_1m":         model.OutputPriceUSDPer1M,
		"embedding_price_usd_per_1m":      model.EmbeddingPriceUSDPer1M,
		"cache_read_price_usd_per_1m":     model.CacheReadPriceUSDPer1M,
		"cache_write_price_usd_per_1m":    model.CacheWritePriceUSDPer1M,
		"cache_write_5m_price_usd_per_1m": model.CacheWrite5mPriceUSDPer1M,
		"cache_write_1h_price_usd_per_1m": model.CacheWrite1hPriceUSDPer1M,
	} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return NewHTTPError(http.StatusBadRequest, "invalid_model_price", fmt.Sprintf("%s must be a finite non-negative number", field))
		}
	}
	return nil
}

// Presence is retained only for HTTP PATCH; internal full-model updates keep
// their existing replacement semantics.
type modelPricePatch struct{ fields map[string]json.RawMessage }
type modelPatchRequest struct{ Model }

func (p *modelPatchRequest) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &p.Model); err != nil {
		return err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	p.Model.pricingPatch = &modelPricePatch{fields: fields}
	return nil
}

func mergeModelPricePatch(current, patch Model) (Model, error) {
	if patch.pricingPatch == nil {
		return patch, nil
	}
	data, err := json.Marshal(current)
	if err != nil {
		return Model{}, err
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(data, &fields); err != nil {
		return Model{}, err
	}
	for _, key := range []string{"input_price_usd_per_1m", "output_price_usd_per_1m", "embedding_price_usd_per_1m", "cache_read_price_usd_per_1m", "cache_write_price_usd_per_1m", "cache_write_price_configured", "cache_write_5m_price_usd_per_1m", "cache_write_5m_price_configured", "cache_write_1h_price_usd_per_1m", "cache_write_1h_price_configured", "pricing_periods"} {
		if value, ok := patch.pricingPatch.fields[key]; ok {
			fields[key] = value
			flags := map[string]string{"cache_write_price_usd_per_1m": "cache_write_price_configured", "cache_write_5m_price_usd_per_1m": "cache_write_5m_price_configured", "cache_write_1h_price_usd_per_1m": "cache_write_1h_price_configured"}
			if flag, ok := flags[key]; ok {
				if _, explicit := patch.pricingPatch.fields[flag]; !explicit {
					if string(value) == "null" {
						fields[flag] = json.RawMessage("false")
					} else {
						fields[flag] = json.RawMessage("true")
					}
				}
			}
		}
	}
	data, err = json.Marshal(fields)
	if err != nil {
		return Model{}, err
	}
	var merged Model
	err = json.Unmarshal(data, &merged)
	return merged, err
}

func modelPricingMetadata(current map[string]string, patch Model) map[string]string {
	metadata := current
	if patch.Metadata != nil {
		metadata = patch.Metadata
	}
	if patch.pricingPatch == nil {
		return metadata
	}
	if configured, ok := patch.Metadata[cacheReadConfiguredKey]; ok {
		return withConfiguredCacheReadPrice(metadata, configured == "true")
	}
	if value, ok := patch.pricingPatch.fields["cache_read_price_usd_per_1m"]; ok {
		return withConfiguredCacheReadPrice(metadata, string(value) != "null")
	}
	return withConfiguredCacheReadPrice(metadata, current[cacheReadConfiguredKey] == "true")
}
