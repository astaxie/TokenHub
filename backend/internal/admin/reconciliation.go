package admin

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

type ReconciliationTransport struct {
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) error
	IsPayloadTooLarge func(error) bool
	ErrorCode         func(error) string
	NewError          func(int, string, string) error
	MapError          func(error) error
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, error)
	Audit             func(*http.Request, AdminActor, AdminAudit)
}
type ReconciliationHandler struct {
	service   ReconciliationApplication
	transport ReconciliationTransport
}

// ReconciliationApplication is the public behavior the HTTP adapter consumes.
// The interface lives here so transport needs cannot widen the domain package.
type ReconciliationApplication interface {
	ListRules() []reconciliation.Rule
	GetRuleForAdmin(string) (reconciliation.Rule, error)
	CreateRule(reconciliation.RuleInput, string) (reconciliation.Rule, error)
	UpdateRule(string, reconciliation.RulePatch, string) (reconciliation.Rule, reconciliation.Rule, error)
	Run(context.Context, string, reconciliation.RunInput, string, string) (reconciliation.Run, error)
	ListRuns(string, int) []reconciliation.Run
	GetRunDetail(string, string, int, int) (reconciliation.Run, []reconciliation.Item, int64, error)
	Lock(string, string) (reconciliation.Run, reconciliation.Run, error)
	RecalculateForAdmin(context.Context, string) (reconciliation.Run, reconciliation.Run, error)
	Export(string, string, int, func(reconciliation.Run) error, func([]reconciliation.Item) error) error
}

// NewReconciliationHandler creates the transport adapter. The application
// interface keeps execution policy in the reconciliation package.
func NewReconciliationHandler(service ReconciliationApplication, transport ReconciliationTransport) *ReconciliationHandler {
	return &ReconciliationHandler{service: service, transport: transport}
}

type reconciliationRuleRequest struct {
	Name                    string                       `json:"name"`
	ConnectorID             string                       `json:"connector_id"`
	Status                  string                       `json:"status"`
	Granularity             string                       `json:"granularity"`
	MatchDimensions         []string                     `json:"match_dimensions"`
	DimensionMappings       map[string]map[string]string `json:"dimension_mappings"`
	AmountTolerance         string                       `json:"amount_tolerance"`
	RatioTolerance          string                       `json:"ratio_tolerance"`
	USDExchangeRate         string                       `json:"usd_exchange_rate"`
	TimeWindowMinutes       int                          `json:"time_window_minutes"`
	BillingDelayMinutes     int                          `json:"billing_delay_minutes"`
	ScheduleIntervalMinutes int                          `json:"schedule_interval_minutes"`
	Timezone                string                       `json:"timezone"`
	Currency                string                       `json:"currency"`
}
type reconciliationRulePatchRequest struct {
	Name                    *string                       `json:"name"`
	Status                  *string                       `json:"status"`
	Granularity             *string                       `json:"granularity"`
	MatchDimensions         *[]string                     `json:"match_dimensions"`
	DimensionMappings       *map[string]map[string]string `json:"dimension_mappings"`
	AmountTolerance         *string                       `json:"amount_tolerance"`
	RatioTolerance          *string                       `json:"ratio_tolerance"`
	USDExchangeRate         *string                       `json:"usd_exchange_rate"`
	TimeWindowMinutes       *int                          `json:"time_window_minutes"`
	BillingDelayMinutes     *int                          `json:"billing_delay_minutes"`
	ScheduleIntervalMinutes *int                          `json:"schedule_interval_minutes"`
	Timezone                *string                       `json:"timezone"`
	Currency                *string                       `json:"currency"`
}
type reconciliationRunRequest struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

