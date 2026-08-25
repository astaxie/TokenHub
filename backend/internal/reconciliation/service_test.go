package reconciliation_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"tokenhub/backend/internal/billing"
	"tokenhub/backend/internal/reconciliation"
)

func TestServiceNormalizesRulesAndHashesConnectorScope(t *testing.T) {
	store := newFakeStore()
	reader := &fakeBillingReader{id: "connector-scope", connector: reconciliation.ConnectorSnapshot{
		Type: billing.ConnectorOneAPI, ProviderID: " provider-a ", ProviderResourceID: "resource-a",
	}}
	service := reconciliation.NewService(store, reader)
	rule, err := service.CreateRule(reconciliation.RuleInput{
		Name: " Scoped rule ", ConnectorID: reader.id,
		MatchDimensions: []string{"currency", "request_id", "currency"},
		DimensionMappings: map[string]map[string]string{
			"resource_account": {"external-account": "resource-a"},
		},
	}, " actor ")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Name != "Scoped rule" || rule.Status != reconciliation.StatusActive || rule.Granularity != reconciliation.GranularityDetail ||
		rule.ProviderID != "provider-a" || rule.ProviderResourceID != "resource-a" || rule.CreatedBy != "actor" ||
		len(rule.MatchDimensions) != 2 || rule.MatchDimensions[0] != "request_id" || rule.MatchDimensions[1] != "currency" {
		t.Fatalf("rule was not normalized and scoped: %#v", rule)
	}
	firstHash := rule.RuleHash
	reader.connector.ProviderID = "provider-b"
	updated, afterScopeChange, err := service.UpdateRule(rule.ID, reconciliation.RulePatch{}, "actor")
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuleHash != firstHash || afterScopeChange.ProviderID != "provider-b" || afterScopeChange.RuleHash == firstHash {
		t.Fatalf("connector scope was omitted from the rule hash: before=%#v after=%#v", updated, afterScopeChange)
	}
	changedMappings := map[string]map[string]string{"resource_account": {"external-account": "resource-b"}}
	_, afterMappingChange, err := service.UpdateRule(rule.ID, reconciliation.RulePatch{DimensionMappings: &changedMappings}, "actor")
	if err != nil {
		t.Fatal(err)
	}
	if afterMappingChange.RuleHash == afterScopeChange.RuleHash {
		t.Fatal("resource-account mapping was omitted from the rule hash")
	}
}

func TestServiceDetailRunMaximizesMatchesBeforeDistance(t *testing.T) {
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	store.usages = []reconciliation.Usage{
		{ID: "usage-minus-five", RequestID: "shared-request", ModelName: "model-a", ProviderID: "provider-a", ProviderCostUSD: 1, CreatedAt: base.Add(-5 * time.Minute)},
		{ID: "usage-four", RequestID: "shared-request", ModelName: "model-a", ProviderID: "provider-a", ProviderCostUSD: 1, CreatedAt: base.Add(4 * time.Minute)},
	}
	reader := &fakeBillingReader{
		id: "connector-detail", connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"},
		records: []reconciliation.BillingRecord{
			{ID: "provider-zero", ExternalID: "external-zero", ExternalRequestID: "shared-request", Model: "model-a", Currency: "USD", NetAmount: "1", UsageStartAt: base},
			{ID: "provider-five", ExternalID: "external-five", ExternalRequestID: "shared-request", Model: "model-a", Currency: "USD", NetAmount: "1", UsageStartAt: base.Add(5 * time.Minute)},
		},
	}
	service := reconciliation.NewService(store, reader)
	rule, err := service.CreateRule(reconciliation.RuleInput{
		Name: "Detail matching", ConnectorID: reader.id, Granularity: reconciliation.GranularityDetail,
		MatchDimensions: []string{"request_id", "model", "currency"}, TimeWindowMinutes: 5,
	}, "actor")
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Run(context.Background(), rule.ID, reconciliation.RunInput{PeriodStart: base, PeriodEnd: base.Add(10 * time.Minute)}, "manual", "actor")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != reconciliation.RunSucceeded || run.MatchedCount != 2 || run.ProviderOnlyCount != 0 || run.TokenHubOnlyCount != 0 || len(store.items) != 2 {
		t.Fatalf("detail matching did not maximize cardinality: run=%#v items=%#v", run, store.items)
	}
}

