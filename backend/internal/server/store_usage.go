package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// summarizeUsage totals an already-scoped set of usage records and request
// logs. Callers decide which rows are in scope; this only aggregates them.
func summarizeUsage(records []UsageRecord, logs []RequestLog) map[string]any {
	var input, cachedInput, cacheWrite, output, reasoningOutput, total int64
	var cost float64
	errorsCount := 0
	for _, record := range records {
		input += record.InputTokens
		cachedInput += record.CachedInputTokens
		cacheWrite += record.CacheWriteTokens
		output += record.OutputTokens
		reasoningOutput += record.ReasoningTokens
		total += record.TotalTokens
		cost += record.CostUSD
	}
	for _, log := range logs {
		if isPlaygroundRequestLog(log) {
			continue
		}
		if log.StatusCode >= 400 {
			errorsCount++
		}
	}
	return map[string]any{
		"request_count":            billableRequestLogCount(logs),
		"usage_record_count":       len(records),
		"input_tokens":             input,
		"cached_input_tokens":      cachedInput,
		"cache_write_input_tokens": cacheWrite,
		"output_tokens":            output,
		"reasoning_output_tokens":  reasoningOutput,
		"total_tokens":             total,
		"estimated_cost_usd":       cost,
		"errors":                   errorsCount,
	}
}

func billableRequestLogCount(logs []RequestLog) int {
	count := 0
	for _, log := range logs {
		if !isPlaygroundRequestLog(log) {
			count++
		}
	}
	return count
}

func isPlaygroundRequestLog(log RequestLog) bool {
	return log.ProjectID == "admin_playground"
}

func (s *GormStore) ListUsageRecords() []UsageRecord {
	var records []UsageRecord
	_ = s.db.Find(&records).Error
	return records
}

