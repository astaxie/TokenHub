package dbschema

import (
	"context"
	"errors"
	"testing"
)

func TestRepairVerifiedCompleteClearsDirty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := mustRunner(t, db, nil).Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	// The migration body completes but the first postcondition call fails,
	// simulating a crash between body commit and dirty-marker cleanup.
	postconditionPasses := false
	runner := mustRunner(t, db, []Migration{{
		Version:          2,
		Name:             "crash-after-body",
		Statements:       []string{"CREATE TABLE repair_demo (id INTEGER PRIMARY KEY)"},
		NonTransactional: true,
		Postcondition: func(ctx context.Context, ex MigrationExecer) error {
			if postconditionPasses {
				return nil
			}
			postconditionPasses = true
			return errors.New("target state not proven yet")
		},
	}})
	if _, err := runner.Migrate(ctx); err == nil {
		t.Fatal("expected the first run to leave a dirty marker")
	}
	status, err := runner.Status(ctx)
	if err != nil || !status.Dirty {
		t.Fatalf("expected dirty marker, got %+v err=%v", status, err)
	}
	postconditionPasses = true
	outcome, err := runner.Repair(ctx, 2)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if outcome != RepairVerifiedComplete {
		t.Fatalf("expected verified_complete, got %q", outcome)
	}
	requireCleanApplied(t, db, 2)
	if err := runner.Verify(ctx); err != nil {
		t.Fatalf("Verify after repair: %v", err)
	}
}

func TestRepairSafeRetryReapplies(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := mustRunner(t, db, nil).Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	bodyFails := true
	runner := mustRunner(t, db, []Migration{{
		Version: 2,
		Name:    "retryable",
		Go: func(ctx context.Context, ex MigrationExecer) error {
			if bodyFails {
				bodyFails = false
				return errors.New("executor died mid-run")
			}
			_, err := ex.ExecContext(ctx, "CREATE TABLE retry_demo (id INTEGER PRIMARY KEY)")
			return err
		},
		NonTransactional: true,
		SafeRetry:        true,
		Postcondition: func(ctx context.Context, ex MigrationExecer) error {
			var count int
			if err := ex.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'retry_demo'").Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				return errors.New("retry_demo not created yet")
			}
			return nil
		},
		ChecksumOverride: "repair-test-retryable-v1",
	}})
	if _, err := runner.Migrate(ctx); err == nil {
		t.Fatal("expected the first run to fail and leave a dirty marker")
	}
	if tableExists(t, db, "retry_demo") {
		t.Fatal("failed body must not have created the table")
	}
	outcome, err := runner.Repair(ctx, 2)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if outcome != RepairRetried {
		t.Fatalf("expected retried, got %q", outcome)
	}
	if !tableExists(t, db, "retry_demo") {
		t.Fatal("retry did not apply the migration")
	}
	requireCleanApplied(t, db, 2)
	if err := runner.Verify(ctx); err != nil {
		t.Fatalf("Verify after repair: %v", err)
	}
}

func TestRepairRefusedWithoutSafeRetry(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := mustRunner(t, db, nil).Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner := mustRunner(t, db, []Migration{{
		Version: 2,
		Name:    "not-retryable",
		Go: func(context.Context, MigrationExecer) error {
			return errors.New("executor died mid-run")
		},
		NonTransactional: true,
		Postcondition: func(context.Context, MigrationExecer) error {
			return errors.New("target state not proven")
		},
		ChecksumOverride: "repair-test-not-retryable-v1",
	}})
	if _, err := runner.Migrate(ctx); err == nil {
		t.Fatal("expected the first run to fail")
	}
	_, err := runner.Repair(ctx, 2)
	requireErrorCode(t, err, ErrCodeRepairRefused)
	status, statusErr := runner.Status(ctx)
	if statusErr != nil || !status.Dirty {
		t.Fatalf("dirty marker must survive a refused repair, got %+v err=%v", status, statusErr)
	}
}

func TestRepairWithoutDirtyMarkerRefused(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	_, err := runner.Repair(ctx, BaselineVersion)
	requireErrorCode(t, err, ErrCodeNotDirty)
}

func TestRegistryRejectsUnsafeSafeRetry(t *testing.T) {
	db := newTestDB(t)
	transactional := Migration{
		Version: 2, Name: "transactional-retry", Statements: []string{"SELECT 1"}, SafeRetry: true,
	}
	if _, err := NewRunner(db, DialectSQLite, []Migration{transactional}); err == nil {
		t.Fatal("expected registry validation to reject SafeRetry without NonTransactional")
	}
}
