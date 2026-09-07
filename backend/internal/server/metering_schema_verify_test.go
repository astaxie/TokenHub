package server

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSchemaVerifyDetectsMissingMeteringObjects(t *testing.T) {
	for _, tc := range []struct{ name, statement string }{
		{"missing_index", "DROP INDEX idx_metering_entries_scope"},
		{"missing_table", "DROP TABLE metering_entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStoreWithConfig(Config{})
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Errorf("close test store: %v", err)
				}
			})
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

func TestSQLiteMeteringMigrationUpgradesAuditCorrelation(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "audit-upgrade.db")
	store, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`DROP INDEX idx_audit_events_correlation_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`ALTER TABLE audit_events DROP COLUMN correlation_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM schema_migrations WHERE version = 4`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO audit_events (id, action, created_at) VALUES ('legacy-audit', 'legacy.read', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatalf("open N-1 database: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	events := upgraded.ListAuditEvents()
	if len(events) != 1 || events[0].ID != "legacy-audit" {
		t.Fatalf("legacy audit events after upgrade = %+v", events)
	}
	upgraded.RecordAuditEvent(AuditEvent{ID: "new-audit", CorrelationID: "request-42", Action: "plugin.test"})
	events = upgraded.ListAuditEvents()
	if len(events) != 2 || events[0].CorrelationID != "request-42" {
		t.Fatalf("new audit event after upgrade = %+v", events)
	}
}
