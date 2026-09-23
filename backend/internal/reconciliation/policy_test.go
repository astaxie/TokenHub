package reconciliation_test

import (
	"testing"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

func TestRuleScheduleAndRunCompletionPolicies(t *testing.T) {
	now := time.Date(2026, time.August, 24, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	rule := reconciliation.Rule{Status: reconciliation.StatusActive, ScheduleIntervalMinutes: 30}
	created := reconciliation.ApplyRuleSchedule(rule, nil, now)
	expectedNext := now.UTC().Add(30 * time.Minute)
	if created.NextRunAt == nil || !created.NextRunAt.Equal(expectedNext) {
		t.Fatalf("create schedule mismatch: %#v", created.NextRunAt)
	}

	unchanged := reconciliation.ApplyRuleSchedule(rule, &created, now.Add(time.Hour))
	if unchanged.NextRunAt != created.NextRunAt {
		t.Fatal("unchanged schedule did not preserve the persisted next run")
	}
	disabled := rule
	disabled.Status = reconciliation.StatusDisabled
	if disabled = reconciliation.ApplyRuleSchedule(disabled, &created, now); disabled.NextRunAt != nil {
		t.Fatalf("disabled rule remained scheduled: %#v", disabled.NextRunAt)
	}

	finishedAt := now.Add(2 * time.Hour).UTC()
	completed := reconciliation.ApplyRunCompletion(created, reconciliation.Run{
		Trigger: "scheduled", StartedAt: now.UTC(), FinishedAt: &finishedAt,
	})
	if completed.LastRunAt == nil || !completed.LastRunAt.Equal(finishedAt) ||
		completed.NextRunAt == nil || !completed.NextRunAt.Equal(finishedAt.Add(30*time.Minute)) {
		t.Fatalf("scheduled completion did not advance rule timestamps: %#v", completed)
	}
}

func TestRunLockAndReplacementPolicies(t *testing.T) {
	now := time.Date(2026, time.August, 24, 1, 0, 0, 0, time.UTC)
	if _, _, err := reconciliation.PrepareRunLock(reconciliation.Run{Status: reconciliation.RunFailed}, "actor", now); errorCode(err) != "reconciliation_run_not_complete" {
		t.Fatalf("incomplete run lock returned %v", err)
	}

	locked, changed, err := reconciliation.PrepareRunLock(reconciliation.Run{Status: reconciliation.RunSucceeded}, " actor ", now)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || locked.LockedAt == nil || !locked.LockedAt.Equal(now) || locked.LockedBy != "actor" {
		t.Fatalf("successful run lock mismatch: %#v", locked)
	}
	alreadyLocked, changed, err := reconciliation.PrepareRunLock(locked, "different-actor", now.Add(time.Hour))
	if err != nil || changed || alreadyLocked.LockedBy != "actor" || !alreadyLocked.LockedAt.Equal(now) {
		t.Fatalf("idempotent lock changed ownership: %#v, %v", alreadyLocked, err)
	}
	if err := reconciliation.ValidateRunReplacement(locked); errorCode(err) != "reconciliation_run_locked" {
		t.Fatalf("locked replacement returned %v", err)
	}
}

func errorCode(err error) string {
	_, code, _, _ := reconciliation.ErrorInfo(err)
	return code
}
