package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeNativeBundle creates a minimal bundle that passes
// validateNativeBundle for the given version.
func writeFakeNativeBundle(t *testing.T, root, version string) {
	t.Helper()
	bundle := filepath.Join(root, "releases", version)
	for _, dir := range []string{"bin", "frontend", "catalog", "deploy"} {
		if err := os.MkdirAll(filepath.Join(bundle, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"bin/tokenhub", "bin/node", "bin/tokenhub-run"} {
		if err := os.WriteFile(filepath.Join(bundle, file), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"frontend/server.js", "catalog/model-catalog.yaml", "catalog/provider-catalog.json", "deploy/tokenhub.service"} {
		if err := os.WriteFile(filepath.Join(bundle, file), []byte("placeholder\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bundle, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func activateFakeRelease(t *testing.T, root, version string) {
	t.Helper()
	target := filepath.Join("releases", version)
	next := filepath.Join(root, ".test-current-"+version)
	if err := os.Symlink(target, next); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
}

func guardConfig(root, databaseURL string) Config {
	return Config{
		BuildType:      "release",
		DeploymentType: "native",
		ManagedUpdates: true,
		InstallRoot:    root,
		AppVersion:     "0.6.0",
		DatabaseURL:    databaseURL,
	}
}

// guardVersions injects a versionService whose restart signal records into
// restartCalled instead of terminating the test process.
func guardVersions(t *testing.T, config Config, restartCalled *bool) *versionService {
	t.Helper()
	original := startupGuardNewService
	startupGuardNewService = func(cfg Config) *versionService {
		versions := newVersionService(cfg)
		versions.restartProcess = func() error { *restartCalled = true; return nil }
		return versions
	}
	t.Cleanup(func() { startupGuardNewService = original })
	return startupGuardNewService(config)
}

func TestUpgradeStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	versions := newVersionService(guardConfig(root, "sqlite:///tmp/irrelevant.db"))
	if err := versions.recordUpgrade("0.4.0", "0.6.0"); err != nil {
		t.Fatalf("recordUpgrade: %v", err)
	}
	state, ok, err := versions.upgradeState()
	if err != nil || !ok {
		t.Fatalf("upgradeState: ok=%t err=%v", ok, err)
	}
	if state.PreviousVersion != "0.4.0" || state.TargetVersion != "0.6.0" || state.BootFailed {
		t.Fatalf("unexpected state: %+v", state)
	}
	if err := versions.markUpgradeBootStarted(); err != nil {
		t.Fatalf("markUpgradeBootStarted: %v", err)
	}
	state, ok, _ = versions.upgradeState()
	if !ok || !state.BootFailed {
		t.Fatalf("expected boot_failed flag after marking, got %+v ok=%t", state, ok)
	}
	if err := versions.settleUpgrade(); err != nil {
		t.Fatalf("settleUpgrade: %v", err)
	}
	if _, ok, _ := versions.upgradeState(); ok {
		t.Fatal("expected upgrade state to be removed after settling")
	}
}

func TestRunStartupGuardReactivatesPreviousOnFailedBoot(t *testing.T) {
	root := t.TempDir()
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "guard.db")
	// Opening the store once adopts the database with a clean ledger.
	if _, err := NewSQLiteStore(databaseURL); err != nil {
		t.Fatal(err)
	}
	writeFakeNativeBundle(t, root, "0.4.0")
	writeFakeNativeBundle(t, root, "0.6.0")
	activateFakeRelease(t, root, "0.6.0")

	config := guardConfig(root, databaseURL)
	restartCalled := false
	versions := guardVersions(t, config, &restartCalled)
	if err := versions.recordUpgrade("0.4.0", "0.6.0"); err != nil {
		t.Fatal(err)
	}
	if err := versions.markUpgradeBootStarted(); err != nil {
		t.Fatal(err)
	}

	if err := RunStartupGuard(context.Background(), config); err != nil {
		t.Fatalf("RunStartupGuard: %v", err)
	}
	active, ok := versions.activeNativeVersion()
	if !ok || active != "0.4.0" {
		t.Fatalf("expected previous release 0.4.0 re-activated, active=%q ok=%t", active, ok)
	}
	if !restartCalled {
		t.Fatal("expected restart after auto-rollback")
	}
	if _, ok, _ := versions.upgradeState(); ok {
		t.Fatal("expected upgrade state settled after auto-rollback")
	}
}

func TestRunStartupGuardFirstBootOnlyMarks(t *testing.T) {
	root := t.TempDir()
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "guard-first.db")
	writeFakeNativeBundle(t, root, "0.6.0")
	activateFakeRelease(t, root, "0.6.0")
	config := guardConfig(root, databaseURL)
	restartCalled := false
	versions := guardVersions(t, config, &restartCalled)
	if err := versions.recordUpgrade("0.4.0", "0.6.0"); err != nil {
		t.Fatal(err)
	}

	if err := RunStartupGuard(context.Background(), config); err != nil {
		t.Fatalf("RunStartupGuard: %v", err)
	}
	state, ok, _ := versions.upgradeState()
	if !ok || !state.BootFailed {
		t.Fatalf("expected first boot to mark boot_failed, got %+v ok=%t", state, ok)
	}
	if restartCalled {
		t.Fatal("first boot must not restart")
	}
	if active, _ := versions.activeNativeVersion(); active != "0.6.0" {
		t.Fatalf("first boot must keep the target release active, got %q", active)
	}
}

func TestRunStartupGuardRefusesIncompatiblePrevious(t *testing.T) {
	root := t.TempDir()
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "guard-dirty.db")
	store, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// A dirty ledger makes every rollback incompatible.
	if err := store.db.Exec("INSERT INTO schema_migrations (version, name, phase, checksum, dirty) VALUES (2, 'dirty', 'expand', 'abc', 1)").Error; err != nil {
		t.Fatal(err)
	}
	writeFakeNativeBundle(t, root, "0.4.0")
	writeFakeNativeBundle(t, root, "0.6.0")
	activateFakeRelease(t, root, "0.6.0")
	config := guardConfig(root, databaseURL)
	restartCalled := false
	versions := guardVersions(t, config, &restartCalled)
	if err := versions.recordUpgrade("0.4.0", "0.6.0"); err != nil {
		t.Fatal(err)
	}
	if err := versions.markUpgradeBootStarted(); err != nil {
		t.Fatal(err)
	}

	if err := RunStartupGuard(context.Background(), config); err != nil {
		t.Fatalf("RunStartupGuard: %v", err)
	}
	if restartCalled {
		t.Fatal("refused rollback must not restart")
	}
	if active, _ := versions.activeNativeVersion(); active != "0.6.0" {
		t.Fatalf("refused rollback must keep the target active, got %q", active)
	}
	if _, ok, _ := versions.upgradeState(); ok {
		t.Fatal("refused rollback must settle the state so it never retries")
	}
}

func TestRecordStartupGuardSuccessSettles(t *testing.T) {
	root := t.TempDir()
	config := guardConfig(root, "sqlite:///tmp/irrelevant.db")
	restartCalled := false
	versions := guardVersions(t, config, &restartCalled)
	if err := versions.recordUpgrade("0.4.0", "0.6.0"); err != nil {
		t.Fatal(err)
	}
	if err := RecordStartupGuardSuccess(config); err != nil {
		t.Fatalf("RecordStartupGuardSuccess: %v", err)
	}
	if _, ok, _ := versions.upgradeState(); ok {
		t.Fatal("expected upgrade state settled after successful boot")
	}
}

func TestRunTargetDatabasePreflight(t *testing.T) {
	root := t.TempDir()
	config := guardConfig(root, "sqlite:///tmp/irrelevant.db")
	versions := newVersionService(config)
	ctx := context.Background()

	// A passing target binary: both subcommands exit 0.
	good := filepath.Join(t.TempDir(), "tokenhub-ok")
	if err := os.WriteFile(good, []byte("#!/bin/sh\ncase \"$2\" in prepare|verify) exit 0;; *) exit 1;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := versions.runTargetDatabasePreflight(ctx, good, "0.6.0"); err != nil {
		t.Fatalf("expected preflight to pass, got %v", err)
	}

	// A failing target binary: `db verify` exits non-zero.
	bad := filepath.Join(t.TempDir(), "tokenhub-bad")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := versions.runTargetDatabasePreflight(ctx, bad, "0.6.0")
	if err == nil || !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("expected preflight failure, got %v", err)
	}
}
