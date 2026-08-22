//go:build integration

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"tokenhub/backend/internal/dbschema"
)

func openPostgresAdmin(t *testing.T) (*sql.DB, string) {
	t.Helper()
	pgURL := strings.TrimSpace(os.Getenv("TEST_POSTGRES_URL"))
	if pgURL == "" {
		t.Skip("TEST_POSTGRES_URL not set, skipping PostgreSQL integration test")
	}
	admin, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	return admin, pgURL
}

func createPostgresSchema(t *testing.T, admin *sql.DB, prefix string) string {
	t.Helper()
	schemaName := prefix + fmt.Sprintf("%d", time.Now().UnixNano())
	if _, err := admin.Exec(fmt.Sprintf("CREATE SCHEMA %s", dbschema.QuoteIdent(schemaName))); err != nil {
		t.Fatalf("create schema %s: %v", schemaName, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", dbschema.QuoteIdent(schemaName)))
	})
	return schemaName
}

func withSearchPath(pgURL, schema string) (string, error) {
	parsed, err := url.Parse(pgURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// TestPostgresBaselineSQLIsCurrent keeps the frozen PostgreSQL baseline in
// sync with the compiled model set and proves a schema created from it
// matches the AutoMigrate reference semantically. Regenerate with
// UPDATE_BASELINE=1, TEST_POSTGRES_URL set, and the integration tag.
func TestPostgresBaselineSQLIsCurrent(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	ctx := context.Background()
	generated, err := exportPostgresBaselineStatements(ctx, admin, pgURL)
	if err != nil {
		t.Fatalf("export baseline statements: %v", err)
	}
	baselinePath := "../dbschema/migrations/postgres/000001_baseline.json"
	if os.Getenv(baselineUpdateEnv) == "1" {
		payload := struct {
			Version    int64    `json:"version"`
			Dialect    string   `json:"dialect"`
			Statements []string `json:"statements"`
		}{Version: dbschema.BaselineVersion, Dialect: "postgres", Statements: generated}
		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselinePath, append(encoded, '\n'), 0o644); err != nil {
			t.Fatalf("rewrite baseline: %v", err)
		}
		t.Logf("baseline rewritten: %s", baselinePath)
		return
	}
	current, err := dbschema.PostgresBaselineStatements()
	if err != nil {
		t.Fatalf("frozen baseline unreadable (regenerate with %s=1): %v", baselineUpdateEnv, err)
	}
	if len(current) == 0 {
		t.Fatalf("frozen baseline is empty; regenerate with %s=1", baselineUpdateEnv)
	}
	if len(generated) != len(current) {
		t.Fatalf("frozen PostgreSQL baseline is stale (%d statements, expected %d); regenerate with %s=1",
			len(current), len(generated), baselineUpdateEnv)
	}
	for i := range generated {
		if generated[i] != current[i] {
			t.Fatalf("frozen PostgreSQL baseline is stale at statement %d; regenerate with %s=1", i, baselineUpdateEnv)
		}
	}

	// Replay the frozen statements into a scratch schema and compare it
	// semantically with an AutoMigrate reference schema.
	replaySchema := createPostgresSchema(t, admin, "tokenhub_pg_replay_")
	replayDSN, err := withSearchPath(pgURL, replaySchema)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := sql.Open("pgx", replayDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	for _, statement := range current {
		if _, err := replay.ExecContext(ctx, statement); err != nil {
			t.Fatalf("execute baseline statement %q: %v", statement, err)
		}
	}

	referenceSchema := createPostgresSchema(t, admin, "tokenhub_pg_ref_")
	referenceDSN, err := withSearchPath(pgURL, referenceSchema)
	if err != nil {
		t.Fatal(err)
	}
	referenceGorm, err := gorm.Open(postgres.Open(referenceDSN), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	referenceSQL, err := referenceGorm.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer referenceSQL.Close()
	if err := migrateSchemaObjects(referenceGorm, "postgres"); err != nil {
		t.Fatalf("build reference schema: %v", err)
	}

	referenceSet, err := dbschema.Introspect(ctx, referenceSQL, dbschema.DialectPostgres, referenceSchema)
	if err != nil {
		t.Fatal(err)
	}
	replaySet, err := dbschema.Introspect(ctx, replay, dbschema.DialectPostgres, replaySchema)
	if err != nil {
		t.Fatal(err)
	}
	if violations := dbschema.CompareObjects(referenceSet, replaySet); len(violations) > 0 {
		t.Fatalf("replayed schema drifted from the AutoMigrate reference: %s", dbschema.FormatViolations(violations))
	}
}

// TestPostgresFreshStoreAdoptsFromBaselineSQL opens a store on an empty
// PostgreSQL schema and verifies the frozen baseline SQL path records the
// adoption without running AutoMigrate.
func TestPostgresFreshStoreAdoptsFromBaselineSQL(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schemaName := createPostgresSchema(t, admin, "tokenhub_pg_fresh_")
	dsn, err := withSearchPath(pgURL, schemaName)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, ConfigFromEnv())
	if err != nil {
		t.Fatalf("open store on fresh postgres schema: %v", err)
	}
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var baselineCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1 AND dirty = 0").Scan(&baselineCount); err != nil {
		t.Fatal(err)
	}
	if baselineCount != 1 {
		t.Fatalf("expected exactly one clean baseline row, got %d", baselineCount)
	}
	if err := VerifySchemaSemantics(context.Background(), dsn); err != nil {
		t.Fatalf("semantic verification after fresh adoption: %v", err)
	}
}

func TestPostgresStartupSchemaLockWaitHonorsContext(t *testing.T) {
	admin, _ := openPostgresAdmin(t)
	holder, err := admin.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close() //nolint:errcheck // test cleanup
	if _, err := holder.ExecContext(t.Context(), "SELECT pg_advisory_lock(hashtextextended($1, 0))", dbschema.SchemaMigrationLockName); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = holder.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", dbschema.SchemaMigrationLockName)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	migrateCalled := false
	started := time.Now()
	err = runSchemaMigrationLocked(ctx, admin, "postgres", func() error {
		migrateCalled = true
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup lock wait error = %v, want context deadline", err)
	}
	if migrateCalled {
		t.Fatal("startup migration ran without acquiring the advisory lock")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("startup lock wait ignored its deadline: %s", elapsed)
	}
}
