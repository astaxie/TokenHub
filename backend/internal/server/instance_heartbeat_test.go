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

func TestInstanceHeartbeatLifecycle(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "heartbeat.db")
	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	stop := store.StartInstanceHeartbeat("v0.5.0-heartbeat-test")
	heartbeats, err := store.ListInstanceHeartbeats(context.Background())
	if err != nil {
		t.Fatalf("list heartbeats: %v", err)
	}
	if len(heartbeats) != 1 || heartbeats[0].Release != "v0.5.0-heartbeat-test" || heartbeats[0].LastSeen == "" {
		t.Fatalf("unexpected heartbeat rows: %+v", heartbeats)
	}
	stop()
	heartbeats, err = store.ListInstanceHeartbeats(context.Background())
	if err != nil {
		t.Fatalf("list heartbeats after stop: %v", err)
	}
	if len(heartbeats) != 0 {
		t.Fatalf("stopped instance must remove its row, got %+v", heartbeats)
	}
	// Stopping twice must not panic.
	stop()
}

func TestOpenStoreFailsWhenInitialHeartbeatCannotPublish(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "heartbeat-startup.db")
	maintenanceStore, err := OpenStoreForMaintenance(databaseURL, Config{AppVersion: "v0.6.0-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := maintenanceStore.db.Exec(instanceHeartbeatTableDDL).Error; err != nil {
		t.Fatal(err)
	}
	if err := maintenanceStore.db.Exec(`CREATE TRIGGER reject_instance_heartbeat_insert
		BEFORE INSERT ON instance_heartbeats
		BEGIN
			SELECT RAISE(FAIL, 'heartbeat insert rejected');
		END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := maintenanceStore.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStoreWithConfig(databaseURL, Config{AppVersion: "v0.6.0-test"})
	if store != nil {
		_ = store.Close()
		t.Fatal("startup must not return a store without an initial heartbeat")
	}
	if err == nil || !strings.Contains(err.Error(), "publish initial instance heartbeat") {
		t.Fatalf("expected initial heartbeat publication failure, got %v", err)
	}
}

func TestHeartbeatTableSetupFailureFailsReadinessClosed(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "heartbeat-refresh.db")
	store, err := OpenStoreForMaintenance(databaseURL, Config{AppVersion: "v0.6.0-test"})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	stop := store.StartInstanceHeartbeat("v0.6.0-test")
	defer stop()
	if got := store.heartbeatState.Load(); got != heartbeatFailing {
		t.Fatalf("heartbeat state = %d, want failing", got)
	}
}

func TestReadinessGatesOnEvolutionState(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "ready.db")
	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)

	request := func() *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return recorder
	}
	if recorder := request(); recorder.Code != http.StatusOK {
		t.Fatalf("expected ready on adopted database, got %d %s", recorder.Code, recorder.Body.String())
	}
	status := store.DatabaseEvolutionStatus(context.Background())
	if !status.Ready || status.SchemaVersion != dbschema.BaselineVersion {
		t.Fatalf("unexpected evolution status: %+v", status)
	}

	// A tampered ledger must pull the instance out of rotation.
	if err := store.db.Exec("UPDATE schema_migrations SET checksum = 'tampered' WHERE version = ?", dbschema.BaselineVersion).Error; err != nil {
		t.Fatal(err)
	}
	recorder := request()
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected unavailable on tampered ledger, got %d %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "verification failed") {
		t.Fatalf("expected a reason in the unready body, got %s", body)
	}
	status = store.DatabaseEvolutionStatus(context.Background())
	if status.Ready || !strings.Contains(status.Reason, "verification failed") {
		t.Fatalf("unexpected evolution status after tamper: %+v", status)
	}
}
