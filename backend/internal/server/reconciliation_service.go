package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ReconciliationService struct {
	store         Store
	mu            sync.Mutex
	active        map[string]bool
	schedulerOnce sync.Once
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

func newReconciliationService(store Store) *ReconciliationService {
	return &ReconciliationService{store: store, active: map[string]bool{}}
}

func (s *ReconciliationService) CreateRule(request ReconciliationRuleRequest, actor string) (ReconciliationRule, error) {
	rule := reconciliationRuleFromRequest(request)
	rule.CreatedBy = strings.TrimSpace(actor)
	rule.UpdatedBy = strings.TrimSpace(actor)
	if err := normalizeAndValidateReconciliationRule(&rule); err != nil {
		return ReconciliationRule{}, err
	}
	connector, err := s.store.GetBillingConnector(rule.ConnectorID, false)
	if err != nil {
		return ReconciliationRule{}, err
	}
	if err := snapshotReconciliationConnector(&rule, connector); err != nil {
		return ReconciliationRule{}, err
	}
	rule.Version = 1
	rule.RuleHash = reconciliationRuleHash(rule)
	return s.store.CreateReconciliationRule(rule)
}

func (s *ReconciliationService) UpdateRule(id string, request ReconciliationRulePatchRequest, actor string) (ReconciliationRule, ReconciliationRule, error) {
	before, err := s.store.GetReconciliationRule(id)
	if err != nil {
		return ReconciliationRule{}, ReconciliationRule{}, err
	}
	rule := before
	applyReconciliationRulePatch(&rule, request)
	rule.UpdatedBy = strings.TrimSpace(actor)
	if err := normalizeAndValidateReconciliationRule(&rule); err != nil {
		return before, ReconciliationRule{}, err
	}
	connector, err := s.store.GetBillingConnector(rule.ConnectorID, false)
	if err != nil {
		return before, ReconciliationRule{}, err
	}
	if err := snapshotReconciliationConnector(&rule, connector); err != nil {
		return before, ReconciliationRule{}, err
	}
	rule.Version = before.Version + 1
	rule.RuleHash = reconciliationRuleHash(rule)
	updated, err := s.store.UpdateReconciliationRule(rule)
	return before, updated, err
}

func (s *ReconciliationService) ensureReconciliationRuleConnectorSnapshot(rule ReconciliationRule) (ReconciliationRule, error) {
	if strings.TrimSpace(rule.ConnectorType) != "" && normalizeReconciliationScope(rule.ProviderID) != "" {
		return rule, validateReconciliationConnectorSnapshot(rule.Granularity, rule.ConnectorType, rule.ProviderID)
	}
	connector, err := s.store.GetBillingConnector(rule.ConnectorID, false)
	if err != nil {
		return ReconciliationRule{}, err
	}
	if err := snapshotReconciliationConnector(&rule, connector); err != nil {
		return ReconciliationRule{}, err
	}
	return s.store.BackfillReconciliationRuleConnectorSnapshot(rule.ID, rule.ConnectorType, rule.ProviderID, rule.ProviderResourceID)
}

func (s *ReconciliationService) ensureReconciliationRunConnectorSnapshot(run ReconciliationRun) (ReconciliationRun, error) {
	if strings.TrimSpace(run.ConnectorType) != "" && normalizeReconciliationScope(run.ProviderID) != "" {
		return run, validateReconciliationConnectorSnapshot(run.Granularity, run.ConnectorType, run.ProviderID)
	}
	connector, err := s.store.GetBillingConnector(run.ConnectorID, false)
	if err != nil {
		return ReconciliationRun{}, err
	}
	if err := snapshotReconciliationRunConnector(&run, connector); err != nil {
		return ReconciliationRun{}, err
	}
	run.RuleHash = reconciliationRunRuleHash(run)
	return run, nil
}

func (s *ReconciliationService) Run(ctx context.Context, ruleID string, request ReconciliationRunRequest, trigger string, actor string) (ReconciliationRun, error) {
	if !s.begin("rule:" + ruleID) {
		return ReconciliationRun{}, NewHTTPError(http.StatusConflict, "reconciliation_in_progress", "A reconciliation is already running for this rule")
	}
	defer s.end("rule:" + ruleID)
	rule, err := s.store.GetReconciliationRule(ruleID)
	if err != nil {
		return ReconciliationRun{}, err
	}
	if rule.Status != StatusActive {
		return ReconciliationRun{}, NewHTTPError(http.StatusConflict, "reconciliation_rule_disabled", "Disabled reconciliation rules cannot be run")
	}
	rule, err = s.ensureReconciliationRuleConnectorSnapshot(rule)
	if err != nil {
		return ReconciliationRun{}, err
	}
	if request.PeriodStart.IsZero() || request.PeriodEnd.IsZero() || !request.PeriodStart.Before(request.PeriodEnd) {
		return ReconciliationRun{}, NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_period", "period_start and period_end must define a non-empty range")
	}
	if request.PeriodEnd.Sub(request.PeriodStart) > 366*24*time.Hour {
		return ReconciliationRun{}, NewHTTPError(http.StatusBadRequest, "reconciliation_period_too_large", "Reconciliation periods cannot exceed 366 days")
	}
	run := reconciliationRunFromRule(rule, request, trigger, actor)
	return s.calculateAndSave(ctx, run, false)
}

func (s *ReconciliationService) Recalculate(ctx context.Context, runID string) (ReconciliationRun, error) {
	if !s.begin("run:" + runID) {
		return ReconciliationRun{}, NewHTTPError(http.StatusConflict, "reconciliation_in_progress", "This reconciliation is already being recalculated")
	}
	defer s.end("run:" + runID)
	run, err := s.store.GetReconciliationRun(runID)
	if err != nil {
		return ReconciliationRun{}, err
	}
	if run.LockedAt != nil {
		return ReconciliationRun{}, NewHTTPError(http.StatusConflict, "reconciliation_run_locked", "Locked reconciliation runs cannot be recalculated")
	}
	run, err = s.ensureReconciliationRunConnectorSnapshot(run)
	if err != nil {
		return ReconciliationRun{}, err
	}
	run.Trigger = "recalculation"
	run.Status = ReconciliationRunRunning
	run.InputHash = ""
	run.ProviderRecordCount = 0
	run.TokenHubRecordCount = 0
	run.MatchedCount = 0
	run.ProviderOnlyCount = 0
	run.TokenHubOnlyCount = 0
	run.AmountMismatchCount = 0
	run.ProviderAmount = "0"
	run.TokenHubAmount = "0"
	run.DifferenceAmount = "0"
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.StartedAt = time.Now().UTC()
	run.FinishedAt = nil
	return s.calculateAndSave(ctx, run, true)
}

func (s *ReconciliationService) calculateAndSave(ctx context.Context, run ReconciliationRun, replace bool) (ReconciliationRun, error) {
	if err := ctx.Err(); err != nil {
		return run, err
	}
	window := time.Duration(run.TimeWindowMinutes) * time.Minute
	err := validateReconciliationConnectorSnapshot(run.Granularity, run.ConnectorType, run.ProviderID)
	var bills []BillingRecord
	var usages []UsageRecord
	if err == nil {
		bills, usages, err = s.store.LoadReconciliationInputs(run.ConnectorID, run.PeriodStart, run.PeriodEnd, window)
	}
	if err == nil {
		usages = scopeReconciliationUsages(run, usages)
		run, varItems, calculateErr := calculateReconciliation(run, bills, usages)
		err = calculateErr
		if err == nil {
			if replace {
				var saved ReconciliationRun
				saved, err = s.store.ReplaceReconciliationRun(run, varItems)
				if err == nil {
					return saved, nil
				}
			} else {
				var saved ReconciliationRun
				saved, err = s.store.SaveReconciliationRun(run, varItems)
				if err == nil {
					return saved, nil
				}
			}
		}
	}
	run = failedReconciliationRun(run, err)
	if !replace {
		_, _ = s.store.SaveReconciliationRun(run, nil)
	}
	return run, err
}

func failedReconciliationRun(run ReconciliationRun, err error) ReconciliationRun {
	httpErr := AsHTTPError(err)
	run.Status = ReconciliationRunFailed
	run.ErrorCode = httpErr.Code
	run.ErrorMessage = httpErr.Message
	finishedAt := time.Now().UTC()
	run.FinishedAt = &finishedAt
	return run
}

func (s *ReconciliationService) RunDue(ctx context.Context, now time.Time) []ReconciliationRun {
	rules := s.store.ListDueReconciliationRules(now, 25)
	runs := make([]ReconciliationRun, 0, len(rules))
	for _, rule := range rules {
		from, to, err := scheduledReconciliationPeriod(rule, now)
		var run ReconciliationRun
		if err == nil {
			run, err = s.Run(ctx, rule.ID, ReconciliationRunRequest{PeriodStart: from, PeriodEnd: to}, "scheduled", "system")
		}
		if run.ID == "" && err != nil {
			httpErr := AsHTTPError(err)
			run = ReconciliationRun{RuleID: rule.ID, ConnectorID: rule.ConnectorID, Trigger: "scheduled", Status: ReconciliationRunFailed, ErrorCode: httpErr.Code, ErrorMessage: httpErr.Message}
		}
		s.store.RecordScheduledReconciliationAudit(run)
		runs = append(runs, run)
		if ctx.Err() != nil {
			break
		}
	}
	return runs
}

func (s *ReconciliationService) StartScheduler(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	s.schedulerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.schedulerStop = cancel
		s.schedulerDone = make(chan struct{})
		go func() {
			defer close(s.schedulerDone)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					s.RunDue(ctx, now.UTC())
				}
			}
		}()
	})
}

