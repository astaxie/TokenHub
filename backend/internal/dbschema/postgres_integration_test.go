//go:build integration

package dbschema

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// openPostgresTestDB opens a PostgreSQL handle in a dedicated throwaway schema
// so repeated runs never collide.
func openPostgresTestDB(t *testing.T) *sql.DB {
	t.Helper()
	pgURL := strings.TrimSpace(os.Getenv("TEST_POSTGRES_URL"))
	if pgURL == "" {
		t.Skip("TEST_POSTGRES_URL not set, skipping PostgreSQL integration test")
	}
	gormDB, err := gorm.Open(postgres.Open(pgURL), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	schema := fmt.Sprintf("dbschema_it_%d", time.Now().UnixNano())
	if err := gormDB.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_ = gormDB.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		sqlDB, _ := gormDB.DB()
		_ = sqlDB.Close()
	})
	parsed, err := url.Parse(pgURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	raw, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatalf("open postgres handle: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

func TestPostgresAdoptMigrateAndVerify(t *testing.T) {
	db := openPostgresTestDB(t)
	ctx := context.Background()
	registry := []Migration{
		{Version: 2, Name: "create-demo", Statements: []string{"CREATE TABLE demo (id BIGINT PRIMARY KEY)"}},
	}
	adoptRunner, err := NewRunner(db, DialectPostgres, nil, WithAppRelease("test-release"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := adoptRunner.Adopt(ctx, nil)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !result.Adopted {
		t.Fatal("expected adoption on fresh postgres schema")
	}
	migrateRunner, err := NewRunner(db, DialectPostgres, registry, WithAppRelease("test-release"))
	if err != nil {
		t.Fatal(err)
	}
	result, err = migrateRunner.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].Version != 2 {
		t.Fatalf("expected version 2 applied, got %+v", result.Applied)
	}
	var appliedRelease string
	if err := db.QueryRowContext(ctx, "SELECT applied_release FROM schema_migrations WHERE version = 2").Scan(&appliedRelease); err != nil {
		t.Fatal(err)
	}
	if appliedRelease != "test-release" {
		t.Fatalf("expected applied_release test-release, got %q", appliedRelease)
	}
	// A reopened runner only verifies and finds nothing pending.
	status, err := migrateRunner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.PendingExpand) != 0 || status.Dirty {
		t.Fatalf("expected clean fully migrated status, got %+v", status)
	}
}

func TestPostgresStatementBudgetIncludesPostconditionQueries(t *testing.T) {
	db := openPostgresTestDB(t)
	ctx := context.Background()
	adoptRunner, err := NewRunner(db, DialectPostgres, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adoptRunner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner, err := NewRunner(db, DialectPostgres, []Migration{{
		Version:          2,
		Name:             "postcondition overspending",
		Statements:       []string{"CREATE TABLE postcondition_budget (id BIGINT PRIMARY KEY)"},
		NonTransactional: true,
		Postcondition: func(ctx context.Context, db MigrationExecer) error {
			var count int
			return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM postcondition_budget").Scan(&count)
		},
		StatementBudget: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = runner.Migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), "postcondition: statement budget exceeded") {
		t.Fatalf("expected shared postcondition statement budget failure, got %v", err)
	}
	requireErrorCode(t, err, ErrCodeApplyFailed)
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT dirty FROM schema_migrations WHERE version = 2").Scan(&dirty); err != nil {
		t.Fatalf("read dirty marker: %v", err)
	}
	if !dirty {
		t.Fatal("postcondition budget failure must keep the dirty marker")
	}
}

// TestPostgresBackfillExecutor pins the dialect placeholder handling of the
// backfill ledger updates (a review finding: reused $1 placeholders made
// every PostgreSQL update fail) and the atomic lease takeover on PostgreSQL.
func TestPostgresBackfillExecutor(t *testing.T) {
	db := openPostgresTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE backfill_work (id BIGINT PRIMARY KEY, converted INTEGER NOT NULL DEFAULT 0)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := db.Exec("INSERT INTO backfill_work (id, converted) VALUES ($1, 0)", i); err != nil {
			t.Fatal(err)
		}
	}
	executor, err := NewBackfillExecutor(db, DialectPostgres, []Backfill{{
		ID:   "pg-blocking",
		Mode: BackfillBlocking,
		RunBlocking: func(ctx context.Context, db Execer) error {
			_, err := db.ExecContext(ctx, "UPDATE backfill_work SET converted = 1")
			return err
		},
	}}, WithBackfillOwner("pg-owner"))
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RunBlocking(ctx); err != nil {
		t.Fatalf("RunBlocking on postgres: %v", err)
	}
	states, err := executor.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].State != BackfillStateComplete {
		t.Fatalf("expected completed blocking backfill, got %+v", states)
	}

	online, err := NewBackfillExecutor(db, DialectPostgres, []Backfill{{
		ID:   "pg-online",
		Mode: BackfillOnline,
		RunBatch: func(ctx context.Context, db Execer, limit int) (int64, error) {
			if _, err := db.ExecContext(ctx,
				"UPDATE backfill_work SET converted = 2 WHERE id IN (SELECT id FROM backfill_work WHERE converted = 1 LIMIT $1)", limit); err != nil {
				return 0, err
			}
			var remaining int64
			err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM backfill_work WHERE converted = 1").Scan(&remaining)
			return remaining, err
		},
		BatchLimit: 2,
	}}, WithBackfillOwner("pg-owner"))
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 10; round++ {
		progress, batchErr := online.RunOnlineBatch(ctx)
		if batchErr != nil {
			t.Fatalf("RunOnlineBatch on postgres: %v", batchErr)
		}
		if len(progress) == 1 && progress[0].Remaining == 0 {
			break
		}
	}
	var unconverted int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM backfill_work WHERE converted = 1").Scan(&unconverted); err != nil {
		t.Fatal(err)
	}
	if unconverted != 0 {
		t.Fatalf("online backfill did not finish, %d rows left", unconverted)
	}
}
