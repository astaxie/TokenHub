package server

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"tokenhub/backend/internal/dbschema"
)

// OpenRawDatabase opens a lightweight database handle without running any
// schema flow. Maintenance CLI commands use it to inspect or evolve the
// database without booting the full store.
func OpenRawDatabase(databaseURL string) (driver string, db *sql.DB, err error) {
	driver, dsn, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return "", nil, err
	}
	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		dialector = sqlite.Open(dsn)
	case "postgres":
		dialector = postgres.Open(dsn)
	default:
		return "", nil, fmt.Errorf("unsupported database driver: %s", driver)
	}
	handle, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		return "", nil, err
	}
	sqlDB, err := handle.DB()
	if err != nil {
		return "", nil, err
	}
	if driver == "sqlite" {
		// Match the runtime handle so maintenance commands enforce the same
		// pragmas: SQLite pragmas are connection-local, and a second pooled
		// connection would silently run without them.
		sqlDB.SetMaxOpenConns(1)
		for _, pragma := range []string{"PRAGMA foreign_keys = ON", "PRAGMA busy_timeout = 5000"} {
			if _, err := sqlDB.ExecContext(context.Background(), pragma); err != nil {
				_ = sqlDB.Close()
				return "", nil, fmt.Errorf("configure sqlite maintenance handle: %w", err)
			}
		}
	}
	return driver, sqlDB, nil
}

// VerifySchemaSemantics compares the database against the reference snapshot
// built from the frozen schema flow plus registered expansions. It backs `tokenhub db verify` and allows
// extra objects left behind by earlier releases.
func VerifySchemaSemantics(ctx context.Context, databaseURL string) error {
	driver, dsn, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return err
	}
	_, db, err := OpenRawDatabase(databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	reference, err := buildSchemaReferenceWithExpansions(ctx, driver, dsn, true)
	if err != nil {
		return err
	}
	actual, err := dbschema.Introspect(ctx, db, dbschema.Dialect(driver), "")
	if err != nil {
		return err
	}
	if violations := dbschema.CompareObjects(reference, actual); len(violations) > 0 {
		return fmt.Errorf("schema verification failed: %s", dbschema.FormatViolations(violations))
	}
	return nil
}

// SchemaMigrationRegistry returns the registered versioned schema migrations.
// Startup and the maintenance CLI share this registry so existing databases
// receive every compatible schema expansion after the adoption baseline.
func SchemaMigrationRegistry() []dbschema.Migration {
	return []dbschema.Migration{
		meteringMigration(),
		{
			Version:          2,
			Name:             "add-granular-billing-columns-sqlite",
			Dialect:          dbschema.DialectSQLite,
			Go:               addGranularBillingColumnsSQLite,
			ChecksumOverride: "tokenhub-schema-granular-billing-sqlite-v1",
			// Every missing column requires a PRAGMA probe and an ALTER TABLE.
			// The full migration currently covers 26 columns (52 operations), so
			// retain a small margin above that worst-case budget.
			StatementBudget: 60,
		},
		{
			Version: 3,
			Name:    "add-granular-billing-columns-postgres",
			Dialect: dbschema.DialectPostgres,
			Statements: []string{
				`ALTER TABLE "models"
					ADD COLUMN IF NOT EXISTS "cache_write_price_usd_per1_m" decimal,
					ADD COLUMN IF NOT EXISTS "cache_write_price_configured" boolean,
					ADD COLUMN IF NOT EXISTS "cache_write5m_price_usd_per1_m" decimal,
					ADD COLUMN IF NOT EXISTS "cache_write5m_price_configured" boolean,
					ADD COLUMN IF NOT EXISTS "cache_write1h_price_usd_per1_m" decimal,
					ADD COLUMN IF NOT EXISTS "cache_write1h_price_configured" boolean,
					ADD COLUMN IF NOT EXISTS "pricing_periods" text`,
				`ALTER TABLE "provider_models"
					ADD COLUMN IF NOT EXISTS "cache_write_price_usd_per1_m" decimal,
					ADD COLUMN IF NOT EXISTS "cache_write_price_configured" boolean,
					ADD COLUMN IF NOT EXISTS "cache_write5m_price_usd_per1_m" decimal,
					ADD COLUMN IF NOT EXISTS "cache_write5m_price_configured" boolean,
					ADD COLUMN IF NOT EXISTS "cache_write1h_price_usd_per1_m" decimal,
					ADD COLUMN IF NOT EXISTS "cache_write1h_price_configured" boolean,
					ADD COLUMN IF NOT EXISTS "pricing_periods" text`,
				`ALTER TABLE "usage_records"
					ADD COLUMN IF NOT EXISTS "cache_write5m_tokens" bigint,
					ADD COLUMN IF NOT EXISTS "cache_write1h_tokens" bigint,
					ADD COLUMN IF NOT EXISTS "input_cost_usd" decimal,
					ADD COLUMN IF NOT EXISTS "cache_read_cost_usd" decimal,
					ADD COLUMN IF NOT EXISTS "cache_write_cost_usd" decimal,
					ADD COLUMN IF NOT EXISTS "output_cost_usd" decimal`,
				`ALTER TABLE "image_jobs"
					ADD COLUMN IF NOT EXISTS "redis_billing_admitted" boolean,
					ADD COLUMN IF NOT EXISTS "redis_key_lease_held" boolean,
					ADD COLUMN IF NOT EXISTS "redis_user_lease_held" boolean`,
				`ALTER TABLE "response_jobs"
					ADD COLUMN IF NOT EXISTS "redis_billing_admitted" boolean,
					ADD COLUMN IF NOT EXISTS "redis_key_lease_held" boolean,
					ADD COLUMN IF NOT EXISTS "redis_user_lease_held" boolean`,
			},
		},
	}
}

