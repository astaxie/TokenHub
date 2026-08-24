package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/server"
)

func TestPrintInitialAdminPassword(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tokenhub.db")
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte("version: 1\nmodels:\n  - name: cli-bootstrap-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOKENHUB_ENV", "prod")
	t.Setenv("TOKENHUB_DATABASE_URL", "sqlite://"+databasePath)
	t.Setenv("TOKENHUB_SECRET_KEY", strings.Repeat("s", 32))
	t.Setenv("TOKENHUB_ADMIN_TOKEN", "")
	t.Setenv("TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("TOKENHUB_MODEL_CATALOG_FILE", catalogPath)

	config, err := server.ConfigFromEnv().PrepareForStartup()
	if err != nil {
		t.Fatal(err)
	}
	store, err := server.OpenStoreForMaintenance(config.DatabaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}
	want, available, err := store.InitialAdminPassword()
	if err != nil || !available {
		t.Fatalf("initial password unavailable: available=%v err=%v", available, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := printInitialAdminPassword(&output); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(output.String()); got != want {
		t.Fatalf("printed password does not match generated password")
	}
	driver, rawDB, err := server.OpenRawDatabase(config.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if driver != "sqlite" {
		t.Fatalf("driver = %q, want sqlite", driver)
	}
	var heartbeatTables int
	if err := rawDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'instance_heartbeats'").Scan(&heartbeatTables); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}
	if heartbeatTables != 0 {
		t.Fatalf("read-only password command published an instance heartbeat")
	}

	store, err = server.OpenStoreForMaintenance(config.DatabaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AuthenticateAdminUser("admin", want, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := printInitialAdminPassword(&output); err == nil {
		t.Fatal("password remained available after first login")
	}
}
