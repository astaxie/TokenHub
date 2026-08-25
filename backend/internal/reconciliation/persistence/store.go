package persistence

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/reconciliation"
)

// Store is the GORM adapter for reconciliation execution and administration.
// The adapter owns table rows; callers only see reconciliation ports.
type Store struct {
	db                   *gorm.DB
	mu                   *sync.Mutex
	recordScheduledAudit func(reconciliation.Run)
}

func NewStore(db *gorm.DB, mu *sync.Mutex, recordScheduledAudit func(reconciliation.Run)) *Store {
	if mu == nil {
		mu = &sync.Mutex{}
	}
	return &Store{db: db, mu: mu, recordScheduledAudit: recordScheduledAudit}
}

var _ reconciliation.Store = (*Store)(nil)

// WithContext returns an adapter view using the supplied GORM context while
// preserving the shared mutex and scheduled-audit callback.
func (s *Store) WithContext(ctx context.Context) *Store {
	if ctx == nil {
		ctx = context.Background()
	}
	view := *s
	view.db = s.db.WithContext(ctx)
	return &view
}

func (s *Store) CreateRule(value reconciliation.Rule) (reconciliation.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if value.ID == "" {
		value.ID = "recrule_" + randomID()
	}
	if value.Version <= 0 {
		value.Version = 1
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	row := ruleToRow(reconciliation.ApplyRuleSchedule(value, nil, now))
	if err := s.db.Create(&row).Error; err != nil {
		return reconciliation.Rule{}, conflictError("reconciliation_rule_conflict", "Reconciliation rule already exists", err)
	}
	return ruleFromRow(row), nil
}

func (s *Store) ListRules() []reconciliation.Rule {
	var rows []RuleRow
	_ = s.db.Order("created_at asc").Find(&rows).Error
	result := make([]reconciliation.Rule, len(rows))
	for i := range rows {
		result[i] = ruleFromRow(rows[i])
	}
	return result
}

func (s *Store) GetRule(id string) (reconciliation.Rule, error) {
	var row RuleRow
	if err := s.db.First(&row, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return reconciliation.Rule{}, notFoundError("reconciliation_rule_not_found", "Reconciliation rule not found", err)
	}
	return ruleFromRow(row), nil
}

func (s *Store) UpdateRule(value reconciliation.Rule) (reconciliation.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing RuleRow
	if err := s.db.First(&existing, "id = ?", strings.TrimSpace(value.ID)).Error; err != nil {
		return reconciliation.Rule{}, notFoundError("reconciliation_rule_not_found", "Reconciliation rule not found", err)
	}
	value.CreatedAt = existing.CreatedAt
	value.LastRunAt = existing.LastRunAt
	value.UpdatedAt = time.Now().UTC()
	existingValue := ruleFromRow(existing)
	value = reconciliation.ApplyRuleSchedule(value, &existingValue, value.UpdatedAt)
	row := ruleToRow(value)
	if err := s.db.Save(&row).Error; err != nil {
		return reconciliation.Rule{}, err
	}
	return value, nil
}

func (s *Store) BackfillRuleConnectorSnapshot(value reconciliation.Rule) (reconciliation.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.UpdatedAt = time.Now().UTC()
	result := s.db.Model(&RuleRow{}).Where("id = ? AND (COALESCE(TRIM(connector_type), '') = '' OR COALESCE(TRIM(provider_id), '') = '')", strings.TrimSpace(value.ID)).Updates(map[string]any{
		"connector_type": value.ConnectorType, "provider_id": value.ProviderID, "provider_resource_id": value.ProviderResourceID,
		"version": value.Version, "rule_hash": value.RuleHash, "updated_at": value.UpdatedAt,
	})
	if result.Error != nil {
		return reconciliation.Rule{}, result.Error
	}
	return s.GetRule(value.ID)
}

func (s *Store) ListDueRules(now time.Time, limit int) []reconciliation.Rule {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var rows []RuleRow
	_ = s.db.Where("status = ? AND schedule_interval_minutes > 0 AND next_run_at IS NOT NULL AND next_run_at <= ?", reconciliation.StatusActive, now.UTC()).Order("next_run_at asc").Limit(limit).Find(&rows).Error
	result := make([]reconciliation.Rule, len(rows))
	for i := range rows {
		result[i] = ruleFromRow(rows[i])
	}
	return result
}

type usageRow struct {
	ID                 string `gorm:"primaryKey"`
	RequestID          string
	ProjectID          string
	ModelName          string `gorm:"column:model_name"`
	ProviderID         string
	ProviderResourceID string
	CostUSD            float64 `gorm:"column:cost_usd"`
	ProviderCostUSD    float64 `gorm:"column:provider_cost_usd"`
	CreatedAt          time.Time
}

func (usageRow) TableName() string { return "usage_records" }

func (s *Store) ListUsages(from, to time.Time, window time.Duration) ([]reconciliation.Usage, error) {
	if window > 0 {
		from = from.Add(-window)
		to = to.Add(window)
	}
	var rows []usageRow
	if err := s.db.Where("created_at >= ? AND created_at < ?", from.UTC(), to.UTC()).Order("created_at asc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]reconciliation.Usage, len(rows))
	for i, row := range rows {
		result[i] = usageFromRecord(row.ID, row.RequestID, row.ProjectID, row.ModelName, row.ProviderID, row.ProviderResourceID, row.CostUSD, row.ProviderCostUSD, row.CreatedAt)
	}
	return result, nil
}

func (s *Store) SaveRun(value reconciliation.Run, items []reconciliation.Item) (reconciliation.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		runRow := runToRow(value)
		if err := tx.Create(&runRow).Error; err != nil {
			return err
		}
		if len(items) > 0 {
			rows := make([]ItemRow, len(items))
			for i := range items {
				rows[i] = itemToRow(items[i])
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return updateRuleAfterRun(tx, value)
	})
	return value, err
}

func (s *Store) ReplaceRun(value reconciliation.Run, items []reconciliation.Item) (reconciliation.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var existing RunRow
		if err := tx.First(&existing, "id = ?", value.ID).Error; err != nil {
			return notFoundError("reconciliation_run_not_found", "Reconciliation run not found", err)
		}
		if err := reconciliation.ValidateRunReplacement(runFromRow(existing)); err != nil {
			return err
		}
		value.LockedAt = nil
		value.LockedBy = ""
		runRow := runToRow(value)
		if err := tx.Save(&runRow).Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", value.ID).Delete(&ItemRow{}).Error; err != nil {
			return err
		}
		if len(items) > 0 {
			rows := make([]ItemRow, len(items))
			for i := range items {
				rows[i] = itemToRow(items[i])
			}
			return tx.Create(&rows).Error
		}
		return nil
	})
	return value, err
}

