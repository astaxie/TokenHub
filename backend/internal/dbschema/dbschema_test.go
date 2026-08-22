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

	_ "github.com/mattn/go-sqlite3"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000", filepath.Join(t.TempDir(), "ledger.db"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustRunner(t *testing.T, db *sql.DB, migrations []Migration, opts ...Option) *Runner {
	t.Helper()
	runner, err := NewRunner(db, DialectSQLite, migrations, opts...)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return runner
}

func requireErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	var schemaErr *Error
	if !errors.As(err, &schemaErr) {
		t.Fatalf("expected *dbschema.Error, got %T: %v", err, err)
	}
	if schemaErr.Code != code {
		t.Fatalf("expected error code %s, got %s (%v)", code, schemaErr.Code, err)
	}
}

func lastAttemptOutcome(t *testing.T, db *sql.DB, version int64) string {
	t.Helper()
	var outcome string
	if err := db.QueryRow("SELECT outcome FROM migration_attempts WHERE version = ? ORDER BY id DESC LIMIT 1", version).Scan(&outcome); err != nil {
		t.Fatalf("read attempt outcome for version %d: %v", version, err)
	}
	return outcome
}

func appliedAt(t *testing.T, db *sql.DB, version int64) string {
	t.Helper()
	var appliedAt string
	if err := db.QueryRow("SELECT applied_at FROM schema_migrations WHERE version = ?", version).Scan(&appliedAt); err != nil {
		t.Fatalf("read applied_at for version %d: %v", version, err)
	}
	return appliedAt
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&count); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return count > 0
}

func requireCleanApplied(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	var dirty int
	if err := db.QueryRow("SELECT dirty FROM schema_migrations WHERE version = ?", version).Scan(&dirty); err != nil {
		t.Fatalf("read dirty flag for version %d: %v", version, err)
	}
	if dirty != 0 {
		t.Fatalf("version %d still marked dirty", version)
	}
}

