package persistence

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"
	"tokenhub/backend/internal/metering"
	"tokenhub/backend/internal/reconciliation"
)

type meteringEvidenceRow struct {
	Kind    string
	Scope   string
	Payload string
}

func (meteringEvidenceRow) TableName() string { return "metering_entries" }

type meteringAttemptEvidence struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	ResourceID string `json:"resource_id"`
}

type meteringPriceEvidence struct {
	Version string `json:"version"`
}

type meteringChargeEvidence struct {
	Price  *meteringPriceEvidence `json:"price"`
	Charge *metering.Charge       `json:"charge"`
}

type meteringAttemptChargeEvidence struct {
	ID      string                 `json:"attempt_id"`
	Number  int                    `json:"number"`
	Pricing meteringChargeEvidence `json:"pricing"`
}

func applyZeroCostEvidence(db *gorm.DB, usages []reconciliation.Usage) error {
	ids := make([]string, 0, len(usages))
	for _, usage := range usages {
		if usage.ProviderCostUSD == 0 && strings.TrimSpace(usage.RequestID) != "" {
			ids = append(ids, usage.RequestID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var rows []meteringEvidenceRow
	if err := db.Where("scope IN ? AND kind IN ?", ids, []string{"attempt_prepared", "shadow_settlement"}).Find(&rows).Error; err != nil {
		return err
	}
	attempts := map[string]meteringAttemptEvidence{}
	settlements := map[string][]meteringAttemptChargeEvidence{}
	for _, row := range rows {
		if row.Kind == "attempt_prepared" {
			var attempt meteringAttemptEvidence
			if err := json.Unmarshal([]byte(row.Payload), &attempt); err != nil {
				return err
			}
			attempts[attempt.ID] = attempt
			continue
		}
		var payload struct {
			Attempts []meteringAttemptChargeEvidence `json:"attempts"`
		}
		if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
			return err
		}
		settlements[row.Scope] = payload.Attempts
	}
	for index := range usages {
		usage := &usages[index]
		if usage.ProviderCostUSD != 0 {
			continue
		}
		var final *meteringAttemptChargeEvidence
		for index := range settlements[usage.RequestID] {
			charge := &settlements[usage.RequestID][index]
			if final == nil || charge.Number > final.Number {
				final = charge
			}
		}
		if final == nil || final.Pricing.Price == nil || strings.TrimSpace(final.Pricing.Price.Version) == "" || final.Pricing.Charge == nil {
			continue
		}
		attempt, ok := attempts[final.ID]
		if !ok || attempt.ProviderID != usage.ProviderID || attempt.ResourceID != usage.ProviderResourceID {
			continue
		}
		amount := final.Pricing.Charge.USD
		if final.Pricing.Charge.Currency == "USD" {
			amount = final.Pricing.Charge.Amount
		}
		if parsed, err := metering.Decimal(amount); err == nil && parsed.Sign() == 0 {
			usage.ProviderCostKnown = true
		}
	}
	return nil
}
