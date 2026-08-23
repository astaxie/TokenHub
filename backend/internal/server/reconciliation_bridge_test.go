package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tokenhub/backend/internal/billing"
	"tokenhub/backend/internal/reconciliation"
)

func TestReconciliationBridgePreservesHTTPError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		original := NewHTTPError(status, "bridge_failure", "Bridge failure")
		original.Details = map[string]string{"field": "value"}
		original.UpstreamStatus = http.StatusBadGateway
		original.Headers = map[string]string{"Retry-After": "5"}
		domainErr := domainReconciliationStoreError(original)
		kind, _, _, classified := reconciliation.ErrorInfo(domainErr)
		switch status {
		case http.StatusBadRequest:
			if !classified || kind != reconciliation.ErrorInvalidInput {
				t.Fatalf("status %d was classified as %q", status, kind)
			}
		case http.StatusNotFound:
			if !classified || kind != reconciliation.ErrorNotFound {
				t.Fatalf("status %d was classified as %q", status, kind)
			}
		case http.StatusConflict:
			if !classified || kind != reconciliation.ErrorConflict {
				t.Fatalf("status %d was classified as %q", status, kind)
			}
		default:
			if classified || domainErr != original {
				t.Fatalf("status %d crossed the domain boundary with a false classification", status)
			}
		}
		mapped := reconciliationHTTPError(domainErr)
		var got *HTTPError
		if !errors.As(mapped, &got) || got != original {
			t.Fatalf("status %d did not preserve the original HTTP error: %#v", status, mapped)
		}
		if got.Status != status || got.UpstreamStatus != original.UpstreamStatus || got.Headers["Retry-After"] != "5" || got.Details == nil {
			t.Fatalf("status %d lost HTTP error metadata: %#v", status, got)
		}
	}
}

func TestReconciliationBridgePreservesBaselineBillingErrorSemantics(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "not found", err: billing.NewError(billing.ErrorNotFound, "billing_connector_not_found", "Billing connector not found")},
		{name: "upstream", err: billing.NewError(billing.ErrorUpstream, "billing_records_unavailable", "Billing records are unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := reconciliationHTTPError(test.err)
			got := AsHTTPError(mapped)
			if mapped != test.err || got.Status != http.StatusInternalServerError || got.Code != "internal_error" || got.Message != test.err.Error() {
				t.Fatalf("billing error compatibility mismatch: mapped=%#v http=%#v", mapped, got)
			}
		})
	}
}

func TestReconciliationHTTPPreservesBaselineBillingErrorSemantics(t *testing.T) {
	app := New(NewMemoryStore()).Handler()
	response := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules", map[string]any{
		"name": "Missing connector", "connector_id": "missing-connector",
	}, "")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body, `"code":"internal_error"`) || strings.Contains(response.Body, "billing_connector_not_found") {
		t.Fatalf("reconciliation billing error semantics changed: %d %s", response.Code, response.Body)
	}
}

func TestReconciliationBillingBridgeReturnsNarrowProjections(t *testing.T) {
	reader := projectionBillingReader{
		connector: billing.Connector{
			ID: "connector", Type: billing.ConnectorOneAPI,
			Config:               map[string]string{"provider_id": "provider-a", "provider_resource_id": "resource-a"},
			CredentialCiphertext: "ciphertext", Credentials: map[string]string{"token": "secret"},
		},
		records: []billing.Record{{
			ID: "record", ExternalID: "external", SourceType: "source", AccountID: "account",
			Model: "model", Currency: "USD", NetAmount: "1", UsageStartAt: time.Now().UTC(),
			ExternalRequestID: "request", Metadata: map[string]string{
				"provider_id": "provider-a", "provider_resource_id": "resource-a", "resource_id": "legacy-resource",
				"project_id": "project-a", "username": "sensitive-user", "token_name": "sensitive-token",
			},
			RawPayload: "sensitive raw payload",
		}},
	}
	bridge := &reconciliationBillingBridge{reader: reader}
	connector, err := bridge.GetConnectorSnapshot("connector")
	if err != nil {
		t.Fatal(err)
	}
	if connector.Type != billing.ConnectorOneAPI || connector.ProviderID != "provider-a" || connector.ProviderResourceID != "resource-a" {
		t.Fatalf("connector projection mismatch: %#v", connector)
	}
	records, err := bridge.ListRecordsInRange("connector", time.Time{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "record" || records[0].ExternalRequestID != "request" || records[0].NetAmount != "1" ||
		records[0].ProviderID != "provider-a" || records[0].ProviderResourceID != "resource-a" || records[0].ResourceID != "legacy-resource" || records[0].ProjectID != "project-a" {
		t.Fatalf("record projection mismatch: %#v", records)
	}
	serialized, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "sensitive-user") || strings.Contains(string(serialized), "sensitive-token") || strings.Contains(string(serialized), "sensitive raw payload") {
		t.Fatalf("unrelated billing data crossed the reconciliation boundary: %s", serialized)
	}
}

func TestReconciliationSnapshotBackfillIsAtomicAcrossStoreInstances(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "reconciliation-backfill.db")
	storeA, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*GormStore{storeA, storeB} {
		database, databaseErr := store.db.DB()
		if databaseErr != nil {
			t.Fatal(databaseErr)
		}
		t.Cleanup(func() { _ = database.Close() })
	}
	assertReconciliationSnapshotBackfillAtomic(t, storeA, storeB, "rule-legacy")
}

