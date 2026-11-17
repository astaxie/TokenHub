package server

import (
	"context"
	"fmt"
	"strings"

	"tokenhub/backend/internal/dbschema"
)

type overviewSourceIndex struct {
	name      string
	table     string
	keys      string
	predicate string
	columns   int
}

var overviewSourceIndexes = []overviewSourceIndex{
	{"idx_overview_alert_created", "alert_events", "created_at DESC, id DESC", "", 2},
	{"idx_overview_alert_project", "alert_events", "scope_id, created_at DESC, id DESC", "", 3},
	{"idx_overview_usage_pending", "usage_records", "id", "overview_projected = 0", 1},
	{"idx_overview_request_pending", "request_logs", "id", "overview_projected = 0", 1},
}

func overviewSourceIndexStatements(concurrently string) []string {
	statements := make([]string, 0, len(overviewSourceIndexes))
	for _, index := range overviewSourceIndexes {
		statement := "CREATE INDEX " + concurrently + index.name + " ON " + index.table + " (" + index.keys + ")"
		if index.predicate != "" {
			statement += " WHERE " + index.predicate
		}
		statements = append(statements, statement)
	}
	return statements
}

// Each large index gets its own durable migration and time budget. Repair of
// one interrupted build leaves previously completed builds intact.
func overviewProjectionIndexMigrations() []dbschema.Migration {
	migrations := make([]dbschema.Migration, 0, len(overviewSourceIndexes))
	creates := overviewSourceIndexStatements("CONCURRENTLY ")
	for i, index := range overviewSourceIndexes {
		migrations = append(migrations, dbschema.Migration{
			Version: int64(14 + i), Name: "add-" + strings.ReplaceAll(index.name, "_", "-"), Dialect: dbschema.DialectPostgres,
			Statements:       []string{"DROP INDEX CONCURRENTLY IF EXISTS " + index.name, creates[i]},
			NonTransactional: true, SafeRetry: true, StatementBudget: 4,
			Postcondition: func(ctx context.Context, db dbschema.MigrationExecer) error {
				return verifyOverviewSourceIndex(ctx, db, index)
			},
		})
	}
	return migrations
}

func verifyOverviewSourceIndex(ctx context.Context, db dbschema.MigrationExecer, index overviewSourceIndex) error {
	var valid, ready, unique bool
	var table, method, definition, predicate string
	var keys, attributes int
	err := db.QueryRowContext(ctx, `SELECT i.indisvalid, i.indisready, i.indisunique,
        t.relname, a.amname, i.indnkeyatts, i.indnatts, pg_get_indexdef(c.oid), COALESCE(pg_get_expr(i.indpred, i.indrelid), '')
        FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_class t ON t.oid = i.indrelid JOIN pg_am a ON a.oid = c.relam
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = $1`, index.name).
		Scan(&valid, &ready, &unique, &table, &method, &keys, &attributes, &definition, &predicate)
	if err != nil {
		return err
	}
	normalize := func(value string) string {
		return strings.NewReplacer(" ", "", "\n", "", "\t", "", "(", "", ")", "").Replace(strings.ToLower(value))
	}
	// Exact keys include their order and DESC options. Key/attribute counts
	// exclude unexpected INCLUDE columns, while the predicate must match fully.
	expectedSuffix := "usingbtree" + normalize(index.keys)
	if index.predicate != "" {
		expectedSuffix += "where" + normalize(index.predicate)
	}
	metadataMatches := valid && ready && !unique && table == index.table && method == "btree" && keys == index.columns && attributes == index.columns
	definitionMatches := strings.HasSuffix(normalize(definition), expectedSuffix) && normalize(predicate) == normalize(index.predicate)
	if !metadataMatches || !definitionMatches {
		return fmt.Errorf("overview index %s does not match its required definition", index.name)
	}
	return nil
}
