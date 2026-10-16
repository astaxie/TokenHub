package server

import (
	"fmt"
	"math"
	"strconv"

	"tokenhub/backend/internal/metering"
)

const audioSecondPriceKey = "audio_price_usd_per_second"

func validateAudioPriceMetadata(metadata map[string]string) error {
	if value, present := metadata[audioSecondPriceKey]; present {
		if _, err := metering.Decimal(value); err != nil {
			return NewHTTPError(400, "invalid_audio_price", "audio_price_usd_per_second must be a non-negative decimal")
		}
	}
	return nil
}

func priceAudioEvidence(e *AudioUsageEvidence, rate string) (metering.Charge, error) {
	if e == nil || e.Seconds == nil || (*e.Seconds < 0 || math.IsNaN(*e.Seconds) || math.IsInf(*e.Seconds, 0)) ||
		(e.Source != "upstream" && e.Source != "plugin") {
		return metering.Charge{}, fmt.Errorf("audio duration evidence is missing or invalid")
	}
	return metering.PriceDuration(audioEvidenceQuantity(e), rate)
}

func nativeAudioCost(model Model, usage Usage) (float64, bool) {
	charge, err := priceAudioEvidence(audioDurationEvidence(usage), model.Metadata[audioSecondPriceKey])
	if err != nil {
		return 0, false
	}
	amount, err := strconv.ParseFloat(charge.USD, 64)
	return amount, err == nil && !math.IsInf(amount, 0)
}

func shadowAudioPrice(result meteringShadowCharge, usage Usage) meteringShadowCharge {
	evidence := result.AudioEvidence
	result.Units, _ = meteringUnits(usage)
	if usage.MeteringRaw != nil {
		result.Units = *usage.MeteringRaw
	}
	result.UsageSource = evidence.Source
	if validateAudioDuration(usage) != nil {
		result.Reason = "inconsistent_usage"
		return result
	}
	if evidence.Seconds == nil {
		result.Reason = "usage_presence_unknown"
		return result
	}
	if result.Price == nil {
		result.Reason = "missing_price"
		return result
	}
	charge, err := priceAudioEvidence(evidence, result.Price.AudioPricePerSecond)
	if err != nil {
		result.Reason = "native_unit_price_required"
		return result
	}
	result.Charge = &charge
	result.Status = "estimated"
	if usage.MeteringInvalid {
		result.Reason = "auxiliary_token_usage_invalid"
	}
	return result
}

func priceAudioUsage(model Model, usage Usage) (Usage, bool) {
	if model.Modality != "audio" || !audioDurationBillable(usage) {
		return usage, false
	}
	usage.CostUSD, usage.InputCostUSD, usage.OutputCostUSD = 0, 0, 0
	usage.CacheReadCostUSD, usage.CacheWriteCostUSD = 0, 0
	if cost, known := nativeAudioCost(model, usage); known {
		usage.InputCostUSD, usage.CostUSD = cost, cost
	}
	return usage, true
}
