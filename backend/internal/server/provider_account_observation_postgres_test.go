//go:build integration

package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

type providerObservationOrderingKey struct{}

type providerObservationOrderingResult struct {
	operation string
	err       error
}

func testPostgresProviderObservationDeletionOrdering(t *testing.T, storeA, storeB *GormStore) {
	t.Helper()
	for _, target := range []string{"resource", "provider"} {
		for _, first := range []string{"quota", "delete"} {
			for _, existing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s-first/existing-%t", target, first, existing), func(t *testing.T) {
					testPostgresProviderObservationDeletionOrder(t, storeA, storeB, target, first, existing)
				})
			}
		}
	}
}

func testPostgresProviderObservationDeletionOrder(t *testing.T, storeA, storeB *GormStore, target, first string, existing bool) {
	t.Helper()
	suffix := NewID("observation-order")
	provider := storeA.AddProvider(Provider{ID: "prv_" + suffix, Name: suffix, Type: ProviderMock, Status: StatusActive})
	resource, err := storeA.AddProviderResource(ProviderResource{ID: "rsrc_" + suffix, ProviderID: provider.ID, Name: suffix, ResourceType: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	// Use the persisted timestamp precision, as production request snapshots do.
	if err := storeA.db.First(&resource, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if existing {
		if err := storeA.SaveProviderResourceQuota(resource.ID, provider.Type, `{"plan":"previous"}`, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), providerObservationOrderingKey{}, suffix), 10*time.Second)
	paused, resume, backendPID := make(chan struct{}), make(chan struct{}), make(chan int, 1)
	var pauseOnce, releaseOnce, pidOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	holder, waiter := storeA, storeB
	if first == "delete" {
		holder, waiter = storeB, storeA
	}
	callbackName := "test:provider-observation-order:" + suffix
	var workers sync.WaitGroup
	defer func() {
		release()
		cancel()
		stopped := make(chan struct{})
		go func() { workers.Wait(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("Observation ordering workers did not stop after cancellation")
			return
		}
		_ = holder.db.Callback().Query().Remove(callbackName + ":hold")
		_ = waiter.db.Callback().Query().Remove(callbackName + ":pid")
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		db := storeA.db.WithContext(cleanupCtx)
		_ = db.Where("resource_id = ?", resource.ID).Delete(&ProviderResourceObservation{}).Error
		_ = db.Where("id = ?", resource.ID).Delete(&ProviderResource{}).Error
		_ = db.Where("id = ?", provider.ID).Delete(&Provider{}).Error
	}()
	matches := func(tx *gorm.DB) bool {
		return tx.Statement.Table == "provider_resources" && tx.Statement.Context.Value(providerObservationOrderingKey{}) == suffix
	}
	if err := holder.db.Callback().Query().After("gorm:query").Register(callbackName+":hold", func(tx *gorm.DB) {
		if tx.Error != nil || !matches(tx) {
			return
		}
		pauseOnce.Do(func() {
			close(paused)
			select {
			case <-resume:
			case <-ctx.Done():
				_ = tx.AddError(ctx.Err())
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := waiter.db.Callback().Query().Before("gorm:query").Register(callbackName+":pid", func(tx *gorm.DB) {
		if !matches(tx) {
			return
		}
		pidOnce.Do(func() {
			var pid int
			if err := tx.Statement.ConnPool.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				_ = tx.AddError(err)
				return
			}
			backendPID <- pid
		})
	}); err != nil {
		t.Fatal(err)
	}
	results := make(chan providerObservationOrderingResult, 2)
	start := func(operation string) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var operationErr error
			if operation == "quota" {
				operationErr = storeA.SaveProviderResourceQuotaIfCurrent(ctx, resource.ID, provider.Type, `{"plan":"fresh"}`, time.Now(), resource)
			} else if target == "resource" {
				operationErr = storeB.WithContext(ctx).DeleteProviderResource(resource.ID)
			} else {
				operationErr = storeB.WithContext(ctx).DeleteProvider(provider.ID)
			}
			results <- providerObservationOrderingResult{operation, operationErr}
		}()
	}
	start(first)
	select {
	case <-paused:
	case result := <-results:
		t.Fatalf("First operation did not reach its resource read barrier: %s: %v", result.operation, result.err)
	case <-ctx.Done():
		t.Fatal("First operation did not reach its resource read barrier")
	}
	second := "delete"
	if first == "delete" {
		second = "quota"
	}
	start(second)
	var pid int
	select {
	case pid = <-backendPID:
	case result := <-results:
		t.Fatalf("Waiting operation did not reach its resource query: %s: %v", result.operation, result.err)
	case <-ctx.Done():
		t.Fatal("Waiting operation did not reach its resource query")
	}
	if err := waitForProviderObservationRowLock(ctx, storeA.db, pid, results); err != nil {
		t.Fatal(err)
	}
	release()
	for range 2 {
		select {
		case result := <-results:
			if first == "delete" && result.operation == "quota" {
				if result.err == nil || AsHTTPError(result.err).Status != http.StatusNotFound {
					t.Errorf("Quota save after deletion = %v, want resource not found", result.err)
				}
			} else if result.err != nil {
				t.Errorf("Concurrent %s failed: %v", result.operation, result.err)
			}
		case <-ctx.Done():
			t.Fatal("Concurrent quota save and deletion did not finish")
		}
	}
	for _, check := range []struct {
		model any
		key   string
	}{
		{&ProviderResource{}, "id"},
		{&ProviderResourceObservation{}, "resource_id"},
	} {
		var count int64
		if err := storeA.db.WithContext(ctx).Model(check.model).Where(check.key+" = ?", resource.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("Deletion retained %T rows: %d", check.model, count)
		}
	}
}

func waitForProviderObservationRowLock(ctx context.Context, db *gorm.DB, pid int, results <-chan providerObservationOrderingResult) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case result := <-results:
			return fmt.Errorf("%s completed before the resource lock was released: %v", result.operation, result.err)
		case <-ctx.Done():
			return fmt.Errorf("operation never waited for the resource row lock: %w", ctx.Err())
		default:
		}
		var waiting int64
		if err := db.WithContext(ctx).Raw("SELECT count(*) FROM pg_stat_activity WHERE pid = ? AND wait_event_type = 'Lock'", pid).Scan(&waiting).Error; err != nil {
			return err
		}
		if waiting > 0 {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
