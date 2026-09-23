package dbschema

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// ObjectSet is a dialect-portable semantic snapshot of a database schema:
// tables with columns, primary keys, indexes, and triggers. It is produced by
// Introspect and compared by CompareObjects; raw DDL text is deliberately not
// part of it because column-order differences would cause false positives.
// The JSON tags keep committed fixtures (for example the immutable N-1 legacy
// snapshot) stable across releases.
type ObjectSet struct {
	Tables []TableObjects `json:"tables"`
}

type TableObjects struct {
	Name      string          `json:"name"`
	Columns   []ColumnObjects `json:"columns"`
	PKColumns []string        `json:"pk_columns"`
	Indexes   []IndexObjects  `json:"indexes"`
	Triggers  []string        `json:"triggers"`
}

type ColumnObjects struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type IndexObjects struct {
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// Violation describes one semantic difference between a reference schema and
// the actual database. Extra objects in the actual schema are not violations.
type Violation struct {
	Table    string
	Kind     string
	Expected string
	Actual   string
}

func (v Violation) String() string {
	switch {
	case v.Table == "":
		return fmt.Sprintf("%s (expected %q, actual %q)", v.Kind, v.Expected, v.Actual)
	default:
		return fmt.Sprintf("%s on table %q (expected %q, actual %q)", v.Kind, v.Table, v.Expected, v.Actual)
	}
}

func FormatViolations(violations []Violation) string {
	parts := make([]string, 0, len(violations))
	for _, v := range violations {
		parts = append(parts, v.String())
	}
	return strings.Join(parts, "; ")
}

// Introspect reads the semantic schema of the connected database. For
// PostgreSQL, schema selects the schema to read; an empty schema reads
// current_schema(). SQLite ignores the schema argument.
func Introspect(ctx context.Context, db *sql.DB, dialect Dialect, schema string) (ObjectSet, error) {
	switch dialect {
	case DialectSQLite:
		return introspectSQLite(ctx, db)
	case DialectPostgres:
		return introspectPostgres(ctx, db, schema)
	default:
		return ObjectSet{}, fmt.Errorf("dbschema: introspect: unsupported dialect %q", dialect)
	}
}

// QuoteIdent quotes an identifier for the dialect-neutral introspection
// queries. Both SQLite PRAGMA and PostgreSQL metadata queries accept double
// quotes; embedded quotes are doubled per SQL standard.
func QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func introspectSQLite(ctx context.Context, db *sql.DB) (ObjectSet, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return ObjectSet{}, fmt.Errorf("dbschema: list sqlite tables: %w", err)
	}
	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return ObjectSet{}, err
		}
		tableNames = append(tableNames, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ObjectSet{}, err
	}
	_ = rows.Close()

	set := ObjectSet{}
	for _, table := range tableNames {
		objects, err := introspectSQLiteTable(ctx, db, table)
		if err != nil {
			return ObjectSet{}, err
		}
		set.Tables = append(set.Tables, objects)
	}
	return set, nil
}

