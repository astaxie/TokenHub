package reconciliation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Service struct {
	store         Store
	billing       BillingReader
	mu            sync.Mutex
	active        map[string]bool
	schedulerOnce sync.Once
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

func NewService(store Store, billingReader BillingReader) *Service {
	return &Service{store: store, billing: billingReader, active: map[string]bool{}}
}

func (s *Service) CreateRule(request RuleInput, actor string) (Rule, error) {
	rule := ruleFromInput(request)
	rule.CreatedBy = strings.TrimSpace(actor)
	rule.UpdatedBy = strings.TrimSpace(actor)
	if err := normalizeAndValidateRule(&rule); err != nil {
		return Rule{}, err
	}
	domainConnector, err := s.billing.GetConnectorSnapshot(rule.ConnectorID)
	if err != nil {
		return Rule{}, err
	}
	connector := domainConnector
	if err := snapshotConnector(&rule, connector); err != nil {
		return Rule{}, err
	}
	rule.Version = 1
	rule.RuleHash = ruleHash(rule)
	return s.store.CreateRule(rule)
}

func (s *Service) UpdateRule(id string, request RulePatch, actor string) (Rule, Rule, error) {
	before, err := s.store.GetRule(id)
	if err != nil {
		return Rule{}, Rule{}, err
	}
	rule := before
	applyRulePatch(&rule, request)
	rule.UpdatedBy = strings.TrimSpace(actor)
	if err := normalizeAndValidateRule(&rule); err != nil {
		return before, Rule{}, err
	}
	domainConnector, err := s.billing.GetConnectorSnapshot(rule.ConnectorID)
	if err != nil {
		return before, Rule{}, err
	}
	connector := domainConnector
	if err := snapshotConnector(&rule, connector); err != nil {
		return before, Rule{}, err
	}
	rule.Version = before.Version + 1
	rule.RuleHash = ruleHash(rule)
	updated, err := s.store.UpdateRule(rule)
	return before, updated, err
}

func (s *Service) ensureRuleConnectorSnapshot(rule Rule) (Rule, error) {
	if strings.TrimSpace(rule.ConnectorType) != "" && normalizeScope(rule.ProviderID) != "" {
		return rule, validateConnectorSnapshot(rule.Granularity, rule.ConnectorType, rule.ProviderID)
	}
	domainConnector, err := s.billing.GetConnectorSnapshot(rule.ConnectorID)
	if err != nil {
		return Rule{}, err
	}
	connector := domainConnector
	if err := snapshotConnector(&rule, connector); err != nil {
		return Rule{}, err
	}
	if rule.Version <= 0 {
		rule.Version = 1
	} else {
		rule.Version++
	}
	rule.RuleHash = ruleHash(rule)
	stored, err := s.store.BackfillRuleConnectorSnapshot(rule)
	if err != nil {
		return Rule{}, err
	}
	return stored, validateConnectorSnapshot(stored.Granularity, stored.ConnectorType, stored.ProviderID)
}

func (s *Service) ensureRunConnectorSnapshot(run Run) (Run, error) {
	if strings.TrimSpace(run.ConnectorType) != "" && normalizeScope(run.ProviderID) != "" {
		return run, validateConnectorSnapshot(run.Granularity, run.ConnectorType, run.ProviderID)
	}
	domainConnector, err := s.billing.GetConnectorSnapshot(run.ConnectorID)
	if err != nil {
		return Run{}, err
	}
	connector := domainConnector
	if err := snapshotRunConnector(&run, connector); err != nil {
		return Run{}, err
	}
	run.RuleHash = runRuleHash(run)
	return run, nil
}

func (s *Service) Run(ctx context.Context, ruleID string, request RunInput, trigger string, actor string) (Run, error) {
	if !s.begin("rule:" + ruleID) {
		return Run{}, NewError(ErrorConflict, "reconciliation_in_progress", "A reconciliation is already running for this rule")
	}
	defer s.end("rule:" + ruleID)
	rule, err := s.store.GetRule(ruleID)
	if err != nil {
		return Run{}, err
	}
	if rule.Status != StatusActive {
		return Run{}, NewError(ErrorConflict, "reconciliation_rule_disabled", "Disabled reconciliation rules cannot be run")
	}
	rule, err = s.ensureRuleConnectorSnapshot(rule)
	if err != nil {
		return Run{}, err
	}
	if request.PeriodStart.IsZero() || request.PeriodEnd.IsZero() || !request.PeriodStart.Before(request.PeriodEnd) {
		return Run{}, NewError(ErrorInvalidInput, "invalid_reconciliation_period", "period_start and period_end must define a non-empty range")
	}
	if request.PeriodEnd.Sub(request.PeriodStart) > 366*24*time.Hour {
		return Run{}, NewError(ErrorInvalidInput, "reconciliation_period_too_large", "Reconciliation periods cannot exceed 366 days")
	}
	run := runFromRule(rule, request, trigger, actor)
	return s.calculateAndSave(ctx, run, false)
}

func (s *Service) Recalculate(ctx context.Context, runID string) (Run, error) {
	if !s.begin("run:" + runID) {
		return Run{}, NewError(ErrorConflict, "reconciliation_in_progress", "This reconciliation is already being recalculated")
	}
	defer s.end("run:" + runID)
	run, err := s.store.GetRun(runID)
	if err != nil {
		return Run{}, err
	}
	if err := ValidateRunReplacement(run); err != nil {
		return Run{}, err
	}
	run, err = s.ensureRunConnectorSnapshot(run)
	if err != nil {
		return Run{}, err
	}
	run.Trigger = "recalculation"
	run.Status = RunRunning
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

func (s *Service) calculateAndSave(ctx context.Context, run Run, replace bool) (Run, error) {
	if err := ctx.Err(); err != nil {
		return run, err
	}
	window := time.Duration(run.TimeWindowMinutes) * time.Minute
	err := validateConnectorSnapshot(run.Granularity, run.ConnectorType, run.ProviderID)
	var bills []BillingRecord
	var usages []Usage
	if err == nil {
		bills, err = s.billing.ListRecordsInRange(run.ConnectorID, run.PeriodStart, run.PeriodEnd)
	}
	if err == nil {
		usages, err = s.store.ListUsages(run.PeriodStart, run.PeriodEnd, window)
	}
	if err == nil {
		usages = scopeUsages(run, usages)
		run, varItems, calculateErr := calculate(run, bills, usages)
		err = calculateErr
		if err == nil {
			if replace {
				var saved Run
				saved, err = s.store.ReplaceRun(run, varItems)
				if err == nil {
					return saved, nil
				}
			} else {
				var saved Run
				saved, err = s.store.SaveRun(run, varItems)
				if err == nil {
					return saved, nil
				}
			}
		}
	}
	run = failedRun(run, err)
	if !replace {
		_, _ = s.store.SaveRun(run, nil)
	}
	return run, err
}

func failedRun(run Run, err error) Run {
	code, message := errorDetails(err)
	run.Status = RunFailed
	run.ErrorCode = code
	run.ErrorMessage = message
	finishedAt := time.Now().UTC()
	run.FinishedAt = &finishedAt
	return run
}

func (s *Service) RunDue(ctx context.Context, now time.Time) []Run {
	rules := s.store.ListDueRules(now, 25)
	runs := make([]Run, 0, len(rules))
	for _, rule := range rules {
		from, to, err := scheduledPeriod(rule, now)
		var run Run
		if err == nil {
			run, err = s.Run(ctx, rule.ID, RunInput{PeriodStart: from, PeriodEnd: to}, "scheduled", "system")
		}
		if run.ID == "" && err != nil {
			code, message := errorDetails(err)
			run = Run{RuleID: rule.ID, ConnectorID: rule.ConnectorID, Trigger: "scheduled", Status: RunFailed, ErrorCode: code, ErrorMessage: message}
		}
		s.store.RecordScheduledAudit(run)
		runs = append(runs, run)
		if ctx.Err() != nil {
			break
		}
	}
	return runs
}

func (s *Service) StartScheduler(interval time.Duration) {
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

func (s *Service) Shutdown(ctx context.Context) error {
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

func (s *Service) begin(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[key] {
		return false
	}
	s.active[key] = true
	return true
}

func (s *Service) end(key string) {
	s.mu.Lock()
	delete(s.active, key)
	s.mu.Unlock()
}

func ruleFromInput(request RuleInput) Rule {
	return Rule{
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

func applyRulePatch(rule *Rule, request RulePatch) {
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

func normalizeAndValidateRule(rule *Rule) error {
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
		rule.Granularity = GranularityDetail
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
		if rule.Granularity == GranularityDetail {
			rule.MatchDimensions = []string{"request_id", "model", "currency"}
		} else {
			rule.MatchDimensions = []string{"model", "currency"}
		}
	}
	rule.MatchDimensions = normalizeDimensions(rule.MatchDimensions)
	mappings, mappingErr := normalizeMappings(rule.DimensionMappings)
	if mappingErr != nil {
		return mappingErr
	}
	rule.DimensionMappings = mappings
	if rule.Name == "" || rule.ConnectorID == "" {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_rule", "name and connector_id are required")
	}
	if rule.Status != StatusActive && rule.Status != StatusDisabled {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_status", "status must be active or disabled")
	}
	switch rule.Granularity {
	case GranularityDetail, GranularityHour, GranularityDay, GranularityMonth:
	default:
		return NewError(ErrorInvalidInput, "invalid_reconciliation_granularity", "granularity must be detail, hour, day, or month")
	}
	if len(rule.MatchDimensions) == 0 {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_dimensions", "At least one matching dimension is required")
	}
	if !contains(rule.MatchDimensions, "currency") {
		return NewError(ErrorInvalidInput, "reconciliation_currency_required", "currency must be a matching dimension")
	}
	if rule.Granularity == GranularityDetail && !contains(rule.MatchDimensions, "request_id") {
		return NewError(ErrorInvalidInput, "reconciliation_request_id_required", "detail reconciliation requires the request_id dimension")
	}
	if rule.Granularity != GranularityDetail && contains(rule.MatchDimensions, "request_id") {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_dimensions", "request_id can only be used with detail reconciliation")
	}
	amount, err := parseMoney(rule.AmountTolerance)
	if err != nil || amount < 0 {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_tolerance", "amount_tolerance must be a non-negative decimal")
	}
	ratio, err := parseMoney(rule.RatioTolerance)
	if err != nil || ratio < 0 || ratio > money(moneyScale) {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_tolerance", "ratio_tolerance must be between 0 and 1")
	}
	rule.AmountTolerance = amount.String()
	rule.RatioTolerance = ratio.String()
	exchangeRate, err := parseMoney(rule.USDExchangeRate)
	if err != nil || exchangeRate <= 0 {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_exchange_rate", "usd_exchange_rate must be a positive decimal")
	}
	if rule.Currency == "USD" && exchangeRate != money(moneyScale) {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_exchange_rate", "USD reconciliation must use an exchange rate of 1")
	}
	rule.USDExchangeRate = exchangeRate.String()
	if rule.TimeWindowMinutes < 0 || rule.TimeWindowMinutes > 24*60 || rule.BillingDelayMinutes < 0 || rule.BillingDelayMinutes > 30*24*60 ||
		rule.ScheduleIntervalMinutes < 0 || rule.ScheduleIntervalMinutes > 30*24*60 {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_window", "time, delay, and schedule windows are outside the supported range")
	}
	if _, err := time.LoadLocation(rule.Timezone); err != nil {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_timezone", "timezone must be a valid IANA timezone")
	}
	if !isCurrency(rule.Currency) {
		return NewError(ErrorInvalidInput, "invalid_reconciliation_currency", "currency must be a three-letter ISO code")
	}
	return nil
}

func isCurrency(value string) bool {
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

func normalizeDimensions(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if _, ok := dimensions[value]; ok {
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

func normalizeMappings(values map[string]map[string]string) (map[string]map[string]string, error) {
	result := map[string]map[string]string{}
	for rawDimension, entries := range values {
		dimension := strings.ToLower(strings.TrimSpace(rawDimension))
		switch dimension {
		case "provider", "resource_account", "model", "project":
		default:
			return nil, NewError(ErrorInvalidInput, "invalid_reconciliation_mapping", "dimension_mappings contains an unsupported dimension")
		}
		if len(entries) > 1000 {
			return nil, NewError(ErrorInvalidInput, "invalid_reconciliation_mapping", "dimension_mappings contains too many entries")
		}
		cleaned := map[string]string{}
		for source, canonical := range entries {
			source = strings.TrimSpace(source)
			canonical = strings.TrimSpace(canonical)
			if source == "" || canonical == "" {
				return nil, NewError(ErrorInvalidInput, "invalid_reconciliation_mapping", "dimension mapping keys and values cannot be empty")
			}
			cleaned[source] = canonical
		}
		if len(cleaned) > 0 {
			result[dimension] = cleaned
		}
	}
	return result, nil
}

func ruleHash(rule Rule) string {
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

func runRuleHash(run Run) string {
	return ruleHash(Rule{
		ConnectorID: run.ConnectorID, ConnectorType: run.ConnectorType,
		ProviderID: run.ProviderID, ProviderResourceID: run.ProviderResourceID,
		Granularity: run.Granularity, MatchDimensions: run.MatchDimensions, DimensionMappings: run.DimensionMappings,
		AmountTolerance: run.AmountTolerance, RatioTolerance: run.RatioTolerance, USDExchangeRate: run.USDExchangeRate,
		TimeWindowMinutes: run.TimeWindowMinutes, BillingDelayMinutes: run.BillingDelayMinutes,
		Timezone: run.Timezone, Currency: run.Currency,
	})
}

func runFromRule(rule Rule, request RunInput, trigger string, actor string) Run {
	return Run{
		ID:                  newID("recon"),
		RuleID:              rule.ID,
		ConnectorID:         rule.ConnectorID,
		ConnectorType:       rule.ConnectorType,
		ProviderID:          rule.ProviderID,
		ProviderResourceID:  rule.ProviderResourceID,
		Trigger:             firstNonEmpty(trigger, "manual"),
		Status:              RunRunning,
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

func scheduledPeriod(rule Rule, now time.Time) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	cutoff := now.UTC().Add(-time.Duration(rule.BillingDelayMinutes) * time.Minute).In(location)
	var from, to time.Time
	switch rule.Granularity {
	case GranularityMonth:
		to = time.Date(cutoff.Year(), cutoff.Month(), 1, 0, 0, 0, 0, location)
		from = to.AddDate(0, -1, 0)
	case GranularityDay:
		to = time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, location)
		from = to.AddDate(0, 0, -1)
	case GranularityHour:
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

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func errorDetails(err error) (string, string) {
	if _, code, message, ok := ErrorInfo(err); ok {
		return code, message
	}
	if err == nil {
		return "internal_error", "Internal error"
	}
	return "internal_error", err.Error()
}

func newID(prefix string) string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
