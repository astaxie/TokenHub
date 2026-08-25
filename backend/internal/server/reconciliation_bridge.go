package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

// ReconciliationService is a temporary W05 adapter for server-owned HTTP and
// persistence representations. W06 will inject reconciliation.Service directly
// after moving those adapters out of server.
type ReconciliationService struct {
	domain *reconciliation.Service
}

func newReconciliationService(store Store, billingReader ReconciliationBillingReader) *ReconciliationService {
	domainStore := &reconciliationStoreBridge{store: store}
	domainBilling := &reconciliationBillingBridge{reader: billingReader}
	return &ReconciliationService{domain: reconciliation.NewService(domainStore, domainBilling)}
}

func (s *ReconciliationService) CreateRule(request ReconciliationRuleRequest, actor string) (ReconciliationRule, error) {
	rule, err := s.domain.CreateRule(reconciliation.RuleInput(request), actor)
	return serverReconciliationRule(rule), reconciliationHTTPError(err)
}

func (s *ReconciliationService) UpdateRule(id string, request ReconciliationRulePatchRequest, actor string) (ReconciliationRule, ReconciliationRule, error) {
	before, updated, err := s.domain.UpdateRule(id, reconciliation.RulePatch(request), actor)
	return serverReconciliationRule(before), serverReconciliationRule(updated), reconciliationHTTPError(err)
}

func (s *ReconciliationService) Run(ctx context.Context, ruleID string, request ReconciliationRunRequest, trigger string, actor string) (ReconciliationRun, error) {
	run, err := s.domain.Run(ctx, ruleID, reconciliation.RunInput(request), trigger, actor)
	return serverReconciliationRun(run), reconciliationHTTPError(err)
}

func (s *ReconciliationService) Recalculate(ctx context.Context, runID string) (ReconciliationRun, error) {
	run, err := s.domain.Recalculate(ctx, runID)
	return serverReconciliationRun(run), reconciliationHTTPError(err)
}

func (s *ReconciliationService) RunDue(ctx context.Context, now time.Time) []ReconciliationRun {
	return serverReconciliationRuns(s.domain.RunDue(ctx, now))
}

func (s *ReconciliationService) StartScheduler(interval time.Duration) {
	s.domain.StartScheduler(interval)
}

func (s *ReconciliationService) Shutdown(ctx context.Context) error {
	return s.domain.Shutdown(ctx)
}

type reconciliationStoreBridge struct {
	store ReconciliationStore
}

var _ reconciliation.Store = (*reconciliationStoreBridge)(nil)

func (b *reconciliationStoreBridge) CreateRule(rule reconciliation.Rule) (reconciliation.Rule, error) {
	result, err := b.store.CreateReconciliationRule(serverReconciliationRule(rule))
	return domainReconciliationRule(result), domainReconciliationStoreError(err)
}

func (b *reconciliationStoreBridge) GetRule(id string) (reconciliation.Rule, error) {
	result, err := b.store.GetReconciliationRule(id)
	return domainReconciliationRule(result), domainReconciliationStoreError(err)
}

func (b *reconciliationStoreBridge) UpdateRule(rule reconciliation.Rule) (reconciliation.Rule, error) {
	result, err := b.store.UpdateReconciliationRule(serverReconciliationRule(rule))
	return domainReconciliationRule(result), domainReconciliationStoreError(err)
}

func (b *reconciliationStoreBridge) BackfillRuleConnectorSnapshot(rule reconciliation.Rule) (reconciliation.Rule, error) {
	result, err := b.store.BackfillReconciliationRuleConnectorSnapshot(serverReconciliationRule(rule))
	return domainReconciliationRule(result), domainReconciliationStoreError(err)
}

func (b *reconciliationStoreBridge) ListDueRules(now time.Time, limit int) []reconciliation.Rule {
	return domainReconciliationRules(b.store.ListDueReconciliationRules(now, limit))
}

func (b *reconciliationStoreBridge) ListUsages(from time.Time, to time.Time, window time.Duration) ([]reconciliation.Usage, error) {
	records, err := b.store.ListReconciliationUsages(from, to, window)
	if err != nil {
		return nil, domainReconciliationStoreError(err)
	}
	result := make([]reconciliation.Usage, len(records))
	for index, record := range records {
		result[index] = reconciliation.Usage{
			ID: record.ID, RequestID: record.RequestID, ProjectID: record.ProjectID,
			ModelName: record.ModelName, ProviderID: record.ProviderID,
			ProviderResourceID: record.ProviderResourceID, CostUSD: record.CostUSD,
			ProviderCostUSD: record.ProviderCostUSD, CreatedAt: record.CreatedAt,
		}
	}
	return result, nil
}