func TestServiceKeepsProviderOnlyReasonForNonUSDCurrency(t *testing.T) {
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	reader := &fakeBillingReader{
		id: "connector-cny", connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"},
		records: []reconciliation.BillingRecord{{
			ID: "bill-cny", ExternalID: "external-cny", Model: "model-cny",
			Currency: "CNY", NetAmount: "1", UsageStartAt: base.Add(time.Minute),
		}},
	}
	service := reconciliation.NewService(store, reader)
	rule, err := service.CreateRule(reconciliation.RuleInput{
		Name: "CNY daily", ConnectorID: reader.id, Granularity: reconciliation.GranularityDay,
		MatchDimensions: []string{"model", "currency"}, Currency: "CNY", USDExchangeRate: "7",
	}, "actor")
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Run(context.Background(), rule.ID, reconciliation.RunInput{PeriodStart: base, PeriodEnd: base.Add(24 * time.Hour)}, "manual", "actor")
	if err != nil {
		t.Fatal(err)
	}
	if run.ProviderOnlyCount != 1 || len(store.items) != 1 || store.items[0].Status != reconciliation.ProviderOnly ||
		store.items[0].PossibleReason != "missing_tokenhub_usage_or_late_data" {
		t.Fatalf("provider-only result was misclassified: run=%#v items=%#v", run, store.items)
	}
}

