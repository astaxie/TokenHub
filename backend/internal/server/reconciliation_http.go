package server

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) serveAdminReconciliationRulesGet(w http.ResponseWriter, _ *http.Request, _ AdminUser) {
	writeJSON(w, http.StatusOK, map[string]any{"data": newReconciliationRuleResponses(s.store.ListReconciliationRules())})
}

func (s *Server) serveAdminReconciliationRulesPost(w http.ResponseWriter, r *http.Request, user AdminUser) {
	var request ReconciliationRuleRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		if isPayloadTooLarge(err) {
			writeError(w, r, err)
			return
		}
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_rule", "Invalid reconciliation rule payload"))
		return
	}
	rule, err := s.reconciliation.CreateRule(request, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "reconciliation_rule", rule.ID, nil, reconciliationRuleAuditSnapshot(rule))
	writeJSON(w, http.StatusCreated, newReconciliationRuleResponse(rule))
}

func (s *Server) handleAdminReconciliationRuleItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	parts := pathPartsAfter(r.URL.Path, "/api/admin/billing/reconciliation-rules/")
	if len(parts) == 0 || len(parts) > 2 {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "reconciliation_rule_not_found", "Reconciliation rule not found"))
		return
	}
	if len(parts) == 2 {
		if parts[1] != "run" || r.Method != http.MethodPost {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "reconciliation_action_not_found", "Reconciliation action not found"))
			return
		}
		s.serveAdminReconciliationRuleRun(w, r, user, parts[0])
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.serveAdminReconciliationRuleGet(w, r, user, parts[0])
	case http.MethodPatch:
		s.serveAdminReconciliationRulePatch(w, r, user, parts[0])
	default:
		jsonMethodNotAllowed("GET, PATCH")(w, r)
	}
}

func (s *Server) serveAdminReconciliationRuleGet(w http.ResponseWriter, r *http.Request, _ AdminUser, ruleID string) {
	rule, err := s.store.GetReconciliationRule(ruleID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newReconciliationRuleResponse(rule))
}

func (s *Server) serveAdminReconciliationRulePatch(w http.ResponseWriter, r *http.Request, user AdminUser, ruleID string) {
	var request ReconciliationRulePatchRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		if isPayloadTooLarge(err) {
			writeError(w, r, err)
			return
		}
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_rule", "Invalid reconciliation rule payload"))
		return
	}
	before, updated, err := s.reconciliation.UpdateRule(ruleID, request, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "reconciliation_rule", updated.ID, reconciliationRuleAuditSnapshot(before), reconciliationRuleAuditSnapshot(updated))
	writeJSON(w, http.StatusOK, newReconciliationRuleResponse(updated))
}

func (s *Server) serveAdminReconciliationRuleRun(w http.ResponseWriter, r *http.Request, user AdminUser, ruleID string) {
	var request ReconciliationRunRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		httpErr := NewHTTPError(http.StatusBadRequest, "invalid_reconciliation_run", "Invalid reconciliation run payload")
		if isPayloadTooLarge(err) {
			httpErr = AsHTTPError(err)
		}
		s.recordAdminAuditWithStatus(r, user, "reconcile", "billing_reconciliation", ruleID, "failed", httpErr.Code, nil, map[string]any{"rule_id": ruleID, "error_code": httpErr.Code})
		writeError(w, r, httpErr)
		return
	}
	run, err := s.reconciliation.Run(r.Context(), ruleID, request, "manual", user.ID)
	if err != nil {
		httpErr := AsHTTPError(err)
		resourceID := ruleID
		after := any(map[string]any{"rule_id": ruleID, "error_code": httpErr.Code})
		if run.ID != "" {
			resourceID = run.ID
			after = reconciliationAuditSnapshot(run)
		}
		s.recordAdminAuditWithStatus(r, user, "reconcile", "billing_reconciliation", resourceID, "failed", httpErr.Code, nil, after)
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "reconcile", "billing_reconciliation", run.ID, nil, reconciliationAuditSnapshot(run))
	writeJSON(w, http.StatusCreated, newReconciliationRunResponse(run))
}

func (s *Server) serveAdminReconciliationsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": newReconciliationRunResponses(s.store.ListReconciliationRuns(r.URL.Query().Get("rule_id"), reconciliationListLimit(r, 100, 500)))})
}

func (s *Server) handleAdminReconciliationItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	parts := pathPartsAfter(r.URL.Path, "/api/admin/billing/reconciliations/")
	if len(parts) == 0 || len(parts) > 2 {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "reconciliation_run_not_found", "Reconciliation run not found"))
		return
	}
	runID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			jsonMethodNotAllowed(http.MethodGet)(w, r)
			return
		}
		s.serveAdminReconciliationGet(w, r, user, runID)
		return
	}
	switch parts[1] {
	case "lock":
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.serveAdminReconciliationLock(w, r, user, runID)
	case "recalculate":
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.serveAdminReconciliationRecalculate(w, r, user, runID)
	case "export":
		if r.Method != http.MethodGet {
			jsonMethodNotAllowed(http.MethodGet)(w, r)
			return
		}
		s.serveAdminReconciliationExport(w, r, user, runID)
	default:
		writeError(w, r, NewHTTPError(http.StatusNotFound, "reconciliation_action_not_found", "Reconciliation action not found"))
	}
}