func (b *reconciliationStoreBridge) SaveRun(run reconciliation.Run, items []reconciliation.Item) (reconciliation.Run, error) {
	result, err := b.store.SaveReconciliationRun(serverReconciliationRun(run), serverReconciliationItems(items))
	return domainReconciliationRun(result), domainReconciliationStoreError(err)
}

func (b *reconciliationStoreBridge) ReplaceRun(run reconciliation.Run, items []reconciliation.Item) (reconciliation.Run, error) {
	result, err := b.store.ReplaceReconciliationRun(serverReconciliationRun(run), serverReconciliationItems(items))
	return domainReconciliationRun(result), domainReconciliationStoreError(err)
}

func (b *reconciliationStoreBridge) GetRun(id string) (reconciliation.Run, error) {
	result, err := b.store.GetReconciliationRun(id)
	return domainReconciliationRun(result), domainReconciliationStoreError(err)
}

func (b *reconciliationStoreBridge) RecordScheduledAudit(run reconciliation.Run) {
	b.store.RecordScheduledReconciliationAudit(serverReconciliationRun(run))
}

type reconciliationBillingBridge struct {
	reader ReconciliationBillingReader
}

var _ reconciliation.BillingReader = (*reconciliationBillingBridge)(nil)

func (b *reconciliationBillingBridge) GetConnectorSnapshot(id string) (reconciliation.ConnectorSnapshot, error) {
	connector, err := b.reader.GetBillingConnector(id, false)
	if err != nil {
		return reconciliation.ConnectorSnapshot{}, err
	}
	return reconciliation.ConnectorSnapshot{
		Type:               connector.Type,
		ProviderID:         connector.Config["provider_id"],
		ProviderResourceID: connector.Config["provider_resource_id"],
	}, nil
}

func (b *reconciliationBillingBridge) ListRecordsInRange(connectorID string, from, to time.Time) ([]reconciliation.BillingRecord, error) {
	records, err := b.reader.ListBillingRecordsInRange(connectorID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]reconciliation.BillingRecord, len(records))
	for index, record := range records {
		result[index] = reconciliation.BillingRecord{
			ID: record.ID, ExternalID: record.ExternalID, SourceType: record.SourceType,
			AccountID: record.AccountID, ProviderID: record.Metadata["provider_id"],
			ProviderResourceID: record.Metadata["provider_resource_id"], ResourceID: record.Metadata["resource_id"],
			ProjectID: record.Metadata["project_id"], Model: record.Model, Currency: record.Currency,
			NetAmount: record.NetAmount, UsageStartAt: record.UsageStartAt, ExternalRequestID: record.ExternalRequestID,
		}
	}
	return result, nil
}

func domainReconciliationStoreError(err error) error {
	var httpErr *HTTPError
	if err == nil || !errors.As(err, &httpErr) {
		return err
	}
	var kind reconciliation.ErrorKind
	switch httpErr.Status {
	case http.StatusBadRequest:
		kind = reconciliation.ErrorInvalidInput
	case http.StatusConflict:
		kind = reconciliation.ErrorConflict
	case http.StatusNotFound:
		kind = reconciliation.ErrorNotFound
	default:
		return err
	}
	return reconciliation.WrapError(err, kind, httpErr.Code, httpErr.Message)
}

func reconciliationHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	if kind, code, message, ok := reconciliation.ErrorInfo(err); ok {
		status := http.StatusInternalServerError
		switch kind {
		case reconciliation.ErrorInvalidInput:
			status = http.StatusBadRequest
		case reconciliation.ErrorConflict:
			status = http.StatusConflict
		case reconciliation.ErrorNotFound:
			status = http.StatusNotFound
		}
		return NewHTTPError(status, code, message)
	}
	return err
}

func domainReconciliationRule(rule ReconciliationRule) reconciliation.Rule {
	return reconciliation.Rule(rule)
}

func domainReconciliationRules(rules []ReconciliationRule) []reconciliation.Rule {
	result := make([]reconciliation.Rule, len(rules))
	for index := range rules {
		result[index] = domainReconciliationRule(rules[index])
	}
	return result
}

func serverReconciliationRule(rule reconciliation.Rule) ReconciliationRule {
	return ReconciliationRule(rule)
}

func domainReconciliationRun(run ReconciliationRun) reconciliation.Run {
	return reconciliation.Run(run)
}

func serverReconciliationRun(run reconciliation.Run) ReconciliationRun {
	return ReconciliationRun(run)
}

func serverReconciliationRuns(runs []reconciliation.Run) []ReconciliationRun {
	result := make([]ReconciliationRun, len(runs))
	for index := range runs {
		result[index] = serverReconciliationRun(runs[index])
	}
	return result
}

func serverReconciliationItems(items []reconciliation.Item) []ReconciliationItem {
	result := make([]ReconciliationItem, len(items))
	for index := range items {
		result[index] = ReconciliationItem(items[index])
	}
	return result
}
