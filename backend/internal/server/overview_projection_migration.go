package server

import (
	"fmt"
	"strings"

	"tokenhub/backend/internal/dbschema"
)

// These statements are versioned schema. Future changes require a new migration.
var overviewProjectionRegistry = buildOverviewProjectionMigrations()

func overviewProjectionMigrations() []dbschema.Migration {
	migrations := append([]dbschema.Migration(nil), overviewProjectionRegistry...)
	for i := range migrations {
		migrations[i].Statements = append([]string(nil), migrations[i].Statements...)
	}
	return migrations
}

func buildOverviewProjectionMigrations() []dbschema.Migration {
	migrations := []dbschema.Migration{
		{Version: 12, Name: "add-overview-projection-sqlite", Dialect: dbschema.DialectSQLite, Statements: overviewProjectionStatements("sqlite"), StatementBudget: 30},
		{Version: 13, Name: "add-overview-projection-postgres", Dialect: dbschema.DialectPostgres, Statements: overviewProjectionStatements("postgres"), StatementBudget: 40},
	}
	migrations = append(migrations, overviewProjectionIndexMigrations()...)
	return append(migrations, dbschema.Migration{
		Version: 18, Name: "add-overview-completion-state", StatementBudget: 1,
		Statements: []string{"ALTER TABLE overview_projection_progress ADD COLUMN ready INTEGER NOT NULL DEFAULT 0"},
	})
}

var overviewDimensionColumns = []string{"project_id", "api_key_id", "attributed_user_id", "model_name", "provider_id", "provider_resource_id"}

var overviewMetricColumns = []string{
	"record_count", "request_count", "usage_record_count", "input_tokens", "cached_input_tokens", "cache_write_tokens", "output_tokens", "reasoning_tokens", "total_tokens", "estimated_cost_usd", "cost_compensation", "errors",
}

