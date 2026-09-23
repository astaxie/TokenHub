package dbschema

import (
	"context"
	"errors"
	"testing"
)

func TestFreshDatabaseAdoptsFromBaselineSQL(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	registry := []Migration{
		{Version: 2, Name: "post-baseline", Statements: []string{"CREATE TABLE post_baseline (id INTEGER PRIMARY KEY)"}},
	}
	runner := mustRunner(t, db, registry, WithFreshBaseline([]string{
		"CREATE TABLE baseline_demo (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)",
	}))
	// A failing legacy callback proves the fresh path never invokes it.
	result, err := runner.Adopt(ctx, func(context.Context) error {
		return errors.New("legacy flow must not run on a fresh database")
	})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !result.Adopted {
		t.Fatal("expected fresh adoption from baseline SQL")
	}
	if !tableExists(t, db, "baseline_demo") || !tableExists(t, db, "post_baseline") {
		t.Fatal("expected baseline and post-baseline tables to exist")
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.CurrentVersion != 2 || status.Dirty || !status.BaselineRecorded {
		t.Fatalf("unexpected status after fresh adoption: %+v", status)
	}
}

func TestLegacyDatabaseRunsFrozenFlowInsteadOfBaselineSQL(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE legacy_business (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	legacyRan := false
	runner := mustRunner(t, db, nil, WithFreshBaseline([]string{
		"CREATE TABLE fresh_only (id INTEGER PRIMARY KEY)",
	}))
	result, err := runner.Adopt(ctx, func(context.Context) error {
		legacyRan = true
		return nil
	})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !result.Adopted || !legacyRan {
		t.Fatalf("expected legacy flow adoption, adopted=%t legacyRan=%t", result.Adopted, legacyRan)
	}
	if tableExists(t, db, "fresh_only") {
		t.Fatal("fresh baseline SQL must not run on a database with business tables")
	}
	if !tableExists(t, db, "legacy_business") {
		t.Fatal("legacy business table must survive adoption")
	}
}