func (s *ReconciliationService) Shutdown(ctx context.Context) error {
	if s.schedulerStop == nil {
		return nil
	}
	s.schedulerStop()
	select {
	case <-s.schedulerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *ReconciliationService) begin(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[key] {
		return false
	}
	s.active[key] = true
	return true
}

func (s *ReconciliationService) end(key string) {
	s.mu.Lock()
	delete(s.active, key)
	s.mu.Unlock()
}

func reconciliationRuleFromRequest(request ReconciliationRuleRequest) ReconciliationRule {
	return ReconciliationRule{
		Name:                    request.Name,
		ConnectorID:             request.ConnectorID,
		Status:                  request.Status,
		Granularity:             request.Granularity,
		MatchDimensions:         request.MatchDimensions,
		DimensionMappings:       request.DimensionMappings,
		AmountTolerance:         request.AmountTolerance,
		RatioTolerance:          request.RatioTolerance,
		USDExchangeRate:         request.USDExchangeRate,
		TimeWindowMinutes:       request.TimeWindowMinutes,
		BillingDelayMinutes:     request.BillingDelayMinutes,
		ScheduleIntervalMinutes: request.ScheduleIntervalMinutes,
		Timezone:                request.Timezone,
		Currency:                request.Currency,
	}
}

func applyReconciliationRulePatch(rule *ReconciliationRule, request ReconciliationRulePatchRequest) {
	if request.Name != nil {
		rule.Name = *request.Name
	}
	if request.Status != nil {
		rule.Status = *request.Status
	}
	if request.Granularity != nil {
		rule.Granularity = *request.Granularity
	}
	if request.MatchDimensions != nil {
		rule.MatchDimensions = *request.MatchDimensions
	}
	if request.DimensionMappings != nil {
		rule.DimensionMappings = *request.DimensionMappings
	}
	if request.AmountTolerance != nil {
		rule.AmountTolerance = *request.AmountTolerance
	}
	if request.RatioTolerance != nil {
		rule.RatioTolerance = *request.RatioTolerance
	}
	if request.USDExchangeRate != nil {
		rule.USDExchangeRate = *request.USDExchangeRate
	}
	if request.TimeWindowMinutes != nil {
		rule.TimeWindowMinutes = *request.TimeWindowMinutes
	}
	if request.BillingDelayMinutes != nil {
		rule.BillingDelayMinutes = *request.BillingDelayMinutes
	}
	if request.ScheduleIntervalMinutes != nil {
		rule.ScheduleIntervalMinutes = *request.ScheduleIntervalMinutes
	}
	if request.Timezone != nil {
		rule.Timezone = *request.Timezone
	}
	if request.Currency != nil {
		rule.Currency = *request.Currency
	}
}

func normalizeAndValidateReconciliationRule(rule *ReconciliationRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.ConnectorID = strings.TrimSpace(rule.ConnectorID)
	rule.Status = strings.ToLower(strings.TrimSpace(rule.Status))
	rule.Granularity = strings.ToLower(strings.TrimSpace(rule.Granularity))
	rule.AmountTolerance = strings.TrimSpace(rule.AmountTolerance)
	rule.RatioTolerance = strings.TrimSpace(rule.RatioTolerance)
	rule.USDExchangeRate = strings.TrimSpace(rule.USDExchangeRate)
	rule.Timezone = strings.TrimSpace(rule.Timezone)
	rule.Currency = strings.ToUpper(strings.TrimSpace(rule.Currency))
	if rule.Status == "" {
		rule.Status = StatusActive
	}
	if rule.Granularity == "" {
		rule.Granularity = ReconciliationGranularityDetail
	}
	if rule.AmountTolerance == "" {
		rule.AmountTolerance = "0"
	}
	if rule.RatioTolerance == "" {
		rule.RatioTolerance = "0"
	}
	if rule.USDExchangeRate == "" {
		rule.USDExchangeRate = "1"
	}
	if rule.Timezone == "" {
		rule.Timezone = "UTC"
	}
	if rule.Currency == "" {
		rule.Currency = "USD"
	}
	if len(rule.MatchDimensions) == 0 {
		if rule.Granularity == ReconciliationGranularityDetail {
			rule.MatchDimensions = []string{"request_id", "model", "currency"}
		} else {
			rule.MatchDimensions = []string{"model", "currency"}
		}
	}
	rule.MatchDimensions = normalizeReconciliationDimensions(rule.MatchDimensions)
	mappings, mappingErr := normalizeReconciliationMappings(rule.DimensionMappings)
	if mappingErr != nil {
		return mappingErr
	}
	rule.DimensionMappings = mappings
	if rule.Name == "" || rule.ConnectorID == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_rule", "name and connector_id are required")
	}
	if rule.Status != StatusActive && rule.Status != StatusDisabled {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_status", "status must be active or disabled")
	}
	switch rule.Granularity {
	case ReconciliationGranularityDetail, ReconciliationGranularityHour, ReconciliationGranularityDay, ReconciliationGranularityMonth:
	default:
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_granularity", "granularity must be detail, hour, day, or month")
	}
	if len(rule.MatchDimensions) == 0 {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_dimensions", "At least one matching dimension is required")
	}
	if !reconciliationContains(rule.MatchDimensions, "currency") {
		return NewHTTPError(http.StatusBadRequest, "reconciliation_currency_required", "currency must be a matching dimension")
	}
	if rule.Granularity == ReconciliationGranularityDetail && !reconciliationContains(rule.MatchDimensions, "request_id") {
		return NewHTTPError(http.StatusBadRequest, "reconciliation_request_id_required", "detail reconciliation requires the request_id dimension")
	}
	if rule.Granularity != ReconciliationGranularityDetail && reconciliationContains(rule.MatchDimensions, "request_id") {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_dimensions", "request_id can only be used with detail reconciliation")
	}
	amount, err := parseReconciliationMoney(rule.AmountTolerance)
	if err != nil || amount < 0 {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_tolerance", "amount_tolerance must be a non-negative decimal")
	}
	ratio, err := parseReconciliationMoney(rule.RatioTolerance)
	if err != nil || ratio < 0 || ratio > reconciliationMoney(reconciliationScale) {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_tolerance", "ratio_tolerance must be between 0 and 1")
	}
	rule.AmountTolerance = amount.String()
	rule.RatioTolerance = ratio.String()
	exchangeRate, err := parseReconciliationMoney(rule.USDExchangeRate)
	if err != nil || exchangeRate <= 0 {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_exchange_rate", "usd_exchange_rate must be a positive decimal")
	}
	if rule.Currency == "USD" && exchangeRate != reconciliationMoney(reconciliationScale) {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_exchange_rate", "USD reconciliation must use an exchange rate of 1")
	}
	rule.USDExchangeRate = exchangeRate.String()
	if rule.TimeWindowMinutes < 0 || rule.TimeWindowMinutes > 24*60 || rule.BillingDelayMinutes < 0 || rule.BillingDelayMinutes > 30*24*60 ||
		rule.ScheduleIntervalMinutes < 0 || rule.ScheduleIntervalMinutes > 30*24*60 {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_window", "time, delay, and schedule windows are outside the supported range")
	}
	if _, err := time.LoadLocation(rule.Timezone); err != nil {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_timezone", "timezone must be a valid IANA timezone")
	}
	if !isReconciliationCurrency(rule.Currency) {
		return NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_currency", "currency must be a three-letter ISO code")
	}
	return nil
}

func isReconciliationCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func normalizeReconciliationDimensions(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if _, ok := reconciliationDimensions[value]; ok {
			seen[value] = true
		}
	}
	order := []string{"request_id", "provider", "resource_account", "model", "project", "currency"}
	result := make([]string, 0, len(seen))
	for _, value := range order {
		if seen[value] {
			result = append(result, value)
		}
	}
	return result
}

func normalizeReconciliationMappings(values map[string]map[string]string) (map[string]map[string]string, error) {
	result := map[string]map[string]string{}
	for rawDimension, entries := range values {
		dimension := strings.ToLower(strings.TrimSpace(rawDimension))
		switch dimension {
		case "provider", "resource_account", "model", "project":
		default:
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_mapping", "dimension_mappings contains an unsupported dimension")
		}
		if len(entries) > 1000 {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_mapping", "dimension_mappings contains too many entries")
		}
		cleaned := map[string]string{}
		for source, canonical := range entries {
			source = strings.TrimSpace(source)
			canonical = strings.TrimSpace(canonical)
			if source == "" || canonical == "" {
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_mapping", "dimension mapping keys and values cannot be empty")
			}
			cleaned[source] = canonical
		}
		if len(cleaned) > 0 {
			result[dimension] = cleaned
		}
	}
	return result, nil
}

