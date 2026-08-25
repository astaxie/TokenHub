package server

import (
	"time"

	"tokenhub/backend/internal/billing"
	"tokenhub/backend/internal/reconciliation"
	reconciliationpersistence "tokenhub/backend/internal/reconciliation/persistence"
)

// unavailableBillingComposition keeps Store decorators that only implement the
// historical Store contract constructible. Billing routes report an explicit
// dependency error until the composition root supplies billing capabilities.
type unavailableBillingComposition struct{}

// ApplicationDependenciesForStore exposes the persistence adapters selected
// by the composition root. It accepts the concrete adapter rather than Store,
// so domain capabilities cannot leak back into the global Store interface.
func ApplicationDependenciesForStore(store *GormStore) ApplicationDependencies {
	if store == nil {
		return ApplicationDependencies{}
	}
	return ApplicationDependencies{
		Repository:           store.billingRepository,
		ReconciliationReader: store.billingPersistence,
		ReconciliationStore:  store.reconciliationPersistence,
	}
}

// applicationDependenciesForCompatibility preserves NewWithConfig behavior
// for existing in-process callers. Deployed binaries use explicit injection.
func applicationDependenciesForCompatibility(store Store) ApplicationDependencies {
	gormStore, ok := store.(*GormStore)
	if !ok {
		return ApplicationDependencies{}
	}
	return ApplicationDependenciesForStore(gormStore)
}

func normalizeApplicationDependencies(dependencies ApplicationDependencies) ApplicationDependencies {
	if dependencies.Repository == nil {
		dependencies.Repository = unavailableBillingComposition{}
	}
	if dependencies.ReconciliationReader == nil {
		dependencies.ReconciliationReader = unavailableBillingComposition{}
	}
	if dependencies.ReconciliationStore == nil {
		dependencies.ReconciliationStore = unavailableReconciliationComposition{}
	}
	return dependencies
}

func (unavailableBillingComposition) unavailable() error {
	return billing.NewError(billing.ErrorUpstream, "billing_repository_unavailable", "Billing persistence is unavailable")
}

func (u unavailableBillingComposition) GetBillingConnector(string, bool) (billing.Connector, error) {
	return billing.Connector{}, u.unavailable()
}

func (u unavailableBillingComposition) StartBillingSyncRun(billing.SyncRun) (billing.SyncRun, error) {
	return billing.SyncRun{}, u.unavailable()
}

func (u unavailableBillingComposition) SaveBillingPage(string, string, []billing.Record) (int, int, error) {
	return 0, 0, u.unavailable()
}

func (u unavailableBillingComposition) FinishBillingSyncRun(billing.SyncRun) (billing.SyncRun, error) {
	return billing.SyncRun{}, u.unavailable()
}

func (unavailableBillingComposition) ListDueBillingConnectors(time.Time, int) []billing.Connector {
	return nil
}

func (unavailableBillingComposition) RecordScheduledBillingAudit(billing.SyncRun) {}

func (u unavailableBillingComposition) CreateBillingConnector(billing.Connector) (billing.Connector, error) {
	return billing.Connector{}, u.unavailable()
}

func (unavailableBillingComposition) ListBillingConnectors() []billing.Connector { return nil }

func (u unavailableBillingComposition) UpdateBillingConnector(string, billing.Connector) (billing.Connector, error) {
	return billing.Connector{}, u.unavailable()
}

func (u unavailableBillingComposition) DeleteBillingConnector(string) error { return u.unavailable() }

func (unavailableBillingComposition) ListBillingRecords(string, int) []billing.Record { return nil }

func (unavailableBillingComposition) ListBillingSyncRuns(string, int) []billing.SyncRun { return nil }

func (u unavailableBillingComposition) ListBillingRecordsInRange(string, time.Time, time.Time) ([]billing.Record, error) {
	return nil, u.unavailable()
}

var _ billing.Repository = unavailableBillingComposition{}
var _ reconciliationpersistence.BillingSource = unavailableBillingComposition{}

type unavailableReconciliationComposition struct{}

func (u unavailableReconciliationComposition) unavailable() error {
	return reconciliation.NewError(reconciliation.ErrorUnavailable, "reconciliation_store_unavailable", "Reconciliation persistence is unavailable")
}
func (u unavailableReconciliationComposition) CreateRule(reconciliation.Rule) (reconciliation.Rule, error) {
	return reconciliation.Rule{}, u.unavailable()
}
func (u unavailableReconciliationComposition) GetRule(string) (reconciliation.Rule, error) {
	return reconciliation.Rule{}, u.unavailable()
}
func (u unavailableReconciliationComposition) UpdateRule(reconciliation.Rule) (reconciliation.Rule, error) {
	return reconciliation.Rule{}, u.unavailable()
}
func (u unavailableReconciliationComposition) BackfillRuleConnectorSnapshot(reconciliation.Rule) (reconciliation.Rule, error) {
	return reconciliation.Rule{}, u.unavailable()
}
func (unavailableReconciliationComposition) ListDueRules(time.Time, int) []reconciliation.Rule {
	return nil
}
func (u unavailableReconciliationComposition) ListUsages(time.Time, time.Time, time.Duration) ([]reconciliation.Usage, error) {
	return nil, u.unavailable()
}
func (u unavailableReconciliationComposition) SaveRun(reconciliation.Run, []reconciliation.Item) (reconciliation.Run, error) {
	return reconciliation.Run{}, u.unavailable()
}
func (u unavailableReconciliationComposition) ReplaceRun(reconciliation.Run, []reconciliation.Item) (reconciliation.Run, error) {
	return reconciliation.Run{}, u.unavailable()
}
func (u unavailableReconciliationComposition) GetRun(string) (reconciliation.Run, error) {
	return reconciliation.Run{}, u.unavailable()
}
func (unavailableReconciliationComposition) RecordScheduledAudit(reconciliation.Run)   {}
func (unavailableReconciliationComposition) ListRules() []reconciliation.Rule          { return nil }
func (unavailableReconciliationComposition) ListRuns(string, int) []reconciliation.Run { return nil }
func (unavailableReconciliationComposition) ListItems(string, string, int, int) ([]reconciliation.Item, int64) {
	return nil, 0
}
func (unavailableReconciliationComposition) ListItemBatch(string, string, string, bool, int) []reconciliation.Item {
	return nil
}
func (u unavailableReconciliationComposition) SaveRunLock(reconciliation.Run) (reconciliation.Run, error) {
	return reconciliation.Run{}, u.unavailable()
}

var _ reconciliation.Store = unavailableReconciliationComposition{}