func TestAdoptRecordsBaselineOnFreshDatabase(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	result, err := runner.Adopt(ctx, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !result.Adopted {
		t.Fatal("expected adoption to be recorded")
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.BaselineRecorded || status.CurrentVersion != BaselineVersion {
		t.Fatalf("expected baseline recorded at version %d, got %+v", BaselineVersion, status)
	}
	if len(status.Applied) != 1 || status.Applied[0].Checksum != AdoptionChecksum {
		t.Fatalf("unexpected applied rows: %+v", status.Applied)
	}
	if outcome := lastAttemptOutcome(t, db, BaselineVersion); outcome != "success" {
		t.Fatalf("expected adoption attempt outcome success, got %q", outcome)
	}
	var executor, appRelease string
	if err := db.QueryRow("SELECT executor, app_release FROM migration_attempts WHERE version = ? ORDER BY id DESC LIMIT 1", BaselineVersion).Scan(&executor, &appRelease); err != nil {
		t.Fatalf("read attempt executor: %v", err)
	}
	if executor != "tokenhub" {
		t.Fatalf("expected default executor stamped on the attempt, got %q", executor)
	}
	if appRelease != "" {
		t.Fatalf("expected empty app release without WithAppRelease, got %q", appRelease)
	}
}

func TestAdoptStampsConfiguredExecutor(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil, WithExecutor("cli:test-host"), WithAppRelease("0.6.0"))
	if _, err := runner.Adopt(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	var executor, appRelease string
	if err := db.QueryRow("SELECT executor, app_release FROM migration_attempts WHERE version = ? ORDER BY id DESC LIMIT 1", BaselineVersion).Scan(&executor, &appRelease); err != nil {
		t.Fatalf("read attempt executor: %v", err)
	}
	if executor != "cli:test-host" {
		t.Fatalf("expected configured executor on the attempt, got %q", executor)
	}
	if appRelease != "0.6.0" {
		t.Fatalf("expected configured app release on the attempt, got %q", appRelease)
	}
}

func TestAdoptVerifiesExistingBaselineWithoutRewriting(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("first Adopt: %v", err)
	}
	before := appliedAt(t, db, BaselineVersion)
	result, err := runner.Adopt(ctx, nil)
	if err != nil {
		t.Fatalf("second Adopt: %v", err)
	}
	if result.Adopted {
		t.Fatal("second adoption must not rewrite the baseline")
	}
	if after := appliedAt(t, db, BaselineVersion); before != after {
		t.Fatalf("baseline applied_at changed: %q -> %q", before, after)
	}
}

func TestChecksumMismatchRefusesStartup(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if _, err := db.Exec("UPDATE schema_migrations SET checksum = 'tampered' WHERE version = ?", BaselineVersion); err != nil {
		t.Fatal(err)
	}
	requireErrorCode(t, runner.Verify(ctx), ErrCodeChecksumMismatch)
	_, migrateErr := runner.Migrate(ctx)
	requireErrorCode(t, migrateErr, ErrCodeChecksumMismatch)
}

func TestDirtyNonTransactionalMigrationRefusesStartupUntilCleared(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	broken := Migration{
		Version:          2,
		Name:             "broken-non-transactional",
		Statements:       []string{"CREATE TABLE broken ("},
		NonTransactional: true,
		Postcondition: func(context.Context, MigrationExecer) error {
			return nil
		},
	}
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner = mustRunner(t, db, []Migration{broken})
	_, err := runner.Migrate(ctx)
	requireErrorCode(t, err, ErrCodeApplyFailed)

	// The dirty marker must survive and refuse the next startup.
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Dirty || status.DirtyVersion != 2 {
		t.Fatalf("expected dirty marker at version 2, got %+v", status)
	}
	_, err = runner.Migrate(ctx)
	requireErrorCode(t, err, ErrCodeDirtyState)

	// Simulate a future repair: drop the dirty row and register a fixed
	// migration; the runner must then apply it cleanly.
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = 2"); err != nil {
		t.Fatal(err)
	}
	fixed := Migration{
		Version:          2,
		Name:             "fixed-non-transactional",
		Statements:       []string{"CREATE TABLE fixed_marker (id INTEGER PRIMARY KEY)"},
		NonTransactional: true,
		Postcondition: func(ctx context.Context, ex MigrationExecer) error {
			var count int
			return ex.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'fixed_marker'").Scan(&count)
		},
	}
	runner = mustRunner(t, db, []Migration{fixed})
	result, err := runner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate after repair: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].Version != 2 {
		t.Fatalf("expected version 2 applied after repair, got %+v", result.Applied)
	}
	if !tableExists(t, db, "fixed_marker") {
		t.Fatal("fixed migration did not create its table")
	}
	requireCleanApplied(t, db, 2)
}

func TestTransactionalMigrationFailureRollsBackVersion(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner = mustRunner(t, db, []Migration{
		{Version: 2, Name: "create-demo", Statements: []string{"CREATE TABLE demo (id INTEGER PRIMARY KEY)"}},
		{Version: 3, Name: "broken", Statements: []string{
			"CREATE TABLE demo_extra (id INTEGER PRIMARY KEY)",
			"INSERT INTO missing_table VALUES (1)",
		}},
	})
	_, err := runner.Migrate(ctx)
	requireErrorCode(t, err, ErrCodeApplyFailed)
	// Only the failing version rolls back; the committed predecessor stays.
	if !tableExists(t, db, "demo") {
		t.Fatal("version 2 committed before the failure must stay applied")
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.CurrentVersion != 2 {
		t.Fatalf("ledger must stay at version 2 after the rollback, got %d", status.CurrentVersion)
	}
	if outcome := lastAttemptOutcome(t, db, 3); outcome != "rolled_back" {
		t.Fatalf("expected rolled_back attempt for version 3, got %q", outcome)
	}

	// With the failing statement removed, the remaining version applies.
	runner = mustRunner(t, db, []Migration{
		{Version: 2, Name: "create-demo", Statements: []string{"CREATE TABLE demo (id INTEGER PRIMARY KEY)"}},
		{Version: 3, Name: "fixed", Statements: []string{
			"CREATE TABLE demo_extra (id INTEGER PRIMARY KEY)",
			"INSERT INTO demo_extra (id) VALUES (1)",
		}},
	})
	result, err := runner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate after fix: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].Version != 3 {
		t.Fatalf("expected only version 3 applied on retry, got %+v", result.Applied)
	}
}

