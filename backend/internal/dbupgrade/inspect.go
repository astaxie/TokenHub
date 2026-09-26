package dbupgrade

import (
	"context"
	"database/sql"
	"fmt"
)

// listTables returns the user tables of the database. The query reads each
// dialect's catalog instead of parsing driver error text, mirroring the
// dbcli maintenance commands.
func listTables(ctx context.Context, db *sql.DB, driver string) ([]string, error) {
	query := "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
	if driver == "postgres" {
		query = "SELECT table_name FROM information_schema.tables " +
			"WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name"
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

// tableExists reports whether one table is present.
func tableExists(tables []string, name string) bool {
	return contains(tables, name)
}

// rowCount counts the rows of one table.
func rowCount(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	// Table names come from the dialect catalog, not user input.
	if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %q", table)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count rows of %s: %w", table, err)
	}
	return count, nil
}

// tableHasRows reports whether one table holds at least one row, without
// counting the whole table.
func tableHasRows(ctx context.Context, db *sql.DB, table string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT 1 FROM %q LIMIT 1", table))
	if err != nil {
		return false, fmt.Errorf("probe rows of %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

// ledgerVersion reads the highest applied migration version from a
// TokenHub ledger, or 0 when the ledger table is absent.
func ledgerVersion(ctx context.Context, db *sql.DB, driver string) (int64, error) {
	mark := "?"
	if driver == "postgres" {
		mark = "$1"
	}
	rows, err := db.QueryContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations WHERE dirty = "+mark, false)
	if err != nil {
		return 0, fmt.Errorf("read migration ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, nil
	}
	var version int64
	if err := rows.Scan(&version); err != nil {
		return 0, err
	}
	return version, rows.Err()
}

// classifyTarget inspects the target database and classifies it for an
// upgrade run.
func classifyTarget(ctx context.Context, db *sql.DB, driver string) (TargetReport, error) {
	report := TargetReport{Driver: driver}
	tables, err := listTables(ctx, db, driver)
	if err != nil {
		return report, err
	}
	if len(tables) == 0 {
		report.State = TargetStateEmpty
		return report, nil
	}
	if !tableExists(tables, "schema_migrations") {
		report.State = TargetStateUnrecognized
		report.Detail = fmt.Sprintf("%d table(s) present but no TokenHub migration ledger", len(tables))
		return report, nil
	}
	version, err := ledgerVersion(ctx, db, driver)
	if err != nil {
		return report, err
	}
	report.State = TargetStateTokenhub
	report.LedgerVersion = version
	// Probing a handful of core tables keeps classification cheap on large
	// targets; COUNT(*) would seq-scan every table.
	for _, table := range []string{"providers", "admin_users", "api_keys", "usage_records", "request_logs"} {
		if !tableExists(tables, table) {
			continue
		}
		hasRows, err := tableHasRows(ctx, db, table)
		if err != nil {
			return report, err
		}
		if hasRows {
			report.HasData = true
			break
		}
	}
	if report.HasData {
		report.Detail = "target already holds data; cutover requires an empty target"
	}
	return report, nil
}
