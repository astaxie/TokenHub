package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReconciliationRunClassifiesLocksAndExportsSafely(t *testing.T) {
	store := NewMemoryStore()
	connector := createReconciliationTestConnector(t, store, "bcon_reconciliation_detail")
	periodStart := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	accountID := "account-sensitive-77"
	createReconciliationBillingRecords(t, store, []BillingRecord{
		{ID: "bill_match", ConnectorID: connector.ID, ExternalID: "external-match", SourceType: BillingConnectorOneAPI, AccountID: accountID, Model: "model-a", Currency: "USD", NetAmount: "1", UsageStartAt: periodStart.Add(10 * time.Minute), UsageEndAt: periodStart.Add(11 * time.Minute), ExternalRequestID: "req-match"},
		{ID: "bill_mismatch", ConnectorID: connector.ID, ExternalID: "external-mismatch", SourceType: BillingConnectorOneAPI, AccountID: accountID, Model: "model-a", Currency: "USD", NetAmount: "2", UsageStartAt: periodStart.Add(20 * time.Minute), UsageEndAt: periodStart.Add(21 * time.Minute), ExternalRequestID: "req-mismatch"},
		{ID: "bill_provider_only", ConnectorID: connector.ID, ExternalID: "external-provider", SourceType: BillingConnectorOneAPI, AccountID: accountID, Model: "model-a", Currency: "USD", NetAmount: "3", UsageStartAt: periodStart.Add(30 * time.Minute), UsageEndAt: periodStart.Add(31 * time.Minute), ExternalRequestID: "req-provider-only"},
	})
	createReconciliationUsageRecords(t, store, []UsageRecord{
		{ID: "use_match", RequestID: "req-match", ProjectID: "project-a", ProviderResourceID: accountID, ModelName: "model-a", ProviderCostUSD: 1.004, CreatedAt: periodStart.Add(10 * time.Minute)},
		{ID: "use_mismatch", RequestID: "req-mismatch", ProjectID: "project-a", ProviderResourceID: accountID, ModelName: "model-a", ProviderCostUSD: 2.5, CreatedAt: periodStart.Add(20 * time.Minute)},
		{ID: "use_tokenhub_only", RequestID: "req-tokenhub-only", ProjectID: "project-a", ProviderResourceID: accountID, ModelName: "model-a", ProviderCostUSD: 4, CreatedAt: periodStart.Add(40 * time.Minute)},
	})

	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "reconciliation-test-secret"})
	app := server.Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules", map[string]any{
		"name":                "Request reconciliation",
		"connector_id":        connector.ID,
		"granularity":         ReconciliationGranularityDetail,
		"match_dimensions":    []string{"request_id", "resource_account", "model", "currency"},
		"amount_tolerance":    "0.01",
		"ratio_tolerance":     "0",
		"time_window_minutes": 15,
		"timezone":            "UTC",
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create reconciliation rule: %d %s", created.Code, created.Body)
	}
	var rule ReconciliationRule
	if err := json.Unmarshal([]byte(created.Body), &rule); err != nil {
		t.Fatal(err)
	}
	if rule.Version != 1 || !strings.HasPrefix(rule.RuleHash, "sha256:") || rule.CreatedBy == "" {
		t.Fatalf("rule version and audit identity were not saved: %#v", rule)
	}

	run := runReconciliationTestRule(t, app, rule.ID, periodStart, periodStart.Add(time.Hour))
	assertReconciliationCounts(t, run, 1, 1, 1, 1)
	if run.ProviderAmount != "6" || run.TokenHubAmount != "7.504" || run.DifferenceAmount != "-1.504" || !strings.HasPrefix(run.InputHash, "sha256:") {
		t.Fatalf("unexpected reconciliation totals or fingerprint: %#v", run)
	}

	detail := getReconciliationTestDetail(t, app, run.ID)
	if len(detail.Items) != 4 {
		t.Fatalf("expected four reconciliation buckets, got %#v", detail.Items)
	}
	serialized, _ := json.Marshal(detail)
	if strings.Contains(string(serialized), accountID) || !strings.Contains(string(serialized), "ac****77") {
		t.Fatalf("resource account was not masked in API detail: %s", serialized)
	}
	statuses := map[string]int{}
	for _, item := range detail.Items {
		statuses[item.Status]++
		if item.Status == ReconciliationMatched && (item.ProviderAmount != "1" || item.TokenHubAmount != "1.004") {
			t.Fatalf("absolute tolerance did not preserve source amounts: %#v", item)
		}
	}
	for _, status := range []string{ReconciliationMatched, ReconciliationProviderOnly, ReconciliationTokenHubOnly, ReconciliationAmountMismatch} {
		if statuses[status] != 1 {
			t.Fatalf("expected one %s result, got %#v", status, statuses)
		}
	}

	recalculated := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliations/"+run.ID+"/recalculate", map[string]any{}, "")
	if recalculated.Code != http.StatusOK {
		t.Fatalf("recalculate reconciliation: %d %s", recalculated.Code, recalculated.Body)
	}
	var repeated ReconciliationRun
	if err := json.Unmarshal([]byte(recalculated.Body), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.InputHash != run.InputHash || repeated.RuleHash != run.RuleHash {
		t.Fatalf("same inputs and rule were not reproducible: before=%#v after=%#v", run, repeated)
	}
	bulkItems := make([]ReconciliationItem, 5001)
	for index := range bulkItems {
		bulkItems[index] = ReconciliationItem{
			ID: fmt.Sprintf("recitem_bulk_%04d", index), RunID: run.ID, MatchKey: fmt.Sprintf("bulk_%04d", index),
			Status: ReconciliationProviderOnly, BucketStart: periodStart, BucketEnd: periodStart, Currency: "USD",
			ProviderAmount: "1", TokenHubAmount: "0", DifferenceAmount: "1", DifferenceRatio: "1",
			PossibleReason: "missing_tokenhub_usage_or_late_data", ResourceAccount: accountID,
			ProviderRecordIDs: []string{fmt.Sprintf("bill_bulk_%04d", index)}, CreatedAt: time.Now().UTC(),
		}
	}
	bulkItems[len(bulkItems)-1].Project = "last-exported-difference"
	if err := store.db.CreateInBatches(&bulkItems, 200).Error; err != nil {
		t.Fatal(err)
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/admin/billing/reconciliations/"+run.ID+"/export", nil)
	exportRequest.Header.Set("Authorization", "Bearer dev_admin_token")
	exportResponse := httptest.NewRecorder()
	app.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK || !strings.Contains(exportResponse.Header().Get("Content-Disposition"), run.ID) {
		t.Fatalf("export reconciliation: %d %s", exportResponse.Code, exportResponse.Body)
	}
	if strings.Contains(exportResponse.Body.String(), accountID) || strings.Contains(exportResponse.Body.String(), "reconciliation-test-secret") || !strings.Contains(exportResponse.Body.String(), "ac****77") {
		t.Fatalf("export leaked protected data or omitted masking: %s", exportResponse.Body)
	}
	if !strings.Contains(exportResponse.Body.String(), "last-exported-difference") || strings.Contains(exportResponse.Body.String(), "\nmatched,") {
		t.Fatalf("difference export was truncated or included matched rows")
	}

	locked := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliations/"+run.ID+"/lock", map[string]any{}, "")
	if locked.Code != http.StatusOK || !strings.Contains(locked.Body, `"locked_at"`) {
		t.Fatalf("lock reconciliation: %d %s", locked.Code, locked.Body)
	}
	blocked := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliations/"+run.ID+"/recalculate", map[string]any{}, "")
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body, "reconciliation_run_locked") {
		t.Fatalf("locked run was recalculated: %d %s", blocked.Code, blocked.Body)
	}
	var audited int
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType == "reconciliation_rule" || event.ResourceType == "billing_reconciliation" {
			audited++
			if strings.Contains(event.BeforeSnapshot+event.AfterSnapshot, accountID) {
				t.Fatalf("sensitive account leaked into audit event: %#v", event)
			}
		}
	}
	if audited < 5 {
		t.Fatalf("expected create, run, recalculate, export, and lock audit events, got %d", audited)
	}
}

