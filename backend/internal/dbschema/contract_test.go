package dbschema

import (
	"context"
	"errors"
	"testing"
)

func contractTestRegistry() []Migration {
	return []Migration{
		{Version: 2, Name: "expand", Statements: []string{"CREATE TABLE contract_demo (id INTEGER PRIMARY KEY)"}},
		{Version: 3, Name: "contract", Phase: PhaseContract, Statements: []string{"DROP TABLE contract_demo"}},
	}
}

func TestContractDryRunExecutesNothing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner = mustRunner(t, db, contractTestRegistry())
	if _, err := runner.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	result, err := runner.ApplyContract(ctx, ContractOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run ApplyContract: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("dry run must not apply anything, got %+v", result.Applied)
	}
	if !tableExists(t, db, "contract_demo") {
		t.Fatal("dry run must not drop the contract target")
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.PendingContract) != 1 {
		t.Fatalf("contract migration must stay pending after dry run, got %+v", status.PendingContract)
	}

	// The real run drops the table and records the attempt.
	result, err = runner.ApplyContract(ctx, ContractOptions{})
	if err != nil {
		t.Fatalf("ApplyContract: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].Version != 3 {
		t.Fatalf("expected contract version 3 applied, got %+v", result.Applied)
	}
	if tableExists(t, db, "contract_demo") {
		t.Fatal("contract migration did not drop its target")
	}
	if outcome := lastAttemptOutcome(t, db, 3); outcome != "success" {
		t.Fatalf("expected success attempt for contract, got %q", outcome)
	}
	// A second call finds nothing pending.
	result, err = runner.ApplyContract(ctx, ContractOptions{})
	if err != nil {
		t.Fatalf("second ApplyContract: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("expected no pending contract on second run, got %+v", result.Applied)
	}
}

func TestContractPreconditionRefusal(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, nil)
	if _, err := runner.Adopt(ctx, nil); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	runner = mustRunner(t, db, contractTestRegistry())
	if _, err := runner.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	options := ContractOptions{
		RequireBackfills: func(context.Context) error { return nil },
		RequireCluster:   func(context.Context) error { return nil },
		RequireBackup:    func(context.Context) error { return errors.New("no verified backup reference") },
	}
	_, err := runner.ApplyContract(ctx, options)
	requireErrorCode(t, err, ErrCodeContractPrecondition)
	if !tableExists(t, db, "contract_demo") {
		t.Fatal("refused contract must not touch its target")
	}
	// Dry runs surface the same refusal so operators see what blocks execution.
	_, err = runner.ApplyContract(ctx, ContractOptions{DryRun: true, RequireBackup: options.RequireBackup})
	requireErrorCode(t, err, ErrCodeContractPrecondition)
}

func TestContractRequiresAdoptedBaseline(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := mustRunner(t, db, contractTestRegistry())
	_, err := runner.ApplyContract(ctx, ContractOptions{})
	requireErrorCode(t, err, ErrCodeBaselineMissing)
}