func overviewProjectionStatements(driver string) []string {
	statements := []string{`CREATE TABLE overview_totals (
        scope TEXT NOT NULL, source TEXT NOT NULL, shard INTEGER NOT NULL DEFAULT 0, project_id TEXT NOT NULL, api_key_id TEXT NOT NULL, attributed_user_id TEXT NOT NULL, model_name TEXT NOT NULL, provider_id TEXT NOT NULL, provider_resource_id TEXT NOT NULL,
        record_count NUMERIC NOT NULL DEFAULT 0, request_count NUMERIC NOT NULL DEFAULT 0, usage_record_count NUMERIC NOT NULL DEFAULT 0,
        input_tokens NUMERIC NOT NULL DEFAULT 0, cached_input_tokens NUMERIC NOT NULL DEFAULT 0, cache_write_tokens NUMERIC NOT NULL DEFAULT 0,
        output_tokens NUMERIC NOT NULL DEFAULT 0, reasoning_tokens NUMERIC NOT NULL DEFAULT 0, total_tokens NUMERIC NOT NULL DEFAULT 0,
        estimated_cost_usd NUMERIC NOT NULL DEFAULT 0, cost_compensation NUMERIC NOT NULL DEFAULT 0, errors NUMERIC NOT NULL DEFAULT 0,
        PRIMARY KEY (scope, source, project_id, api_key_id, attributed_user_id, model_name, provider_id, provider_resource_id, shard))`,
		`CREATE INDEX idx_overview_totals_user ON overview_totals (scope, source, attributed_user_id)`,
		`CREATE INDEX idx_overview_totals_key ON overview_totals (scope, source, api_key_id)`,
		`CREATE TABLE overview_projection_progress (source TEXT PRIMARY KEY, processed NUMERIC NOT NULL DEFAULT 0, overflowed INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO overview_projection_progress (source) VALUES ('usage_records'), ('request_logs')`,
	}

	if driver == "postgres" {
		statements = append(statements, `CREATE FUNCTION overview_transaction_shard() RETURNS integer LANGUAGE plpgsql AS $$
        DECLARE saved text; candidate integer := 0;
        BEGIN
            saved := NULLIF(current_setting('tokenhub.overview_shard', true), '');
            IF saved IS NOT NULL AND pg_try_advisory_xact_lock(hashtext('tokenhub:overview-writer'), saved::integer) THEN RETURN saved::integer; END IF;
            LOOP
                IF pg_try_advisory_xact_lock(hashtext('tokenhub:overview-writer'), candidate) THEN
                    PERFORM set_config('tokenhub.overview_shard', candidate::text, true);
                    RETURN candidate;
                END IF;
                candidate := candidate + 1;
            END LOOP;
        END $$`)
	}
	for _, table := range []string{"usage_records", "request_logs"} {
		statements = append(statements,
			fmt.Sprintf("ALTER TABLE %s ADD COLUMN overview_projected INTEGER NOT NULL DEFAULT 0", table),
		)
		add := overviewProjectionDelta(table, "NEW", 1, driver)
		subtract := overviewProjectionDelta(table, "OLD", -1, driver)
		if driver == "sqlite" {
			subtract += overviewSQLiteOverflowSQL(table, "OLD")
			add += overviewSQLiteOverflowSQL(table, "NEW")
		}
		fields := append([]string{"overview_projected"}, overviewDimensionColumns...)
		if table == "usage_records" {
			fields = append(fields, "input_tokens", "cached_input_tokens", "cache_write_tokens", "output_tokens", "reasoning_tokens", "total_tokens", "cost_usd")
		} else {
			fields = append(fields, "status_code", "error_code")
		}
		if driver == "sqlite" {
			statements = append(statements,
				fmt.Sprintf("CREATE TRIGGER overview_%s_insert AFTER INSERT ON %s BEGIN UPDATE %s SET overview_projected = 1 WHERE id = NEW.id; END", table, table, table),
				fmt.Sprintf("CREATE TRIGGER overview_%s_update AFTER UPDATE OF %s ON %s WHEN NEW.overview_projected > 0 AND (OLD.overview_projected > 0 OR NEW.overview_projected = 1) BEGIN %s %s END", table, strings.Join(fields, ", "), table, subtract, add),
				fmt.Sprintf("CREATE TRIGGER overview_%s_delete AFTER DELETE ON %s WHEN OLD.overview_projected > 0 BEGIN %s END", table, table, subtract),
			)
		} else {
			statements = append(statements,
				fmt.Sprintf("CREATE FUNCTION overview_%s_insert_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE %s SET overview_projected = 1 WHERE id = NEW.id; RETURN NEW; END $$", table, table),
				fmt.Sprintf("CREATE FUNCTION overview_%s_update_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN %s %s RETURN NEW; END $$", table, subtract, add),
				fmt.Sprintf("CREATE FUNCTION overview_%s_delete_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN %s RETURN OLD; END $$", table, subtract),
				fmt.Sprintf("CREATE TRIGGER overview_%s_insert AFTER INSERT ON %s FOR EACH ROW EXECUTE FUNCTION overview_%s_insert_fn()", table, table, table),
				fmt.Sprintf("CREATE TRIGGER overview_%s_update AFTER UPDATE OF %s ON %s FOR EACH ROW WHEN (NEW.overview_projected > 0 AND (OLD.overview_projected > 0 OR NEW.overview_projected = 1)) EXECUTE FUNCTION overview_%s_update_fn()", table, strings.Join(fields, ", "), table, table),
				fmt.Sprintf("CREATE TRIGGER overview_%s_delete AFTER DELETE ON %s FOR EACH ROW WHEN (OLD.overview_projected > 0) EXECUTE FUNCTION overview_%s_delete_fn()", table, table, table),
			)
		}
	}
	if driver == "sqlite" {
		statements = append(statements, overviewSourceIndexStatements("")...)
	}
	return statements
}

func overviewProjectionDelta(table, row string, sign int, driver string) string {
	values := overviewRowMetricValues(table, row)
	metrics := make([]string, len(overviewMetricColumns))
	updates := overviewMetricUpdates(driver)
	for i, column := range overviewMetricColumns {
		value := values[column]
		if value == "" {
			value = "0"
		}
		metrics[i] = fmt.Sprintf("%d * CAST((%s) AS NUMERIC)", sign, value)
	}

	shard := "0"
	if driver == "postgres" {
		shard = "overview_transaction_shard()"
	}
	predicate := row + ".overview_projected > 0"
	statements := make([]string, 0, 2)
	for _, scope := range []string{"global", "tuple"} {
		dimensions := make([]string, len(overviewDimensionColumns))
		for i := range dimensions {
			dimensions[i] = "''"
		}
		if scope == "tuple" {
			for i, column := range overviewDimensionColumns {
				dimensions[i] = "COALESCE(" + row + "." + column + ", '')"
			}
		}
		statements = append(statements, fmt.Sprintf("INSERT INTO overview_totals (scope, source, shard, project_id, api_key_id, attributed_user_id, model_name, provider_id, provider_resource_id, %s) SELECT '%s', '%s', %s, %s, %s WHERE %s ON CONFLICT (scope, source, project_id, api_key_id, attributed_user_id, model_name, provider_id, provider_resource_id, shard) DO UPDATE SET %s;", strings.Join(overviewMetricColumns, ", "), scope, table, shard, strings.Join(dimensions, ", "), strings.Join(metrics, ", "), predicate, strings.Join(updates, ", ")))
	}
	return strings.Join(statements, "\n")
}