func TestReconciliationAggregatesPreciseAmountsByCurrencyAndTimezone(t *testing.T) {
	store := NewMemoryStore()
	connector := createReconciliationTestConnector(t, store, "bcon_reconciliation_daily")
	periodStart := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	usageTime := time.Date(2026, time.July, 1, 16, 30, 0, 0, time.UTC)
	createReconciliationBillingRecords(t, store, []BillingRecord{
		{ID: "bill_decimal_one", ConnectorID: connector.ID, ExternalID: "decimal-one", SourceType: BillingConnectorOneAPI, Model: "model-money", Currency: "USD", NetAmount: "0.1", UsageStartAt: usageTime, UsageEndAt: usageTime, CreatedAt: periodStart.Add(72 * time.Hour)},
		{ID: "bill_decimal_two", ConnectorID: connector.ID, ExternalID: "decimal-two", SourceType: BillingConnectorOneAPI, Model: "model-money", Currency: "USD", NetAmount: "0.2", UsageStartAt: usageTime.Add(time.Minute), UsageEndAt: usageTime.Add(time.Minute)},
		{ID: "bill_cny", ConnectorID: connector.ID, ExternalID: "currency-cny", SourceType: BillingConnectorAliyun, Model: "model-money", Currency: "CNY", NetAmount: "1", UsageStartAt: usageTime, UsageEndAt: usageTime},
	})
	createReconciliationUsageRecords(t, store, []UsageRecord{
		{ID: "use_decimal", RequestID: "req-decimal", ModelName: "model-money", ProviderCostUSD: 0.3, CreatedAt: usageTime},
	})
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules", map[string]any{
		"name":             "Daily currency reconciliation",
		"connector_id":     connector.ID,
		"granularity":      ReconciliationGranularityDay,
		"match_dimensions": []string{"model", "currency"},
		"amount_tolerance": "0",
		"ratio_tolerance":  "0",
		"timezone":         "Asia/Shanghai",
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create aggregate rule: %d %s", created.Code, created.Body)
	}
	var rule ReconciliationRule
	if err := json.Unmarshal([]byte(created.Body), &rule); err != nil {
		t.Fatal(err)
	}
	run := runReconciliationTestRule(t, app, rule.ID, periodStart, periodStart.Add(48*time.Hour))
	assertReconciliationCounts(t, run, 1, 0, 0, 0)
	if run.ProviderRecordCount != 2 || run.ProviderAmount != "0.3" || run.TokenHubAmount != "0.3" {
		t.Fatalf("late bill, currencies, or exact decimal sum was wrong: %#v", run)
	}
	detail := getReconciliationTestDetail(t, app, run.ID)
	for _, item := range detail.Items {
		switch item.Currency {
		case "USD":
			if item.Status != ReconciliationMatched || item.ProviderAmount != "0.3" || item.TokenHubAmount != "0.3" {
				t.Fatalf("0.1 + 0.2 was not reconciled exactly: %#v", item)
			}
			expectedBucket := time.Date(2026, time.July, 1, 16, 0, 0, 0, time.UTC)
			if !item.BucketStart.Equal(expectedBucket) {
				t.Fatalf("Asia/Shanghai day boundary was not applied: got %s want %s", item.BucketStart, expectedBucket)
			}
		default:
			t.Fatalf("unexpected currency bucket: %#v", item)
		}
	}
	cnyRuleResponse := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules", map[string]any{
		"name": "Daily CNY reconciliation", "connector_id": connector.ID, "granularity": ReconciliationGranularityDay,
		"match_dimensions": []string{"model", "currency"}, "timezone": "Asia/Shanghai", "currency": "CNY", "usd_exchange_rate": "3.333333", "amount_tolerance": "0.000001",
	}, "")
	if cnyRuleResponse.Code != http.StatusCreated {
		t.Fatalf("create CNY reconciliation rule: %d %s", cnyRuleResponse.Code, cnyRuleResponse.Body)
	}
	var cnyRule ReconciliationRule
	if err := json.Unmarshal([]byte(cnyRuleResponse.Body), &cnyRule); err != nil {
		t.Fatal(err)
	}
	cnyRun := runReconciliationTestRule(t, app, cnyRule.ID, periodStart, periodStart.Add(48*time.Hour))
	assertReconciliationCounts(t, cnyRun, 1, 0, 0, 0)
	if cnyRun.ProviderAmount != "1" || cnyRun.TokenHubAmount != "1" || cnyRun.USDExchangeRate != "3.333333" {
		t.Fatalf("CNY cost was not converted with the configured exchange rate: %#v", cnyRun)
	}
}

