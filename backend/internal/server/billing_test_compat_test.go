package server

import (
	"encoding/json"
	"net/http"
	"time"

	"tokenhub/backend/internal/billing"
	billingadapters "tokenhub/backend/internal/billing/adapters"
	"tokenhub/backend/internal/billing/persistence"
)

type BillingRawSnapshot = persistence.RawSnapshotRow
type BillingFetchRequest = billing.FetchRequest
type BillingFetchPage = billing.FetchPage
type NewAPIBillingAdapter = billingadapters.NewAPIBillingAdapter
type AliyunBillingAdapter = billingadapters.AliyunBillingAdapter
type OneAPIBillingAdapter = billingadapters.OneAPIBillingAdapter

const (
	BillingSyncRunning   = billing.SyncRunning
	BillingSyncSucceeded = billing.SyncSucceeded
	BillingSyncFailed    = billing.SyncFailed
)

// These are HTTP response views used by the server-level endpoint tests.
// Billing domain models intentionally do not prescribe JSON representations.
type billingSyncRunResponse struct {
	Status          string `json:"status"`
	PagesFetched    int    `json:"pages_fetched"`
	Attempts        int    `json:"attempts"`
	RecordsInserted int    `json:"records_inserted"`
	RecordsUpdated  int    `json:"records_updated"`
}

type billingRecordResponse struct {
	Currency   string `json:"currency"`
	NetAmount  string `json:"net_amount"`
	RawPayload string `json:"raw_payload"`
}

func newBillingService(store *GormStore) *billing.Service {
	return billing.NewService(store.BillingRepositoryForComposition(), billingadapters.NewRegistry(&http.Client{Timeout: 30 * time.Second}))
}

// BillingRepository keeps legacy server characterization tests readable while
// remaining test-only; production callers must use composition-owned ports.
func (s *GormStore) BillingRepository() billing.Repository {
	return s.BillingRepositoryForComposition()
}

type billingConnectorJSON struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	Type                    string            `json:"type"`
	BaseURL                 string            `json:"base_url"`
	Status                  string            `json:"status"`
	ScheduleIntervalMinutes int               `json:"schedule_interval_minutes"`
	Config                  map[string]string `json:"config"`
	CredentialsConfigured   bool              `json:"credentials_configured"`
	CredentialFields        []string          `json:"credential_fields"`
}

func decodeBillingConnector(data string) (BillingConnector, error) {
	var value billingConnectorJSON
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return BillingConnector{}, err
	}
	return BillingConnector{ID: value.ID, Name: value.Name, Type: value.Type, BaseURL: value.BaseURL, Status: value.Status,
		ScheduleIntervalMinutes: value.ScheduleIntervalMinutes, Config: value.Config, CredentialsConfigured: value.CredentialsConfigured,
		CredentialFields: value.CredentialFields}, nil
}

func normalizeOneAPIBillingRecord(item map[string]any, connector BillingConnector) (BillingRecord, error) {
	return billingadapters.NormalizeOneAPIBillingRecord(item, connector)
}

func billingDecimalValue(value any) (string, error) {
	return billingadapters.BillingDecimalValue(value)
}
func billingDecimalAdd(values ...any) (string, error) {
	return billingadapters.BillingDecimalAdd(values...)
}
func billingDecimalRatio(numerator, denominator int64) string {
	return billingadapters.BillingDecimalRatio(numerator, denominator)
}
func billingRecordStartsInRange(record BillingRecord, from, to time.Time) bool {
	return billingadapters.BillingRecordStartsInRange(record, from, to)
}
func aliyunCanonicalQuery(values map[string][]string) string {
	return billingadapters.AliyunCanonicalQuery(values)
}
func aliyunPercentEncode(value string) string { return billingadapters.AliyunPercentEncode(value) }

var _ billing.Adapter = (*billingadapters.OneAPIBillingAdapter)(nil)
