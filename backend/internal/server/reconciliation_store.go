package server

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"tokenhub/backend/internal/reconciliation"
)

func (s *GormStore) CreateReconciliationRule(rule ReconciliationRule) (ReconciliationRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if rule.ID == "" {
		rule.ID = NewID("recrule")
	}
	if rule.Version <= 0 {
		rule.Version = 1
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	rule = serverReconciliationRule(reconciliation.ApplyRuleSchedule(domainReconciliationRule(rule), nil, now))
	if err := s.db.Create(&rule).Error; err != nil {
		return ReconciliationRule{}, writeConflict(err, "reconciliation_rule_conflict", "Reconciliation rule already exists")
	}
	return rule, nil
}

func (s *GormStore) ListReconciliationRules() []ReconciliationRule {
	var rules []ReconciliationRule
	_ = s.db.Order("created_at asc").Find(&rules).Error
	return rules
}

func (s *GormStore) GetReconciliationRule(id string) (ReconciliationRule, error) {
	var rule ReconciliationRule
	if err := s.db.First(&rule, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return ReconciliationRule{}, notFound(err, "reconciliation_rule_not_found", "Reconciliation rule not found")
	}
	return rule, nil
}

func (s *GormStore) UpdateReconciliationRule(rule ReconciliationRule) (ReconciliationRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing ReconciliationRule
	if err := s.db.First(&existing, "id = ?", strings.TrimSpace(rule.ID)).Error; err != nil {
		return ReconciliationRule{}, notFound(err, "reconciliation_rule_not_found", "Reconciliation rule not found")
	}
	rule.CreatedAt = existing.CreatedAt
	rule.LastRunAt = existing.LastRunAt
	rule.UpdatedAt = time.Now().UTC()
	domainExisting := domainReconciliationRule(existing)
	rule = serverReconciliationRule(reconciliation.ApplyRuleSchedule(domainReconciliationRule(rule), &domainExisting, rule.UpdatedAt))
	if err := s.db.Save(&rule).Error; err != nil {
		return ReconciliationRule{}, err
	}
	return rule, nil
}

func (s *GormStore) BackfillReconciliationRuleConnectorSnapshot(rule ReconciliationRule) (ReconciliationRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule.UpdatedAt = time.Now().UTC()
	result := s.db.Model(&ReconciliationRule{}).
		Where("id = ? AND (COALESCE(TRIM(connector_type), '') = '' OR COALESCE(TRIM(provider_id), '') = '')", strings.TrimSpace(rule.ID)).
		Updates(map[string]any{
			"connector_type":       rule.ConnectorType,
			"provider_id":          rule.ProviderID,
			"provider_resource_id": rule.ProviderResourceID,
			"version":              rule.Version,
			"rule_hash":            rule.RuleHash,
			"updated_at":           rule.UpdatedAt,
		})
	if result.Error != nil {
		return ReconciliationRule{}, result.Error
	}
	var stored ReconciliationRule
	if err := s.db.First(&stored, "id = ?", strings.TrimSpace(rule.ID)).Error; err != nil {
		return ReconciliationRule{}, notFound(err, "reconciliation_rule_not_found", "Reconciliation rule not found")
	}
	return stored, nil
}

func (s *GormStore) ListDueReconciliationRules(now time.Time, limit int) []ReconciliationRule {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var rules []ReconciliationRule
	_ = s.db.Where("status = ? AND schedule_interval_minutes > 0 AND next_run_at IS NOT NULL AND next_run_at <= ?", StatusActive, now.UTC()).
		Order("next_run_at asc").Limit(limit).Find(&rules).Error
	return rules
}

func (s *GormStore) ListReconciliationUsages(from time.Time, to time.Time, window time.Duration) ([]UsageRecord, error) {
	usageFrom := from.UTC()
	usageTo := to.UTC()
	if window > 0 {
		usageFrom = usageFrom.Add(-window)
		usageTo = usageTo.Add(window)
	}
	var usages []UsageRecord
	if err := s.db.Where("created_at >= ? AND created_at < ?", usageFrom, usageTo).
		Order("created_at asc, id asc").Find(&usages).Error; err != nil {
		return nil, err
	}
	return usages, nil
}

func (s *GormStore) SaveReconciliationRun(run ReconciliationRun, items []ReconciliationItem) (ReconciliationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		if len(items) > 0 {
			if err := tx.Create(&items).Error; err != nil {
				return err
			}
		}
		return updateReconciliationRuleAfterRun(tx, run)
	})
	return run, err
}