func TestServiceSchedulerRunsDueRulesAndRecordsAudit(t *testing.T) {
	store := newFakeStore()
	store.rules["rule-due"] = reconciliation.Rule{
		ID: "rule-due", Name: "Due rule", ConnectorID: "connector-due", ConnectorType: billing.ConnectorOneAPI,
		ProviderID: "provider-a", Status: reconciliation.StatusActive, Granularity: reconciliation.GranularityDay,
		MatchDimensions: []string{"model", "currency"}, AmountTolerance: "0", RatioTolerance: "0",
		USDExchangeRate: "1", Timezone: "UTC", Currency: "USD", Version: 1, RuleHash: "sha256:test",
	}
	store.due = []reconciliation.Rule{store.rules["rule-due"]}
	reader := &fakeBillingReader{id: "connector-due", connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"}}
	service := reconciliation.NewService(store, reader)
	service.StartScheduler(time.Millisecond)
	select {
	case run := <-store.auditSignal:
		if run.Status != reconciliation.RunSucceeded || run.Trigger != "scheduled" || run.RuleID != "rule-due" {
			t.Fatalf("unexpected scheduled run: %#v", run)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not run a due rule")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}

func TestServicePropagatesBillingReaderErrors(t *testing.T) {
	store := newFakeStore()
	reader := &fakeBillingReader{
		id: "connector-errors", connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"},
		connectorErr: billing.NewError(billing.ErrorNotFound, "billing_connector_not_found", "Billing connector not found"),
	}
	service := reconciliation.NewService(store, reader)
	_, err := service.CreateRule(reconciliation.RuleInput{Name: "Reader error", ConnectorID: reader.id}, "actor")
	if kind, code, _, ok := billing.ErrorInfo(err); !ok || kind != billing.ErrorNotFound || code != "billing_connector_not_found" {
		t.Fatalf("connector error was not preserved: %v", err)
	}

	reader.connectorErr = errors.New("connector lookup failed")
	if _, err := service.CreateRule(reconciliation.RuleInput{Name: "Reader error", ConnectorID: reader.id}, "actor"); err == nil || err.Error() != "connector lookup failed" {
		t.Fatalf("generic connector error was not preserved: %v", err)
	}
}

func TestServicePersistsFailedRunWithPortErrorDetails(t *testing.T) {
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	rule := validRule("rule-failed", "connector-failed")
	store.rules[rule.ID] = rule
	reader := &fakeBillingReader{
		id: rule.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"},
		recordsErr: billing.NewError(billing.ErrorUpstream, "billing_records_unavailable", "Billing records are unavailable"),
	}
	run, err := reconciliation.NewService(store, reader).Run(
		context.Background(), rule.ID,
		reconciliation.RunInput{PeriodStart: base, PeriodEnd: base.Add(time.Hour)}, "manual", "actor",
	)
	if err == nil || run.Status != reconciliation.RunFailed || run.ErrorCode != "internal_error" || run.ErrorMessage != "Billing records are unavailable" {
		t.Fatalf("billing port failure was not returned as a failed run: run=%#v err=%v", run, err)
	}
	stored, getErr := store.GetRun(run.ID)
	if getErr != nil || stored.Status != reconciliation.RunFailed || stored.ErrorCode != run.ErrorCode || stored.FinishedAt == nil {
		t.Fatalf("failed run was not persisted for audit: run=%#v err=%v", stored, getErr)
	}

	store.listUsagesErr = errors.New("usage query failed")
	reader.recordsErr = nil
	run, err = reconciliation.NewService(store, reader).Run(
		context.Background(), rule.ID,
		reconciliation.RunInput{PeriodStart: base, PeriodEnd: base.Add(time.Hour)}, "manual", "actor",
	)
	if err == nil || run.Status != reconciliation.RunFailed || run.ErrorCode != "internal_error" || run.ErrorMessage != "usage query failed" {
		t.Fatalf("usage port failure was not returned as a failed run: run=%#v err=%v", run, err)
	}
}

func TestServiceFailedRecalculationPreservesSuccessfulRun(t *testing.T) {
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	prior := reconciliation.Run{
		ID: "run-success", RuleID: "rule-success", ConnectorID: "connector-success",
		ConnectorType: billing.ConnectorOneAPI, ProviderID: "provider-a", Status: reconciliation.RunSucceeded,
		PeriodStart: base, PeriodEnd: base.Add(time.Hour), Granularity: reconciliation.GranularityDay,
		MatchDimensions: []string{"model", "currency"}, AmountTolerance: "0", RatioTolerance: "0",
		USDExchangeRate: "1", Timezone: "UTC", Currency: "USD", InputHash: "sha256:original",
	}
	store.runs[prior.ID] = prior
	reader := &fakeBillingReader{
		id: prior.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"},
		recordsErr: errors.New("recalculation source failed"),
	}
	run, err := reconciliation.NewService(store, reader).Recalculate(context.Background(), prior.ID)
	if err == nil || run.Status != reconciliation.RunFailed || run.ErrorMessage != "recalculation source failed" {
		t.Fatalf("failed recalculation result mismatch: run=%#v err=%v", run, err)
	}
	stored, getErr := store.GetRun(prior.ID)
	if getErr != nil || stored.Status != reconciliation.RunSucceeded || stored.InputHash != prior.InputHash || store.replaceCalls != 0 {
		t.Fatalf("failed recalculation changed the prior result: run=%#v replace_calls=%d err=%v", stored, store.replaceCalls, getErr)
	}
}

func TestServiceRunDuePreservesCancellationBehavior(t *testing.T) {
	store := newFakeStore()
	first := validRule("rule-cancelled", "connector-cancelled")
	second := validRule("rule-skipped", "connector-skipped")
	store.rules[first.ID], store.rules[second.ID] = first, second
	store.due = []reconciliation.Rule{first, second}
	reader := &fakeBillingReader{id: first.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runs := reconciliation.NewService(store, reader).RunDue(ctx, time.Now().UTC())
	if len(runs) != 1 || runs[0].RuleID != first.ID || runs[0].Status != reconciliation.RunRunning || runs[0].ErrorCode != "" {
		t.Fatalf("cancelled scheduler result mismatch: %#v", runs)
	}
	if len(store.audits) != 1 || store.audits[0].Status != reconciliation.RunRunning || store.audits[0].ErrorCode != "" {
		t.Fatalf("cancelled scheduler audit mismatch: %#v", store.audits)
	}
	if _, err := store.GetRun(runs[0].ID); errorCode(err) != "reconciliation_run_not_found" {
		t.Fatalf("cancelled scheduled run changed persistence: %v", err)
	}
}

func TestServiceRecalculatesLegacyRunWithConnectorSnapshot(t *testing.T) {
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	legacy := reconciliation.Run{
		ID: "run-legacy", RuleID: "rule-legacy", ConnectorID: "connector-legacy", Status: reconciliation.RunSucceeded,
		PeriodStart: base, PeriodEnd: base.Add(time.Hour), Granularity: reconciliation.GranularityDay,
		MatchDimensions: []string{"model", "currency"}, AmountTolerance: "0", RatioTolerance: "0",
		USDExchangeRate: "1", Timezone: "UTC", Currency: "USD", RuleVersion: 3, RuleHash: "legacy:without-scope",
	}
	store.runs[legacy.ID] = legacy
	reader := &fakeBillingReader{
		id:        legacy.ConnectorID,
		connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a", ProviderResourceID: "resource-a"},
	}
	run, err := reconciliation.NewService(store, reader).Recalculate(context.Background(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != reconciliation.RunSucceeded || run.ConnectorType != billing.ConnectorOneAPI || run.ProviderID != "provider-a" ||
		run.ProviderResourceID != "resource-a" || run.RuleVersion != legacy.RuleVersion || run.RuleHash == legacy.RuleHash {
		t.Fatalf("legacy run snapshot was not recalculated: %#v", run)
	}
}

func TestServiceScheduledPeriodsCoverGranularitiesAndDST(t *testing.T) {
	tests := []struct {
		name        string
		granularity string
		timezone    string
		interval    int
		now         time.Time
		wantFrom    time.Time
		wantTo      time.Time
	}{
		{name: "hour", granularity: reconciliation.GranularityHour, timezone: "UTC", now: time.Date(2026, time.July, 1, 12, 34, 0, 0, time.UTC), wantFrom: time.Date(2026, time.July, 1, 11, 0, 0, 0, time.UTC), wantTo: time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)},
		{name: "month", granularity: reconciliation.GranularityMonth, timezone: "UTC", now: time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC), wantFrom: time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC), wantTo: time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)},
		{name: "detail", granularity: reconciliation.GranularityDetail, timezone: "UTC", interval: 30, now: time.Date(2026, time.July, 1, 12, 34, 0, 0, time.UTC), wantFrom: time.Date(2026, time.July, 1, 12, 4, 0, 0, time.UTC), wantTo: time.Date(2026, time.July, 1, 12, 34, 0, 0, time.UTC)},
		{name: "day across DST", granularity: reconciliation.GranularityDay, timezone: "America/New_York", now: time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC), wantFrom: time.Date(2026, time.November, 1, 4, 0, 0, 0, time.UTC), wantTo: time.Date(2026, time.November, 2, 5, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeStore()
			rule := validRule("rule-"+test.name, "connector-"+test.name)
			rule.Granularity, rule.Timezone, rule.ScheduleIntervalMinutes = test.granularity, test.timezone, test.interval
			store.rules[rule.ID] = rule
			store.due = []reconciliation.Rule{rule}
			reader := &fakeBillingReader{id: rule.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"}}
			runs := reconciliation.NewService(store, reader).RunDue(context.Background(), test.now)
			if len(runs) != 1 || !runs[0].PeriodStart.Equal(test.wantFrom) || !runs[0].PeriodEnd.Equal(test.wantTo) {
				t.Fatalf("scheduled period mismatch: %#v", runs)
			}
		})
	}
}

func TestServicePersistenceFailuresDoNotReplacePriorResults(t *testing.T) {
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	t.Run("save run", func(t *testing.T) {
		store := newFakeStore()
		rule := validRule("rule-save-error", "connector-save-error")
		store.rules[rule.ID] = rule
		store.saveRunErr = errors.New("save run failed")
		reader := &fakeBillingReader{id: rule.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"}}
		run, err := reconciliation.NewService(store, reader).Run(context.Background(), rule.ID, reconciliation.RunInput{PeriodStart: base, PeriodEnd: base.Add(time.Hour)}, "manual", "actor")
		if err == nil || run.Status != reconciliation.RunFailed || run.ErrorMessage != "save run failed" || store.saveCalls != 2 {
			t.Fatalf("save failure mismatch: run=%#v calls=%d err=%v", run, store.saveCalls, err)
		}
		if _, err := store.GetRun(run.ID); errorCode(err) != "reconciliation_run_not_found" {
			t.Fatalf("failed save unexpectedly persisted a run: %v", err)
		}
	})

	t.Run("replace run", func(t *testing.T) {
		store := newFakeStore()
		prior := reconciliation.Run{
			ID: "run-replace-error", RuleID: "rule-replace-error", ConnectorID: "connector-replace-error",
			ConnectorType: billing.ConnectorOneAPI, ProviderID: "provider-a", Status: reconciliation.RunSucceeded,
			PeriodStart: base, PeriodEnd: base.Add(time.Hour), Granularity: reconciliation.GranularityDay,
			MatchDimensions: []string{"model", "currency"}, AmountTolerance: "0", RatioTolerance: "0",
			USDExchangeRate: "1", Timezone: "UTC", Currency: "USD", InputHash: "sha256:original",
		}
		store.runs[prior.ID] = prior
		store.replaceRunErr = errors.New("replace run failed")
		reader := &fakeBillingReader{id: prior.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"}}
		run, err := reconciliation.NewService(store, reader).Recalculate(context.Background(), prior.ID)
		stored, getErr := store.GetRun(prior.ID)
		if err == nil || run.Status != reconciliation.RunFailed || run.ErrorMessage != "replace run failed" || getErr != nil || stored.InputHash != prior.InputHash {
			t.Fatalf("replace failure changed the prior run: result=%#v stored=%#v err=%v get_err=%v", run, stored, err, getErr)
		}
	})
}

func TestServiceShutdownHonorsDeadlineWhileRunIsBlocked(t *testing.T) {
	store := newFakeStore()
	rule := validRule("rule-blocked", "connector-blocked")
	store.rules[rule.ID] = rule
	store.due = []reconciliation.Rule{rule}
	reader := &fakeBillingReader{
		id: rule.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"},
		recordsStarted: make(chan struct{}), recordsRelease: make(chan struct{}),
	}
	service := reconciliation.NewService(store, reader)
	service.StartScheduler(time.Millisecond)
	select {
	case <-reader.recordsStarted:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not reach the blocked billing reader")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-shutdownContext.Done()
	if err := service.Shutdown(shutdownContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown ignored its context: %v", err)
	}
	close(reader.recordsRelease)
	cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	if err := service.Shutdown(cleanupContext); err != nil {
		t.Fatal(err)
	}
}

func TestServiceConcurrentLegacySnapshotBackfillUsesPersistedWinner(t *testing.T) {
	base := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	rule := validRule("rule-legacy", "connector-legacy")
	rule.ConnectorType, rule.ProviderID, rule.RuleHash = "", "", ""
	store.rules[rule.ID] = rule
	store.getRuleBarrier = make(chan struct{})
	store.backfillBarrier = make(chan struct{})
	readers := []*fakeBillingReader{
		{id: rule.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-a"}},
		{id: rule.ConnectorID, connector: reconciliation.ConnectorSnapshot{Type: billing.ConnectorOneAPI, ProviderID: "provider-b"}},
	}
	results := make(chan reconciliation.Run, len(readers))
	errorsFound := make(chan error, len(readers))
	var workers sync.WaitGroup
	for _, reader := range readers {
		workers.Add(1)
		go func(reader *fakeBillingReader) {
			defer workers.Done()
			run, err := reconciliation.NewService(store, reader).Run(
				context.Background(), rule.ID,
				reconciliation.RunInput{PeriodStart: base, PeriodEnd: base.Add(time.Hour)}, "manual", "actor",
			)
			results <- run
			errorsFound <- err
		}(reader)
	}
	workers.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	persisted, err := store.GetRule(rule.ID)
	if err != nil || persisted.ProviderID == "" || persisted.RuleHash == "" || store.backfillWrites != 1 {
		t.Fatalf("legacy snapshot was not backfilled atomically: rule=%#v writes=%d err=%v", persisted, store.backfillWrites, err)
	}
	for run := range results {
		if run.ProviderID != persisted.ProviderID || run.RuleHash != persisted.RuleHash || run.RuleVersion != persisted.Version {
			t.Fatalf("run did not use the persisted snapshot winner: run=%#v rule=%#v", run, persisted)
		}
	}
}

func validRule(id string, connectorID string) reconciliation.Rule {
	return reconciliation.Rule{
		ID: id, Name: id, ConnectorID: connectorID, ConnectorType: billing.ConnectorOneAPI,
		ProviderID: "provider-a", Status: reconciliation.StatusActive, Granularity: reconciliation.GranularityDay,
		MatchDimensions: []string{"model", "currency"}, AmountTolerance: "0", RatioTolerance: "0",
		USDExchangeRate: "1", Timezone: "UTC", Currency: "USD", Version: 1, RuleHash: "sha256:test",
	}
}

type fakeStore struct {
	mu               sync.Mutex
	rules            map[string]reconciliation.Rule
	runs             map[string]reconciliation.Run
	usages           []reconciliation.Usage
	items            []reconciliation.Item
	due              []reconciliation.Rule
	auditSignal      chan reconciliation.Run
	audits           []reconciliation.Run
	nextRuleID       int
	listUsagesErr    error
	saveRunErr       error
	replaceRunErr    error
	saveCalls        int
	replaceCalls     int
	getRuleBarrier   chan struct{}
	getRuleArrivals  int
	backfillBarrier  chan struct{}
	backfillArrivals int
	backfillWrites   int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rules: map[string]reconciliation.Rule{}, runs: map[string]reconciliation.Run{},
		auditSignal: make(chan reconciliation.Run, 10),
	}
}

func (s *fakeStore) CreateRule(rule reconciliation.Rule) (reconciliation.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rule.ID == "" {
		s.nextRuleID++
		rule.ID = fmt.Sprintf("rule-%d", s.nextRuleID)
	}
	s.rules[rule.ID] = rule
	return rule, nil
}

func (s *fakeStore) GetRule(id string) (reconciliation.Rule, error) {
	s.mu.Lock()
	rule, ok := s.rules[id]
	barrier := s.getRuleBarrier
	if barrier != nil {
		s.getRuleArrivals++
		if s.getRuleArrivals == 2 {
			close(barrier)
		}
	}
	s.mu.Unlock()
	if barrier != nil {
		<-barrier
	}
	if !ok {
		return reconciliation.Rule{}, reconciliation.NewError(reconciliation.ErrorNotFound, "reconciliation_rule_not_found", "Reconciliation rule not found")
	}
	return rule, nil
}

func (s *fakeStore) UpdateRule(rule reconciliation.Rule) (reconciliation.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[rule.ID] = rule
	return rule, nil
}

func (s *fakeStore) BackfillRuleConnectorSnapshot(rule reconciliation.Rule) (reconciliation.Rule, error) {
	s.mu.Lock()
	barrier := s.backfillBarrier
	if barrier != nil {
		s.backfillArrivals++
		if s.backfillArrivals == 2 {
			close(barrier)
		}
	}
	s.mu.Unlock()
	if barrier != nil {
		<-barrier
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.rules[rule.ID]; existing.ConnectorType != "" && existing.ProviderID != "" {
		return existing, nil
	}
	s.rules[rule.ID] = rule
	s.backfillWrites++
	return rule, nil
}

func (s *fakeStore) ListRules() []reconciliation.Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]reconciliation.Rule, 0, len(s.rules))
	for _, rule := range s.rules {
		values = append(values, rule)
	}
	return values
}

func (s *fakeStore) ListDueRules(time.Time, int) []reconciliation.Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]reconciliation.Rule(nil), s.due...)
}

func (s *fakeStore) ListUsages(time.Time, time.Time, time.Duration) ([]reconciliation.Usage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listUsagesErr != nil {
		return nil, s.listUsagesErr
	}
	return append([]reconciliation.Usage(nil), s.usages...), nil
}

func (s *fakeStore) SaveRun(run reconciliation.Run, items []reconciliation.Item) (reconciliation.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.saveRunErr != nil {
		return reconciliation.Run{}, s.saveRunErr
	}
	s.runs[run.ID] = run
	s.items = append([]reconciliation.Item(nil), items...)
	return run, nil
}

func (s *fakeStore) ReplaceRun(run reconciliation.Run, items []reconciliation.Item) (reconciliation.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaceCalls++
	if s.replaceRunErr != nil {
		return reconciliation.Run{}, s.replaceRunErr
	}
	s.runs[run.ID] = run
	s.items = append([]reconciliation.Item(nil), items...)
	return run, nil
}

func (s *fakeStore) GetRun(id string) (reconciliation.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return reconciliation.Run{}, reconciliation.NewError(reconciliation.ErrorNotFound, "reconciliation_run_not_found", "Reconciliation run not found")
	}
	return run, nil
}

