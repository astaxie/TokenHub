//go:build integration

package server

import (
	"context"
	"testing"
)

func TestPostgresMeteringSchemaVerification(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	for _, tc := range []struct{ name, statement string }{
		{"missing_index", "DROP INDEX idx_metering_entries_scope"},
		{"missing_table", "DROP TABLE metering_entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := createPostgresSchema(t, admin, "tokenhub_pg_metering_")
			dsn, err := withSearchPath(pgURL, schema)
			if err != nil {
				t.Fatal(err)
			}
			store, err := NewStoreWithDialect(dsn, Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := VerifySchemaSemantics(context.Background(), dsn); err != nil {
				t.Fatalf("intact database: %v", err)
			}
			if err := store.db.Exec(tc.statement).Error; err != nil {
				t.Fatal(err)
			}
			if err := VerifySchemaSemantics(context.Background(), dsn); err == nil {
				t.Fatalf("verification accepted %s", tc.name)
			}
		})
	}
}
