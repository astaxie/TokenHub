package server

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/allowance"
)

func (s *GormStore) saveAllowanceEntitlements(ctx context.Context, expected ProviderResource, revision string, a allowance.Snapshot) error {
	return s.withClusterLease(ctx, providerResourceMutationLeaseName(expected.ID), func(ctx context.Context) error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var resource ProviderResource
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&resource, "id = ?", expected.ID).Error; err != nil {
				return err
			}
			if !sameProviderAccountSnapshot(resource, expected) {
				return nil
			}
			actual, err := allowanceSettingRevision(tx, expected.ID)
			if err != nil {
				return err
			}
			if actual != revision {
				return nil
			}
			var o ProviderResourceObservation
			if err := tx.First(&o, "resource_id = ?", expected.ID).Error; err != nil {
				return err
			}
			if o.QuotaFetchedAt == nil || !o.QuotaFetchedAt.Equal(a.FetchedAt) {
				return nil
			}
			current := allowance.Read([]byte(o.QuotaSnapshot), *o.QuotaFetchedAt)
			current.Resets = a.Resets
			var root map[string]any
			if err := json.Unmarshal([]byte(o.QuotaSnapshot), &root); err != nil {
				return err
			}
			root["allowance"] = current
			encoded, err := json.Marshal(root)
			if err != nil {
				return err
			}
			if err := recordProviderAllowancePoint(tx, expected.ID, string(encoded), time.Now().UTC()); err != nil {
				return err
			}
			return tx.Model(&o).Updates(map[string]any{"quota_snapshot": string(encoded), "updated_at": time.Now().UTC()}).Error
		})
	})
}

func retainAllowanceResetDetails(raw, previous string, previousAt, now time.Time) string {
	current := allowance.Read([]byte(raw), now)
	old := allowance.Read([]byte(previous), previousAt)
	if current.Resets == nil || old.Resets == nil || current.Resets.Count != old.Resets.Count || old.Resets.Unknown || now.Sub(old.Resets.CheckedAt) > 5*time.Minute {
		return raw
	}
	current.Resets = old.Resets
	var root map[string]any
	if json.Unmarshal([]byte(raw), &root) != nil {
		return raw
	}
	root["allowance"] = current
	encoded, err := json.Marshal(root)
	if err != nil {
		return raw
	}
	return string(encoded)
}