func overviewRowMetricValues(table, row string) map[string]string {
	values := map[string]string{"record_count": "1"}
	if table == "usage_records" {
		values["usage_record_count"] = "1"
		for _, column := range []string{"input_tokens", "cached_input_tokens", "cache_write_tokens", "output_tokens", "reasoning_tokens", "total_tokens"} {
			values[column] = "COALESCE(" + row + "." + column + ", 0)"
		}
		values["estimated_cost_usd"] = "COALESCE(" + row + ".cost_usd, 0)"
	} else {
		values["request_count"] = "CASE WHEN COALESCE(" + row + ".project_id, '') <> 'admin_playground' THEN 1 ELSE 0 END"
		values["errors"] = "CASE WHEN COALESCE(" + row + ".project_id, '') <> 'admin_playground' AND " + row + ".status_code >= 400 AND NOT (" + row + ".status_code = 499 AND COALESCE(" + row + ".error_code, '') = 'request_cancelled') THEN 1 ELSE 0 END"
	}
	return values
}

const overviewSQLiteAmountOverflow = "ABS(estimated_cost_usd) > 1.7976931348623157e308 OR ABS(cost_compensation) > 1.7976931348623157e308"

func overviewSQLiteOverflowSQL(table, row string) string {
	conditions := make([]string, 0, len(overviewMetricColumns))
	for _, column := range overviewMetricColumns {
		if column != "estimated_cost_usd" && column != "cost_compensation" {
			conditions = append(conditions, "typeof("+column+") = 'real'")
		}
	}
	if table == "usage_records" {
		conditions = append(conditions, overviewSQLiteAmountOverflow)
	}
	from := " FROM overview_totals WHERE source = '" + table + "' AND (" + strings.Join(conditions, " OR ") + ") AND "
	affected := "SELECT 1" + from + "scope = 'global'"
	if row != "" {
		dimensions := make([]string, 0, len(overviewDimensionColumns))
		for _, column := range overviewDimensionColumns {
			dimensions = append(dimensions, column+" = COALESCE("+row+"."+column+", '')")
		}
		affected += " UNION ALL SELECT 1" + from + "scope = 'tuple' AND " + strings.Join(dimensions, " AND ")
	}
	return "UPDATE overview_projection_progress SET overflowed = 1 WHERE source = '" + table + "' AND overflowed = 0 AND EXISTS (" + affected + ");"
}

func overviewMetricUpdates(driver string) []string {
	updates := make([]string, len(overviewMetricColumns))
	for i, column := range overviewMetricColumns {
		expression := "overview_totals." + column + " + excluded." + column
		if driver == "sqlite" && column == "cost_compensation" {
			expression += ` + CASE WHEN ABS(overview_totals.estimated_cost_usd) >= ABS(excluded.estimated_cost_usd)
                THEN (overview_totals.estimated_cost_usd - (overview_totals.estimated_cost_usd + excluded.estimated_cost_usd)) + excluded.estimated_cost_usd
                ELSE (excluded.estimated_cost_usd - (overview_totals.estimated_cost_usd + excluded.estimated_cost_usd)) + overview_totals.estimated_cost_usd END`
			expression = "COALESCE(" + expression + ", 0)"
		}
		if driver == "sqlite" && (column == "estimated_cost_usd" || column == "cost_compensation") {
			expression = "CASE WHEN overview_totals.record_count + excluded.record_count = 0 THEN 0 ELSE " + expression + " END"
		}
		updates[i] = column + " = " + expression
	}
	return updates
}