func introspectSQLiteTable(ctx context.Context, db *sql.DB, table string) (TableObjects, error) {
	objects := TableObjects{Name: table}

	infoRows, err := db.QueryContext(ctx, "PRAGMA table_info("+QuoteIdent(table)+")")
	if err != nil {
		return TableObjects{}, fmt.Errorf("dbschema: sqlite table_info(%s): %w", table, err)
	}
	type sqliteColumn struct {
		name     string
		typeName string
		notNull  bool
		pk       int
	}
	var columns []sqliteColumn
	for infoRows.Next() {
		var cid int
		var defaultValue sql.NullString
		var col sqliteColumn
		if err := infoRows.Scan(&cid, &col.name, &col.typeName, &col.notNull, &defaultValue, &col.pk); err != nil {
			_ = infoRows.Close()
			return TableObjects{}, err
		}
		columns = append(columns, col)
	}
	if err := infoRows.Err(); err != nil {
		_ = infoRows.Close()
		return TableObjects{}, err
	}
	_ = infoRows.Close()

	pkByPosition := map[int]string{}
	for _, col := range columns {
		// SQLite's pragma reports INTEGER PRIMARY KEY columns as nullable even
		// though the rowid alias enforces NOT NULL; normalize to SQL semantics
		// so comparisons stay meaningful.
		nullable := !col.notNull && col.pk == 0
		objects.Columns = append(objects.Columns, ColumnObjects{
			Name:     col.name,
			Type:     normalizeColumnType(col.typeName),
			Nullable: nullable,
		})
		if col.pk > 0 {
			pkByPosition[col.pk] = col.name
		}
	}
	for position := 1; ; position++ {
		name, ok := pkByPosition[position]
		if !ok {
			break
		}
		objects.PKColumns = append(objects.PKColumns, name)
	}

	indexRows, err := db.QueryContext(ctx, "PRAGMA index_list("+QuoteIdent(table)+")")
	if err != nil {
		return TableObjects{}, fmt.Errorf("dbschema: sqlite index_list(%s): %w", table, err)
	}
	type sqliteIndex struct {
		name   string
		unique bool
		origin string
	}
	var indexes []sqliteIndex
	for indexRows.Next() {
		var seq int
		var idx sqliteIndex
		var partial bool
		if err := indexRows.Scan(&seq, &idx.name, &idx.unique, &idx.origin, &partial); err != nil {
			_ = indexRows.Close()
			return TableObjects{}, err
		}
		indexes = append(indexes, idx)
	}
	if err := indexRows.Err(); err != nil {
		_ = indexRows.Close()
		return TableObjects{}, err
	}
	_ = indexRows.Close()

	for _, idx := range indexes {
		// Primary-key autoindexes duplicate the PK column comparison.
		if idx.origin == "pk" {
			continue
		}
		columns, err := sqliteIndexColumns(ctx, db, idx.name)
		if err != nil {
			return TableObjects{}, err
		}
		if len(columns) == 0 {
			continue
		}
		objects.Indexes = append(objects.Indexes, IndexObjects{Columns: columns, Unique: idx.unique})
	}

	triggerRows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name = ? ORDER BY name", table)
	if err != nil {
		return TableObjects{}, fmt.Errorf("dbschema: sqlite triggers(%s): %w", table, err)
	}
	for triggerRows.Next() {
		var name string
		if err := triggerRows.Scan(&name); err != nil {
			_ = triggerRows.Close()
			return TableObjects{}, err
		}
		objects.Triggers = append(objects.Triggers, name)
	}
	if err := triggerRows.Err(); err != nil {
		_ = triggerRows.Close()
		return TableObjects{}, err
	}
	_ = triggerRows.Close()
	return objects, nil
}

func sqliteIndexColumns(ctx context.Context, db *sql.DB, index string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA index_info("+QuoteIdent(index)+")")
	if err != nil {
		return nil, fmt.Errorf("dbschema: sqlite index_info(%s): %w", index, err)
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var seqno, cid int
		var name sql.NullString
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		// Expression indexes have no plain column name; skip them.
		if !name.Valid || name.String == "" {
			return nil, nil
		}
		columns = append(columns, name.String)
	}
	return columns, rows.Err()
}

