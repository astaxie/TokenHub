package dbschema

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RepairOutcome reports how a dirty migration was repaired.
type RepairOutcome string

const (
	// RepairVerifiedComplete: the postcondition proved the target state, so
	// the dirty marker was cleared and the version counts as applied.
	RepairVerifiedComplete RepairOutcome = "verified_complete"
	// RepairRetried: the migration declares SafeRetry, so the dirty row was
	// dropped and the migration re-applied.
	RepairRetried RepairOutcome = "retried"
)

// Repair clears a dirty non-transactional migration only through the two
// verified paths documented in docs/database-evolution.md: prove the target
// state with the migration's postcondition, or, when
// the migration declares SafeRetry, drop the dirty row and re-apply it. It
// never rewrites arbitrary ledger rows and refuses everything else.
func (r *Runner) Repair(ctx context.Context, version int64) (RepairOutcome, error) {
	if err := r.ensureLedger(ctx); err != nil {
		return "", err
	}
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return "", err
	}
	row := findApplied(applied, version)
	if row == nil || !row.Dirty {
		return "", newError(ErrCodeNotDirty, version, errors.New("version has no dirty marker"))
	}
	var migration *Migration
	for i := range r.registry {
		if r.registry[i].Version == version {
			migration = &r.registry[i]
			break
		}
	}
	if migration == nil || migration.Postcondition == nil {
		return "", newError(ErrCodeRepairRefused, version, errors.New("no registered migration with a postcondition"))
	}
	release, err := r.acquireLock(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	// Re-read under the lock: another executor may have repaired concurrently.
	applied, err = r.loadApplied(ctx)
	if err != nil {
		return "", err
	}
	row = findApplied(applied, version)
	if row == nil || !row.Dirty {
		return "", newError(ErrCodeNotDirty, version, errors.New("version has no dirty marker"))
	}
	attemptID, err := r.beginAttempt(ctx, version)
	if err != nil {
		return "", err
	}
	started := time.Now()
	verificationCtx := ctx
	var cancel context.CancelFunc
	if migration.LockTimeoutSeconds > 0 {
		verificationCtx, cancel = context.WithTimeout(ctx, time.Duration(migration.LockTimeoutSeconds)*time.Second)
		defer cancel()
	}
	budgeted := newBudgetedExecer(r.db, *migration)
	if err := migration.Postcondition(verificationCtx, budgeted); err == nil {
		if err := r.clearDirty(ctx, version); err != nil {
			r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeApplyFailed)
			return "", newError(ErrCodeApplyFailed, version, err)
		}
		r.finishAttempt(ctx, attemptID, "repair_verified", time.Since(started), "")
		return RepairVerifiedComplete, nil
	} else if !migration.SafeRetry {
		r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeRepairRefused)
		return "", newError(ErrCodeRepairRefused, version,
			fmt.Errorf("postcondition failed and the migration does not allow a safe retry: %w", err))
	}
	if err := r.deleteAppliedRow(ctx, version); err != nil {
		r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeApplyFailed)
		return "", newError(ErrCodeApplyFailed, version, err)
	}
	if _, _, applyErr := r.applyMigration(ctx, *migration); applyErr != nil {
		r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeApplyFailed)
		return "", applyErr
	}
	r.finishAttempt(ctx, attemptID, "repair_retried", time.Since(started), "")
	return RepairRetried, nil
}
