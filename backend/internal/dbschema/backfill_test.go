package dbschema

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func mustExecutor(t *testing.T, db *sql.DB, backfills []Backfill, opts ...BackfillOption) *BackfillExecutor {
	t.Helper()
	executor, err := NewBackfillExecutor(db, DialectSQLite, backfills, opts...)
	if err != nil {
		t.Fatalf("NewBackfillExecutor: %v", err)
	}
	return executor
}

// seedBackfillWork creates a work table with `total` rows left to convert.
func seedBackfillWork(t *testing.T, db *sql.DB, total int) {
	t.Helper()
	if _, err := db.Exec("CREATE TABLE backfill_work (id INTEGER PRIMARY KEY, converted INTEGER NOT NULL DEFAULT 0)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= total; i++ {
		if _, err := db.Exec("INSERT INTO backfill_work (id, converted) VALUES (?, 0)", i); err != nil {
			t.Fatal(err)
		}
	}
}

// convertBatch converts up to limit unconverted rows and reports how many
// remain; it resumes safely from any committed state.
func convertBatch(ctx context.Context, db Execer, limit int) (int64, error) {
	if _, err := db.ExecContext(ctx,
		"UPDATE backfill_work SET converted = 1 WHERE id IN (SELECT id FROM backfill_work WHERE converted = 0 LIMIT ?)", limit); err != nil {
		return 0, err
	}
	var remaining int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM backfill_work WHERE converted = 0").Scan(&remaining); err != nil {
		return 0, err
	}
	return remaining, nil
}

func remainingWork(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var remaining int64
	if err := db.QueryRow("SELECT COUNT(*) FROM backfill_work WHERE converted = 0").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	return remaining
}

func backfillStateByID(t *testing.T, executor *BackfillExecutor, id string) BackfillState {
	t.Helper()
	states, err := executor.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, state := range states {
		if state.ID == id {
			return state
		}
	}
	t.Fatalf("backfill state %q not found", id)
	return BackfillState{}
}

func TestBackfillRegistryValidation(t *testing.T) {
	db := newTestDB(t)
	if _, err := NewBackfillExecutor(db, DialectSQLite, []Backfill{{ID: "x", Mode: BackfillBlocking}}); err == nil {
		t.Fatal("expected validation error for blocking backfill without RunBlocking")
	}
	if _, err := NewBackfillExecutor(db, DialectSQLite, []Backfill{{ID: "x", Mode: BackfillOnline}}); err == nil {
		t.Fatal("expected validation error for online backfill without RunBatch")
	}
	if _, err := NewBackfillExecutor(db, DialectSQLite, []Backfill{{ID: "x"}}); err == nil {
		t.Fatal("expected validation error for missing mode")
	}
	duplicate := []Backfill{
		{ID: "x", Mode: BackfillBlocking, RunBlocking: func(context.Context, Execer) error { return nil }},
		{ID: "x", Mode: BackfillBlocking, RunBlocking: func(context.Context, Execer) error { return nil }},
	}
	if _, err := NewBackfillExecutor(db, DialectSQLite, duplicate); err == nil {
		t.Fatal("expected validation error for duplicate backfill ID")
	}
}

func TestBlockingBackfillCompletesOnce(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedBackfillWork(t, db, 6)
	invocations := 0
	executor := mustExecutor(t, db, []Backfill{{
		ID:   "blocking-demo",
		Mode: BackfillBlocking,
		RunBlocking: func(ctx context.Context, db Execer) error {
			invocations++
			_, err := db.ExecContext(ctx, "UPDATE backfill_work SET converted = 1")
			return err
		},
	}})
	if err := executor.RunBlocking(ctx); err != nil {
		t.Fatalf("RunBlocking: %v", err)
	}
	if invocations != 1 {
		t.Fatalf("expected one invocation, got %d", invocations)
	}
	if remainingWork(t, db) != 0 {
		t.Fatal("blocking backfill did not convert everything")
	}
	state := backfillStateByID(t, executor, "blocking-demo")
	if state.State != BackfillStateComplete || state.Remaining != 0 {
		t.Fatalf("unexpected completed state: %+v", state)
	}
	// A second run skips the completed backfill entirely.
	if err := executor.RunBlocking(ctx); err != nil {
		t.Fatalf("second RunBlocking: %v", err)
	}
	if invocations != 1 {
		t.Fatalf("completed backfill must not rerun, invocations=%d", invocations)
	}
}

func TestBlockingBackfillFailureRetriesOnNextRun(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedBackfillWork(t, db, 3)
	failFirst := true
	executor := mustExecutor(t, db, []Backfill{{
		ID:   "blocking-flaky",
		Mode: BackfillBlocking,
		RunBlocking: func(context.Context, Execer) error {
			if failFirst {
				failFirst = false
				return errors.New("transient failure")
			}
			return nil
		},
	}})
	err := executor.RunBlocking(ctx)
	requireErrorCode(t, err, ErrCodeBackfillFailed)
	if state := backfillStateByID(t, executor, "blocking-flaky"); state.State != BackfillStateRunning {
		t.Fatalf("failed backfill must stay running, got %+v", state)
	}
	if err := executor.RunBlocking(ctx); err != nil {
		t.Fatalf("retry RunBlocking: %v", err)
	}
	if state := backfillStateByID(t, executor, "blocking-flaky"); state.State != BackfillStateComplete {
		t.Fatalf("expected complete after retry, got %+v", state)
	}
}