func introspectPostgres(ctx context.Context, db *sql.DB, schema string) (ObjectSet, error) {
	schemaFilter, schemaArg := postgresSchemaFilter(schema)
	tableRows, err := db.QueryContext(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema = "+schemaFilter+" ORDER BY table_name", schemaArg...)
	if err != nil {
		return ObjectSet{}, fmt.Errorf("dbschema: list postgres tables: %w", err)
	}
	var tableNames []string
	for tableRows.Next() {
		var name string
		if err := tableRows.Scan(&name); err != nil {
			_ = tableRows.Close()
			return ObjectSet{}, err
		}
		tableNames = append(tableNames, name)
	}
	if err := tableRows.Err(); err != nil {
		_ = tableRows.Close()
		return ObjectSet{}, err
	}
	_ = tableRows.Close()

	set := ObjectSet{}
	for _, table := range tableNames {
		objects, err := introspectPostgresTable(ctx, db, schema, table)
		if err != nil {
			return ObjectSet{}, err
		}
		set.Tables = append(set.Tables, objects)
	}
	return set, nil
}

// postgresSchemaFilter returns a SQL expression (and optional argument) that
// restricts information_schema queries to the requested schema.
func postgresSchemaFilter(schema string) (string, []any) {
	if schema == "" {
		return "current_schema()", nil
	}
	return "$1", []any{schema}
}

func introspectPostgresTable(ctx context.Context, db *sql.DB, schema, table string) (TableObjects, error) {
	objects := TableObjects{Name: table}
	schemaFilter, schemaArg := postgresSchemaFilter(schema)

	columnRows, err := db.QueryContext(ctx,
		"SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = "+schemaFilter+" AND table_name = $"+fmt.Sprint(len(schemaArg)+1)+" ORDER BY ordinal_position",
		append(schemaArg, table)...)
	if err != nil {
		return TableObjects{}, fmt.Errorf("dbschema: postgres columns(%s): %w", table, err)
	}
	for columnRows.Next() {
		var col ColumnObjects
		var isNullable string
		if err := columnRows.Scan(&col.Name, &col.Type, &isNullable); err != nil {
			_ = columnRows.Close()
			return TableObjects{}, err
		}
		col.Type = normalizeColumnType(col.Type)
		col.Nullable = isNullable == "YES"
		objects.Columns = append(objects.Columns, col)
	}
	if err := columnRows.Err(); err != nil {
		_ = columnRows.Close()
		return TableObjects{}, err
	}
	_ = columnRows.Close()

	pkRows, err := db.QueryContext(ctx,
		"SELECT kcu.column_name FROM information_schema.table_constraints tc "+
			"JOIN information_schema.key_column_usage kcu ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema AND kcu.table_name = tc.table_name "+
			"WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = "+schemaFilter+" AND tc.table_name = $"+fmt.Sprint(len(schemaArg)+1)+" ORDER BY kcu.ordinal_position",
		append(schemaArg, table)...)
	if err != nil {
		return TableObjects{}, fmt.Errorf("dbschema: postgres primary key(%s): %w", table, err)
	}
	for pkRows.Next() {
		var name string
		if err := pkRows.Scan(&name); err != nil {
			_ = pkRows.Close()
			return TableObjects{}, err
		}
		objects.PKColumns = append(objects.PKColumns, name)
	}
	if err := pkRows.Err(); err != nil {
		_ = pkRows.Close()
		return TableObjects{}, err
	}
	_ = pkRows.Close()

	indexRows, err := db.QueryContext(ctx,
		"SELECT i.relname, ix.indisunique, a.attname FROM pg_index ix "+
			"JOIN pg_class i ON i.oid = ix.indexrelid "+
			"JOIN pg_class t ON t.oid = ix.indrelid "+
			"JOIN pg_namespace n ON n.oid = t.relnamespace "+
			// Preserve the index's declared column order (indkey position)
			// instead of the table's physical column order: legacy databases
			// append columns with ALTER TABLE, so the same index would
			// introspect with a different column order than a fresh schema.
			"JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true "+
			"JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum "+
			"WHERE n.nspname = "+schemaFilter+" AND t.relname = $"+fmt.Sprint(len(schemaArg)+1)+" AND ix.indisprimary = false "+
			"AND ix.indisvalid AND ix.indisready "+
			"ORDER BY i.relname, k.ord",
		append(schemaArg, table)...)
	if err != nil {
		return TableObjects{}, fmt.Errorf("dbschema: postgres indexes(%s): %w", table, err)
	}
	indexColumns := map[string][]string{}
	indexUnique := map[string]bool{}
	var indexOrder []string
	for indexRows.Next() {
		var indexName, columnName string
		var unique bool
		if err := indexRows.Scan(&indexName, &unique, &columnName); err != nil {
			_ = indexRows.Close()
			return TableObjects{}, err
		}
		if _, seen := indexColumns[indexName]; !seen {
			indexOrder = append(indexOrder, indexName)
			indexUnique[indexName] = unique
		}
		indexColumns[indexName] = append(indexColumns[indexName], columnName)
	}
	if err := indexRows.Err(); err != nil {
		_ = indexRows.Close()
		return TableObjects{}, err
	}
	_ = indexRows.Close()
	for _, indexName := range indexOrder {
		columns := indexColumns[indexName]
		if len(columns) == 0 {
			continue
		}
		objects.Indexes = append(objects.Indexes, IndexObjects{Columns: columns, Unique: indexUnique[indexName]})
	}

	triggerRows, err := db.QueryContext(ctx,
		"SELECT trigger_name FROM information_schema.triggers WHERE trigger_schema = "+schemaFilter+" AND event_object_table = $"+fmt.Sprint(len(schemaArg)+1)+" ORDER BY trigger_name",
		append(schemaArg, table)...)
	if err != nil {
		return TableObjects{}, fmt.Errorf("dbschema: postgres triggers(%s): %w", table, err)
	}
	for triggerRows.Next() {
		var name string
		if err := triggerRows.Scan(&name); err != nil {
			_ = triggerRows.Close()
			return TableObjects{}, err
		}
		objects.Triggers = append(objects.Triggers, name)
	}
	if err := triggerRows.Err(); err != nil {
		_ = triggerRows.Close()
		return TableObjects{}, err
	}
	_ = triggerRows.Close()
	return objects, nil
}

// normalizeColumnType makes types comparable within one dialect: lowercase and
// drop width/typmod suffixes such as varchar(255).
func normalizeColumnType(typeName string) string {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	if open := strings.Index(normalized, "("); open >= 0 {
		normalized = normalized[:open]
	}
	return strings.TrimSpace(normalized)
}

// CompareObjects checks that every object in the reference schema exists in
// the actual schema with the same semantics. Extra objects in the actual
// schema are allowed.
func CompareObjects(reference, actual ObjectSet) []Violation {
	var violations []Violation
	actualByTable := make(map[string]TableObjects, len(actual.Tables))
	for _, table := range actual.Tables {
		actualByTable[table.Name] = table
	}
	for _, expected := range reference.Tables {
		found, ok := actualByTable[expected.Name]
		if !ok {
			violations = append(violations, Violation{Table: expected.Name, Kind: "missing_table"})
			continue
		}
		violations = append(violations, compareColumns(expected, found)...)
		violations = append(violations, comparePrimaryKey(expected, found)...)
		violations = append(violations, compareIndexes(expected, found)...)
		violations = append(violations, compareTriggers(expected, found)...)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Table != violations[j].Table {
			return violations[i].Table < violations[j].Table
		}
		return violations[i].Kind < violations[j].Kind
	})
	return violations
}

