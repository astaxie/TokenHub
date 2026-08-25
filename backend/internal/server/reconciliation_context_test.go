package server

import (
	"context"
	"errors"
	"testing"

	"tokenhub/backend/internal/reconciliation"
)

func TestWithContextScopesReconciliationPersistence(t *testing.T) {
	store := NewMemoryStore()
	rule, err := ApplicationDependenciesForStore(store).ReconciliationStore.CreateRule(reconciliation.Rule{ID: "context-rule", Name: "context rule"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ApplicationDependenciesForStore(store.WithContext(ctx)).ReconciliationStore.GetRule(rule.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reconciliation adapter did not inherit the store context: %v", err)
	}
}