func reconciliationRuleHash(rule ReconciliationRule) string {
	payload, _ := json.Marshal(struct {
		ConnectorID         string                       `json:"connector_id"`
		ConnectorType       string                       `json:"connector_type"`
		ProviderID          string                       `json:"provider_id"`
		ProviderResourceID  string                       `json:"provider_resource_id"`
		Granularity         string                       `json:"granularity"`
		MatchDimensions     []string                     `json:"match_dimensions"`
		DimensionMappings   map[string]map[string]string `json:"dimension_mappings"`
		AmountTolerance     string                       `json:"amount_tolerance"`
		RatioTolerance      string                       `json:"ratio_tolerance"`
		USDExchangeRate     string                       `json:"usd_exchange_rate"`
		TimeWindowMinutes   int                          `json:"time_window_minutes"`
		BillingDelayMinutes int                          `json:"billing_delay_minutes"`
		Timezone            string                       `json:"timezone"`
		Currency            string                       `json:"currency"`
	}{rule.ConnectorID, rule.ConnectorType, rule.ProviderID, rule.ProviderResourceID, rule.Granularity, rule.MatchDimensions, rule.DimensionMappings, rule.AmountTolerance, rule.RatioTolerance, rule.USDExchangeRate, rule.TimeWindowMinutes, rule.BillingDelayMinutes, rule.Timezone, rule.Currency})
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func reconciliationRunRuleHash(run ReconciliationRun) string {
	return reconciliationRuleHash(ReconciliationRule{
		ConnectorID: run.ConnectorID, ConnectorType: run.ConnectorType,
		ProviderID: run.ProviderID, ProviderResourceID: run.ProviderResourceID,
		Granularity: run.Granularity, MatchDimensions: run.MatchDimensions, DimensionMappings: run.DimensionMappings,
		AmountTolerance: run.AmountTolerance, RatioTolerance: run.RatioTolerance, USDExchangeRate: run.USDExchangeRate,
		TimeWindowMinutes: run.TimeWindowMinutes, BillingDelayMinutes: run.BillingDelayMinutes,
		Timezone: run.Timezone, Currency: run.Currency,
	})
}

func reconciliationRunFromRule(rule ReconciliationRule, request ReconciliationRunRequest, trigger string, actor string) ReconciliationRun {
	return ReconciliationRun{
		ID:                  NewID("recon"),
		RuleID:              rule.ID,
		ConnectorID:         rule.ConnectorID,
		ConnectorType:       rule.ConnectorType,
		ProviderID:          rule.ProviderID,
		ProviderResourceID:  rule.ProviderResourceID,
		Trigger:             firstNonEmpty(trigger, "manual"),
		Status:              ReconciliationRunRunning,
		PeriodStart:         request.PeriodStart.UTC(),
		PeriodEnd:           request.PeriodEnd.UTC(),
		Granularity:         rule.Granularity,
		MatchDimensions:     append([]string(nil), rule.MatchDimensions...),
		DimensionMappings:   rule.DimensionMappings,
		AmountTolerance:     rule.AmountTolerance,
		RatioTolerance:      rule.RatioTolerance,
		USDExchangeRate:     rule.USDExchangeRate,
		TimeWindowMinutes:   rule.TimeWindowMinutes,
		BillingDelayMinutes: rule.BillingDelayMinutes,
		Timezone:            rule.Timezone,
		Currency:            rule.Currency,
		RuleVersion:         rule.Version,
		RuleHash:            rule.RuleHash,
		ProviderAmount:      "0",
		TokenHubAmount:      "0",
		DifferenceAmount:    "0",
		CreatedBy:           strings.TrimSpace(actor),
		StartedAt:           time.Now().UTC(),
	}
}

func scheduledReconciliationPeriod(rule ReconciliationRule, now time.Time) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	cutoff := now.UTC().Add(-time.Duration(rule.BillingDelayMinutes) * time.Minute).In(location)
	var from, to time.Time
	switch rule.Granularity {
	case ReconciliationGranularityMonth:
		to = time.Date(cutoff.Year(), cutoff.Month(), 1, 0, 0, 0, 0, location)
		from = to.AddDate(0, -1, 0)
	case ReconciliationGranularityDay:
		to = time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, location)
		from = to.AddDate(0, 0, -1)
	case ReconciliationGranularityHour:
		to = time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), cutoff.Hour(), 0, 0, 0, location)
		from = to.Add(-time.Hour)
	default:
		to = cutoff
		interval := time.Duration(rule.ScheduleIntervalMinutes) * time.Minute
		if interval <= 0 {
			interval = time.Hour
		}
		from = to.Add(-interval)
	}
	return from.UTC(), to.UTC(), nil
}

func reconciliationContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
