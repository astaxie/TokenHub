package server

import (
	"errors"
	"net/http"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

// These helpers preserve the old server test seam while migration tests are
// gradually moved to reconciliation and admin packages. They are excluded from
// production builds and must not be used by new code.
func (s *GormStore) CreateReconciliationRule(v ReconciliationRule) (ReconciliationRule, error) {
	r, e := s.reconciliationPersistence.CreateRule(reconciliation.Rule(v))
	return ReconciliationRule(r), reconciliationHTTPError(e)
}
func (s *GormStore) ListReconciliationRules() []ReconciliationRule {
	values := s.reconciliationPersistence.ListRules()
	out := make([]ReconciliationRule, len(values))
	for i := range values {
		out[i] = ReconciliationRule(values[i])
	}
	return out
}
func (s *GormStore) GetReconciliationRule(id string) (ReconciliationRule, error) {
	r, e := s.reconciliationPersistence.GetRule(id)
	return ReconciliationRule(r), reconciliationHTTPError(e)
}
func (s *GormStore) UpdateReconciliationRule(v ReconciliationRule) (ReconciliationRule, error) {
	r, e := s.reconciliationPersistence.UpdateRule(reconciliation.Rule(v))
	return ReconciliationRule(r), reconciliationHTTPError(e)
}
func (s *GormStore) BackfillReconciliationRuleConnectorSnapshot(v ReconciliationRule) (ReconciliationRule, error) {
	r, e := s.reconciliationPersistence.BackfillRuleConnectorSnapshot(reconciliation.Rule(v))
	return ReconciliationRule(r), reconciliationHTTPError(e)
}
func (s *GormStore) ListDueReconciliationRules(now time.Time, limit int) []ReconciliationRule {
	values := s.reconciliationPersistence.ListDueRules(now, limit)
	out := make([]ReconciliationRule, len(values))
	for i := range values {
		out[i] = ReconciliationRule(values[i])
	}
	return out
}
func (s *GormStore) ListReconciliationUsages(from, to time.Time, window time.Duration) ([]UsageRecord, error) {
	values, e := s.reconciliationPersistence.ListUsages(from, to, window)
	out := make([]UsageRecord, len(values))
	for i, v := range values {
		out[i] = UsageRecord{ID: v.ID, RequestID: v.RequestID, ProjectID: v.ProjectID, ModelName: v.ModelName, ProviderID: v.ProviderID, ProviderResourceID: v.ProviderResourceID, CostUSD: v.CostUSD, ProviderCostUSD: v.ProviderCostUSD, CreatedAt: v.CreatedAt}
	}
	return out, e
}
func (s *GormStore) SaveReconciliationRun(v ReconciliationRun, items []ReconciliationItem) (ReconciliationRun, error) {
	is := make([]reconciliation.Item, len(items))
	for i := range items {
		is[i] = reconciliation.Item(items[i])
	}
	r, e := s.reconciliationPersistence.SaveRun(reconciliation.Run(v), is)
	return ReconciliationRun(r), reconciliationHTTPError(e)
}
func (s *GormStore) ReplaceReconciliationRun(v ReconciliationRun, items []ReconciliationItem) (ReconciliationRun, error) {
	is := make([]reconciliation.Item, len(items))
	for i := range items {
		is[i] = reconciliation.Item(items[i])
	}
	r, e := s.reconciliationPersistence.ReplaceRun(reconciliation.Run(v), is)
	return ReconciliationRun(r), reconciliationHTTPError(e)
}
func (s *GormStore) ListReconciliationRuns(id string, limit int) []ReconciliationRun {
	values := s.reconciliationPersistence.ListRuns(id, limit)
	out := make([]ReconciliationRun, len(values))
	for i := range values {
		out[i] = ReconciliationRun(values[i])
	}
	return out
}
func (s *GormStore) GetReconciliationRun(id string) (ReconciliationRun, error) {
	r, e := s.reconciliationPersistence.GetRun(id)
	return ReconciliationRun(r), reconciliationHTTPError(e)
}
func (s *GormStore) ListReconciliationItems(id, status string, limit, offset int) ([]ReconciliationItem, int64) {
	values, total := s.reconciliationPersistence.ListItems(id, status, limit, offset)
	out := make([]ReconciliationItem, len(values))
	for i := range values {
		out[i] = ReconciliationItem(values[i])
	}
	return out, total
}
func (s *GormStore) ListReconciliationItemBatch(id, status, after string, exclude bool, limit int) []ReconciliationItem {
	values := s.reconciliationPersistence.ListItemBatch(id, status, after, exclude, limit)
	out := make([]ReconciliationItem, len(values))
	for i := range values {
		out[i] = ReconciliationItem(values[i])
	}
	return out
}
func (s *GormStore) LockReconciliationRun(id, actor string) (ReconciliationRun, error) {
	r, e := s.reconciliationPersistence.GetRun(id)
	if e == nil {
		prepared, changed, policyErr := reconciliation.PrepareRunLock(r, actor, time.Now().UTC())
		if policyErr != nil {
			e = policyErr
		} else if changed {
			r, e = s.reconciliationPersistence.SaveRunLock(prepared)
		}
	}
	return ReconciliationRun(r), reconciliationHTTPError(e)
}

func domainReconciliationStoreError(err error) error {
	var httpErr *HTTPError
	if err == nil || !errors.As(err, &httpErr) {
		return err
	}
	kind := reconciliation.ErrorKind("")
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
