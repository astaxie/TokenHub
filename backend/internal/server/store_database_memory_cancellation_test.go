package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrivateSQLiteMemoryUsageCancellationPreservesDatabase(t *testing.T) {
	for _, dsn := range []string{"sqlite::memory:", "file::memory:", "file:private_usage_cancel?mode=memory&cache=private"} {
		t.Run(dsn, func(t *testing.T) {
			store, err := NewSQLiteStoreWithConfig(dsn, Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			record := usageSummaryRecord("private_usage_cancel", "project_memory", "key_memory", "", 10, time.Now().UTC())
			if err := store.db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			visited := 0
			err = store.StreamUsageGroups(ctx, UsageSummaryScope{Global: true}, []string{"projects", "models"}, func(dimension string, group UsageGroup) {
				visited++
				if dimension != "projects" || group.UsageRecordCount != 1 {
					t.Errorf("unexpected aggregate before cancellation: dimension=%s group=%+v", dimension, group)
				}
				cancel()
				// Keep the callback active while another request waits for the
				// connection. A canceled SQL transaction can otherwise discard
				// the private database before this reader returns.
				probeCtx, stopProbe := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer stopProbe()
				pool, poolErr := store.db.DB()
				if poolErr != nil {
					t.Error(poolErr)
					return
				}
				if err := pool.PingContext(probeCtx); !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("cancellation released the connection before the reader returned: %v", err)
				}
			})
			if !errors.Is(err, context.Canceled) || visited != 1 {
				t.Fatalf("query did not stop after cancellation: visits=%d error=%v", visited, err)
			}
			var count int64
			if err := store.db.Model(&UsageRecord{}).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("canceled analytics query lost the private database: count=%d error=%v", count, err)
			}
			another := usageSummaryRecord("private_usage_after_cancel", "project_memory", "key_memory", "", 20, time.Now().UTC())
			if err := store.db.Create(&another).Error; err != nil {
				t.Fatalf("canceled analytics query left the writer unusable: %v", err)
			}
			if err := store.db.Model(&UsageRecord{}).Count(&count).Error; err != nil || count != 2 {
				t.Fatalf("write after cancellation was not persisted: count=%d error=%v", count, err)
			}
		})
	}
}

func TestPrivateSQLiteMemoryUsagePoolWaitHonorsDeadline(t *testing.T) {
	for _, dsn := range []string{"sqlite::memory:", "file::memory:", "file:private_usage_wait?mode=memory&cache=private"} {
		t.Run(dsn, func(t *testing.T) {
			store, err := NewSQLiteStoreWithConfig(dsn, Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			record := usageSummaryRecord("private_usage_wait", "project_memory", "key_memory", "", 10, time.Now().UTC())
			if err := store.db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			pool, err := store.db.DB()
			if err != nil {
				t.Fatal(err)
			}
			held, err := pool.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- store.StreamUsageGroups(ctx, UsageSummaryScope{Global: true}, []string{"projects"}, func(string, UsageGroup) {})
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("pool wait ignored the request deadline: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("analytics remained blocked after the request deadline")
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := store.db.Model(&UsageRecord{}).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("timed-out pool wait lost the private database: count=%d error=%v", count, err)
			}
			groups := 0
			if err := store.StreamUsageGroups(t.Context(), UsageSummaryScope{Global: true}, []string{"projects"}, func(string, UsageGroup) { groups++ }); err != nil || groups != 1 {
				t.Fatalf("analytics did not recover after releasing the connection: groups=%d error=%v", groups, err)
			}
		})
	}
}
