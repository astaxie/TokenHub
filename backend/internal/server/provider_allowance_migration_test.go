package server

import (
	"path/filepath"
	"testing"
)

func TestSQLiteProviderAllowanceMigrationPreservesExistingData(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "allowance-upgrade.db")
	store, err := NewStoreWithDialect(databaseURL, Config{})
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{"provider_allowance_wallet_counters", "provider_allowance_sessions", "provider_allowance_events", "provider_allowance_points", "provider_allowance_settings"}
	if err := store.db.Exec(`INSERT INTO audit_events (id, action, created_at) VALUES ('allowance-upgrade-audit', 'test.read', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if err := store.db.Exec(`DROP TABLE "` + table + `"`).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.Exec("DELETE FROM schema_migrations WHERE version = 11").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewStoreWithDialect(databaseURL, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	if err := VerifySchemaSemantics(t.Context(), databaseURL); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if !upgraded.db.Migrator().HasTable(table) {
			t.Fatalf("missing allowance table %s", table)
		}
	}
	var audit AuditEvent
	if err := upgraded.db.First(&audit, "id = ?", "allowance-upgrade-audit").Error; err != nil {
		t.Fatal("existing audit data lost during expansion")
	}
}
