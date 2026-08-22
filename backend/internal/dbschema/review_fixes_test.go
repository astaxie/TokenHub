package dbschema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newSingleConnDB mirrors the server's SQLite pool (one connection): helpers
// that request a second connection while holding one deadlock there.
func newSingleConnDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000", filepath.Join(t.TempDir(), "single.db"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openSharedSQLiteTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open shared sqlite: %v", err)
	}
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestNonTransactionalSuccessWithSingleConnectionPool pins the deadlock fix:
// the dirty-marker cleanup must run on the connection the migration already
// holds instead of asking a one-connection pool for a second one.
func TestNonTransactionalSuccessWithSingleConnectionPool(t *testing.T) {
	db := newSingleConnDB(t)
	ctx := context.Background()
	if _, err := mustRunner(t, db, nil).Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner := mustRunner(t, db, []Migration{{
		Version:          2,
		Name:             "online-index-single-conn",
		Statements:       []string{"CREATE TABLE single_conn_demo (id INTEGER PRIMARY KEY)"},
		NonTransactional: true,
		Postcondition: func(ctx context.Context, ex MigrationExecer) error {
			var count int
			return ex.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'single_conn_demo'").Scan(&count)
		},
	}})
	result, err := runner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate on a single-connection pool: %v", err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("expected version 2 applied, got %+v", result.Applied)
	}
	requireCleanApplied(t, db, 2)
}