func TestOnlineBackfillBatchesToCompletion(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedBackfillWork(t, db, 10)
	executor := mustExecutor(t, db, []Backfill{{
		ID:         "online-demo",
		Mode:       BackfillOnline,
		RunBatch:   convertBatch,
		BatchLimit: 4,
	}})
	var rounds int
	for range 10 {
		progress, err := executor.RunOnlineBatch(ctx)
		if err != nil {
			t.Fatalf("RunOnlineBatch: %v", err)
		}
		rounds++
		if len(progress) == 1 && progress[0].Remaining == 0 {
			break
		}
	}
	if remainingWork(t, db) != 0 {
		t.Fatalf("online backfill did not finish, %d rows left after %d rounds", remainingWork(t, db), rounds)
	}
	if rounds != 3 {
		t.Fatalf("expected 3 batches of 4/4/2 rows, got %d rounds", rounds)
	}
	state := backfillStateByID(t, executor, "online-demo")
	if state.State != BackfillStateComplete {
		t.Fatalf("expected complete state, got %+v", state)
	}
	// Completed online backfills never re-batch.
	progress, err := executor.RunOnlineBatch(ctx)
	if err != nil {
		t.Fatalf("post-complete RunOnlineBatch: %v", err)
	}
	if len(progress) != 0 {
		t.Fatalf("completed backfill must not report progress, got %+v", progress)
	}
}

func TestOnlineLeaseBlocksSecondExecutor(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedBackfillWork(t, db, 10)
	fixedClock := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	registry := []Backfill{{
		ID:         "online-leased",
		Mode:       BackfillOnline,
		RunBatch:   convertBatch,
		BatchLimit: 4,
	}}
	ownerA := mustExecutor(t, db, registry,
		WithBackfillOwner("executor-a"),
		WithBackfillClock(func() time.Time { return fixedClock }))
	progress, err := ownerA.RunOnlineBatch(ctx)
	if err != nil {
		t.Fatalf("owner A batch: %v", err)
	}
	if len(progress) != 1 || progress[0].Remaining != 6 {
		t.Fatalf("expected first batch with 6 remaining, got %+v", progress)
	}
	// Owner B at the same instant cannot claim the live lease.
	ownerB := mustExecutor(t, db, registry,
		WithBackfillOwner("executor-b"),
		WithBackfillClock(func() time.Time { return fixedClock }))
	progress, err = ownerB.RunOnlineBatch(ctx)
	if err != nil {
		t.Fatalf("owner B batch: %v", err)
	}
	if len(progress) != 0 {
		t.Fatalf("live lease must block other executors, got %+v", progress)
	}
	if remainingWork(t, db) != 6 {
		t.Fatalf("owner B must not have converted rows, %d left", remainingWork(t, db))
	}
	// After the lease expires, owner B takes over and finishes the task. The
	// takeover instant is strictly past the expiry: the atomic claim treats an
	// exactly-current lease as still live.
	ownerB = mustExecutor(t, db, registry,
		WithBackfillOwner("executor-b"),
		WithBackfillLeaseTTL(time.Minute),
		WithBackfillClock(func() time.Time { return fixedClock.Add(3 * time.Minute) }))
	for range 10 {
		progress, err = ownerB.RunOnlineBatch(ctx)
		if err != nil {
			t.Fatalf("owner B takeover batch: %v", err)
		}
		if len(progress) == 1 && progress[0].Remaining == 0 {
			break
		}
	}
	if remainingWork(t, db) != 0 {
		t.Fatal("lease takeover did not finish the backfill")
	}
}

func TestOnlineBackfillRejectsProgressAfterLeaseLoss(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	executor := mustExecutor(t, db, []Backfill{{
		ID:   "online-lease-loss",
		Mode: BackfillOnline,
		RunBatch: func(ctx context.Context, db Execer, _ int) (int64, error) {
			_, err := db.ExecContext(ctx,
				"UPDATE data_backfills SET lease_owner = ? WHERE id = ?", "executor-b", "online-lease-loss")
			return 7, err
		},
	}}, WithBackfillOwner("executor-a"))

	progress, err := executor.RunOnlineBatch(ctx)
	requireErrorCode(t, err, ErrCodeBackfillFailed)
	if len(progress) != 0 {
		t.Fatalf("lost lease must not report progress: %+v", progress)
	}
	state := backfillStateByID(t, executor, "online-lease-loss")
	if state.LeaseOwner != "executor-b" || state.Remaining != -1 {
		t.Fatalf("lost lease must preserve the new owner's ledger state: %+v", state)
	}
}