func (s *Server) serveAdminReconciliationGet(w http.ResponseWriter, r *http.Request, _ AdminUser, runID string) {
	run, err := s.store.GetReconciliationRun(runID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit := reconciliationListLimit(r, 100, 500)
	offset := reconciliationListOffset(r)
	items, total := s.store.ListReconciliationItems(runID, r.URL.Query().Get("status"), limit, offset)
	writeJSON(w, http.StatusOK, newReconciliationDetailResponse(ReconciliationDetail{Run: run, Items: items, Total: total, Limit: limit, Offset: offset}))
}

func (s *Server) serveAdminReconciliationLock(w http.ResponseWriter, r *http.Request, user AdminUser, runID string) {
	before, err := s.store.GetReconciliationRun(runID)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordAdminAuditWithStatus(r, user, "lock", "billing_reconciliation", runID, "failed", httpErr.Code, nil, map[string]any{"id": runID, "error_code": httpErr.Code})
		writeError(w, r, err)
		return
	}
	run, err := s.store.LockReconciliationRun(runID, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "lock", "billing_reconciliation", run.ID, reconciliationAuditSnapshot(before), reconciliationAuditSnapshot(run))
	writeJSON(w, http.StatusOK, newReconciliationRunResponse(run))
}

func (s *Server) serveAdminReconciliationRecalculate(w http.ResponseWriter, r *http.Request, user AdminUser, runID string) {
	before, err := s.store.GetReconciliationRun(runID)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordAdminAuditWithStatus(r, user, "recalculate", "billing_reconciliation", runID, "failed", httpErr.Code, nil, map[string]any{"id": runID, "error_code": httpErr.Code})
		writeError(w, r, err)
		return
	}
	run, err := s.reconciliation.Recalculate(r.Context(), runID)
	if err != nil {
		httpErr := AsHTTPError(err)
		after := any(map[string]any{"id": runID, "error_code": httpErr.Code})
		if run.ID != "" {
			after = reconciliationAuditSnapshot(run)
		}
		s.recordAdminAuditWithStatus(r, user, "recalculate", "billing_reconciliation", runID, "failed", httpErr.Code, reconciliationAuditSnapshot(before), after)
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "recalculate", "billing_reconciliation", run.ID, reconciliationAuditSnapshot(before), reconciliationAuditSnapshot(run))
	writeJSON(w, http.StatusOK, newReconciliationRunResponse(run))
}

func (s *Server) serveAdminReconciliationExport(w http.ResponseWriter, r *http.Request, user AdminUser, runID string) {
	run, err := s.store.GetReconciliationRun(runID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	s.recordAdminAudit(r, user, "export", "billing_reconciliation", run.ID, nil, reconciliationAuditSnapshot(run))
	s.writeReconciliationCSV(w, run, status)
}

func pathPartsAfter(path string, prefix string) []string {
	trimmed := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func reconciliationListLimit(r *http.Request, fallback int, maximum int) int {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > maximum {
		return fallback
	}
	return limit
}

func reconciliationListOffset(r *http.Request) int {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		return 0
	}
	return offset
}

func (s *Server) writeReconciliationCSV(w http.ResponseWriter, run ReconciliationRun, status string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="reconciliation-%s.csv"`, run.ID))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{
		"status", "bucket_start", "bucket_end", "request_id", "provider", "resource_account", "model", "project", "currency",
		"provider_amount", "tokenhub_amount", "difference_amount", "difference_ratio", "possible_reason", "provider_record_ids", "tokenhub_record_ids",
	})
	for afterID := ""; ; {
		items := s.store.ListReconciliationItemBatch(run.ID, status, afterID, status == "", 500)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			_ = writer.Write([]string{
				safeReconciliationCSVCell(item.Status), item.BucketStart.Format("2006-01-02T15:04:05Z07:00"), item.BucketEnd.Format("2006-01-02T15:04:05Z07:00"),
				safeReconciliationCSVCell(item.RequestID), safeReconciliationCSVCell(item.Provider), safeReconciliationCSVCell(item.ResourceAccountMasked),
				safeReconciliationCSVCell(item.Model), safeReconciliationCSVCell(item.Project), safeReconciliationCSVCell(item.Currency),
				item.ProviderAmount, item.TokenHubAmount, item.DifferenceAmount, item.DifferenceRatio, safeReconciliationCSVCell(item.PossibleReason),
				safeReconciliationCSVCell(strings.Join(item.ProviderRecordIDs, "|")), safeReconciliationCSVCell(strings.Join(item.TokenHubRecordIDs, "|")),
			})
		}
		afterID = items[len(items)-1].ID
		writer.Flush()
	}
	writer.Flush()
}

func safeReconciliationCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
