package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestDeleteRequestPayloadLogsBeforeDeletesOneBoundedBatch(t *testing.T) {
	store := NewMemoryStore()
	cutoff := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)

	payloads := make([]RequestPayloadLog, 0, 503)
	for i := 0; i < 501; i++ {
		payloads = append(payloads, RequestPayloadLog{
			ID:        fmt.Sprintf("payload-old-%03d", i),
			RequestID: fmt.Sprintf("request-old-%03d", i),
			CreatedAt: cutoff.Add(-time.Hour),
		})
	}
	payloads = append(payloads,
		RequestPayloadLog{ID: "payload-boundary", RequestID: "request-boundary", CreatedAt: cutoff},
		RequestPayloadLog{ID: "payload-new", RequestID: "request-new", CreatedAt: cutoff.Add(time.Hour)},
	)
	if err := store.db.CreateInBatches(payloads, 100).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := store.DeleteRequestPayloadLogsBefore(context.Background(), cutoff, 500)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 500 {
		t.Fatalf("deleted %d rows, want 500", deleted)
	}

	var remaining []RequestPayloadLog
	if err := store.db.Order("id").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining payload count = %d, want 3", len(remaining))
	}
	remainingByID := make(map[string]RequestPayloadLog, len(remaining))
	for _, payload := range remaining {
		remainingByID[payload.ID] = payload
	}
	for _, id := range []string{"payload-boundary", "payload-new"} {
		if _, ok := remainingByID[id]; !ok {
			t.Fatalf("payload at or after cutoff was deleted: %s", id)
		}
	}

	deleted, err = store.DeleteRequestPayloadLogsBefore(context.Background(), cutoff, 500)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("second batch deleted %d rows, want 1", deleted)
	}
	deleted, err = store.DeleteRequestPayloadLogsBefore(context.Background(), cutoff, 500)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("empty batch deleted %d rows, want 0", deleted)
	}
}

