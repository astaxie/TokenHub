package server

import (
	"context"
	"testing"
)

func TestSchemaVerifyDetectsMissingMeteringObjects(t *testing.T) {
	for _, tc := range []struct{ name, statement string }{
		{"missing_index", "DROP INDEX idx_metering_entries_scope"},
		{"missing_table", "DROP TABLE metering_entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStoreWithConfig(Config{})
			defer store.Close()
			if err := VerifySchemaSemantics(context.Background(), store.sqliteDSN); err != nil {
				t.Fatalf("intact database: %v", err)
			}
			if err := store.db.Exec(tc.statement).Error; err != nil {
				t.Fatal(err)
			}
			if err := VerifySchemaSemantics(context.Background(), store.sqliteDSN); err == nil {
				t.Errorf("schema verification accepted %s with schema ledger still at v4", tc.name)
			}
		})
	}
}
