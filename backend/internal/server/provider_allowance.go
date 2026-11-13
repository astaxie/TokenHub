package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/allowance"
)

func withNormalizedAllowance(snapshot string, fetchedAt time.Time) string {
	var data map[string]json.RawMessage
	if json.Unmarshal([]byte(snapshot), &data) != nil || data == nil {
		return snapshot
	}
	normalized := allowance.Normalize([]byte(snapshot), fetchedAt)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return snapshot
	}
	data["allowance"] = encoded
	result, err := json.Marshal(data)
	if err != nil {
		return snapshot
	}
	return string(result)
}

// A failed quota read keeps the last successful reading and its original age.
// The credential revision and request start fence late failures after refresh or
// reauthorization, just as successful quota writes fence replaced accounts.
func (s *GormStore) saveProviderQuotaFailure(ctx context.Context, expected ProviderResource, code string, started time.Time) error {
	return s.saveProviderQuotaFailureWithRevision(ctx, expected, code, started, nil)
}

func (s *GormStore) saveProviderQuotaFailureWithRevision(ctx context.Context, expected ProviderResource, code string, started time.Time, revision *string) error {
	return s.withClusterLease(ctx, providerResourceMutationLeaseName(expected.ID), func(leaseCtx context.Context) error {
		return s.db.WithContext(leaseCtx).Transaction(func(tx *gorm.DB) error {
			var resource ProviderResource
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&resource, "id = ?", expected.ID).Error; err != nil {
				return err
			}
			if !sameProviderAccountSnapshot(resource, expected) {
				return nil
			}
			if revision != nil {
				current, err := allowanceSettingRevision(tx, expected.ID)
				if err != nil {
					return err
				}
				if current != *revision {
					return nil
				}
			}
			var provider Provider
			if err := tx.First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
				return err
			}
			var observation ProviderResourceObservation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_id = ?", expected.ID).First(&observation).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if observation.QuotaFetchedAt != nil && observation.QuotaFetchedAt.After(started) {
				return nil
			}
			fetched := time.Time{}
			if observation.QuotaFetchedAt != nil {
				fetched = *observation.QuotaFetchedAt
			}
			normalized := allowance.Read([]byte(observation.QuotaSnapshot), fetched)
			if normalized.CheckedAt.After(started) {
				return nil
			}
			normalized.Status, normalized.ErrorCode, normalized.CheckedAt = allowance.QueryError, code, time.Now().UTC()
			var data map[string]any
			if json.Unmarshal([]byte(observation.QuotaSnapshot), &data) != nil || data == nil {
				data = map[string]any{}
			}
			data["allowance"] = normalized
			encoded, err := json.Marshal(data)
			if err != nil {
				return err
			}
			row := ProviderResourceObservation{ResourceID: expected.ID, AdapterType: provider.Type, QuotaSnapshot: string(encoded), QuotaFetchedAt: observation.QuotaFetchedAt, UpdatedAt: time.Now().UTC()}
			return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"quota_snapshot", "updated_at"})}).Create(&row).Error
		})
	})
}

func (s *Server) recordProviderQuotaFailure(ctx context.Context, expected ProviderResource, err error, started time.Time) {
	if err == nil || ctx.Err() != nil {
		return
	}
	if store, ok := s.store.(interface {
		saveProviderQuotaFailure(context.Context, ProviderResource, string, time.Time) error
	}); ok {
		_ = store.saveProviderQuotaFailure(ctx, expected, AsHTTPError(err).Code, started)
	}
}
