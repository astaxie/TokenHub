package server

import (
	"testing"
	"time"
	"tokenhub/backend/internal/metering"
	"tokenhub/backend/internal/reconciliation"
)

func TestReconciliationZeroCostRequiresMatchingVersionedEvidence(t *testing.T) {
	store := NewMemoryStore()
	at := time.Now().UTC()
	attempt := meteringAttemptSnapshot{ID: "attempt", RequestID: "request", ProviderID: "provider", ResourceID: "resource", At: at}
	if err := saveMeteringEntry(store.db, attempt.ID, "attempt_prepared", "request", attempt, at); err != nil {
		t.Fatal(err)
	}
	usages := []reconciliation.Usage{{RequestID: "request", ProviderID: "provider", ProviderResourceID: "resource"}}
	if err := store.applyZeroCostEvidence(usages); err != nil || usages[0].ProviderCostKnown {
		t.Fatalf("absence became free: %v %v", usages, err)
	}
	settlement := struct {
		Attempts []meteringAttemptCharge `json:"attempts"`
	}{Attempts: []meteringAttemptCharge{{ID: "attempt", Number: 1, Charge: meteringShadowCharge{Price: &meteringPriceSnapshot{Version: "rate-card-v1"}, Charge: &metering.Charge{Currency: "USD", Amount: "0"}}}}}
	if err := saveMeteringEntry(store.db, "settlement", "shadow_settlement", "request", settlement, at); err != nil {
		t.Fatal(err)
	}
	if err := store.applyZeroCostEvidence(usages); err != nil || !usages[0].ProviderCostKnown {
		t.Fatalf("explicit zero lost: %v %v", usages, err)
	}
	usages[0].ProviderCostKnown = false
	usages[0].ProviderResourceID = "different-resource"
	if err := store.applyZeroCostEvidence(usages); err != nil || usages[0].ProviderCostKnown {
		t.Fatalf("unrelated evidence became free: %v %v", usages, err)
	}
}
