package server

import (
	"time"

	"tokenhub/backend/internal/metering"
)

// Legacy inventory stores missing prices as zero. Only a positive legacy value,
// a configured cache-write price, or an explicit period override proves a rate.
// Exact cards retain their own presence and may always declare free categories.
func providerLegacyMeteringRates(original, resolved Model, rates metering.Rates, at time.Time) metering.Rates {
	var period ModelPricingPeriod
	for _, candidate := range original.PricingPeriods {
		if pricingPeriodMatches(candidate, at) {
			period = candidate
			break
		}
	}
	if resolved.InputPriceUSDPer1M == 0 && period.InputPriceUSDPer1M == nil {
		rates.Input = ""
	}
	if resolved.OutputPriceUSDPer1M == 0 && period.OutputPriceUSDPer1M == nil {
		rates.Output = ""
	}
	if resolved.CacheReadPriceUSDPer1M == 0 && period.CacheReadPriceUSDPer1M == nil {
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
