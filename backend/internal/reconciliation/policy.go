package reconciliation

import (
	"strings"
	"time"
)

// ApplyRuleSchedule derives persisted scheduling state for a create or update.
func ApplyRuleSchedule(rule Rule, previous *Rule, now time.Time) Rule {
	if rule.Status != StatusActive || rule.ScheduleIntervalMinutes <= 0 {
		rule.NextRunAt = nil
		return rule
	}
	if previous != nil && previous.ScheduleIntervalMinutes == rule.ScheduleIntervalMinutes &&
		previous.Status == rule.Status && previous.NextRunAt != nil {
		rule.NextRunAt = previous.NextRunAt
		return rule
	}
	next := now.UTC().Add(time.Duration(rule.ScheduleIntervalMinutes) * time.Minute)
	rule.NextRunAt = &next
	return rule
}

// ApplyRunCompletion derives rule timestamps after a run is persisted.
func ApplyRunCompletion(rule Rule, run Run) Rule {
	finishedAt := run.StartedAt
	if run.FinishedAt != nil {
		finishedAt = *run.FinishedAt
	}
	rule.LastRunAt = &finishedAt
	if run.Trigger == "scheduled" {
		rule = ApplyRuleSchedule(rule, nil, finishedAt)
	}
	return rule
}

// PrepareRunLock validates and applies the lock transition atomically persisted
// by an adapter.
func PrepareRunLock(run Run, actor string, now time.Time) (Run, bool, error) {
	if run.Status != RunSucceeded {
		return Run{}, false, NewError(ErrorConflict, "reconciliation_run_not_complete", "Only successful reconciliation runs can be locked")
	}
	if run.LockedAt != nil {
		return run, false, nil
	}
	lockedAt := now.UTC()
	run.LockedAt = &lockedAt
	run.LockedBy = strings.TrimSpace(actor)
	return run, true, nil
}

// ValidateRunReplacement protects a successful result after it is locked.
func ValidateRunReplacement(run Run) error {
	if run.LockedAt != nil {
		return NewError(ErrorConflict, "reconciliation_run_locked", "Locked reconciliation runs cannot be recalculated")
	}
	return nil
}
