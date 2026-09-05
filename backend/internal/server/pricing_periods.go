package server

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func cacheWriteTokenParts(usage Usage) (int64, int64, int64) {
	fiveMinuteTokens := maxInt64(usage.CacheWrite5mInputTokens, 0)
	oneHourTokens := maxInt64(usage.CacheWrite1hInputTokens, 0)
	total := maxInt64(usage.CacheWriteInputTokens, 0)
	if total == 0 {
		total = saturatingAddNonNegative(fiveMinuteTokens, oneHourTokens)
	}
	if fiveMinuteTokens > total {
		fiveMinuteTokens = total
	}
	if oneHourTokens > total-fiveMinuteTokens {
		oneHourTokens = total - fiveMinuteTokens
	}
	return fiveMinuteTokens, oneHourTokens, total - fiveMinuteTokens - oneHourTokens
}

func clampBillableInputTokens(usage Usage) Usage {
	usage.CachedInputTokens = minInt64(maxInt64(usage.CachedInputTokens, 0), usage.PromptTokens)
	usage.CacheWrite5mInputTokens = maxInt64(usage.CacheWrite5mInputTokens, 0)
	usage.CacheWrite1hInputTokens = maxInt64(usage.CacheWrite1hInputTokens, 0)
	usage.CacheWriteInputTokens = maxInt64(usage.CacheWriteInputTokens, 0)
	if usage.CacheWriteInputTokens == 0 {
		usage.CacheWriteInputTokens = saturatingAddNonNegative(usage.CacheWrite5mInputTokens, usage.CacheWrite1hInputTokens)
	}
	usage.CacheWriteInputTokens = minInt64(usage.CacheWriteInputTokens, maxInt64(usage.PromptTokens-usage.CachedInputTokens, 0))
	usage.CacheWrite5mInputTokens = minInt64(usage.CacheWrite5mInputTokens, usage.CacheWriteInputTokens)
	usage.CacheWrite1hInputTokens = minInt64(usage.CacheWrite1hInputTokens, usage.CacheWriteInputTokens-usage.CacheWrite5mInputTokens)
	return usage
}

func modelPriceAt(model Model, requestStartedAt time.Time) Model {
	if requestStartedAt.IsZero() {
		requestStartedAt = time.Now().UTC()
	}
	for _, period := range model.PricingPeriods {
		if !pricingPeriodMatches(period, requestStartedAt) {
			continue
		}
		model.InputPriceUSDPer1M = priceOverride(model.InputPriceUSDPer1M, period.InputPriceUSDPer1M)
		model.OutputPriceUSDPer1M = priceOverride(model.OutputPriceUSDPer1M, period.OutputPriceUSDPer1M)
		if period.CacheReadPriceUSDPer1M != nil {
			model.CacheReadPriceUSDPer1M = *period.CacheReadPriceUSDPer1M
			model.Metadata = withConfiguredCacheReadPrice(model.Metadata, true)
		}
		if period.CacheWritePriceUSDPer1M != nil {
			model.CacheWritePriceUSDPer1M = *period.CacheWritePriceUSDPer1M
			model.CacheWritePriceConfigured = true
		}
		if period.CacheWrite5mPriceUSDPer1M != nil {
			model.CacheWrite5mPriceUSDPer1M = *period.CacheWrite5mPriceUSDPer1M
			model.CacheWrite5mPriceConfigured = true
		}
		if period.CacheWrite1hPriceUSDPer1M != nil {
			model.CacheWrite1hPriceUSDPer1M = *period.CacheWrite1hPriceUSDPer1M
			model.CacheWrite1hPriceConfigured = true
		}
		return model
	}
	return model
}

func pricingPeriodMatches(period ModelPricingPeriod, instant time.Time) bool {
	if !pricingEffectiveWindowMatches(period, instant) {
		return false
	}
	location := time.UTC
	if zone := strings.TrimSpace(period.Timezone); zone != "" {
		loaded, err := time.LoadLocation(zone)
		if err != nil {
			return false
		}
		location = loaded
	}
	local := instant.In(location)
	startMinute, hasStart := parsePricingClock(period.StartTime)
	endMinute, hasEnd := parsePricingClock(period.EndTime)
	weekday := int(local.Weekday())
	if hasStart && hasEnd && startMinute > endMinute && local.Hour()*60+local.Minute() < endMinute {
		weekday = (weekday + 6) % 7
	}
	if !pricingWeekdayMatches(period.Weekdays, weekday) {
		return false
	}
	if !hasStart && !hasEnd {
		return true
	}
	if hasStart != hasEnd {
		return false
	}
	currentMinute := local.Hour()*60 + local.Minute()
	if startMinute == endMinute {
		return true
	}
	if startMinute < endMinute {
		return currentMinute >= startMinute && currentMinute < endMinute
	}
	return currentMinute >= startMinute || currentMinute < endMinute
}

func validateModelPricingPeriods(periods []ModelPricingPeriod) error {
	if len(periods) > 64 {
		return invalidModelPricingPeriod(64, "at most 64 pricing periods are supported")
	}
	for index, period := range periods {
		if zone := strings.TrimSpace(period.Timezone); zone != "" {
			if _, err := time.LoadLocation(zone); err != nil {
				return invalidModelPricingPeriod(index, "timezone must be a valid IANA timezone")
			}
		}
		_, hasStart := parsePricingClock(period.StartTime)
		_, hasEnd := parsePricingClock(period.EndTime)
		if hasStart != hasEnd {
			return invalidModelPricingPeriod(index, "start_time and end_time must be configured together")
		}
		if strings.TrimSpace(period.StartTime) != "" && !validPricingClock(period.StartTime) {
			return invalidModelPricingPeriod(index, "start_time must use HH:MM")
		}
		if strings.TrimSpace(period.EndTime) != "" && !validPricingClock(period.EndTime) {
			return invalidModelPricingPeriod(index, "end_time must use HH:MM")
		}
		if value := strings.TrimSpace(period.EffectiveFrom); value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return invalidModelPricingPeriod(index, "effective_from must be RFC3339")
			}
		}
		if value := strings.TrimSpace(period.EffectiveUntil); value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return invalidModelPricingPeriod(index, "effective_until must be RFC3339")
			}
		}
		for _, price := range pricingPeriodPriceOverrides(period) {
			if price.value != nil && (*price.value < 0 || math.IsNaN(*price.value) || math.IsInf(*price.value, 0)) {
				return invalidModelPricingPeriod(index, fmt.Sprintf("%s must be a finite non-negative number", price.name))
			}
		}
	}
	return validatePricingSchedule(periods)
}

