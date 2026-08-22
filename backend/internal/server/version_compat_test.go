package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tokenhub/backend/internal/dbschema"
)

func TestRollbackCompatibilityPreflight(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "rollback-compat.db")
	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "rollback-test-token"})
	ctx := context.Background()

	if verdict := server.rollbackCompatibility(ctx, "v0.4.0"); verdict.Compatibility != rollbackCompatible {
		t.Fatalf("expected v0.4.0 compatible on baseline database, got %+v", verdict)
	}
	if verdict := server.rollbackCompatibility(ctx, "0.4.0"); verdict.Compatibility != rollbackCompatible {
		t.Fatalf("expected canonicalization of 0.4.0, got %+v", verdict)
	}
	if verdict := server.rollbackCompatibility(ctx, "0.5.0"); verdict.Compatibility != rollbackCompatible {
		t.Fatalf("expected v0.5.0 compatible on baseline database (fixture-verified), got %+v", verdict)
	}
	unknown := server.rollbackCompatibility(ctx, "0.3.0")
	if unknown.Compatibility != rollbackUnknown || unknown.ReasonCode != "compatibility_record_missing" ||
		unknown.ReasonParams["version"] != "0.3.0" || !strings.Contains(unknown.Reason, "no verified record") {
		t.Fatalf("expected unknown compatibility for 0.3.0, got %+v", unknown)
	}
	notSemver := server.rollbackCompatibility(ctx, "latest")
	if notSemver.Compatibility != rollbackUnknown || notSemver.ReasonCode != "requested_version_invalid" {
		t.Fatalf("expected unknown compatibility for non-semantic version, got %+v", notSemver)
	}

	// A dirty or unverifiable ledger makes every rollback incompatible.
	if err := store.db.Exec("UPDATE schema_migrations SET checksum = 'tampered' WHERE version = ?", dbschema.BaselineVersion).Error; err != nil {
		t.Fatal(err)
	}
	dirty := server.rollbackCompatibility(ctx, "v0.4.0")
	if dirty.Compatibility != rollbackIncompatible || dirty.ReasonCode != "database_evolution_not_clean" || !strings.Contains(dirty.Reason, "not clean") {
		t.Fatalf("expected incompatible on tampered ledger, got %+v", dirty)
	}
}

func TestRollbackHandlerRefusesUnknownCompatibility(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "rollback-handler.db")
	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "rollback-test-token"})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/system/rollback", strings.NewReader(`{"version":"0.3.0"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer rollback-test-token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown compatibility, got %d %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "rollback_compatibility_unknown") {
		t.Fatalf("expected rollback_compatibility_unknown code, got %s", body)
	}
}

func TestRollbackCompatibilityOnMemoryStore(t *testing.T) {
	server := New(NewMemoryStore())
	verdict := server.rollbackCompatibility(context.Background(), "v0.4.0")
	if verdict.Compatibility != rollbackCompatible {
		t.Fatalf("stores without evolution state have no schema constraints, got %+v", verdict)
	}
}
