package dbcli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"tokenhub/backend/internal/server"
)

func cliTestEnv(t *testing.T) string {
	t.Helper()
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "cli.db")
	t.Setenv("TOKENHUB_DATABASE_URL", databaseURL)
	return databaseURL
}

func runCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	cliTestEnv(t)
	code, output := runCLI(t, "bogus")
	if code != 2 || !strings.Contains(output, "unknown db command") {
		t.Fatalf("expected exit 2 with unknown-command message, got %d %q", code, output)
	}
}

func TestStatusAndMigrateOnUnadoptedDatabase(t *testing.T) {
	cliTestEnv(t)
	code, output := runCLI(t, "status")
	if code != 0 || !strings.Contains(output, "baseline recorded:   false") {
		t.Fatalf("status on empty database: code=%d output=%q", code, output)
	}
	code, output = runCLI(t, "migrate")
	if code != 1 || !strings.Contains(output, "adoption baseline") {
		t.Fatalf("migrate before adoption must point at server startup, got %d %q", code, output)
	}
}

func TestPrepareAdoptsLegacyDatabaseWithoutPublishingHeartbeat(t *testing.T) {
	databaseURL := cliTestEnv(t)
	_, db, err := server.OpenRawDatabase(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// A known pre-ledger TokenHub table selects the frozen legacy-adoption
	// path instead of the fresh-baseline replay.
	if _, err := db.Exec("CREATE TABLE projects (id TEXT PRIMARY KEY)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	code, output := runCLI(t, "prepare")
	if code != 0 || !strings.Contains(output, "database prepared") {
		t.Fatalf("prepare legacy database: code=%d output=%q", code, output)
	}
	code, output = runCLI(t, "verify")
	if code != 0 || !strings.Contains(output, "schema: verified") {
		t.Fatalf("verify prepared legacy database: code=%d output=%q", code, output)
	}

	_, db, err = server.OpenRawDatabase(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	var baselineCount, heartbeatTableCount, heartbeatCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1 AND dirty = 0").Scan(&baselineCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'instance_heartbeats'").Scan(&heartbeatTableCount); err != nil {
		t.Fatal(err)
	}
	if heartbeatTableCount > 0 {
		if err := db.QueryRow("SELECT COUNT(*) FROM instance_heartbeats").Scan(&heartbeatCount); err != nil {
			t.Fatal(err)
		}
	}
	if baselineCount != 1 || heartbeatCount != 0 {
		t.Fatalf("prepare state: baseline=%d heartbeats=%d, want 1 and 0", baselineCount, heartbeatCount)
	}
}

func TestCommandsOnAdoptedDatabase(t *testing.T) {
	databaseURL := cliTestEnv(t)
	// Opening the store once adopts the database and records the baseline.
	// The file handle is released by the test process; nothing reopens it.
	if _, err := server.NewSQLiteStore(databaseURL); err != nil {
		t.Fatalf("open store: %v", err)
	}

	code, output := runCLI(t, "status")
	if code != 0 || !strings.Contains(output, "baseline recorded:   true") || !strings.Contains(output, "current version:     1") {
		t.Fatalf("status on adopted database: code=%d output=%q", code, output)
	}
	code, output = runCLI(t, "verify")
	if code != 0 || !strings.Contains(output, "ledger: verified") || !strings.Contains(output, "schema: verified") {
		t.Fatalf("verify on adopted database: code=%d output=%q", code, output)
	}
	code, output = runCLI(t, "migrate")
	if code != 0 || !strings.Contains(output, "nothing to migrate") {
		t.Fatalf("migrate on adopted database: code=%d output=%q", code, output)
	}
}

func TestRepairRequiresDirtyVersion(t *testing.T) {
	databaseURL := cliTestEnv(t)
	if _, err := server.NewSQLiteStore(databaseURL); err != nil {
		t.Fatal(err)
	}
	code, output := runCLI(t, "repair", "--version", "0")
	if code != 1 || !strings.Contains(output, "requires --version") {
		t.Fatalf("repair without version: code=%d output=%q", code, output)
	}
	code, output = runCLI(t, "repair", "--version", "1")
	if code != 1 || !strings.Contains(output, "not_dirty") {
		t.Fatalf("repair on clean version: code=%d output=%q", code, output)
	}
}

func TestContractDryRunOnAdoptedDatabase(t *testing.T) {
	databaseURL := cliTestEnv(t)
	// Open the store once to adopt, then simulate a drained serving instance:
	// the heartbeat table exists but holds no live rows. Without the table the
	// cluster preflight fails closed (it cannot prove no instance is serving).
	store, err := server.NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	stopHeartbeat := store.StartInstanceHeartbeat("cli-test")
	stopHeartbeat()
	code, output := runCLI(t, "contract", "--dry-run")
	if code != 0 || !strings.Contains(output, "pending contract migrations: 0") || !strings.Contains(output, "dry run") {
		t.Fatalf("contract dry run: code=%d output=%q", code, output)
	}
	// Executing without the maintenance assertion is refused even with
	// nothing pending; on sqlite the internal backup only happens after it.
	code, output = runCLI(t, "contract")
	if code != 1 || !strings.Contains(output, "--maintenance") {
		t.Fatalf("contract without maintenance assertion: code=%d output=%q", code, output)
	}
}

func TestSQLiteContractBackupStoreDoesNotPublishHeartbeat(t *testing.T) {
	databaseURL := cliTestEnv(t)
	config := server.ConfigFromEnv()
	config.DatabaseURL = databaseURL
	config.SQLiteBackupDir = t.TempDir()

	runtimeStore, err := server.OpenStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	stopHeartbeat := runtimeStore.StartInstanceHeartbeat("cli-test")
	stopHeartbeat()
	if err := runtimeStore.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := openSession(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	maintenanceStore, err := server.OpenStoreForMaintenance(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenanceStore.Close() //nolint:errcheck // test cleanup
	backup, err := maintenanceStore.CreateSQLiteBackup("tokenhub-db-contract", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenanceStore.GetSQLiteBackup(backup.ID); err != nil {
		t.Fatalf("verify backup: %v", err)
	}
	if err := requireNoLiveInstances(context.Background(), s); err != nil {
		t.Fatalf("maintenance backup store must not block contract: %v", err)
	}
}
