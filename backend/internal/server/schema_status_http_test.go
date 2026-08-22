package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAdminSchemaStatusEndpoint(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "schema-status.db")
	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "schema-status-token", AppVersion: "v0.5.0-schema-test"})

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/admin/system/schema-status", nil)
	unauthorizedRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code == http.StatusOK {
		t.Fatal("schema status must require admin authentication")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/admin/system/schema-status", nil)
	request.Header.Set("Authorization", "Bearer schema-status-token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Ready         bool  `json:"ready"`
		SchemaVersion int64 `json:"schema_version"`
		Compatibility struct {
			TargetVersion int64 `json:"target"`
			MinCompatible int64 `json:"min_compatible"`
			MaxCompatible int64 `json:"max_compatible"`
		} `json:"compatibility"`
		Backfills []map[string]any `json:"backfills"`
		Instances []map[string]any `json:"instances"`
		Pending   []map[string]any `json:"pending_expand"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Ready || payload.SchemaVersion != 1 {
		t.Fatalf("expected ready baseline status, got %+v", payload)
	}
	if payload.Compatibility.TargetVersion != 1 || payload.Compatibility.MinCompatible != 1 || payload.Compatibility.MaxCompatible != 1 {
		t.Fatalf("unexpected compatibility manifest: %+v", payload.Compatibility)
	}
	if len(payload.Instances) != 1 || payload.Instances[0]["release"] != "v0.5.0-schema-test" {
		t.Fatalf("expected this instance's heartbeat, got %+v", payload.Instances)
	}
}

func TestAnnotateRollbackCompatibility(t *testing.T) {
	server := New(NewMemoryStore())
	versions := []rollbackVersionInfo{
		{Version: "0.4.0"},
		{Version: "0.3.0"},
	}
	server.annotateRollbackCompatibility(context.Background(), versions)
	if versions[0].Compatibility != rollbackCompatible {
		t.Fatalf("expected v0.4.0 compatible, got %+v", versions[0])
	}
	if versions[1].Compatibility != rollbackUnknown || versions[1].CompatibilityReason == "" ||
		versions[1].CompatibilityReasonCode != "compatibility_record_missing" ||
		versions[1].CompatibilityReasonParams["version"] != "0.3.0" {
		t.Fatalf("expected v0.3.0 unknown with reason, got %+v", versions[1])
	}
}
