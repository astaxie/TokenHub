package server

import (
	"fmt"
	"strings"

	"tokenhub/backend/internal/dbschema"
)

// SQLite REPLACE can delete the conflicting row without firing its delete
// trigger. Preserve the old contribution before the insert resolves. SQLite
// serializes writers. A single insert can conflict with at most the primary
// ID and an explicitly supplied SQLite rowid in these source tables.
func overviewReplacementMigration() dbschema.Migration {
	migration := dbschema.Migration{Version: 19, Name: "account-for-sqlite-overview-replacements", Dialect: dbschema.DialectSQLite, StatementBudget: 14}
	for _, source := range []string{"usage_records", "request_logs"} {
		previous := "overview_previous_" + source
		columns := append([]string{"id", "overview_projected"}, overviewDimensionColumns...)
		if source == "usage_records" {
			columns = append(columns, "input_tokens", "cached_input_tokens", "cache_write_tokens", "output_tokens", "reasoning_tokens", "total_tokens", "cost_usd")
		} else {
			columns = append(columns, "status_code", "error_code")
		}
		fields := strings.Join(columns, ", ")
		// IGNORE and ON CONFLICT DO UPDATE skip AFTER INSERT. Clear a leftover
		// candidate before the next insert. An actual DELETE (including REPLACE
		// with recursive triggers enabled) has already subtracted the old row.
		migration.Statements = append(migration.Statements,
			fmt.Sprintf("CREATE TABLE %s AS SELECT rowid AS previous_rowid, %s, 0 AS consume FROM %s WHERE 0", previous, fields, source),
			fmt.Sprintf(`CREATE TRIGGER overview_%[1]s_capture BEFORE INSERT ON %[1]s
				WHEN EXISTS (SELECT 1 FROM %[2]s) OR EXISTS (SELECT 1 FROM %[1]s WHERE (id = NEW.id OR rowid = NEW.rowid) AND overview_projected > 0)
				BEGIN DELETE FROM %[2]s;
				INSERT INTO %[2]s (previous_rowid, %[3]s) SELECT rowid, %[3]s FROM %[1]s WHERE (id = NEW.id OR rowid = NEW.rowid) AND overview_projected > 0; END`, source, previous, fields),
			fmt.Sprintf(`CREATE TRIGGER overview_%[1]s_discard AFTER DELETE ON %[1]s
				BEGIN DELETE FROM %[2]s WHERE id = OLD.id; END`, source, previous),
			fmt.Sprintf(`CREATE TRIGGER overview_%s_subtract_replaced AFTER UPDATE OF consume ON %s BEGIN %s %s END`,
				source, previous, overviewProjectionDelta(source, "OLD", -1, "sqlite"), overviewSQLiteOverflowSQL(source, "OLD")),
			"DROP TRIGGER overview_"+source+"_insert",
			fmt.Sprintf(`CREATE TRIGGER overview_%[1]s_insert AFTER INSERT ON %[1]s BEGIN
				UPDATE %[2]s SET consume = 1 WHERE id = NEW.id OR previous_rowid = NEW.rowid;
				DELETE FROM %[2]s;
				UPDATE %[1]s SET overview_projected = 0 WHERE NEW.overview_projected <> 0 AND id = NEW.id;
				UPDATE %[1]s SET overview_projected = 1 WHERE id = NEW.id; END`, source, previous),
			// On an earlier projection a missed implicit delete inflated the
			// global record count. Only existing projections need the one-time
			// comparison. A mismatch selects the verified raw-aggregate path.
			fmt.Sprintf(`UPDATE overview_projection_progress SET overflowed = 1
				WHERE source = '%[1]s' AND overflowed = 0 AND EXISTS (
					SELECT 1 FROM overview_totals WHERE scope = 'global' AND source = '%[1]s'
					AND record_count <> (SELECT COUNT(*) FROM %[1]s) - (SELECT COUNT(*) FROM %[1]s WHERE overview_projected = 0))`, source),
		)
	}
	return migration
}

// This common expansion records both jobs while the schema lock is held.
// Existing leases and completed work belong to the online executor.
func overviewBackfillRegistrationMigration() dbschema.Migration {
	migration := dbschema.Migration{Version: 20, Name: "register-overview-online-backfills", StatementBudget: 4}
	for _, source := range []string{"usage_records", "request_logs"} {
		migration.Statements = append(migration.Statements,
			fmt.Sprintf(`UPDATE overview_projection_progress SET ready = 1 WHERE source = '%[1]s' AND ready = 0
				AND NOT EXISTS (SELECT 1 FROM %[1]s WHERE overview_projected = 0)`, source),
			fmt.Sprintf(`INSERT INTO data_backfills (id, mode, state, remaining)
				SELECT 'overview-v1-%[1]s', 'online', CASE WHEN ready = 1 THEN 'complete' ELSE 'pending' END,
					CASE WHEN ready = 1 THEN 0 ELSE -1 END FROM overview_projection_progress WHERE source = '%[1]s'
				ON CONFLICT (id) DO NOTHING`, source),
		)
	}
	return migration
}