func (s *GormStore) GenerateBillingPeriod(period string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	period = normalizeBillingPeriod(period, time.Now().UTC())
	var records []UsageRecord
	if err := s.db.Where("created_at >= ? AND created_at < ?", periodStart(period), periodEnd(period)).
		Find(&records).Error; err != nil {
		return nil, err
	}

	type bucket struct {
		CostCenter        string
		ProjectID         string
		TeamID            string
		RequestCount      int64
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		TotalTokens       int64
		CostUSD           float64
	}
	buckets := map[string]*bucket{}
	projectTotals := map[string]float64{}
	teamTotals := map[string]float64{}
	for _, record := range records {
		project, _ := s.GetProject(record.ProjectID)
		costCenter := s.costCenterForProject(project)
		key := costCenter + "\x00" + record.ProjectID
		item, ok := buckets[key]
		if !ok {
			item = &bucket{CostCenter: costCenter, ProjectID: record.ProjectID, TeamID: project.TeamID}
			buckets[key] = item
		}
		item.RequestCount++
		item.InputTokens += record.InputTokens
		item.CachedInputTokens += record.CachedInputTokens
		item.OutputTokens += record.OutputTokens
		item.TotalTokens += record.TotalTokens
		item.CostUSD += record.CostUSD
		if strings.TrimSpace(record.ProjectID) != "" {
			projectTotals[record.ProjectID] += record.CostUSD
		}
		if strings.TrimSpace(project.TeamID) != "" {
			teamTotals[project.TeamID] += record.CostUSD
		}
	}

	chargebacks := 0
	invoices := 0
	totals := map[string]float64{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := deleteGeneratedResourcesByPeriod(tx, "chargebacks", period); err != nil {
			return err
		}
		if err := deleteGeneratedResourcesByPeriod(tx, "invoices", period); err != nil {
			return err
		}
		for _, item := range buckets {
			if item.CostUSD <= 0 && item.TotalTokens <= 0 {
				continue
			}
			totals[item.CostCenter] += item.CostUSD
			if err := tx.Create(&AdminResource{
				ID:          NewID(resourcePrefix("chargebacks")),
				Kind:        "chargebacks",
				Name:        fmt.Sprintf("%s %s %s 分摊", period, item.CostCenter, item.ProjectID),
				Description: "由 TokenHub 用量记录自动生成",
				Status:      StatusActive,
				Fields: map[string]any{
					"period":              period,
					"cost_center":         item.CostCenter,
					"project_id":          item.ProjectID,
					"team_id":             item.TeamID,
					"allocated_cost_usd":  roundMoney(item.CostUSD),
					"request_count":       item.RequestCount,
					"input_tokens":        item.InputTokens,
					"cached_input_tokens": item.CachedInputTokens,
					"output_tokens":       item.OutputTokens,
					"total_tokens":        item.TotalTokens,
					"allocation_rule":     "actual_usage_cost",
					"generated_by":        "tokenhub",
				},
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
			chargebacks++
		}
		for costCenter, amount := range totals {
			if err := tx.Create(&AdminResource{
				ID:          NewID(resourcePrefix("invoices")),
				Kind:        "invoices",
				Name:        fmt.Sprintf("%s %s 内部账单", period, costCenter),
				Description: "由部门分摊记录汇总生成",
				Status:      "pending",
				Fields: map[string]any{
					"period":       period,
					"cost_center":  costCenter,
					"amount_usd":   roundMoney(amount),
					"invoice_note": defaultInvoiceNote(period, costCenter, amount),
					"confirmed_by": "",
					"generated_by": "tokenhub",
				},
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
			invoices++
		}
		return updateBudgetsFromUsage(tx, period, totals, projectTotals, teamTotals)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"period":             period,
		"usage_records":      len(records),
		"cost_centers":       len(totals),
		"chargebacks":        chargebacks,
		"invoices":           invoices,
		"allocated_cost_usd": roundMoney(sumFloatMap(totals)),
	}, nil
}

func (s *GormStore) ListRequestLogs() []RequestLog {
	var items []RequestLog
	_ = s.db.Order("created_at desc").Find(&items).Error
	return items
}

func (s *GormStore) ListProviderObservations(since time.Time) []ProviderObservation {
	var items []ProviderObservation
	query := s.db
	if !since.IsZero() {
		query = query.Where("observed_at >= ?", since.UTC())
	}
	_ = query.Order("observed_at desc").Find(&items).Error
	return items
}

func (s *GormStore) RecordProviderObservation(observation ProviderObservation) {
	if observation.ID == "" {
		observation.ID = NewID("pob")
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	_ = s.db.Create(&observation).Error
}

func (s *GormStore) GetProviderResourceObservation(resourceID string) (ProviderResourceObservation, bool) {
	var observation ProviderResourceObservation
	err := s.db.First(&observation, "resource_id = ?", strings.TrimSpace(resourceID)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ProviderResourceObservation{}, false
	}
	return observation, err == nil
}

func (s *GormStore) SaveProviderResourceQuota(resourceID string, adapterType string, snapshot string, fetchedAt time.Time) error {
	observation := ProviderResourceObservation{
		ResourceID:     strings.TrimSpace(resourceID),
		AdapterType:    strings.TrimSpace(adapterType),
		QuotaSnapshot:  snapshot,
		QuotaFetchedAt: &fetchedAt,
		UpdatedAt:      time.Now().UTC(),
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "resource_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"adapter_type",
			"quota_snapshot",
			"quota_fetched_at",
			"updated_at",
		}),
	}).Create(&observation).Error
}

func (s *GormStore) GetRequestDetail(requestID string) (map[string]any, error) {
	var log RequestLog
	if err := s.db.First(&log, "request_id = ?", requestID).Error; err != nil {
		return nil, notFound(err, "request_not_found", "Request not found")
	}
	var usage []UsageRecord
	_ = s.db.Where("request_id = ?", requestID).Find(&usage).Error
	var attempts []RouteAttemptLog
	_ = s.db.Where("request_id = ?", requestID).Order("attempt_index asc").Find(&attempts).Error
	var payload RequestPayloadLog
	var payloadValue any
	if err := s.db.First(&payload, "request_id = ?", requestID).Error; err == nil {
		payloadValue = payload
	}
	return map[string]any{
		"log":      log,
		"usage":    usage,
		"attempts": attempts,
		"payload":  payloadValue,
	}, nil
}

func (s *GormStore) ListAlerts() []AlertEvent {
	var items []AlertEvent
	_ = s.db.Order("created_at desc").Find(&items).Error
	return items
}

func (s *GormStore) GetAlert(id string) (AlertEvent, error) {
	var item AlertEvent
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		return AlertEvent{}, notFound(err, "alert_not_found", "Alert not found")
	}
	return item, nil
}

func (s *GormStore) ListAlertDeliveries() []AlertDelivery {
	var items []AlertDelivery
	_ = s.db.Order("created_at desc").Limit(500).Find(&items).Error
	return items
}

func (s *GormStore) RecordAlertDelivery(delivery AlertDelivery) AlertDelivery {
	if delivery.ID == "" {
		delivery.ID = NewID("dlv")
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = time.Now().UTC()
	}
	if delivery.Status == "" {
		delivery.Status = "pending"
	}
	_ = s.db.Create(&delivery).Error
	return delivery
}

func (s *GormStore) ListAuditEvents() []AuditEvent {
	var items []AuditEvent
	_ = s.db.Order("created_at desc").Limit(500).Find(&items).Error
	return items
}

func (s *GormStore) RecordAuditEvent(event AuditEvent) {
	if event.ID == "" {
		event.ID = NewID("audit")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Status == "" {
		event.Status = "success"
	}
	_ = s.db.Create(&event).Error
}
