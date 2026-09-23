package server

import (
	"encoding/json"

	"tokenhub/backend/internal/reconciliation"
)

// A legacy zero has no presence bit. Only a matching persisted, explicitly
// versioned price and zero charge can establish that the final attempt was free.
func (s *GormStore) applyZeroCostEvidence(usages []reconciliation.Usage) error {
	for start := 0; start < len(usages); start += 500 {
		end := min(start+500, len(usages))
		ids := []string{}
		for _, usage := range usages[start:end] {
			if usage.ProviderCostUSD == 0 {
				ids = append(ids, usage.RequestID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		var rows []meteringEntry
		if err := s.db.Where("scope IN ? AND kind IN ?", ids, []string{"attempt_prepared", "shadow_settlement"}).Find(&rows).Error; err != nil {
			return err
		}
		attempts := map[string]meteringAttemptSnapshot{}
		settlements := map[string][]meteringAttemptCharge{}
		for _, row := range rows {
			if row.Kind == "attempt_prepared" {
				var attempt meteringAttemptSnapshot
				if err := json.Unmarshal([]byte(row.Payload), &attempt); err != nil {
					return err
				}
				attempts[attempt.ID] = attempt
			} else {
				var payload struct {
					Attempts []meteringAttemptCharge `json:"attempts"`
				}
				if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
					return err
				}
				settlements[row.Scope] = payload.Attempts
			}
		}
		for index := start; index < end; index++ {
			usage := &usages[index]
			if usage.ProviderCostUSD != 0 {
				continue
			}
			var final *meteringAttemptCharge
			for _, charge := range settlements[usage.RequestID] {
				if final == nil || charge.Number > final.Number {
					copy := charge
					final = &copy
				}
			}
			if final == nil {
				continue
			}
			attempt, ok := attempts[final.ID]
			price, charge := final.Charge.Price, final.Charge.Charge
			if !ok || attempt.ProviderID != usage.ProviderID || attempt.ResourceID != usage.ProviderResourceID || price == nil || price.Version == "" || charge == nil {
				continue
			}
			usd := charge.USD
			if charge.Currency == "USD" {
				usd = charge.Amount
			}
			usage.ProviderCostKnown = equalStatementMoney(usd, "0")
		}
	}
	return nil
}
