package server

import (
	"time"

	"tokenhub/backend/internal/billing"
)

// unavailableBillingComposition keeps Store decorators that only implement the
// historical Store contract constructible. Billing routes report an explicit
// dependency error until the composition root supplies billing capabilities.
type unavailableBillingComposition struct{}

type billingCompositionSource interface {
	BillingRepositoryForComposition() billing.Repository
	BillingReaderForComposition() ReconciliationBillingReader
}

func billingDependenciesFromStore(store Store) BillingDependencies {
	composition, ok := store.(billingCompositionSource)
	if !ok {
		return BillingDependencies{}
	}
	return BillingDependencies{
		Repository:           composition.BillingRepositoryForComposition(),
		ReconciliationReader: composition.BillingReaderForComposition(),
	}
}

func normalizeBillingDependencies(dependencies BillingDependencies) BillingDependencies {
	if dependencies.Repository == nil {
		dependencies.Repository = unavailableBillingComposition{}
	}
	if dependencies.ReconciliationReader == nil {
		dependencies.ReconciliationReader = unavailableBillingComposition{}
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
var _ ReconciliationBillingReader = unavailableBillingComposition{}