func pricingPeriodPriceOverrides(period ModelPricingPeriod) []struct {
	name  string
	value *float64
} {
	return []struct {
		name  string
		value *float64
	}{
		{name: "input_price_usd_per_1m", value: period.InputPriceUSDPer1M},
		{name: "output_price_usd_per_1m", value: period.OutputPriceUSDPer1M},
		{name: "cache_read_price_usd_per_1m", value: period.CacheReadPriceUSDPer1M},
		{name: "cache_write_price_usd_per_1m", value: period.CacheWritePriceUSDPer1M},
		{name: "cache_write_5m_price_usd_per_1m", value: period.CacheWrite5mPriceUSDPer1M},
		{name: "cache_write_1h_price_usd_per_1m", value: period.CacheWrite1hPriceUSDPer1M},
	}
}

func applyModelPricingPatch(model *Model, patch Model) error {
	var err error
	patch, err = mergeModelPricePatch(*model, patch)
	if err != nil {
		return err
	}
	model.InputPriceUSDPer1M = patch.InputPriceUSDPer1M
	model.CacheReadPriceUSDPer1M = patch.CacheReadPriceUSDPer1M
	model.CacheWritePriceUSDPer1M = patch.CacheWritePriceUSDPer1M
	model.CacheWritePriceConfigured = patch.CacheWritePriceConfigured
	model.CacheWrite5mPriceUSDPer1M = patch.CacheWrite5mPriceUSDPer1M
	model.CacheWrite5mPriceConfigured = patch.CacheWrite5mPriceConfigured
	model.CacheWrite1hPriceUSDPer1M = patch.CacheWrite1hPriceUSDPer1M
	model.CacheWrite1hPriceConfigured = patch.CacheWrite1hPriceConfigured
	model.OutputPriceUSDPer1M = patch.OutputPriceUSDPer1M
	model.EmbeddingPriceUSDPer1M = patch.EmbeddingPriceUSDPer1M
	model.PricingPeriods = append([]ModelPricingPeriod(nil), patch.PricingPeriods...)
	normalizeModelCacheWriteConfiguration(model)
	if err := validateModelPricingPeriods(model.PricingPeriods); err != nil {
		return err
	}
	if model.Modality == "embedding" {
		model.CacheReadPriceUSDPer1M = 0
		model.CacheWritePriceUSDPer1M = 0
		model.CacheWritePriceConfigured = false
		model.CacheWrite5mPriceUSDPer1M = 0
		model.CacheWrite5mPriceConfigured = false
		model.CacheWrite1hPriceUSDPer1M = 0
		model.CacheWrite1hPriceConfigured = false
	}
	return nil
}

func normalizeModelCacheWriteConfiguration(model *Model) {
	if model.CacheWritePriceUSDPer1M != 0 {
		model.CacheWritePriceConfigured = true
	}
	if model.CacheWrite5mPriceUSDPer1M != 0 {
		model.CacheWrite5mPriceConfigured = true
	}
	if model.CacheWrite1hPriceUSDPer1M != 0 {
		model.CacheWrite1hPriceConfigured = true
	}
}

func invalidModelPricingPeriod(index int, message string) error {
	return NewHTTPError(
		http.StatusBadRequest,
		"invalid_model_pricing_period",
		fmt.Sprintf("pricing_periods[%d]: %s", index, message),
	)
}

func pricingEffectiveWindowMatches(period ModelPricingPeriod, instant time.Time) bool {
	if from := strings.TrimSpace(period.EffectiveFrom); from != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil || instant.Before(parsed) {
			return false
		}
	}
	if until := strings.TrimSpace(period.EffectiveUntil); until != "" {
		parsed, err := time.Parse(time.RFC3339, until)
		if err != nil || !instant.Before(parsed) {
			return false
		}
	}
	return true
}

func parsePricingClock(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func validPricingClock(value string) bool {
	_, ok := parsePricingClock(value)
	return ok && len(strings.TrimSpace(value)) == len("00:00")
}

func priceOverride(fallback float64, override *float64) float64 {
	if override == nil {
		return fallback
	}
	return *override
}

func effectiveCacheWritePriceUSDPer1M(model Model) float64 {
	if model.Modality == "embedding" {
		return 0
	}
	if model.CacheWritePriceConfigured {
		return model.CacheWritePriceUSDPer1M
	}
	return model.InputPriceUSDPer1M
}

func effectiveCacheWrite5mPriceUSDPer1M(model Model) float64 {
	if model.CacheWrite5mPriceConfigured {
		return model.CacheWrite5mPriceUSDPer1M
	}
	return effectiveCacheWritePriceUSDPer1M(model)
}

func effectiveCacheWrite1hPriceUSDPer1M(model Model) float64 {
	if model.CacheWrite1hPriceConfigured {
		return model.CacheWrite1hPriceUSDPer1M
	}
	return effectiveCacheWritePriceUSDPer1M(model)
}
