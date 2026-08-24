package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"tokenhub/backend/internal/dbschema"
)

const baselineUpdateEnv = "UPDATE_BASELINE"

const sqliteMasterDumpQuery = "SELECT sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' " +
	"ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 ELSE 2 END, name"

func dumpSQLiteMasterStatements(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(sqliteMasterDumpQuery)
	if err != nil {
		t.Fatalf("dump sqlite_master: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var statements []string
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			t.Fatal(err)
		}
		statements = append(statements, statement)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return statements
}

// TestSQLiteBaselineSQLIsCurrent keeps the frozen SQLite baseline in sync with
// the compiled model set and proves that a fresh database created from it is
// semantically and statement-for-statement identical to what the frozen
// AutoMigrate flow produces.
func TestSQLiteBaselineSQLIsCurrent(t *testing.T) {
	ctx := context.Background()
	generated, err := exportSQLiteBaselineStatements(ctx)
	if err != nil {
		t.Fatalf("export baseline statements: %v", err)
	}
	baselinePath := filepath.Join("..", "dbschema", "migrations", "sqlite", "000001_baseline.json")
	if os.Getenv(baselineUpdateEnv) == "1" {
		payload := struct {
			Version    int64    `json:"version"`
			Dialect    string   `json:"dialect"`
			Statements []string `json:"statements"`
		}{Version: dbschema.BaselineVersion, Dialect: "sqlite", Statements: generated}
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
	current, err := dbschema.SQLiteBaselineStatements()
	if err != nil {
		t.Fatalf("frozen baseline unreadable (regenerate with %s=1): %v", baselineUpdateEnv, err)
	}
	if !slices.Equal(generated, current) {
		t.Fatalf("frozen SQLite baseline is stale; regenerate with %s=1 go test ./internal/server -run TestSQLiteBaselineSQLIsCurrent", baselineUpdateEnv)
	}

	freshDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer freshDB.Close()
	for _, statement := range current {
		if _, err := freshDB.Exec(statement); err != nil {
			t.Fatalf("execute baseline statement %q: %v", statement, err)
		}
	}
	scratchGorm, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", NewID("schemacheck"))), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	scratchSQL, err := scratchGorm.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer scratchSQL.Close()
	if err := migrateSchemaObjects(scratchGorm, "sqlite"); err != nil {
		t.Fatalf("build scratch reference: %v", err)
	}
	scratchSet, err := dbschema.Introspect(ctx, scratchSQL, dbschema.DialectSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	freshSet, err := dbschema.Introspect(ctx, freshDB, dbschema.DialectSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if violations := dbschema.CompareObjects(scratchSet, freshSet); len(violations) > 0 {
		t.Fatalf("fresh database from frozen SQL drifted from the AutoMigrate reference: %s", dbschema.FormatViolations(violations))
	}
	// Statement-for-statement: the schema texts registered by a database
	// created from the frozen SQL must equal the AutoMigrate reference texts.
	// (Seed INSERTs from the baseline are data, not sqlite_master entries.)
	freshDump := dumpSQLiteMasterStatements(t, freshDB)
	scratchDump := dumpSQLiteMasterStatements(t, scratchSQL)
	if !slices.Equal(freshDump, scratchDump) {
		t.Fatal("fresh database sqlite_master statements diverge from the AutoMigrate reference")
	}
}