type reconciliationRuleResponse struct {
	ID                      string                       `json:"id"`
	Name                    string                       `json:"name"`
	ConnectorID             string                       `json:"connector_id"`
	ConnectorType           string                       `json:"connector_type"`
	ProviderID              string                       `json:"provider_id"`
	Status                  string                       `json:"status"`
	Granularity             string                       `json:"granularity"`
	MatchDimensions         []string                     `json:"match_dimensions"`
	DimensionMappings       map[string]map[string]string `json:"dimension_mappings,omitempty"`
	AmountTolerance         string                       `json:"amount_tolerance"`
	RatioTolerance          string                       `json:"ratio_tolerance"`
	USDExchangeRate         string                       `json:"usd_exchange_rate"`
	TimeWindowMinutes       int                          `json:"time_window_minutes"`
	BillingDelayMinutes     int                          `json:"billing_delay_minutes"`
	ScheduleIntervalMinutes int                          `json:"schedule_interval_minutes"`
	Timezone                string                       `json:"timezone"`
	Currency                string                       `json:"currency,omitempty"`
	Version                 int                          `json:"version"`
	RuleHash                string                       `json:"rule_hash"`
	CreatedBy               string                       `json:"created_by"`
	UpdatedBy               string                       `json:"updated_by"`
	LastRunAt               *time.Time                   `json:"last_run_at,omitempty"`
	NextRunAt               *time.Time                   `json:"next_run_at,omitempty"`
	CreatedAt               time.Time                    `json:"created_at"`
	UpdatedAt               time.Time                    `json:"updated_at"`
}
type reconciliationRunResponse struct {
	ID                  string                       `json:"id"`
	RuleID              string                       `json:"rule_id"`
	ConnectorID         string                       `json:"connector_id"`
	ConnectorType       string                       `json:"connector_type"`
	ProviderID          string                       `json:"provider_id"`
	Status              string                       `json:"status"`
	Trigger             string                       `json:"trigger"`
	PeriodStart         time.Time                    `json:"period_start"`
	PeriodEnd           time.Time                    `json:"period_end"`
	Granularity         string                       `json:"granularity"`
	MatchDimensions     []string                     `json:"match_dimensions"`
	DimensionMappings   map[string]map[string]string `json:"dimension_mappings,omitempty"`
	AmountTolerance     string                       `json:"amount_tolerance"`
	RatioTolerance      string                       `json:"ratio_tolerance"`
	USDExchangeRate     string                       `json:"usd_exchange_rate"`
	TimeWindowMinutes   int                          `json:"time_window_minutes"`
	BillingDelayMinutes int                          `json:"billing_delay_minutes"`
	Timezone            string                       `json:"timezone"`
	Currency            string                       `json:"currency,omitempty"`
	RuleVersion         int                          `json:"rule_version"`
	RuleHash            string                       `json:"rule_hash"`
	InputHash           string                       `json:"input_hash"`
	ProviderRecordCount int                          `json:"provider_record_count"`
	TokenHubRecordCount int                          `json:"tokenhub_record_count"`
	MatchedCount        int                          `json:"matched_count"`
	ProviderOnlyCount   int                          `json:"provider_only_count"`
	TokenHubOnlyCount   int                          `json:"tokenhub_only_count"`
	AmountMismatchCount int                          `json:"amount_mismatch_count"`
	ProviderAmount      string                       `json:"provider_amount"`
	TokenHubAmount      string                       `json:"tokenhub_amount"`
	DifferenceAmount    string                       `json:"difference_amount"`
	CreatedBy           string                       `json:"created_by"`
	StartedAt           time.Time                    `json:"started_at"`
	FinishedAt          *time.Time                   `json:"finished_at,omitempty"`
	LockedAt            *time.Time                   `json:"locked_at,omitempty"`
	LockedBy            string                       `json:"locked_by,omitempty"`
	ErrorCode           string                       `json:"error_code,omitempty"`
	ErrorMessage        string                       `json:"error_message,omitempty"`
}
type reconciliationDetailResponse struct {
	Run    reconciliationRunResponse    `json:"run"`
	Items  []reconciliationItemResponse `json:"items"`
	Total  int64                        `json:"total"`
	Limit  int                          `json:"limit"`
	Offset int                          `json:"offset"`
}
type reconciliationItemResponse struct {
	ID                    string    `json:"id"`
	RunID                 string    `json:"run_id"`
	MatchKey              string    `json:"match_key"`
	Status                string    `json:"status"`
	BucketStart           time.Time `json:"bucket_start"`
	BucketEnd             time.Time `json:"bucket_end"`
	RequestID             string    `json:"request_id,omitempty"`
	Provider              string    `json:"provider,omitempty"`
	ResourceAccountMasked string    `json:"resource_account,omitempty"`
	Model                 string    `json:"model,omitempty"`
	Project               string    `json:"project,omitempty"`
	Currency              string    `json:"currency"`
	ProviderAmount        string    `json:"provider_amount"`
	TokenHubAmount        string    `json:"tokenhub_amount"`
	DifferenceAmount      string    `json:"difference_amount"`
	DifferenceRatio       string    `json:"difference_ratio"`
	PossibleReason        string    `json:"possible_reason"`
	ProviderRecordIDs     []string  `json:"provider_record_ids"`
	TokenHubRecordIDs     []string  `json:"tokenhub_record_ids"`
	CreatedAt             time.Time `json:"created_at"`
}