func TestPendingMigrationsApplyInOrder(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner = mustRunner(t, db, []Migration{
		{Version: 3, Name: "third", Statements: []string{"CREATE TABLE third (id INTEGER PRIMARY KEY)"}},
		{Version: 2, Name: "second", Statements: []string{"CREATE TABLE second (id INTEGER PRIMARY KEY)"}},
	})
	result, err := runner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(result.Applied) != 2 || result.Applied[0].Version != 2 || result.Applied[1].Version != 3 {
		t.Fatalf("expected ascending order 2 then 3, got %+v", result.Applied)
	}
}

func TestContractMigrationsNeverAutoApply(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner = mustRunner(t, db, []Migration{
		{Version: 2, Name: "expand", Statements: []string{"CREATE TABLE expandable (id INTEGER PRIMARY KEY)"}},
		{Version: 3, Name: "contract", Phase: PhaseContract, Statements: []string{"DROP TABLE expandable"}},
	})
	result, err := runner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].Version != 2 {
		t.Fatalf("expected only the expand migration applied, got %+v", result.Applied)
	}
	if !tableExists(t, db, "expandable") {
		t.Fatal("contract migration must not run automatically")
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.PendingContract) != 1 || status.PendingContract[0].Version != 3 {
		t.Fatalf("expected contract migration pending, got %+v", status.PendingContract)
	}
}

func TestDialectFilteredMigrationSkippedOnSQLite(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, []Migration{
		{Version: 2, Name: "postgres-only", Dialect: DialectPostgres, Statements: []string{"CREATE EXTENSION IF NOT EXISTS pg_trgm"}},
	})
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	result, err := runner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("postgres-only migration must not apply on sqlite, got %+v", result.Applied)
	}
	status, err := runner.Status(ctx)
	if err != nil || len(status.PendingExpand) != 0 {
		t.Fatalf("postgres-only migration must not be pending on sqlite: %+v err=%v", status, err)
	}
}

func TestUnknownAppliedVersionRefusesStartup(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if _, err := db.Exec("INSERT INTO schema_migrations (version, name, phase, checksum) VALUES (99, 'foreign', 'expand', 'x')"); err != nil {
		t.Fatal(err)
	}
	requireErrorCode(t, runner.Verify(ctx), ErrCodeUnknownApplied)
}

func TestRegistryValidation(t *testing.T) {
	db := newTestDB(t)
	goWithoutChecksum := Migration{Version: 2, Name: "go-migration", Go: func(context.Context, MigrationExecer) error { return nil }}
	_, err := NewRunner(db, DialectSQLite, []Migration{goWithoutChecksum})
	requireErrorCode(t, err, ErrCodeInvalidRegistry)

	nonTxWithoutPostcondition := Migration{Version: 2, Name: "loose", Statements: []string{"SELECT 1"}, NonTransactional: true}
	if _, err := NewRunner(db, DialectSQLite, []Migration{nonTxWithoutPostcondition}); err == nil {
		t.Fatal("expected registry validation to reject non-transactional migration without Postcondition")
	}
	reservedBaseline := Migration{Version: BaselineVersion, Name: "baseline-clone", Statements: []string{"SELECT 1"}}
	if _, err := NewRunner(db, DialectSQLite, []Migration{reservedBaseline}); err == nil {
		t.Fatal("expected registry validation to reject the reserved baseline version")
	}
}

