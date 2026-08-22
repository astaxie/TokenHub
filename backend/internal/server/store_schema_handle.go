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
// built from the frozen schema flow. It backs `tokenhub db verify` and allows
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
	reference, err := schemaReferenceSnapshot(ctx, driver, dsn)
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
// The bridge release ships none beyond the adoption baseline; later releases
// register their expand and contract migrations here so startup and the
// maintenance CLI share one registry.
func SchemaMigrationRegistry() []dbschema.Migration {
	return nil
}