func compareColumns(expected, actual TableObjects) []Violation {
	var violations []Violation
	actualColumns := make(map[string]ColumnObjects, len(actual.Columns))
	for _, column := range actual.Columns {
		actualColumns[column.Name] = column
	}
	for _, column := range expected.Columns {
		found, ok := actualColumns[column.Name]
		if !ok {
			violations = append(violations, Violation{Table: expected.Name, Kind: "missing_column", Expected: column.Name})
			continue
		}
		if found.Type != column.Type {
			violations = append(violations, Violation{Table: expected.Name, Kind: "column_type_mismatch", Expected: column.Name + ":" + column.Type, Actual: column.Name + ":" + found.Type})
		}
		if found.Nullable != column.Nullable {
			violations = append(violations, Violation{Table: expected.Name, Kind: "column_nullability_mismatch", Expected: fmt.Sprintf("%s nullable=%t", column.Name, column.Nullable), Actual: fmt.Sprintf("%s nullable=%t", found.Name, found.Nullable)})
		}
	}
	return violations
}

// comparePrimaryKey reports a mismatch between the expected and actual primary
// key columns. It is a table-level property, so it lives beside the column and
// index comparators rather than inside compareColumns.
func comparePrimaryKey(expected, actual TableObjects) []Violation {
	if strings.Join(expected.PKColumns, ",") != strings.Join(actual.PKColumns, ",") {
		return []Violation{{Table: expected.Name, Kind: "primary_key_mismatch", Expected: strings.Join(expected.PKColumns, ","), Actual: strings.Join(actual.PKColumns, ",")}}
	}
	return nil
}

func compareIndexes(expected, actual TableObjects) []Violation {
	var violations []Violation
	actualIndexes := make(map[string]bool, len(actual.Indexes))
	for _, index := range actual.Indexes {
		actualIndexes[indexKey(index)] = true
	}
	for _, index := range expected.Indexes {
		if !actualIndexes[indexKey(index)] {
			violations = append(violations, Violation{Table: expected.Name, Kind: "missing_index", Expected: indexKey(index)})
		}
	}
	return violations
}

func indexKey(index IndexObjects) string {
	return fmt.Sprintf("unique=%t(%s)", index.Unique, strings.Join(index.Columns, ","))
}

func compareTriggers(expected, actual TableObjects) []Violation {
	var violations []Violation
	actualTriggers := make(map[string]bool, len(actual.Triggers))
	for _, name := range actual.Triggers {
		actualTriggers[name] = true
	}
	for _, name := range expected.Triggers {
		if !actualTriggers[name] {
			violations = append(violations, Violation{Table: expected.Name, Kind: "missing_trigger", Expected: name})
		}
	}
	return violations
}