func (s *Store) ListRuns(ruleID string, limit int) []reconciliation.Run {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := s.db.Order("started_at desc").Limit(limit)
	if ruleID = strings.TrimSpace(ruleID); ruleID != "" {
		q = q.Where("rule_id = ?", ruleID)
	}
	var rows []RunRow
	_ = q.Find(&rows).Error
	result := make([]reconciliation.Run, len(rows))
	for i := range rows {
		result[i] = runFromRow(rows[i])
	}
	return result
}
func (s *Store) GetRun(id string) (reconciliation.Run, error) {
	var row RunRow
	if err := s.db.First(&row, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return reconciliation.Run{}, notFoundError("reconciliation_run_not_found", "Reconciliation run not found", err)
	}
	return runFromRow(row), nil
}
func (s *Store) ListItems(runID, status string, limit, offset int) ([]reconciliation.Item, int64) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := s.db.Model(&ItemRow{}).Where("run_id = ?", strings.TrimSpace(runID))
	if status = strings.TrimSpace(status); status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	_ = q.Count(&total).Error
	var rows []ItemRow
	_ = q.Order("status asc, bucket_start asc, id asc").Limit(limit).Offset(offset).Find(&rows).Error
	result := make([]reconciliation.Item, len(rows))
	for i := range rows {
		result[i] = maskItem(itemFromRow(rows[i]))
	}
	return result, total
}
func (s *Store) ListItemBatch(runID, status, afterID string, excludeMatched bool, limit int) []reconciliation.Item {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	q := s.db.Where("run_id = ?", strings.TrimSpace(runID))
	if status = strings.TrimSpace(status); status != "" {
		q = q.Where("status = ?", status)
	} else if excludeMatched {
		q = q.Where("status <> ?", reconciliation.Matched)
	}
	if afterID = strings.TrimSpace(afterID); afterID != "" {
		q = q.Where("id > ?", afterID)
	}
	var rows []ItemRow
	_ = q.Order("id asc").Limit(limit).Find(&rows).Error
	result := make([]reconciliation.Item, len(rows))
	for i := range rows {
		result[i] = maskItem(itemFromRow(rows[i]))
	}
	return result
}
func maskItem(value reconciliation.Item) reconciliation.Item {
	value.ResourceAccountMasked = maskIdentifier(value.ResourceAccount)
	return value
}
func maskIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + "****" + value[len(value)-2:]
}
func (s *Store) SaveRunLock(value reconciliation.Run) (reconciliation.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runRow := runToRow(value)
	if err := s.db.Save(&runRow).Error; err != nil {
		return reconciliation.Run{}, err
	}
	return value, nil
}
func (s *Store) RecordScheduledAudit(run reconciliation.Run) {
	if s.recordScheduledAudit != nil {
		s.recordScheduledAudit(run)
	}
}
func updateRuleAfterRun(tx *gorm.DB, run reconciliation.Run) error {
	var row RuleRow
	if err := tx.First(&row, "id = ?", run.RuleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	rule := reconciliation.ApplyRunCompletion(ruleFromRow(row), run)
	return tx.Model(&RuleRow{}).Where("id = ?", rule.ID).Updates(map[string]any{"last_run_at": rule.LastRunAt, "next_run_at": rule.NextRunAt}).Error
}

func randomID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return base64.RawURLEncoding.EncodeToString(value[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
func notFoundError(code, message string, cause error) error {
	if !errors.Is(cause, gorm.ErrRecordNotFound) {
		return cause
	}
	return reconciliation.WrapError(cause, reconciliation.ErrorNotFound, code, message)
}
func conflictError(code, message string, cause error) error {
	if !errors.Is(cause, gorm.ErrDuplicatedKey) {
		return cause
	}
	return reconciliation.WrapError(cause, reconciliation.ErrorConflict, code, message)
}