func TestDetailReconciliationMapsDimensionsAndMatchesPeriodBoundariesOneToOne(t *testing.T) {
	store := NewMemoryStore()
	connector := createReconciliationTestConnector(t, store, "bcon_reconciliation_mapping")
	connector.Config = map[string]string{"provider_id": "provider-local", "provider_resource_id": "resource-local"}
	if err := store.db.Save(&connector).Error; err != nil {
		t.Fatal(err)
	}
	periodStart := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	metadata := map[string]string{
		"provider_id": "external-provider", "provider_resource_id": "external-account", "project_id": "external-project",
	}
	createReconciliationBillingRecords(t, store, []BillingRecord{
		{ID: "bill_boundary", ConnectorID: connector.ID, ExternalID: "external-boundary", SourceType: BillingConnectorOneAPI, Model: "external-model", Currency: "USD", NetAmount: "1", UsageStartAt: periodStart.Add(time.Minute), UsageEndAt: periodStart.Add(time.Minute), ExternalRequestID: "req-boundary", Metadata: metadata},
		{ID: "bill_near", ConnectorID: connector.ID, ExternalID: "external-near", SourceType: BillingConnectorOneAPI, Model: "external-model", Currency: "USD", NetAmount: "2", UsageStartAt: periodStart.Add(20 * time.Minute), UsageEndAt: periodStart.Add(20 * time.Minute), ExternalRequestID: "req-duplicate", Metadata: metadata},
		{ID: "bill_far", ConnectorID: connector.ID, ExternalID: "external-far", SourceType: BillingConnectorOneAPI, Model: "external-model", Currency: "USD", NetAmount: "3", UsageStartAt: periodStart.Add(40 * time.Minute), UsageEndAt: periodStart.Add(40 * time.Minute), ExternalRequestID: "req-duplicate", Metadata: metadata},
	})
	createReconciliationUsageRecords(t, store, []UsageRecord{
		{ID: "use_boundary", RequestID: "req-boundary", ProviderID: "provider-local", ProviderResourceID: "resource-local", ProjectID: "project-local", ModelName: "model-local", ProviderCostUSD: 1, CreatedAt: periodStart.Add(-2 * time.Minute)},
		{ID: "use_near", RequestID: "req-duplicate", ProviderID: "provider-local", ProviderResourceID: "resource-local", ProjectID: "project-local", ModelName: "model-local", ProviderCostUSD: 2, CreatedAt: periodStart.Add(21 * time.Minute)},
	})
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules", map[string]any{
		"name": "Mapped detail reconciliation", "connector_id": connector.ID, "granularity": ReconciliationGranularityDetail,
		"match_dimensions":    []string{"request_id", "provider", "resource_account", "model", "project", "currency"},
		"time_window_minutes": 5, "currency": "USD",
		"dimension_mappings": map[string]map[string]string{
			"provider": {"external-provider": "provider-local"}, "resource_account": {"external-account": "resource-local"},
			"model": {"external-model": "model-local"}, "project": {"external-project": "project-local"},
		},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create mapped reconciliation rule: %d %s", created.Code, created.Body)
	}
	var rule ReconciliationRule
	if err := json.Unmarshal([]byte(created.Body), &rule); err != nil {
		t.Fatal(err)
	}
	run := runReconciliationTestRule(t, app, rule.ID, periodStart, periodStart.Add(time.Hour))
	assertReconciliationCounts(t, run, 2, 1, 0, 0)
	if run.ProviderAmount != "6" || run.TokenHubAmount != "3" || run.TokenHubRecordCount != 2 || len(run.DimensionMappings) != 3 {
		t.Fatalf("mapped or boundary inputs were not captured correctly: %#v", run)
	}
	storedRun, err := store.GetReconciliationRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedRun.DimensionMappings) != 4 || storedRun.DimensionMappings["resource_account"]["external-account"] != "resource-local" {
		t.Fatalf("resource account mapping was not retained in the run snapshot: %#v", storedRun.DimensionMappings)
	}
	detail := getReconciliationTestDetail(t, app, run.ID)
	var sawBoundary, sawFar bool
	for _, item := range detail.Items {
		if item.RequestID == "req-boundary" && item.Status == ReconciliationMatched {
			sawBoundary = item.Provider == "provider-local" && item.ResourceAccountMasked == "re****al" && item.Model == "model-local" && item.Project == "project-local" &&
				len(item.ProviderRecordIDs) == 1 && item.ProviderRecordIDs[0] == "bill_boundary" && len(item.TokenHubRecordIDs) == 1 && item.TokenHubRecordIDs[0] == "use_boundary"
		}
		if len(item.ProviderRecordIDs) == 1 && item.ProviderRecordIDs[0] == "bill_far" {
			sawFar = item.Status == ReconciliationProviderOnly && item.PossibleReason == "outside_time_window"
		}
	}
	if !sawBoundary || !sawFar {
		t.Fatalf("boundary mapping or one-to-one time matching was wrong: %#v", detail.Items)
	}
}

