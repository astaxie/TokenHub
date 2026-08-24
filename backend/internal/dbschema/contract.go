package dbschema

import (
	"context"
	"fmt"
)

// ContractOptions carries the caller-verified preconditions that must hold
// before a contract migration may run, as documented in
// docs/database-evolution.md. The runner cannot observe
// these facts itself: data-backfill completion (Group 2 ledger), live
// incompatible instances (heartbeat), backup evidence, and drain or
// maintenance state all belong to the maintenance command that drives the
// runner. Every non-nil hook must succeed, for dry-runs as well, so operators
// see exactly what blocks execution.
type ContractOptions struct {
	DryRun           bool
	RequireBackfills func(ctx context.Context) error
	RequireCluster   func(ctx context.Context) error
	RequireBackup    func(ctx context.Context) error
	RequireWindow    func(ctx context.Context) error
}

// ContractPlan reports what an ApplyContract call would execute.
type ContractPlan struct {
	Migrations []Migration
	DryRun     bool
}

// PlanContract lists pending contract migrations for the dialect after ledger
// verification.
func (r *Runner) PlanContract(ctx context.Context) (ContractPlan, error) {
	if err := r.ensureLedger(ctx); err != nil {
		return ContractPlan{}, err
	}
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return ContractPlan{}, err
	}
	if err := r.verifyApplied(applied); err != nil {
		return ContractPlan{}, err
	}
	if findApplied(applied, BaselineVersion) == nil {
		return ContractPlan{}, newError(ErrCodeBaselineMissing, BaselineVersion, errNoBaseline)
	}
	// Contract migrations may only run once the release's own expand
	// migrations have been applied; executing them against an unexpanded
	// schema would drop or reshape structures their expands never created.
	if pending := r.pendingByPhase(applied, PhaseExpand); len(pending) > 0 {
		return ContractPlan{}, newError(ErrCodeExpandPending, 0, fmt.Errorf("%d expand migration(s) still pending; run tokenhub db migrate or restart the server first", len(pending)))
	}
	return ContractPlan{Migrations: r.pendingByPhase(applied, PhaseContract)}, nil
}

// ApplyContract executes pending contract migrations after every configured
// precondition passes. A dry run performs the same verification but executes
// nothing. Ordinary startup paths never call this.
func (r *Runner) ApplyContract(ctx context.Context, options ContractOptions) (Result, error) {
	plan, err := r.PlanContract(ctx)
	if err != nil {
		return Result{}, err
	}
	plan.DryRun = options.DryRun
	if options.DryRun {
		// Dry runs only report: no lock, but preconditions still run so
		// operators see exactly what blocks execution.
		if err := r.runContractPreconditions(ctx, options); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	}
	if len(plan.Migrations) == 0 {
		return Result{}, nil
	}
	release, err := r.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := r.verifyApplied(applied); err != nil {
		return Result{}, err
	}
	// Preconditions run under the lock, in the same lock domain as server
	// startup: an instance that begins serving has to pass this lock first,
	// so the cluster check cannot go stale between preflight and execution.
	if err := r.runContractPreconditions(ctx, options); err != nil {
		return Result{}, err
	}
	result := Result{}
	for _, m := range r.pendingByPhase(applied, PhaseContract) {
		record, outcome, applyErr := r.applyMigration(ctx, m)
		if applyErr != nil {
			return result, applyErr
		}
		if outcome == "success" {
			result.Applied = append(result.Applied, record)
		}
	}
	return result, nil
}

func (r *Runner) runContractPreconditions(ctx context.Context, options ContractOptions) error {
	checks := []struct {
		name string
		fn   func(ctx context.Context) error
	}{
		{"data_backfills_complete", options.RequireBackfills},
		{"cluster_compatible", options.RequireCluster},
		// The maintenance window precedes backup evidence so a refused run
		// does not produce a backup the operator never asked for.
		{"maintenance_window", options.RequireWindow},
		{"backup_evidence", options.RequireBackup},
	}
	for _, check := range checks {
		if check.fn == nil {
			continue
		}
		if err := check.fn(ctx); err != nil {
			return newError(ErrCodeContractPrecondition, 0, fmt.Errorf("%s: %w", check.name, err))
		}
	}
	return nil
}