func (s *GormStore) ReplaceReconciliationRun(run ReconciliationRun, items []ReconciliationItem) (ReconciliationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var existing ReconciliationRun
		if err := tx.First(&existing, "id = ?", run.ID).Error; err != nil {
			return notFound(err, "reconciliation_run_not_found", "Reconciliation run not found")
		}
		if err := reconciliation.ValidateRunReplacement(domainReconciliationRun(existing)); err != nil {
			return reconciliationHTTPError(err)
		}
		run.LockedAt = nil
		run.LockedBy = ""
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", run.ID).Delete(&ReconciliationItem{}).Error; err != nil {
			return err
		}
		if len(items) > 0 {
			return tx.Create(&items).Error
		}
		return nil
	})
	return run, err
}

func (s *GormStore) ListReconciliationRuns(ruleID string, limit int) []ReconciliationRun {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := s.db.Order("started_at desc").Limit(limit)
	if ruleID = strings.TrimSpace(ruleID); ruleID != "" {
		query = query.Where("rule_id = ?", ruleID)
	}
	var runs []ReconciliationRun
	_ = query.Find(&runs).Error
	return runs
}

func (s *GormStore) GetReconciliationRun(id string) (ReconciliationRun, error) {
	var run ReconciliationRun
	if err := s.db.First(&run, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return ReconciliationRun{}, notFound(err, "reconciliation_run_not_found", "Reconciliation run not found")
	}
	return run, nil
}

func (s *GormStore) ListReconciliationItems(runID string, status string, limit int, offset int) ([]ReconciliationItem, int64) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	query := s.db.Model(&ReconciliationItem{}).Where("run_id = ?", strings.TrimSpace(runID))
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	_ = query.Count(&total).Error
	var items []ReconciliationItem
	_ = query.Order("status asc, bucket_start asc, id asc").Limit(limit).Offset(offset).Find(&items).Error
	maskReconciliationItems(items)
	return items, total
}

func (s *GormStore) ListReconciliationItemBatch(runID string, status string, afterID string, excludeMatched bool, limit int) []ReconciliationItem {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	query := s.db.Where("run_id = ?", strings.TrimSpace(runID))
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("status = ?", status)
	} else if excludeMatched {
		query = query.Where("status <> ?", ReconciliationMatched)
	}
	if afterID = strings.TrimSpace(afterID); afterID != "" {
		query = query.Where("id > ?", afterID)
	}
	var items []ReconciliationItem
	_ = query.Order("id asc").Limit(limit).Find(&items).Error
	maskReconciliationItems(items)
	return items
}

func maskReconciliationItems(items []ReconciliationItem) {
	for index := range items {
		items[index].ResourceAccountMasked = maskReconciliationIdentifier(items[index].ResourceAccount)
	}
}

func maskReconciliationIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + "****" + value[len(value)-2:]
}

func (s *GormStore) LockReconciliationRun(id string, actor string) (ReconciliationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var run ReconciliationRun
	if err := s.db.First(&run, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return ReconciliationRun{}, notFound(err, "reconciliation_run_not_found", "Reconciliation run not found")
	}
	prepared, changed, err := reconciliation.PrepareRunLock(domainReconciliationRun(run), actor, time.Now().UTC())
	if err != nil {
		return ReconciliationRun{}, reconciliationHTTPError(err)
	}
	run = serverReconciliationRun(prepared)
	if !changed {
		return run, nil
	}
	return run, s.db.Save(&run).Error
}

func (s *GormStore) RecordScheduledReconciliationAudit(run ReconciliationRun) {
	status := "success"
	if run.Status != ReconciliationRunSucceeded {
		status = "failed"
	}
	s.RecordAuditEvent(AuditEvent{
		ActorUserID:   "system",
		ActorName:     "TokenHub Scheduler",
		ActorRole:     "system",
		Action:        "reconcile",
		ResourceType:  "billing_reconciliation",
		ResourceID:    run.ID,
		Status:        status,
		Message:       run.ErrorCode,
		AfterSnapshot: snapshotJSON(reconciliationAuditSnapshot(run)),
	})
}

func updateReconciliationRuleAfterRun(tx *gorm.DB, run ReconciliationRun) error {
	var rule ReconciliationRule
	if err := tx.First(&rule, "id = ?", run.RuleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	rule = serverReconciliationRule(reconciliation.ApplyRunCompletion(domainReconciliationRule(rule), domainReconciliationRun(run)))
	return tx.Model(&ReconciliationRule{}).Where("id = ?", rule.ID).Updates(map[string]any{
		"last_run_at": rule.LastRunAt,
		"next_run_at": rule.NextRunAt,
	}).Error
}