func TestScheduledReconciliationRespectsBillingDelay(t *testing.T) {
	store := NewMemoryStore()
	connector := createReconciliationTestConnector(t, store, "bcon_reconciliation_scheduled")
	service := newReconciliationService(store)
	rule, err := service.CreateRule(ReconciliationRuleRequest{
		Name: "Scheduled daily reconciliation", ConnectorID: connector.ID, Granularity: ReconciliationGranularityDay,
		MatchDimensions: []string{"model", "currency"}, BillingDelayMinutes: 120, ScheduleIntervalMinutes: 30, Timezone: "Asia/Shanghai",
	}, "admin-scheduler")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	if err := store.db.Model(&ReconciliationRule{}).Where("id = ?", rule.ID).Update("next_run_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	runs := service.RunDue(t.Context(), now)
	if len(runs) != 1 || runs[0].Status != ReconciliationRunSucceeded || runs[0].Trigger != "scheduled" {
		t.Fatalf("expected one successful scheduled run, got %#v", runs)
	}
	expectedEnd := time.Date(2026, time.August, 1, 16, 0, 0, 0, time.UTC)
	if !runs[0].PeriodEnd.Equal(expectedEnd) || runs[0].BillingDelayMinutes != 120 {
		t.Fatalf("billing delay and timezone did not define the finalized day: %#v", runs[0])
	}
	if repeated := service.RunDue(t.Context(), now); len(repeated) != 0 {
		t.Fatalf("scheduled rule ran twice before its next interval: %#v", repeated)
	}
	var scheduledAudit bool
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType == "billing_reconciliation" && event.ActorUserID == "system" {
			scheduledAudit = true
		}
	}
	if !scheduledAudit {
		t.Fatal("scheduled reconciliation was not audited")
	}
}