func (s *fakeStore) ListRuns(ruleID string, limit int) []reconciliation.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]reconciliation.Run, 0, len(s.runs))
	for _, run := range s.runs {
		if ruleID == "" || run.RuleID == ruleID {
			values = append(values, run)
		}
	}
	return values
}

func (s *fakeStore) ListItems(runID, status string, limit, offset int) ([]reconciliation.Item, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]reconciliation.Item, 0, len(s.items))
	for _, item := range s.items {
		if item.RunID == runID && (status == "" || item.Status == status) {
			values = append(values, item)
		}
	}
	total := int64(len(values))
	if offset < 0 {
		offset = 0
	}
	if offset >= len(values) {
		return nil, total
	}
	if limit > 0 && offset+limit < len(values) {
		values = values[offset : offset+limit]
	} else {
		values = values[offset:]
	}
	return values, total
}

func (s *fakeStore) ListItemBatch(runID, status, afterID string, excludeMatched bool, limit int) []reconciliation.Item {
	items, _ := s.ListItems(runID, status, limit, 0)
	values := make([]reconciliation.Item, 0, len(items))
	for _, item := range items {
		if item.ID <= afterID || (excludeMatched && item.Status == reconciliation.Matched) {
			continue
		}
		values = append(values, item)
	}
	return values
}