func addGranularBillingColumnsSQLite(ctx context.Context, db dbschema.MigrationExecer) error {
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{table: "models", name: "cache_write_price_usd_per1_m", definition: "real"},
		{table: "models", name: "cache_write_price_configured", definition: "numeric"},
		{table: "models", name: "cache_write5m_price_usd_per1_m", definition: "real"},
		{table: "models", name: "cache_write5m_price_configured", definition: "numeric"},
		{table: "models", name: "cache_write1h_price_usd_per1_m", definition: "real"},
		{table: "models", name: "cache_write1h_price_configured", definition: "numeric"},
		{table: "models", name: "pricing_periods", definition: "text"},
		{table: "provider_models", name: "cache_write_price_usd_per1_m", definition: "real"},
		{table: "provider_models", name: "cache_write_price_configured", definition: "numeric"},
		{table: "provider_models", name: "cache_write5m_price_usd_per1_m", definition: "real"},
		{table: "provider_models", name: "cache_write5m_price_configured", definition: "numeric"},
		{table: "provider_models", name: "cache_write1h_price_usd_per1_m", definition: "real"},
		{table: "provider_models", name: "cache_write1h_price_configured", definition: "numeric"},
		{table: "provider_models", name: "pricing_periods", definition: "text"},
		{table: "usage_records", name: "cache_write5m_tokens", definition: "integer"},
		{table: "usage_records", name: "cache_write1h_tokens", definition: "integer"},
		{table: "usage_records", name: "input_cost_usd", definition: "real"},
		{table: "usage_records", name: "cache_read_cost_usd", definition: "real"},
		{table: "usage_records", name: "cache_write_cost_usd", definition: "real"},
		{table: "usage_records", name: "output_cost_usd", definition: "real"},
		{table: "image_jobs", name: "redis_billing_admitted", definition: "numeric"},
		{table: "image_jobs", name: "redis_key_lease_held", definition: "numeric"},
		{table: "image_jobs", name: "redis_user_lease_held", definition: "numeric"},
		{table: "response_jobs", name: "redis_billing_admitted", definition: "numeric"},
		{table: "response_jobs", name: "redis_key_lease_held", definition: "numeric"},
		{table: "response_jobs", name: "redis_user_lease_held", definition: "numeric"},
	} {
		exists, err := sqliteColumnExists(ctx, db, column.table, column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %q ADD COLUMN %q %s", column.table, column.name, column.definition)); err != nil {
			return fmt.Errorf("add sqlite column %s.%s: %w", column.table, column.name, err)
		}
	}
	return nil
}

func sqliteColumnExists(ctx context.Context, db dbschema.MigrationExecer, table string, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