func createReconciliationTestConnector(t *testing.T, store *GormStore, id string) BillingConnector {
	t.Helper()
	connector := BillingConnector{ID: id, Name: id, Type: BillingConnectorOneAPI, BaseURL: "https://billing.example.test", Status: StatusActive, Config: map[string]string{"provider_id": BillingConnectorOneAPI}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.db.Create(&connector).Error; err != nil {
		t.Fatal(err)
	}
	return connector
}

func createReconciliationBillingRecords(t *testing.T, store *GormStore, records []BillingRecord) {
	t.Helper()
	for index := range records {
		if records[index].GrossAmount == "" {
			records[index].GrossAmount = records[index].NetAmount
		}
		if records[index].SourceTimezone == "" {
			records[index].SourceTimezone = "UTC"
		}
		if records[index].BillingPeriod == "" {
			records[index].BillingPeriod = records[index].UsageStartAt.Format("2006-01")
		}
		if records[index].CreatedAt.IsZero() {
			records[index].CreatedAt = time.Now().UTC()
		}
		records[index].UpdatedAt = records[index].CreatedAt
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
}

func createReconciliationUsageRecords(t *testing.T, store *GormStore, records []UsageRecord) {
	t.Helper()
	for index := range records {
		if records[index].ProviderID == "" {
			records[index].ProviderID = BillingConnectorOneAPI
		}
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
}

func runReconciliationTestRule(t *testing.T, app http.Handler, ruleID string, from time.Time, to time.Time) ReconciliationRun {
	t.Helper()
	response := doJSON(t, app, http.MethodPost, "/api/admin/billing/reconciliation-rules/"+ruleID+"/run", map[string]any{
		"period_start": from.Format(time.RFC3339), "period_end": to.Format(time.RFC3339),
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("run reconciliation: %d %s", response.Code, response.Body)
	}
	var run ReconciliationRun
	if err := json.Unmarshal([]byte(response.Body), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func getReconciliationTestDetail(t *testing.T, app http.Handler, runID string) ReconciliationDetail {
	t.Helper()
	response := doJSON(t, app, http.MethodGet, "/api/admin/billing/reconciliations/"+runID, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("get reconciliation detail: %d %s", response.Code, response.Body)
	}
	var detail ReconciliationDetail
	if err := json.Unmarshal([]byte(response.Body), &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}

func assertReconciliationCounts(t *testing.T, run ReconciliationRun, matched int, providerOnly int, tokenHubOnly int, mismatch int) {
	t.Helper()
	if run.MatchedCount != matched || run.ProviderOnlyCount != providerOnly || run.TokenHubOnlyCount != tokenHubOnly || run.AmountMismatchCount != mismatch {
		t.Fatalf("unexpected reconciliation counts: %#v", run)
	}
}
