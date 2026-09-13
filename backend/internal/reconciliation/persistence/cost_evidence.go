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
	usageIndexes := make(map[string][]int)
	seen := make(map[string]struct{})
	for index, usage := range usages {
		if usage.ProviderCostUSD == 0 && strings.TrimSpace(usage.RequestID) != "" {
			requestID := usage.RequestID
			usageIndexes[requestID] = append(usageIndexes[requestID], index)
			if _, ok := seen[requestID]; !ok {
				seen[requestID] = struct{}{}
				ids = append(ids, requestID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		batch := ids[start:end]
		var rows []meteringEvidenceRow
		if err := db.Where("scope IN ? AND kind IN ?", batch, []string{"attempt_prepared", "shadow_settlement"}).Find(&rows).Error; err != nil {
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
		for _, requestID := range batch {
			for _, index := range usageIndexes[requestID] {
				usage := &usages[index]
				var final *meteringAttemptChargeEvidence
				for index := range settlements[requestID] {
					charge := &settlements[requestID][index]
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
		}
	}
	return nil
}