func itemResponse(v reconciliation.Item) reconciliationItemResponse {
	return reconciliationItemResponse{ID: v.ID, RunID: v.RunID, MatchKey: v.MatchKey, Status: v.Status, BucketStart: v.BucketStart, BucketEnd: v.BucketEnd, RequestID: v.RequestID, Provider: v.Provider, ResourceAccountMasked: v.ResourceAccountMasked, Model: v.Model, Project: v.Project, Currency: v.Currency, ProviderAmount: v.ProviderAmount, TokenHubAmount: v.TokenHubAmount, DifferenceAmount: v.DifferenceAmount, DifferenceRatio: v.DifferenceRatio, PossibleReason: v.PossibleReason, ProviderRecordIDs: v.ProviderRecordIDs, TokenHubRecordIDs: v.TokenHubRecordIDs, CreatedAt: v.CreatedAt}
}

func ruleResponse(v reconciliation.Rule) reconciliationRuleResponse {
	return reconciliationRuleResponse{ID: v.ID, Name: v.Name, ConnectorID: v.ConnectorID, ConnectorType: v.ConnectorType, ProviderID: v.ProviderID, Status: v.Status, Granularity: v.Granularity, MatchDimensions: v.MatchDimensions, DimensionMappings: publicMappings(v.DimensionMappings), AmountTolerance: v.AmountTolerance, RatioTolerance: v.RatioTolerance, USDExchangeRate: v.USDExchangeRate, TimeWindowMinutes: v.TimeWindowMinutes, BillingDelayMinutes: v.BillingDelayMinutes, ScheduleIntervalMinutes: v.ScheduleIntervalMinutes, Timezone: v.Timezone, Currency: v.Currency, Version: v.Version, RuleHash: v.RuleHash, CreatedBy: v.CreatedBy, UpdatedBy: v.UpdatedBy, LastRunAt: v.LastRunAt, NextRunAt: v.NextRunAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func runResponse(v reconciliation.Run) reconciliationRunResponse {
	v.DimensionMappings = publicMappings(v.DimensionMappings)
	v.ProviderResourceID = ""
	return reconciliationRunResponse{ID: v.ID, RuleID: v.RuleID, ConnectorID: v.ConnectorID, ConnectorType: v.ConnectorType, ProviderID: v.ProviderID, Status: v.Status, Trigger: v.Trigger, PeriodStart: v.PeriodStart, PeriodEnd: v.PeriodEnd, Granularity: v.Granularity, MatchDimensions: v.MatchDimensions, DimensionMappings: v.DimensionMappings, AmountTolerance: v.AmountTolerance, RatioTolerance: v.RatioTolerance, USDExchangeRate: v.USDExchangeRate, TimeWindowMinutes: v.TimeWindowMinutes, BillingDelayMinutes: v.BillingDelayMinutes, Timezone: v.Timezone, Currency: v.Currency, RuleVersion: v.RuleVersion, RuleHash: v.RuleHash, InputHash: v.InputHash, ProviderRecordCount: v.ProviderRecordCount, TokenHubRecordCount: v.TokenHubRecordCount, MatchedCount: v.MatchedCount, ProviderOnlyCount: v.ProviderOnlyCount, TokenHubOnlyCount: v.TokenHubOnlyCount, AmountMismatchCount: v.AmountMismatchCount, ProviderAmount: v.ProviderAmount, TokenHubAmount: v.TokenHubAmount, DifferenceAmount: v.DifferenceAmount, CreatedBy: v.CreatedBy, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, LockedAt: v.LockedAt, LockedBy: v.LockedBy, ErrorCode: v.ErrorCode, ErrorMessage: v.ErrorMessage}
}
func publicMappings(values map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for dimension, entries := range values {
		if dimension != "provider" && dimension != "model" && dimension != "project" {
			continue
		}
		out[dimension] = map[string]string{}
		for k, v := range entries {
			out[dimension][k] = v
		}
	}
	return out
}
func (h *ReconciliationHandler) ListRules(w http.ResponseWriter, _ *http.Request, _ AdminActor) {
	rules := h.service.ListRules()
	out := make([]reconciliationRuleResponse, len(rules))
	for i := range rules {
		out[i] = ruleResponse(rules[i])
	}
	h.transport.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (h *ReconciliationHandler) CreateRule(w http.ResponseWriter, r *http.Request, actor AdminActor) {
	var req reconciliationRuleRequest
	if err := h.transport.DecodeJSON(w, r, &req); err != nil {
		h.badJSON(w, r, err, "invalid_reconciliation_rule")
		return
	}
	v, err := h.service.CreateRule(reconciliation.RuleInput(req), actor.ID)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	response := ruleResponse(v)
	h.audit(r, actor, "create", "reconciliation_rule", v.ID, "success", ruleAuditSnapshot(v), nil)
	h.transport.WriteJSON(w, http.StatusCreated, response)
}
func (h *ReconciliationHandler) GetRule(w http.ResponseWriter, r *http.Request, _ AdminActor, id string) {
	v, err := h.service.GetRuleForAdmin(id)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.transport.WriteJSON(w, http.StatusOK, ruleResponse(v))
}
func (h *ReconciliationHandler) PatchRule(w http.ResponseWriter, r *http.Request, actor AdminActor, id string) {
	var req reconciliationRulePatchRequest
	if err := h.transport.DecodeJSON(w, r, &req); err != nil {
		h.badJSON(w, r, err, "invalid_reconciliation_rule")
		return
	}
	before, after, err := h.service.UpdateRule(id, reconciliation.RulePatch(req), actor.ID)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.audit(r, actor, "update", "reconciliation_rule", after.ID, "success", ruleAuditSnapshot(after), ruleAuditSnapshot(before))
	h.transport.WriteJSON(w, http.StatusOK, ruleResponse(after))
}
func (h *ReconciliationHandler) RunRule(w http.ResponseWriter, r *http.Request, actor AdminActor, id string) {
	var req reconciliationRunRequest
	if err := h.transport.DecodeJSON(w, r, &req); err != nil {
		code := "invalid_reconciliation_run"
		if h.transport.IsPayloadTooLarge != nil && h.transport.IsPayloadTooLarge(err) && h.transport.ErrorCode != nil {
			if mapped := strings.TrimSpace(h.transport.ErrorCode(err)); mapped != "" {
				code = mapped
			}
		}
		if h.transport.Audit != nil {
			h.transport.Audit(r, actor, AdminAudit{Action: "reconcile", ResourceType: "billing_reconciliation", ResourceID: id, Status: "failed", Message: code, After: map[string]any{"rule_id": id, "error_code": code}})
		}
		h.badJSON(w, r, err, code)
		return
	}
	run, err := h.service.Run(r.Context(), id, reconciliation.RunInput(req), "manual", actor.ID)
	if err != nil {
		resourceID := id
		after := any(map[string]any{"rule_id": id, "error_code": reconciliationErrorCode(err)})
		if run.ID != "" {
			resourceID = run.ID
			after = reconciliation.RunAuditSnapshot(run)
		}
		h.failureAudit(r, actor, "reconcile", resourceID, err, nil, after)
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.audit(r, actor, "reconcile", "billing_reconciliation", run.ID, "success", reconciliation.RunAuditSnapshot(run), nil)
	h.transport.WriteJSON(w, http.StatusCreated, runResponse(run))
}
func (h *ReconciliationHandler) ListRuns(w http.ResponseWriter, r *http.Request, _ AdminActor) {
	h.transport.WriteJSON(w, http.StatusOK, map[string]any{"data": h.runs(r.URL.Query().Get("rule_id"), reconciliationListLimit(r, 100, 500))})
}
func (h *ReconciliationHandler) GetRun(w http.ResponseWriter, r *http.Request, _ AdminActor, id string) {
	limit := reconciliationListLimit(r, 100, 500)
	offset := listOffset(r)
	run, items, total, err := h.service.GetRunDetail(id, r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	responses := make([]reconciliationItemResponse, len(items))
	for i := range items {
		responses[i] = itemResponse(items[i])
	}
	h.transport.WriteJSON(w, http.StatusOK, reconciliationDetailResponse{Run: runResponse(run), Items: responses, Total: total, Limit: limit, Offset: offset})
}
func (h *ReconciliationHandler) LockRun(w http.ResponseWriter, r *http.Request, actor AdminActor, id string) {
	before, run, err := h.service.Lock(id, actor.ID)
	if err != nil {
		if before.ID == "" {
			h.failAudit(w, r, actor, "lock", id, err, map[string]any{"id": id, "error_code": reconciliationErrorCode(err)})
			return
		}
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.audit(r, actor, "lock", "billing_reconciliation", id, "success", reconciliation.RunAuditSnapshot(run), reconciliation.RunAuditSnapshot(before))
	h.transport.WriteJSON(w, http.StatusOK, runResponse(run))
}
func (h *ReconciliationHandler) Recalculate(w http.ResponseWriter, r *http.Request, actor AdminActor, id string) {
	before, run, err := h.service.RecalculateForAdmin(r.Context(), id)
	if err != nil {
		if before.ID == "" {
			h.failAudit(w, r, actor, "recalculate", id, err, map[string]any{"id": id, "error_code": reconciliationErrorCode(err)})
			return
		}
		after := any(map[string]any{"id": id, "error_code": reconciliationErrorCode(err)})
		if run.ID != "" {
			after = reconciliation.RunAuditSnapshot(run)
		}
		h.failureAudit(r, actor, "recalculate", id, err, reconciliation.RunAuditSnapshot(before), after)
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.audit(r, actor, "recalculate", "billing_reconciliation", id, "success", reconciliation.RunAuditSnapshot(run), reconciliation.RunAuditSnapshot(before))
	h.transport.WriteJSON(w, http.StatusOK, runResponse(run))
}
func (h *ReconciliationHandler) Export(w http.ResponseWriter, r *http.Request, actor AdminActor, id string) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	var writer *csv.Writer
	err := h.service.Export(id, status, 500, func(run reconciliation.Run) error {
		h.audit(r, actor, "export", "billing_reconciliation", id, "success", reconciliation.RunAuditSnapshot(run), nil)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="reconciliation-%s.csv"`, run.ID))
		_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
		writer = csv.NewWriter(w)
		_ = writer.Write([]string{"status", "bucket_start", "bucket_end", "request_id", "provider", "resource_account", "model", "project", "currency", "provider_amount", "tokenhub_amount", "difference_amount", "difference_ratio", "possible_reason", "provider_record_ids", "tokenhub_record_ids"})
		return nil
	}, func(items []reconciliation.Item) error {
		for _, item := range items {
			_ = writer.Write([]string{safeCSV(item.Status), item.BucketStart.Format(time.RFC3339), item.BucketEnd.Format(time.RFC3339), safeCSV(item.RequestID), safeCSV(item.Provider), safeCSV(item.ResourceAccountMasked), safeCSV(item.Model), safeCSV(item.Project), safeCSV(item.Currency), item.ProviderAmount, item.TokenHubAmount, item.DifferenceAmount, item.DifferenceRatio, safeCSV(item.PossibleReason), safeCSV(strings.Join(item.ProviderRecordIDs, "|")), safeCSV(strings.Join(item.TokenHubRecordIDs, "|"))})
		}
		writer.Flush()
		return nil
	})
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	if writer != nil {
		writer.Flush()
	}
}
func (h *ReconciliationHandler) runs(ruleID string, limit int) []any {
	values := h.service.ListRuns(ruleID, limit)
	out := make([]any, len(values))
	for i := range values {
		out[i] = runResponse(values[i])
	}
	return out
}
func (h *ReconciliationHandler) badJSON(w http.ResponseWriter, r *http.Request, err error, code string) {
	if h.transport.IsPayloadTooLarge(err) {
		h.transport.WriteError(w, r, err)
		return
	}
	message := "Invalid reconciliation rule payload"
	if code == "invalid_reconciliation_run" {
		message = "Invalid reconciliation run payload"
	}
	h.transport.WriteError(w, r, h.transport.NewError(http.StatusBadRequest, code, message))
}
func (h *ReconciliationHandler) audit(r *http.Request, actor AdminActor, action, resource, id, status string, after, before any) {
	if h.transport.Audit != nil {
		h.transport.Audit(r, actor, AdminAudit{Action: action, ResourceType: resource, ResourceID: id, Status: status, Message: "", After: after, Before: before})
	}
}
func (h *ReconciliationHandler) failureAudit(r *http.Request, actor AdminActor, action, id string, err error, before, after any) {
	if h.transport.Audit != nil {
		code := reconciliationErrorCode(err)
		h.transport.Audit(r, actor, AdminAudit{Action: action, ResourceType: reconciliationResourceType(action), ResourceID: id, Status: "failed", Message: code, Before: before, After: after})
	}
}
func (h *ReconciliationHandler) failAudit(w http.ResponseWriter, r *http.Request, actor AdminActor, action, id string, err error, after any) {
	if h.transport.Audit != nil {
		h.transport.Audit(r, actor, AdminAudit{Action: action, ResourceType: reconciliationResourceType(action), ResourceID: id, Status: "failed", Message: reconciliationErrorCode(err), After: after})
	}
	h.transport.WriteError(w, r, h.transport.MapError(err))
}

func reconciliationResourceType(action string) string {
	if action == "create" || action == "update" {
		return "reconciliation_rule"
	}
	return "billing_reconciliation"
}

func ruleAuditSnapshot(rule reconciliation.Rule) map[string]any {
	mappings := map[string]map[string]string{}
	resourceMappingCount := 0
	for dimension, entries := range rule.DimensionMappings {
		if dimension == "resource_account" {
			resourceMappingCount = len(entries)
			continue
		}
		copied := make(map[string]string, len(entries))
		for source, canonical := range entries {
			copied[source] = canonical
		}
		mappings[dimension] = copied
	}
	return map[string]any{
		"id": rule.ID, "name": rule.Name, "connector_id": rule.ConnectorID,
		"connector_type": rule.ConnectorType, "provider_scope_configured": rule.ProviderID != "",
		"provider_resource_scope_configured": rule.ProviderResourceID != "", "status": rule.Status,
		"granularity": rule.Granularity, "match_dimensions": rule.MatchDimensions,
		"dimension_mappings": mappings, "resource_account_mapping_count": resourceMappingCount,
		"amount_tolerance": rule.AmountTolerance, "ratio_tolerance": rule.RatioTolerance,
		"usd_exchange_rate": rule.USDExchangeRate, "time_window_minutes": rule.TimeWindowMinutes,
		"billing_delay_minutes": rule.BillingDelayMinutes, "schedule_interval_minutes": rule.ScheduleIntervalMinutes,
		"timezone": rule.Timezone, "currency": rule.Currency, "version": rule.Version, "rule_hash": rule.RuleHash,
		"created_by": rule.CreatedBy, "updated_by": rule.UpdatedBy,
	}
}

func reconciliationErrorCode(err error) string {
	if _, code, _, ok := reconciliation.ErrorInfo(err); ok {
		return code
	}
	return "internal_error"
}
func reconciliationListLimit(r *http.Request, fallback, max int) int {
	v, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if v <= 0 || v > max {
		return fallback
	}
	return v
}
func listOffset(r *http.Request) int {
	v, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if v < 0 {
		return 0
	}
	return v
}
func safeCSV(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
