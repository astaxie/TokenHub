package server

import (
	"time"

	"tokenhub/backend/internal/metering"
)

// Legacy inventory stores missing prices as zero. A positive legacy value,
// an explicit inventory confirmation, or a period override proves a base rate.
// Exact cards retain their own presence and may always declare free categories.
func providerLegacyMeteringRates(original, resolved Model, rates metering.Rates, at time.Time) metering.Rates {
	retrieval := original.Modality == "embedding" || original.Modality == "rerank"
	// Retrieval's compact editor only confirms its input or native-unit price;
	// it does not expose the output and cache-read rates in the full cost form.
	confirmedInventory := !retrieval && original.Metadata["pricing_status"] == "configured"
	confirmedRetrieval := retrieval && original.Metadata["retrieval_pricing_confirmed"] == "true"
	var period ModelPricingPeriod
	for _, candidate := range original.PricingPeriods {
		if pricingPeriodMatches(candidate, at) {
			period = candidate
			break
		}
	}
	if resolved.InputPriceUSDPer1M == 0 && period.InputPriceUSDPer1M == nil && !confirmedRetrieval && !confirmedInventory {
		rates.Input = ""
	}
	if resolved.OutputPriceUSDPer1M == 0 && period.OutputPriceUSDPer1M == nil && !confirmedInventory {
		rates.Output = ""
	}
	if resolved.CacheReadPriceUSDPer1M == 0 && period.CacheReadPriceUSDPer1M == nil && !confirmedInventory {
		rates.CacheRead = ""
	}
	if !resolved.CacheWritePriceConfigured {
		rates.CacheWrite = rates.Input
	}
	if !resolved.CacheWrite5mPriceConfigured {
		rates.CacheWrite5m = rates.CacheWrite
	}
	if !resolved.CacheWrite1hPriceConfigured {
		rates.CacheWrite1h = rates.CacheWrite
	}
	return rates
}

func providerLegacyMeteringKnown(snapshot *meteringAttemptSnapshot, usage Usage) bool {
	if snapshot == nil || snapshot.LegacyModel == nil {
		return false
	}
	price := legacyMeteringPrice(*snapshot.LegacyModel, snapshot.At, true)
	// Use the same usage-presence and consistency checks as shadow pricing. This
	// keeps an exact card from making an unknown legacy comparison look like zero.
	return shadowPrice(&price, usage, 0).Charge != nil
}
