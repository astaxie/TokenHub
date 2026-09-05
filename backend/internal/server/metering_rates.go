package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tokenhub/backend/internal/metering"
)

type meteringPeriod struct {
	ModelPricingPeriod
	Rates metering.Rates `json:"rates"`
}

type meteringRateCard struct {
	Revision      int64            `json:"revision"`
	ID            string           `json:"id"`
	Kind          string           `json:"kind"`
	Target        string           `json:"target"`
	Currency      string           `json:"currency"`
	Source        string           `json:"source"`
	EffectiveFrom time.Time        `json:"effective_from"`
	Rates         metering.Rates   `json:"rates"`
	Periods       []meteringPeriod `json:"periods,omitempty"`
}

type meteringPriceSnapshot struct {
	ExchangeRateVersion string         `json:"exchange_rate_version,omitempty"`
	ExchangeRate        string         `json:"exchange_rate,omitempty"`
	Version             string         `json:"version"`
	Currency            string         `json:"currency"`
	Source              string         `json:"source"`
	At                  time.Time      `json:"at"`
	Period              string         `json:"period,omitempty"`
	Rates               metering.Rates `json:"rates"`
}

func (card meteringRateCard) validate() error {
	if card.Kind != "tenant" && card.Kind != "provider" {
		return fmt.Errorf("kind must be tenant or provider")
	}
	if strings.TrimSpace(card.Target) == "" || strings.TrimSpace(card.Source) == "" {
		return fmt.Errorf("target and source are required")
	}
	if card.Kind == "tenant" && card.Currency != "USD" {
		return fmt.Errorf("tenant prices must use USD")
	}
	if _, err := metering.Price(card.Rates, metering.Units{}, card.Currency, ""); err != nil {
		return err
	}
	if err := card.Rates.Validate(card.Kind == "tenant"); err != nil {
		return err
	}
	periods := make([]ModelPricingPeriod, len(card.Periods))
	for i, p := range card.Periods {
		periods[i] = p.ModelPricingPeriod
		if err := p.Rates.Validate(false); err != nil {
			return err
		}
		for _, override := range pricingPeriodPriceOverrides(p.ModelPricingPeriod) {
			if override.value != nil {
				return fmt.Errorf("exact period prices must be decimal strings in rates")
			}
		}
	}
	return validateModelPricingPeriods(periods)
}

func (card meteringRateCard) at(at time.Time) meteringPriceSnapshot {
	snapshot := meteringPriceSnapshot{Version: card.ID, Currency: card.Currency, Source: card.Source, At: at, Rates: card.Rates}
	for _, period := range card.Periods {
		if !pricingPeriodMatches(period.ModelPricingPeriod, at) {
			continue
		}
		snapshot.Period = period.Name
		base := []*string{&snapshot.Rates.Input, &snapshot.Rates.CacheRead, &snapshot.Rates.CacheWrite, &snapshot.Rates.CacheWrite5m, &snapshot.Rates.CacheWrite1h, &snapshot.Rates.Output}
		overrides := []string{period.Rates.Input, period.Rates.CacheRead, period.Rates.CacheWrite, period.Rates.CacheWrite5m, period.Rates.CacheWrite1h, period.Rates.Output}
		for i, value := range overrides {
			if value != "" {
				*base[i] = value
			}
		}
		break
	}
	return snapshot
}

func meteringUnits(usage Usage) (metering.Units, error) {
	if usage.PromptTokens < 0 || usage.CachedInputTokens < 0 || usage.CacheWriteInputTokens < 0 || usage.CacheWrite5mInputTokens < 0 || usage.CacheWrite1hInputTokens < 0 || usage.CompletionTokens < 0 {
		return metering.Units{}, fmt.Errorf("negative usage")
	}
	writes := usage.CacheWriteInputTokens
	if writes == 0 {
		writes = saturatingAddNonNegative(usage.CacheWrite5mInputTokens, usage.CacheWrite1hInputTokens)
	}
	if usage.CachedInputTokens > usage.PromptTokens || writes > usage.PromptTokens-usage.CachedInputTokens || usage.CacheWrite5mInputTokens > writes || usage.CacheWrite1hInputTokens > writes-usage.CacheWrite5mInputTokens {
		return metering.Units{}, fmt.Errorf("inconsistent cache usage")
	}
	return metering.Units{Input: usage.PromptTokens - usage.CachedInputTokens - writes, CacheRead: usage.CachedInputTokens, CacheWrite: writes - usage.CacheWrite5mInputTokens - usage.CacheWrite1hInputTokens, CacheWrite5m: usage.CacheWrite5mInputTokens, CacheWrite1h: usage.CacheWrite1hInputTokens, Output: usage.CompletionTokens}, nil
}

func encodeMetering(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
