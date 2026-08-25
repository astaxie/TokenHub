//go:build integration

package server

import "testing"

func TestReconciliationSnapshotBackfillIsAtomicAcrossPostgresInstances(t *testing.T) {
	storeA, storeB, _ := openSharedPostgresStores(t)
	assertReconciliationSnapshotBackfillAtomic(t, storeA, storeB, NewID("reconciliation_legacy"))
}