func TestSQLiteNonTransactionalMigrationExcludesConcurrentRepair(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000", filepath.Join(t.TempDir(), "shared-non-transactional.db"))
	firstDB := openSharedSQLiteTestDB(t, dsn)
	secondDB := openSharedSQLiteTestDB(t, dsn)
	ctx := context.Background()
	if _, err := mustRunner(t, firstDB, nil).Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	bodyStarted := make(chan struct{})
	allowBody := make(chan struct{})
	var startOnce sync.Once
	migration := Migration{
		Version:          2,
		Name:             "coordinated-non-transactional",
		NonTransactional: true,
		Go: func(ctx context.Context, db MigrationExecer) error {
			startOnce.Do(func() { close(bodyStarted) })
			select {
			case <-allowBody:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := db.ExecContext(ctx, "CREATE TABLE coordinated_non_tx (id INTEGER PRIMARY KEY)")
			return err
		},
		Postcondition: func(ctx context.Context, db MigrationExecer) error {
			var count int
			return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'coordinated_non_tx'").Scan(&count)
		},
		ChecksumOverride: "coordinated-non-transactional-test",
	}
	firstRunner := mustRunner(t, firstDB, []Migration{migration})
	waiting := make(chan struct{})
	var waitingOnce sync.Once
	secondRunner := mustRunner(t, secondDB, []Migration{migration}, WithLogger(func(string, ...any) {
		waitingOnce.Do(func() { close(waiting) })
	}))

	migrateDone := make(chan error, 1)
	go func() {
		_, err := firstRunner.Migrate(ctx)
		migrateDone <- err
	}()
	select {
	case <-bodyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("non-transactional migration did not reach its body")
	}
	var dirty bool
	if err := secondDB.QueryRow("SELECT dirty FROM schema_migrations WHERE version = 2").Scan(&dirty); err != nil || !dirty {
		t.Fatalf("expected committed dirty marker while body runs, dirty=%t err=%v", dirty, err)
	}

	repairDone := make(chan error, 1)
	go func() {
		_, err := secondRunner.Repair(ctx, 2)
		repairDone <- err
	}()
	select {
	case <-waiting:
	case err := <-repairDone:
		t.Fatalf("repair escaped SQLite coordination while migration was running: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("repair did not contend on the SQLite migration lock")
	}
	select {
	case err := <-repairDone:
		t.Fatalf("repair completed before the migration released its lock: %v", err)
	default:
	}

	close(allowBody)
	if err := <-migrateDone; err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	err := <-repairDone
	requireErrorCode(t, err, ErrCodeNotDirty)
	requireCleanApplied(t, firstDB, 2)
}

func TestSQLiteContractPreconditionAndMutationShareStartupLock(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000", filepath.Join(t.TempDir(), "shared-contract.db"))
	contractDB := openSharedSQLiteTestDB(t, dsn)
	startupDB := openSharedSQLiteTestDB(t, dsn)
	ctx := context.Background()
	registry := contractTestRegistry()
	runner := mustRunner(t, contractDB, registry)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if _, err := runner.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	preconditionStarted := make(chan struct{})
	allowContract := make(chan struct{})
	contractDone := make(chan error, 1)
	go func() {
		_, err := runner.ApplyContract(ctx, ContractOptions{
			RequireCluster: func(ctx context.Context) error {
				close(preconditionStarted)
				select {
				case <-allowContract:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		})
		contractDone <- err
	}()
	select {
	case <-preconditionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("contract did not reach the cluster precondition")
	}

	startupWaiting := make(chan struct{})
	var waitingOnce sync.Once
	startupAcquired := make(chan func(), 1)
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	go func() {
		release, err := AcquireMigrationLock(lockCtx, startupDB, DialectSQLite, time.Second, func(string, ...any) {
			waitingOnce.Do(func() { close(startupWaiting) })
		})
		if err != nil {
			startupAcquired <- nil
			return
		}
		startupAcquired <- release
	}()
	select {
	case <-startupWaiting:
	case release := <-startupAcquired:
		if release != nil {
			release()
		}
		t.Fatal("startup entered the schema section between contract preflight and mutation")
	case <-time.After(2 * time.Second):
		t.Fatal("startup did not contend on the contract migration lock")
	}

	close(allowContract)
	if err := <-contractDone; err != nil {
		t.Fatalf("ApplyContract: %v", err)
	}
	release := <-startupAcquired
	if release == nil {
		t.Fatal("startup did not acquire the schema lock after contract completion")
	}
	release()
	if tableExists(t, contractDB, "contract_demo") {
		t.Fatal("contract mutation did not complete before startup acquired the lock")
	}
}

func TestContractRefusedWhileExpandPending(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := mustRunner(t, db, nil).Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	registry := []Migration{
		{Version: 2, Name: "expand", Statements: []string{"CREATE TABLE contract_gate (id INTEGER PRIMARY KEY)"}},
		{Version: 3, Name: "contract", Phase: PhaseContract, Statements: []string{"DROP TABLE contract_gate"}},
	}
	runner := mustRunner(t, db, registry)
	_, err := runner.PlanContract(ctx)
	requireErrorCode(t, err, ErrCodeExpandPending)
	_, err = runner.ApplyContract(ctx, ContractOptions{})
	requireErrorCode(t, err, ErrCodeExpandPending)
	if tableExists(t, db, "contract_gate") {
		t.Fatal("expand not applied yet; contract target must not exist")
	}
	// Applying the expands unblocks planning.
	if _, err := runner.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	plan, err := runner.PlanContract(ctx)
	if err != nil || len(plan.Migrations) != 1 {
		t.Fatalf("expected contract plannable after expands, got %+v err=%v", plan, err)
	}
}

func TestDataBackfillTableDoesNotBlockFreshAdoption(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	// Simulate `tokenhub db status` creating the backfill ledger on an empty
	// database before the first server start.
	if _, err := db.Exec(`CREATE TABLE data_backfills (
		id TEXT PRIMARY KEY, mode TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending',
		remaining INTEGER NOT NULL DEFAULT -1, lease_owner TEXT NOT NULL DEFAULT '',
		lease_expires_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	runner := mustRunner(t, db, nil, WithFreshBaseline([]string{
		"CREATE TABLE still_fresh (id INTEGER PRIMARY KEY)",
	}))
	result, err := runner.Adopt(ctx, nil)
	if err != nil {
		t.Fatalf("Adopt with backfill ledger present: %v", err)
	}
	if !result.Adopted || !tableExists(t, db, "still_fresh") {
		t.Fatalf("expected fresh adoption from baseline SQL, got %+v", result)
	}
}

func TestLegacyRecognizerRefusesUnrecognizedDatabase(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE alien_payload (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	recognizer := func(ctx context.Context, db *sql.DB) error {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('admin_users', 'projects', 'providers', 'request_logs')").Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return errors.New("no known TokenHub table found")
		}
		return nil
	}
	runner := mustRunner(t, db, nil, WithLegacyRecognizer(recognizer))
	_, err := runner.Adopt(ctx, func(context.Context) error {
		_, createErr := db.Exec("CREATE TABLE legacy_flow_probe (id INTEGER PRIMARY KEY)")
		return createErr
	})
	requireErrorCode(t, err, ErrCodeUnrecognizedDatabase)
	status, statusErr := runner.Status(ctx)
	if statusErr != nil || status.BaselineRecorded {
		t.Fatalf("unrecognized database must not gain a baseline, got %+v err=%v", status, statusErr)
	}
	// The frozen flow never ran: the table its callback would have created is
	// absent (the alien table itself persists untouched).
	if tableExists(t, db, "legacy_flow_probe") {
		t.Fatal("refused adoption must not run the frozen flow")
	}

}