func TestNonTransactionalSuccessClearsDirtyMarker(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner = mustRunner(t, db, []Migration{
		{
			Version:          2,
			Name:             "online-index",
			Statements:       []string{"CREATE TABLE online_demo (id INTEGER PRIMARY KEY)"},
			NonTransactional: true,
			Postcondition: func(ctx context.Context, ex MigrationExecer) error {
				var count int
				return ex.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'online_demo'").Scan(&count)
			},
		},
	})
	result, err := runner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("expected version 2 applied, got %+v", result.Applied)
	}
	requireCleanApplied(t, db, 2)
}

func TestConcurrentMigrateSerializesOnSQLite(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	registry := []Migration{
		{Version: 2, Name: "create", Statements: []string{"CREATE TABLE concurrent_demo (id INTEGER PRIMARY KEY)"}},
		{Version: 3, Name: "insert", Statements: []string{"INSERT INTO concurrent_demo (id) VALUES (1)"}},
	}
	if _, err := mustRunner(t, db, nil).Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for slot := range 2 {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			runner, err := NewRunner(db, DialectSQLite, registry, WithLockWait(5*time.Second))
			if err != nil {
				errs[slot] = err
				return
			}
			results[slot], errs[slot] = runner.Migrate(ctx)
		}(slot)
	}
	wg.Wait()
	for slot := range 2 {
		if errs[slot] != nil {
			t.Fatalf("concurrent runner %d failed: %v", slot, errs[slot])
		}
	}
	if appliedTotal := len(results[0].Applied) + len(results[1].Applied); appliedTotal != 2 {
		t.Fatalf("expected each migration applied exactly once across runners, got %d applications", appliedTotal)
	}
	var rows int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM concurrent_demo").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly one inserted row, got %d", rows)
	}
}

func TestNormalizeMigrationsFillsPhaseBudgets(t *testing.T) {
	registry, err := NormalizeMigrations([]Migration{
		{Version: 2, Name: "expand", Statements: []string{"CREATE TABLE budget_a (id INTEGER PRIMARY KEY)"}},
		{Version: 3, Name: "contract", Phase: PhaseContract, Statements: []string{"DROP TABLE budget_a"}},
	})
	if err != nil {
		t.Fatalf("NormalizeMigrations: %v", err)
	}
	if len(registry) != 2 {
		t.Fatalf("expected two normalized migrations, got %d", len(registry))
	}
	expand := registry[0]
	if expand.LockTimeoutSeconds != DefaultExpandLockTimeoutSeconds || expand.StatementBudget != DefaultExpandStatementBudget {
		t.Fatalf("expand budgets not defaulted to the short values: %+v", expand)
	}
	contract := registry[1]
	if contract.LockTimeoutSeconds != DefaultContractLockTimeoutSeconds || contract.StatementBudget != DefaultContractStatementBudget {
		t.Fatalf("contract budgets not defaulted to the maintenance values: %+v", contract)
	}

	explicit, err := NormalizeMigrations([]Migration{
		{Version: 2, Name: "big-contract", Phase: PhaseContract, Statements: []string{"DROP TABLE budget_a"}, LockTimeoutSeconds: 1200, StatementBudget: 500},
	})
	if err != nil {
		t.Fatalf("NormalizeMigrations with explicit budgets: %v", err)
	}
	if got := explicit[0]; got.LockTimeoutSeconds != 1200 || got.StatementBudget != 500 {
		t.Fatalf("explicit budgets were not preserved: %+v", got)
	}

	if _, err := NormalizeMigrations([]Migration{
		{Version: 2, Name: "negative-budget", Statements: []string{"CREATE TABLE x (id INTEGER PRIMARY KEY)"}, StatementBudget: -1},
	}); err == nil {
		t.Fatal("expected negative statement budget to be rejected")
	}
}
