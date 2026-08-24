package server

import (
	"errors"
	"path/filepath"
	"testing"

	"tokenhub/backend/internal/dbschema"
)

func TestSchemaLedgerBaselineAdoptedAndVerified(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-ledger.db")
	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}

	// Opening the store records the adoption baseline exactly once.
	var baselineCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ? AND dirty = 0", dbschema.BaselineVersion).Scan(&baselineCount); err != nil {
		t.Fatal(err)
	}
	if baselineCount != 1 {
		t.Fatalf("expected one clean adoption baseline row, got %d", baselineCount)
	}

	// A tampered ledger must refuse the next startup instead of booting on an
	// unverified schema state.
	if _, err := sqlDB.Exec("UPDATE schema_migrations SET checksum = 'tampered' WHERE version = ?", dbschema.BaselineVersion); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = NewSQLiteStore("sqlite://" + databasePath)
	if err == nil {
		t.Fatal("expected store open to refuse a tampered schema ledger")
	}
	var schemaErr *dbschema.Error
	if !errors.As(err, &schemaErr) || schemaErr.Code != dbschema.ErrCodeChecksumMismatch {
		t.Fatalf("expected checksum_mismatch refusal, got %v", err)
	}
}
