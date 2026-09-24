package server

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"tokenhub/backend/internal/dbschema"
)

func TestSQLiteGatewayExpandMigrationUpgradesV6Schema(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE api_keys (id text PRIMARY KEY, project_id text, owner_user_id text, name text, key_hash text, status text)`,
		`CREATE TABLE usage_records (id text PRIMARY KEY, request_id text, project_id text, api_key_id text, created_at datetime)`,
		`INSERT INTO api_keys (id, name, key_hash, status) VALUES ('key_existing', 'Existing key', 'hash_existing', 'active')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	var migration dbschema.Migration
	for _, candidate := range SchemaMigrationRegistry() {
		if candidate.Name == "add-gateway-control-plane-schema-sqlite" {
			migration = candidate
			break
		}
	}
	if migration.Go == nil {
		t.Fatal("SQLite Gateway schema migration is not registered as a Go migration")
	}
	if err := migration.Go(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var keyName string
	if err := db.QueryRow("SELECT name FROM api_keys WHERE id = 'key_existing'").Scan(&keyName); err != nil {
		t.Fatal(err)
	}
	if keyName != "Existing key" {
		t.Fatalf("existing API key was not preserved: %q", keyName)
	}
	for _, table := range []string{"gateway_tenants", "gateway_organizations", "gateway_principals", "gateway_principal_organization_bindings", "gateway_projects", "gateway_workloads", "integration_inbox"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("expected migrated table %s, count=%d err=%v", table, count, err)
		}
	}
	rows, err := db.Query("PRAGMA table_info(api_keys)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make([]string, 0)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	for _, column := range []string{"tenant_external_id", "project_external_id", "principal_type", "principal_external_id", "environment", "managed_by", "control_request_id", "control_request_digest", "key_ciphertext"} {
		if !containsGatewayMigrationColumn(columns, column) {
			t.Fatalf("expected API key column %s, columns=%s", column, strings.Join(columns, ","))
		}
	}
}

func TestGatewayExpandMigrationsCoverBothDialects(t *testing.T) {
	seen := map[dbschema.Dialect]bool{}
	for _, migration := range SchemaMigrationRegistry() {
		if strings.HasPrefix(migration.Name, "add-gateway-control-plane-schema-") {
			seen[migration.Dialect] = true
		}
	}
	for _, dialect := range []dbschema.Dialect{dbschema.DialectSQLite, dbschema.DialectPostgres} {
		if !seen[dialect] {
			t.Fatalf("missing Gateway schema migration for %s", dialect)
		}
	}
}

func containsGatewayMigrationColumn(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
