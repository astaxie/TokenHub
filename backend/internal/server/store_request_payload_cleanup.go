package server

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

func ensureRequestPayloadCreatedAtIndex(db *gorm.DB, driver string) error {
	createIndex := "CREATE INDEX IF NOT EXISTS"
	if driver == "postgres" {
		createIndex = "CREATE INDEX CONCURRENTLY IF NOT EXISTS"
	}
	if err := db.Exec(createIndex + ` idx_request_payload_logs_created_at
ON request_payload_logs(created_at)`).Error; err != nil {
		return fmt.Errorf("index request payload creation time: %w", err)
	}
	return nil
}

func (s *GormStore) DeleteRequestPayloadLogsBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, fmt.Errorf("request payload cleanup batch size must be positive")
	}
	var deleted int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&RequestPayloadLog{}).
			Where("created_at < ?", cutoff).
			Order("created_at ASC").
			Order("id ASC").
			Limit(batchSize).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		result := tx.Where("id IN ?", ids).Delete(&RequestPayloadLog{})
		deleted = result.RowsAffected
		return result.Error
	})
	return deleted, err
}
