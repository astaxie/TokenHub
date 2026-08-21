package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"tokenhub/backend/internal/dbschema"
)

// adoptSchemaLedger records or verifies the dbschema adoption baseline around
// the frozen startup schema flow using the bridge-release semantics documented
// in docs/database-evolution.md. The caller already serializes schema work
// across processes, so the runner runs
// under external coordination. Empty databases adopt from the frozen baseline
// SQL (SQLite and PostgreSQL); databases with business tables run the legacy
// callback and are semantically verified against the reference snapshot
// before the baseline is recorded. Ordinary restarts only verify the ledger
// and checksums. The caller holds the shared schema migration lock for both
// SQLite and PostgreSQL.
func adoptSchemaLedger(ctx context.Context, db *sql.DB, driver, dsn string, legacy func(ctx context.Context) error) error {
	reference := func(ctx context.Context) (dbschema.ObjectSet, error) {
		return schemaReferenceSnapshot(ctx, driver, dsn)
	}
	options := []dbschema.Option{
		dbschema.WithExternalCoordination(),
		dbschema.WithLogger(log.Printf),
		dbschema.WithAdoptionReference(reference),
		dbschema.WithExecutor(adoptionExecutor()),
	}
	var statements []string
	var err error
	switch dbschema.Dialect(driver) {
	case dbschema.DialectSQLite:
		statements, err = dbschema.SQLiteBaselineStatements()
	case dbschema.DialectPostgres:
		statements, err = dbschema.PostgresBaselineStatements()
	default:
		err = fmt.Errorf("unsupported schema ledger driver %q", driver)
	}
	if err != nil {
		return err
	}
	if len(statements) > 0 {
		options = append(options, dbschema.WithFreshBaseline(statements))
	}
	options = append(options, dbschema.WithLegacyRecognizer(legacyLooksLikeTokenHub(driver)))
	runner, err := dbschema.NewRunner(db, dbschema.Dialect(driver), SchemaMigrationRegistry(), options...)
	if err != nil {
		return err
	}
	_, err = runner.Adopt(ctx, legacy)
	return err
}

// adoptionExecutor names the instance that runs the startup adoption, used to
// stamp migration_attempts rows. The host name distinguishes
// instances on shared databases without leaking identifiers into logs.
func adoptionExecutor() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return "server:" + host
	}
	return "server"
}

// legacyLooksLikeTokenHub refuses legacy adoption of databases that hold
// tables but none from a known TokenHub release: the frozen flow would simply
// complete an unrelated database and record it as the supported baseline
// . This any-known-table heuristic is a bridge-release gate; full
// fingerprints verified against real v0.4.0/v0.5.0 fixtures replace it when
// the migration chain lands.
func legacyLooksLikeTokenHub(driver string) func(ctx context.Context, db *sql.DB) error {
	return func(ctx context.Context, db *sql.DB) error {
		query := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('admin_users', 'projects', 'providers', 'request_logs')"
		if driver == "postgres" {
			query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema = current_schema() " +
				"AND table_name IN ('admin_users', 'projects', 'providers', 'request_logs')"
		}
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return errors.New("database holds tables but none from a known TokenHub release")
		}
		return nil
	}
}

// schemaReferenceCache holds one reference snapshot per driver per process:
// the snapshot depends only on the compiled model set, not the target
// database.
var schemaReferenceCache sync.Map // driver string -> dbschema.ObjectSet

// schemaReferenceSnapshot returns the semantic reference schema for the
// driver, building it once by running the frozen structural flow on a
// throwaway database and introspecting the result.
func schemaReferenceSnapshot(ctx context.Context, driver, dsn string) (dbschema.ObjectSet, error) {
	if cached, ok := schemaReferenceCache.Load(driver); ok {
		return cached.(dbschema.ObjectSet), nil
	}
	built, err := buildSchemaReference(ctx, driver, dsn)
	if err != nil {
		return dbschema.ObjectSet{}, err
	}
	stored, _ := schemaReferenceCache.LoadOrStore(driver, built)
	return stored.(dbschema.ObjectSet), nil
}

func buildSchemaReference(ctx context.Context, driver, dsn string) (dbschema.ObjectSet, error) {
	switch driver {
	case "sqlite":
		scratchDSN := fmt.Sprintf("file:%s?mode=memory&cache=shared", NewID("schemaref"))
		db, err := gorm.Open(sqlite.Open(scratchDSN), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			return dbschema.ObjectSet{}, fmt.Errorf("open sqlite schema reference: %w", err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			return dbschema.ObjectSet{}, err
		}
		defer sqlDB.Close()
		if err := migrateSchemaObjects(db, driver); err != nil {
			return dbschema.ObjectSet{}, fmt.Errorf("build sqlite schema reference: %w", err)
		}
		return dbschema.Introspect(ctx, sqlDB, dbschema.DialectSQLite, "")
	case "postgres":
		// A dedicated scratch schema builds the reference. PostgreSQL cannot
		// host the frozen flow's function and trigger in pg_temp (unqualified
		// CREATE FUNCTION lands outside the temporary schema), so this needs
		// the database-level CREATE privilege once; setups where the role owns
		// the public schema but lacks it get an actionable error rather than a
		// silently skipped verification.
		schemaName := "tokenhub_schema_ref_" + strings.ToLower(NewID(""))
		admin, err := sql.Open("pgx", dsn)
		if err != nil {
			return dbschema.ObjectSet{}, fmt.Errorf("open postgres schema reference admin handle: %w", err)
		}
		defer admin.Close()
		if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", dbschema.QuoteIdent(schemaName))); err != nil {
			return dbschema.ObjectSet{}, fmt.Errorf("create schema reference schema %s (grant CREATE on the database to this role once, e.g. GRANT CREATE ON DATABASE <db> TO <role>): %w", schemaName, err)
		}
		defer func() {
			_, _ = admin.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", dbschema.QuoteIdent(schemaName)))
		}()
		scratchDSN, err := postgresSearchPathDSN(dsn, schemaName)
		if err != nil {
			return dbschema.ObjectSet{}, err
		}
		db, err := gorm.Open(postgres.Open(scratchDSN), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			return dbschema.ObjectSet{}, fmt.Errorf("open postgres schema reference: %w", err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			return dbschema.ObjectSet{}, err
		}
		defer sqlDB.Close()
		if err := migrateSchemaObjects(db, driver); err != nil {
			return dbschema.ObjectSet{}, fmt.Errorf("build postgres schema reference: %w", err)
		}
		return dbschema.Introspect(ctx, sqlDB, dbschema.DialectPostgres, schemaName)
	default:
		return dbschema.ObjectSet{}, fmt.Errorf("unsupported schema reference driver %q", driver)
	}
}

// postgresSearchPathDSN points a PostgreSQL DSN at the given schema. URL-style
// DSNs carry it as a query parameter; keyword DSNs append it as a runtime
// parameter, which the pgx parser accepts.
func postgresSearchPathDSN(dsn, schema string) (string, error) {
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	return dsn + " search_path=" + schema, nil
}
