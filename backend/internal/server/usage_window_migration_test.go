package server

import (
	"path/filepath"
	"testing"
)

func TestUsageWindowIndexesUpgradeExistingLedger(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "usage-window-upgrade.db")
	store, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP INDEX idx_usage_records_api_key_created",
		"DROP INDEX idx_usage_records_attributed_user_created",
		"DELETE FROM schema_migrations WHERE version = 9",
	} {
		if err := store.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	if err := VerifySchemaSemantics(t.Context(), databaseURL); err != nil {
		t.Fatalf("upgraded usage indexes: %v", err)
	}
	for _, index := range []string{"idx_usage_records_api_key_created", "idx_usage_records_attributed_user_created"} {
		if !upgraded.db.Migrator().HasIndex(&UsageRecord{}, index) {
			t.Fatalf("upgrade omitted %s", index)
		}
	}
	if err := upgraded.db.Exec("DROP INDEX idx_usage_records_api_key_created").Error; err != nil {
		t.Fatal(err)
	}
	if err := VerifySchemaSemantics(t.Context(), databaseURL); err == nil {
		t.Fatal("schema verification accepted a missing usage window index")
	}
}
