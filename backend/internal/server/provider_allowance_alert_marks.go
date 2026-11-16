package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
	"tokenhub/backend/internal/allowance"
)

func (s *GormStore) recordOneAllowanceAlert(ctx context.Context, resource ProviderResource, p allowance.AlertPolicy, key, name, kind, cycle string, active bool, now time.Time) error {
	lock := sha256.Sum256([]byte(resource.ID + "\x00" + key + "\x00" + kind))
	return s.withClusterLease(ctx, fmt.Sprintf("allowance-alert:%x", lock[:]), func(ctx context.Context) error {
		if cycle != "wallet" {
			var previous providerAllowanceEvent
			if s.db.WithContext(ctx).Where("resource_id = ? AND kind = ? AND details = ?", resource.ID, kind, name).Order("updated_at desc").First(&previous).Error == nil {
				old, e1 := time.Parse(time.RFC3339, previous.Cycle)
				current, e2 := time.Parse(time.RFC3339, cycle)
				if e1 == nil && e2 == nil && current.Sub(old).Abs() <= 10*time.Minute {
					cycle = previous.Cycle
				}
			}
		}
		digest := sha256.Sum256([]byte(resource.ID + "\x00" + key + "\x00" + cycle + "\x00" + kind))
		id := fmt.Sprintf("ala_%x", digest[:])
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			row := providerAllowanceEvent{ID: id, ResourceID: resource.ID, Kind: kind, Cycle: cycle, State: "armed", CreatedAt: now, UpdatedAt: now, Details: name}
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
			alert := AlertEvent{ID: NewID("alt"), ScopeType: "provider", ScopeID: resource.ProviderID, ResourceID: resource.ID, Severity: "warning", Code: kind, Message: fmt.Sprintf("Provider account %s: %s (%s)", resource.ID, kind, name), CreatedAt: now}
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
		return err
	})
}