func (s *fakeStore) SaveRunLock(run reconciliation.Run) (reconciliation.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = run
	return run, nil
}

func (s *fakeStore) RecordScheduledAudit(run reconciliation.Run) {
	s.mu.Lock()
	s.audits = append(s.audits, run)
	s.mu.Unlock()
	select {
	case s.auditSignal <- run:
	default:
	}
}

type fakeBillingReader struct {
	id                 string
	connector          reconciliation.ConnectorSnapshot
	records            []reconciliation.BillingRecord
	connectorErr       error
	recordsErr         error
	recordsStarted     chan struct{}
	recordsRelease     chan struct{}
	recordsStartedOnce sync.Once
}

func (r *fakeBillingReader) GetConnectorSnapshot(id string) (reconciliation.ConnectorSnapshot, error) {
	if r.connectorErr != nil {
		return reconciliation.ConnectorSnapshot{}, r.connectorErr
	}
	if id != r.id {
		return reconciliation.ConnectorSnapshot{}, billing.NewError(billing.ErrorNotFound, "billing_connector_not_found", "Billing connector not found")
	}
	return r.connector, nil
}

func (r *fakeBillingReader) ListRecordsInRange(string, time.Time, time.Time) ([]reconciliation.BillingRecord, error) {
	if r.recordsStarted != nil {
		r.recordsStartedOnce.Do(func() { close(r.recordsStarted) })
		<-r.recordsRelease
	}
	if r.recordsErr != nil {
		return nil, r.recordsErr
	}
	return append([]reconciliation.BillingRecord(nil), r.records...), nil
}

var _ reconciliation.Store = (*fakeStore)(nil)
var _ reconciliation.BillingReader = (*fakeBillingReader)(nil)
