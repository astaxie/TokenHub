package plugin

import "testing"

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
		{name: "mandatory disabled", state: PackageState{Status: StatusDisabled, Mandatory: true}},
		{name: "rollback without version", state: PackageState{Status: StatusRollbackAvailable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizePackageState(tc.state); err == nil {
				t.Fatal("package state normalized successfully")
			}
		})
	}
}