func TestDeleteRequestPayloadLogsBeforeLeavesOperationalRecords(t *testing.T) {
	store := NewMemoryStore()
	cutoff := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	requestID := "request-retained-operational-data"

	if err := store.db.Create(&RequestPayloadLog{ID: "payload-expired", RequestID: requestID, CreatedAt: cutoff.Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	for _, record := range []any{
		&RequestLog{ID: "log-retained", RequestID: requestID, CreatedAt: cutoff.Add(-time.Hour)},
		&UsageRecord{ID: "usage-retained", RequestID: requestID, CreatedAt: cutoff.Add(-time.Hour)},
		&RouteAttemptLog{ID: "attempt-retained", RequestID: requestID},
		&BillingRecord{ID: "billing-retained", ConnectorID: "connector-retained", ExternalID: "external-retained", ExternalRequestID: requestID},
	} {
		if err := store.db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}

	if _, err := store.DeleteRequestPayloadLogsBefore(context.Background(), cutoff, 500); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		model any
		field string
		name  string
	}{
		{model: &RequestLog{}, field: "request_id", name: "request log"},
		{model: &UsageRecord{}, field: "request_id", name: "usage record"},
		{model: &RouteAttemptLog{}, field: "request_id", name: "route attempt"},
		{model: &BillingRecord{}, field: "external_request_id", name: "billing record"},
	} {
		var count int64
		if err := store.db.Model(check.model).Where(check.field+" = ?", requestID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", check.name, count)
		}
	}
}

func TestRequestPayloadLogCreatedAtIsIndexed(t *testing.T) {
	store := NewMemoryStore()
	if !store.db.Migrator().HasIndex(&RequestPayloadLog{}, "idx_request_payload_logs_created_at") {
		t.Fatal("request payload created_at index was not created")
	}
}

func TestRequestPayloadCleanupRunsOncePerUTCDay(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	cutoff := now.UTC().AddDate(0, 0, -7)
	for _, payload := range []RequestPayloadLog{
		{ID: "payload-expired-first", RequestID: "request-expired-first", CreatedAt: cutoff.Add(-time.Second)},
		{ID: "payload-boundary-daily", RequestID: "request-boundary-daily", CreatedAt: cutoff},
	} {
		if err := store.db.Create(&payload).Error; err != nil {
			t.Fatal(err)
		}
	}
	cleanup := newRequestPayloadCleanupService(store, 7)

	deleted, err := cleanup.Run(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("first daily cleanup deleted %d rows, want 1", deleted)
	}

	second := RequestPayloadLog{ID: "payload-expired-second", RequestID: "request-expired-second", CreatedAt: cutoff.Add(-time.Hour)}
	if err := store.db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err = cleanup.Run(context.Background(), now.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("same UTC day cleanup deleted %d rows, want 0", deleted)
	}

	deleted, err = cleanup.Run(context.Background(), now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("next UTC day cleanup deleted %d rows, want 2", deleted)
	}
}

func TestRequestPayloadCleanupDisabledLeavesPayloads(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	if err := store.db.Create(&RequestPayloadLog{
		ID:        "payload-cleanup-disabled",
		RequestID: "request-cleanup-disabled",
		CreatedAt: now.AddDate(0, 0, -365),
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := newRequestPayloadCleanupService(store, 0).Run(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("disabled cleanup deleted %d rows, want 0", deleted)
	}
	var count int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", "payload-cleanup-disabled").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("disabled cleanup left %d payload rows, want 1", count)
	}
}

func TestRequestPayloadCleanupRetriesFailedDailyTask(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -7)
	payloads := make([]RequestPayloadLog, 0, 501)
	for i := 0; i < 501; i++ {
		payloads = append(payloads, RequestPayloadLog{
			ID:        fmt.Sprintf("payload-retry-%03d", i),
			RequestID: fmt.Sprintf("request-retry-%03d", i),
			CreatedAt: cutoff.Add(-time.Hour),
		})
	}
	if err := store.db.CreateInBatches(payloads, 100).Error; err != nil {
		t.Fatal(err)
	}
	callbackName := "test:fail-request-payload-cleanup"
	failure := errors.New("forced cleanup failure")
	deleteAttempts := 0
	if err := store.db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "request_payload_logs" {
			deleteAttempts++
			if deleteAttempts == 2 {
				tx.AddError(failure)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	cleanup := newRequestPayloadCleanupService(store, 7)
	if _, err := cleanup.Run(context.Background(), now); !errors.Is(err, failure) {
		t.Fatalf("cleanup error = %v, want %v", err, failure)
	}
	var remaining int64
	if err := store.db.Model(&RequestPayloadLog{}).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("failed second batch left %d payloads, want 1", remaining)
	}
	var taskStates int64
	if err := store.db.Model(&ClusterTaskState{}).Where("name = ?", requestPayloadCleanupTaskName).Count(&taskStates).Error; err != nil {
		t.Fatal(err)
	}
	if taskStates != 0 {
		t.Fatalf("failed cleanup recorded %d completed task states, want 0", taskStates)
	}
	if err := store.db.Callback().Delete().Remove(callbackName); err != nil {
		t.Fatal(err)
	}

	deleted, err := cleanup.Run(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("retried cleanup deleted %d rows, want 1", deleted)
	}
}

func TestRequestPayloadCleanupRunsOnceAcrossReplicas(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "payload-cleanup.db")
	storeA, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*GormStore{storeA, storeB} {
		sqlDB, err := store.db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	if err := storeA.db.Create(&RequestPayloadLog{
		ID:        "payload-multi-replica",
		RequestID: "request-multi-replica",
		CreatedAt: now.AddDate(0, 0, -8),
	}).Error; err != nil {
		t.Fatal(err)
	}
	var cleanupQueries atomic.Int64
	for index, store := range []*GormStore{storeA, storeB} {
		callbackName := fmt.Sprintf("test:count-payload-cleanup-%d", index)
		if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "request_payload_logs" {
				cleanupQueries.Add(1)
			}
		}); err != nil {
			t.Fatal(err)
		}
		store := store
		t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })
	}

	results := make(chan int64, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for _, store := range []*GormStore{storeA, storeB} {
		wait.Add(1)
		go func(store *GormStore) {
			defer wait.Done()
			deleted, err := newRequestPayloadCleanupService(store, 7).Run(context.Background(), now)
			results <- deleted
			errors <- err
		}(store)
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var totalDeleted int64
	for deleted := range results {
		totalDeleted += deleted
	}
	if totalDeleted != 1 {
		t.Fatalf("replicas deleted %d rows in total, want 1", totalDeleted)
	}
	if got := cleanupQueries.Load(); got != 1 {
		t.Fatalf("replicas queried payload cleanup %d times, want 1", got)
	}
}

func TestRequestPayloadCleanupSchedulerRunsImmediatelyAndShutsDown(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	if err := store.db.Create(&RequestPayloadLog{
		ID:        "payload-scheduler",
		RequestID: "request-scheduler",
		CreatedAt: now.AddDate(0, 0, -8),
	}).Error; err != nil {
		t.Fatal(err)
	}
	cleanup := newRequestPayloadCleanupService(store, 7)
	cleanup.now = func() time.Time { return now }
	cleanup.StartScheduler(time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int64
		if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", "payload-scheduler").Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup cleanup did not run immediately")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cleanup.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown cleanup scheduler: %v", err)
	}
}

func TestServerStartsConfiguredRequestPayloadCleanupScheduler(t *testing.T) {
	store := NewMemoryStore()
	if err := store.db.Create(&RequestPayloadLog{
		ID:        "payload-server-scheduler",
		RequestID: "request-server-scheduler",
		CreatedAt: time.Now().UTC().AddDate(0, 0, -8),
	}).Error; err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, Config{
		AdminToken:                  "dev_admin_token",
		RequestPayloadRetentionDays: 7,
	})
	app.StartRequestPayloadCleanupScheduler()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int64
		if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", "payload-server-scheduler").Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start request payload cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("server shutdown: %v", err)
	}
}