func assertReconciliationSnapshotBackfillAtomic(t *testing.T, storeA *GormStore, storeB *GormStore, ruleID string) {
	t.Helper()
	legacy := ReconciliationRule{ID: ruleID, Name: "Legacy", ConnectorID: "connector", Status: StatusActive}
	if err := storeA.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.db.Delete(&ReconciliationRule{}, "id = ?", legacy.ID).Error })
	if err := storeA.db.Exec("UPDATE reconciliation_rules SET connector_type = NULL, provider_id = NULL WHERE id = ?", legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	candidates := []ReconciliationRule{
		{ID: legacy.ID, ConnectorType: billing.ConnectorOneAPI, ProviderID: "provider-a", Version: 2, RuleHash: "sha256:a"},
		{ID: legacy.ID, ConnectorType: billing.ConnectorOneAPI, ProviderID: "provider-b", Version: 2, RuleHash: "sha256:b"},
	}
	stores := []*GormStore{storeA, storeB}
	start := make(chan struct{})
	results := make(chan ReconciliationRule, len(stores))
	errorsFound := make(chan error, len(stores))
	var workers sync.WaitGroup
	for index, store := range stores {
		workers.Add(1)
		go func(store *GormStore, candidate ReconciliationRule) {
			defer workers.Done()
			<-start
			stored, backfillErr := store.BackfillReconciliationRuleConnectorSnapshot(candidate)
			results <- stored
			errorsFound <- backfillErr
		}(store, candidates[index])
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsFound)
	for backfillErr := range errorsFound {
		if backfillErr != nil {
			t.Fatal(backfillErr)
		}
	}
	persisted, err := storeA.GetReconciliationRule(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	for result := range results {
		if result.ProviderID != persisted.ProviderID || result.RuleHash != persisted.RuleHash || result.Version != persisted.Version {
			t.Fatalf("backfill caller did not observe the persisted winner: result=%#v persisted=%#v", result, persisted)
		}
	}
}

func TestReconciliationRepeatedLockDoesNotWrite(t *testing.T) {
	store := NewMemoryStore()
	lockedAt := time.Now().UTC()
	run := ReconciliationRun{ID: "run-locked", Status: ReconciliationRunSucceeded, LockedAt: &lockedAt, LockedBy: "first-actor"}
	if err := store.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`CREATE TRIGGER reject_locked_run_update
		BEFORE UPDATE ON reconciliation_runs
		BEGIN SELECT RAISE(FAIL, 'locked run must not be updated'); END`).Error; err != nil {
		t.Fatal(err)
	}
	result, err := store.LockReconciliationRun(run.ID, "second-actor")
	if err != nil {
		t.Fatalf("idempotent lock unexpectedly wrote to persistence: %v", err)
	}
	if result.LockedAt == nil || !result.LockedAt.Equal(lockedAt) || result.LockedBy != run.LockedBy {
		t.Fatalf("idempotent lock changed ownership: %#v", result)
	}
}

type projectionBillingReader struct {
	connector billing.Connector
	records   []billing.Record
}

func (r projectionBillingReader) GetBillingConnector(string, bool) (BillingConnector, error) {
	return r.connector, nil
}

func (r projectionBillingReader) ListBillingRecordsInRange(string, time.Time, time.Time) ([]BillingRecord, error) {
	return r.records, nil
}

var _ ReconciliationBillingReader = projectionBillingReader{}
