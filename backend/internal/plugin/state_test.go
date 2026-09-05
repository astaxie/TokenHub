package plugin

import (
	"path/filepath"
	"testing"
)

func TestReadPackageStateDefaultsLegacyPackageToNormalizedEnabledState(t *testing.T) {
	state, err := readPackageState(t.TempDir())
	if err != nil {
		t.Fatalf("read package state: %v", err)
	}
	if state.Status != StatusEnabled || state.Health != PackageHealthUnknown || !state.Loadable() {
		t.Fatalf("default package state = %+v", state)
	}
}

func TestNormalizePackageStateSupportsLifecycleContract(t *testing.T) {
	enabled, err := NormalizePackageState(PackageState{})
	if err != nil {
		t.Fatalf("normalize default state: %v", err)
	}
	if enabled.Status != StatusEnabled || enabled.Health != PackageHealthUnknown || !enabled.Loadable() {
		t.Fatalf("default state = %+v", enabled)
	}

	pending, err := NormalizePackageState(PackageState{Status: StatusPendingRestart})
	if err != nil {
		t.Fatalf("normalize pending restart state: %v", err)
	}
	if !pending.PendingRestart() || pending.Loadable() {
		t.Fatalf("pending restart state = %+v", pending)
	}

	failed, err := NormalizePackageState(PackageState{
		Status:        StatusFailedValidation,
		Health:        PackageHealthUnhealthy,
		LastErrorCode: "plugin_manifest_schema_unsupported",
		AuditEvent:    PackageLifecycleValidationFailed,
	})
	if err != nil {
		t.Fatalf("normalize failed validation state: %v", err)
	}
	if !failed.FailedValidation() || failed.Loadable() || failed.Health != PackageHealthUnhealthy {
		t.Fatalf("failed validation state = %+v", failed)
	}

	startupFailed, err := NormalizePackageState(PackageState{
		Status:         StatusFailedStartup,
		Health:         PackageHealthUnhealthy,
		RollbackTarget: PackageRollbackTargetBuiltIn,
		LastErrorCode:  "plugin_startup_failed",
		AuditEvent:     PackageLifecycleStartupFailed,
	})
	if err != nil {
		t.Fatalf("normalize failed startup state: %v", err)
	}
	if !startupFailed.FailedStartup() || startupFailed.Loadable() || !startupFailed.BuiltInFallbackAvailable() {
		t.Fatalf("failed startup state = %+v", startupFailed)
	}

	rollback, err := NormalizePackageState(PackageState{
		Status:          StatusRollbackAvailable,
		RollbackVersion: "1.0.0",
		AuditEvent:      PackageLifecycleRollbackAvailable,
	})
	if err != nil {
		t.Fatalf("normalize rollback state: %v", err)
	}
	if !rollback.RollbackAvailable() || !rollback.Loadable() {
		t.Fatalf("rollback state = %+v", rollback)
	}
	if rollback.RollbackTarget != PackageRollbackTargetPreviousPackage {
		t.Fatalf("rollback target = %q, want previous package", rollback.RollbackTarget)
	}

	restartWithRollback, err := NormalizePackageState(PackageState{
		Status:          StatusEnabled,
		RestartRequired: true,
		RollbackVersion: "1.0.0",
		AuditEvent:      PackageLifecyclePendingRestart,
	})
	if err != nil {
		t.Fatalf("normalize restart rollback state: %v", err)
	}
	if !restartWithRollback.RollbackAvailable() || !restartWithRollback.PendingRestart() || !restartWithRollback.Loadable() {
		t.Fatalf("restart rollback state = %+v", restartWithRollback)
	}

	mandatory, err := NormalizePackageState(PackageState{Status: StatusMandatory})
	if err != nil {
		t.Fatalf("normalize mandatory state: %v", err)
	}
	if !mandatory.Mandatory || !mandatory.Loadable() {
		t.Fatalf("mandatory state = %+v", mandatory)
	}
}

func TestNormalizePackageStateRejectsInvalidLifecycleState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state PackageState
	}{
		{name: "unsupported status", state: PackageState{Status: Status("paused")}},
		{name: "unsupported health", state: PackageState{Health: PackageHealthStatus("warming")}},
		{name: "unsupported audit event", state: PackageState{AuditEvent: PackageLifecycleEvent("moved")}},
		{name: "unsupported rollback target", state: PackageState{RollbackTarget: PackageRollbackTarget("sidecar")}},
		{name: "mandatory disabled", state: PackageState{Status: StatusDisabled, Mandatory: true}},
		{name: "rollback without version", state: PackageState{Status: StatusRollbackAvailable}},
		{name: "previous package target without version", state: PackageState{RollbackTarget: PackageRollbackTargetPreviousPackage}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizePackageState(tc.state); err == nil {
				t.Fatal("package state normalized successfully")
			}
		})
	}
}

func TestRuntimeCompleteRuntimeRestartClearsAppliedRestartFlags(t *testing.T) {
	root := t.TempDir()
	runtime := NewRuntime(root)
	externalDir := filepath.Join(root, "privacy")
	writeManifest(t, externalDir, lifecycleHookManifest("tokenhub.privacy"))
	writePackageStateFile(t, externalDir, `{"status":"disabled","restart_required":true,"audit_event":"disabled"}`)
	pendingDir := filepath.Join(root, "pending")
	writeManifest(t, pendingDir, lifecycleHookManifest("tokenhub.pending"))
	writePackageStateFile(t, pendingDir, `{"status":"pending_restart","audit_event":"pending_restart"}`)
	if _, err := runtime.UpdateBuiltInPackageState("tokenhub.provider.qwen", PackageState{
		Status:          StatusDisabled,
		RestartRequired: true,
		AuditEvent:      PackageLifecycleDisabled,
	}); err != nil {
		t.Fatalf("write built-in package state: %v", err)
	}

	if err := runtime.CompleteRuntimeRestart(); err != nil {
		t.Fatalf("complete runtime restart: %v", err)
	}

	external, err := readPackageState(externalDir)
	if err != nil {
		t.Fatalf("read external package state: %v", err)
	}
	if external.RestartRequired || external.Status != StatusDisabled || external.AuditEvent != PackageLifecycleDisabled {
		t.Fatalf("external package state = %+v, want applied disabled state", external)
	}
	builtIn, found, err := runtime.ReadBuiltInPackageState("tokenhub.provider.qwen")
	if err != nil || !found {
		t.Fatalf("read built-in package state found=%t err=%v", found, err)
	}
	if builtIn.RestartRequired || builtIn.Status != StatusDisabled || builtIn.AuditEvent != PackageLifecycleDisabled {
		t.Fatalf("built-in package state = %+v, want applied disabled state", builtIn)
	}
	pending, err := readPackageState(pendingDir)
	if err != nil {
		t.Fatalf("read pending package state: %v", err)
	}
	if !pending.RestartRequired || pending.Status != StatusPendingRestart {
		t.Fatalf("pending package state = %+v, want unresolved pending restart", pending)
	}
}
