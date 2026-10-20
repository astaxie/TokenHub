package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestSQLiteSharedMemDBUsageQueries(t *testing.T) {
	for _, dsn := range []string{
		"file:/shared_usage?vfs=memdb", "file:%2Fencoded_shared_usage?%76fs=%6demdb",
		"file:%5Cshared_usage?vfs=memdb", "/bare_shared_usage?vfs=memdb",
		"file:relative_shared_usage?vfs=memdb&cache=shared",
		"file:/private_cache_usage?vfs=memdb&cache=private",
		"file:/memory_mode_usage?vfs=memdb&mode=memory&cache=shared",
	} {
		t.Run(dsn, func(t *testing.T) {
			store, err := NewSQLiteStoreWithConfig(dsn, Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if store.readDB != nil || store.analyticsDB == store.db {
				t.Fatal("shared memdb must retain separate analytics without a WAL read pool")
			}
			record := usageSummaryRecord("shared_usage", "project_memory", "key_memory", "", 10, time.Now().UTC())
			if err := store.db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err = store.StreamUsageGroups(ctx, UsageSummaryScope{Global: true}, []string{"projects", "models"}, func(string, UsageGroup) { cancel() })
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("shared memdb query did not honor cancellation: %v", err)
			}
			query := UsageSummaryQuery{UsageRecords: UsageSummaryScope{Global: true}, RequestLogs: UsageSummaryScope{Global: true}}
			summary, err := store.QueryUsageSummary(t.Context(), query)
			if err != nil || summary.UsageRecordCount != 1 {
				t.Fatalf("shared analytics lost data after cancellation: %+v, %v", summary, err)
			}
			if _, err := store.QueryTokenCostPage(t.Context(), TokenCostQuery{}); err != nil {
				t.Fatal(err)
			}
			if err := store.db.Model(&UsageRecord{}).Where("id = ?", record.ID).Update("total_tokens", 25).Error; err != nil {
				t.Fatalf("shared memdb writer failed after cancellation: %v", err)
			}
		})
	}
}

func TestSQLiteSharedMemDBKeepsReadSnapshot(t *testing.T) {
	store, err := NewSQLiteStoreWithConfig("file:/shared_snapshot?vfs=memdb", Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := usageSummaryRecord("shared_snapshot", "project_memory", "key_memory", "", 10, time.Now().UTC())
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	err = store.withAnalyticsReadSnapshot(t.Context(), func(tx *gorm.DB) error {
		var first UsageRecord
		if err := tx.First(&first, "id = ?", record.ID).Error; err != nil {
			return err
		}
		// A second physical connection cannot change a row in this snapshot.
		// Disable its busy wait so the rejected write is deterministic and fast.
		if err := store.db.Exec("PRAGMA busy_timeout = 0").Error; err != nil {
			return err
		}
		if err := store.db.Model(&UsageRecord{}).Where("id = ?", record.ID).Update("total_tokens", 25).Error; err == nil {
			t.Fatal("shared memdb writer changed a row during the read snapshot")
		}
		var second UsageRecord
		if err := tx.First(&second, "id = ?", record.ID).Error; err != nil {
			return err
		}
		if second.TotalTokens != first.TotalTokens {
			t.Fatalf("shared memdb snapshot changed from %d to %d", first.TotalTokens, second.TotalTokens)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&UsageRecord{}).Where("id = ?", record.ID).Update("total_tokens", 25).Error; err != nil {
		t.Fatalf("shared memdb writer did not recover after snapshot release: %v", err)
	}
}
