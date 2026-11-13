package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/allowance"
)

func (s *Server) refreshAllowanceEntitlements(ctx context.Context, resource ProviderResource, cfg providerAllowanceConfig) {
	store, ok := s.store.(*GormStore)
	if !ok {
		return
	}
	observation, found := s.store.GetProviderResourceObservation(resource.ID)
	a := allowance.Snapshot{Version: allowance.Version, Status: allowance.Unknown}
	if found {
		at := time.Time{}
		if observation.QuotaFetchedAt != nil {
			at = *observation.QuotaFetchedAt
		}
		a = allowance.Read([]byte(observation.QuotaSnapshot), at)
	}
	now := time.Now().UTC()
	if err := store.refreshManualAllowance(ctx, resource.ID, cfg, now); err != nil {
		return
	}
	a, err := store.withManualAllowance(ctx, resource.ID, a, cfg.Policy.Wallet, now)
	if err != nil {
		return
	}
	if cfg.Policy.Wallet.Enabled {
		data, _ := json.Marshal(map[string]any{"allowance": a})
		_ = recordProviderAllowancePoint(store.db.WithContext(ctx), resource.ID, string(data), now)
	}
	if cfg.Policy.Alerts.Enabled {
		_ = store.recordAllowanceAlerts(ctx, resource, a, cfg.Policy.Alerts, now)
	}

	// Only the Codex implementation has a durable, reconciled reset executor.
	provider, exists := s.store.GetProvider(resource.ProviderID)
	if !exists || provider.Type != ProviderOpenAICodex {
		return
	}
	if (a.Resets == nil || a.Resets.Count == 0) && !cfg.Policy.Reset.BeforeExpiry && !cfg.Policy.Reset.OnExhausted {
		return
	}
	if a.Resets != nil && !a.Resets.Unknown && now.Sub(a.Resets.CheckedAt) < 5*time.Minute && !cfg.Policy.Reset.BeforeExpiry && !cfg.Policy.Reset.OnExhausted {
		return
	}
	details, err := s.queryOpenAIAccountQuotaResetCredits(ctx, resource.ID)
	if err != nil {
		return
	}
	a.Resets = &allowance.ResetCredits{Count: details.AvailableCount, CheckedAt: now}
	if details.PendingOperation != nil {
		a.Resets.Unknown = true
	}
	for _, credit := range details.Credits {
		if credit.Status != "available" {
			continue
		}
		card := allowance.ResetCard{ID: credit.ID}
		if credit.ExpiresAt != nil {
			t, err := time.Parse(time.RFC3339, *credit.ExpiresAt)
			if err != nil {
				a.Resets.Unknown = true
				continue
			}
			card.ExpiresAt = &t
		}
		a.Resets.Cards = append(a.Resets.Cards, card)
	}
	if len(a.Resets.Cards) != details.AvailableCount {
		a.Resets.Unknown = true
	}
	if err := store.saveAllowanceEntitlements(ctx, resource, cfg.Revision, a); err != nil {
		return
	}
	if !cfg.Policy.Reset.BeforeExpiry && !cfg.Policy.Reset.OnExhausted {
		return
	}
	// An exhausted reset additionally requires an execution-created intent whose
	// candidates came from that request's already authorized route plan.
	noAlternative := s.allowanceResetIntentCurrent(ctx, resource.ID, now)
	if allowance.Evaluate(a, cfg.Policy, "", now, accountAllowanceMaxAge).Held || allowance.ResetHeldByCap(a, cfg.Policy, now, accountAllowanceMaxAge) {
		return
	}
	card, reason, eligible := allowance.ResetCandidate(a, cfg.Policy.Reset, now, noAlternative)
	if !eligible {
		return
	}
	_ = s.runAllowanceReset(ctx, resource, cfg, card, reason, details.AvailableCount, now)
}

func (s *GormStore) recordAllowanceAlerts(ctx context.Context, resource ProviderResource, a allowance.Snapshot, p allowance.AlertPolicy, now time.Time) error {
	if a.For("", now, accountAllowanceMaxAge).Status == allowance.QueryError || a.FetchedAt.IsZero() || now.Sub(a.FetchedAt) > accountAllowanceMaxAge {
		return nil
	}
	for _, w := range a.Windows {
		if w.Unknown || w.Informational || w.ResetAt != nil && !now.Before(*w.ResetAt) {
			continue
		}
		cycle := "wallet"
		if w.ResetAt != nil {
			cycle = w.ResetAt.UTC().Format(time.RFC3339)
		}
		conditions := map[string]bool{}
		threshold := p.UsedPercent
		if threshold == 0 {
			threshold = 90
		}
		if w.UsedPercent != nil {
			conditions["allowance_high_usage"] = *w.UsedPercent >= threshold
		}
		if p.LowBalance != nil && w.Balance != nil {
			conditions["allowance_low_balance"] = *w.Balance <= *p.LowBalance
		}
		if p.UnusedBeforeResetSeconds > 0 && w.ResetAt != nil && w.UsedPercent != nil {
			conditions["allowance_unused_before_reset"] = w.ResetAt.Sub(now) <= time.Duration(p.UnusedBeforeResetSeconds)*time.Second && *w.UsedPercent < threshold
		}
		for kind, active := range conditions {
			digest := sha256.Sum256([]byte(resource.ID + "\x00" + allowanceWalletKey(w) + "\x00" + cycle + "\x00" + kind))
			id := fmt.Sprintf("ala_%x", digest[:])
			err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				row := providerAllowanceEvent{ID: id, ResourceID: resource.ID, Kind: kind, Cycle: cycle, State: "armed", CreatedAt: now, UpdatedAt: now, Details: w.Name}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
					return err
				}
				if !active {
					return tx.Model(&providerAllowanceEvent{}).Where("id = ?", id).Updates(map[string]any{"state": "armed", "updated_at": now}).Error
				}
				changed := tx.Model(&providerAllowanceEvent{}).Where("id = ? AND state = ?", id, "armed").Updates(map[string]any{"state": "fired", "updated_at": now})
				if changed.Error != nil {
					return changed.Error
				}
				if changed.RowsAffected == 0 {
					return nil
				}
				alert := AlertEvent{ID: NewID("alt"), ScopeType: "provider", ScopeID: resource.ProviderID, ResourceID: resource.ID, Severity: "warning", Code: kind, Message: fmt.Sprintf("Provider account %s: %s (%s)", resource.ID, kind, w.Name), CreatedAt: now}
				if err := tx.Create(&alert).Error; err != nil {
					return err
				}
				for _, channel := range p.ChannelIDs {
					payload, _ := json.Marshal(allowanceAlertDelivery{AlertID: alert.ID, ChannelID: channel})
					event := providerAllowanceEvent{ID: NewID("alad"), ResourceID: resource.ID, Kind: "alert_delivery", Cycle: cycle, State: "queued", CreatedAt: now, UpdatedAt: now, Details: string(payload)}
					if err := tx.Create(&event).Error; err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}
