package server

import (
	"net/http"
	"strings"
)

func (s *Server) registerReconciliationRoutes() {
	reconciliationMethodNotAllowed := func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("billing", allowedMethods)
	}
	s.registerMethodRoutes("/api/admin/billing/reconciliation-rules", reconciliationMethodNotAllowed,
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminReconciliationRulesGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminReconciliationRulesPost},
	)
	s.registerMethodRoutes("/api/admin/billing/reconciliation-rules/{rule_id}", s.adminReconciliationRuleMethodNotAllowed,
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminReconciliationRuleGet},
		methodRoute{Method: http.MethodPatch, Handler: s.handleAdminReconciliationRulePatch},
	)
	// The legacy nested handler deliberately reports a non-POST /run as a 404.
	// Register only the successful method so its fallback retains that contract.
	s.registerDynamicMethodRoute(http.MethodPost, "/api/admin/billing/reconciliation-rules/{rule_id}/run", s.handleAdminReconciliationRuleRunPost)
	s.mux.HandleFunc("/api/admin/billing/reconciliation-rules/", s.handleAdminReconciliationRuleItem)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/billing/reconciliations", s.handleAdminReconciliationsGet, s.adminMethodNotAllowed("billing", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/billing/reconciliations/{run_id}", s.handleAdminReconciliationGet, s.adminReconciliationRunMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/billing/reconciliations/{run_id}/lock", s.handleAdminReconciliationLockPost, s.adminReconciliationRunMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/billing/reconciliations/{run_id}/recalculate", s.handleAdminReconciliationRecalculatePost, s.adminReconciliationRunMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/billing/reconciliations/{run_id}/export", s.handleAdminReconciliationExportGet, s.adminReconciliationRunMethodNotAllowed(http.MethodGet))
	s.mux.HandleFunc("/api/admin/billing/reconciliations/", s.handleAdminReconciliationItem)
}
func (s *Server) adminReconciliationRuleMethodNotAllowed(m string) http.HandlerFunc {
	reject := s.adminMethodNotAllowed("billing", m)
	return func(w http.ResponseWriter, r *http.Request) {
		if ruleID := r.PathValue("rule_id"); ruleID == "" || strings.Contains(ruleID, "/") {
			s.handleAdminReconciliationRuleItem(w, r)
			return
		}
		reject(w, r)
	}
}
func (s *Server) adminReconciliationRunMethodNotAllowed(m string) http.HandlerFunc {
	reject := s.adminMethodNotAllowed("billing", m)
	return func(w http.ResponseWriter, r *http.Request) {
		if runID := r.PathValue("run_id"); runID == "" || strings.Contains(runID, "/") {
			s.handleAdminReconciliationItem(w, r)
			return
		}
		reject(w, r)
	}
}
func (s *Server) handleAdminReconciliationRulesGet(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.requireAdmin(w, r, "billing", r.Method); ok {
		if !s.requireReconciliationReadComposition(w, r) {
			return
		}
		s.reconciliationAdmin.ListRules(w, r, billingActor(u))
	}
}
func (s *Server) handleAdminReconciliationRulesPost(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.requireAdmin(w, r, "billing", r.Method); ok {
		if !s.requireReconciliationExecutionComposition(w, r) {
			return
		}
		s.reconciliationAdmin.CreateRule(w, r, billingActor(u))
	}
}
func (s *Server) handleAdminReconciliationRuleGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRuleRoute(w, r, s.requireReconciliationReadComposition, func(u AdminUser, id string) { s.reconciliationAdmin.GetRule(w, r, billingActor(u), id) })
}
func (s *Server) handleAdminReconciliationRulePatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRuleRoute(w, r, s.requireReconciliationExecutionComposition, func(u AdminUser, id string) { s.reconciliationAdmin.PatchRule(w, r, billingActor(u), id) })
}
func (s *Server) handleAdminReconciliationRuleRunPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRuleRoute(w, r, s.requireReconciliationExecutionComposition, func(u AdminUser, id string) { s.reconciliationAdmin.RunRule(w, r, billingActor(u), id) })
}
func (s *Server) handleAdminReconciliationRuleRoute(w http.ResponseWriter, r *http.Request, require func(http.ResponseWriter, *http.Request) bool, f func(AdminUser, string)) {
	id := r.PathValue("rule_id")
	if id == "" || strings.Contains(id, "/") {
		s.handleAdminReconciliationRuleItem(w, r)
		return
	}
	if u, ok := s.requireAdmin(w, r, "billing", r.Method); ok {
		if !require(w, r) {
			return
		}
		f(u, id)
	}
}
func (s *Server) handleAdminReconciliationsGet(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.requireAdmin(w, r, "billing", r.Method); ok {
		if !s.requireReconciliationReadComposition(w, r) {
			return
		}
		s.reconciliationAdmin.ListRuns(w, r, billingActor(u))
	}
}
func (s *Server) handleAdminReconciliationGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.requireReconciliationReadComposition, func(u AdminUser, id string) { s.reconciliationAdmin.GetRun(w, r, billingActor(u), id) })
}
func (s *Server) handleAdminReconciliationLockPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.requireReconciliationReadComposition, func(u AdminUser, id string) { s.reconciliationAdmin.LockRun(w, r, billingActor(u), id) })
}
func (s *Server) handleAdminReconciliationRecalculatePost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.requireReconciliationExecutionComposition, func(u AdminUser, id string) { s.reconciliationAdmin.Recalculate(w, r, billingActor(u), id) })
}
func (s *Server) handleAdminReconciliationExportGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.requireReconciliationReadComposition, func(u AdminUser, id string) { s.reconciliationAdmin.Export(w, r, billingActor(u), id) })
}
func (s *Server) handleAdminReconciliationRoute(w http.ResponseWriter, r *http.Request, require func(http.ResponseWriter, *http.Request) bool, f func(AdminUser, string)) {
	id := r.PathValue("run_id")
	if id == "" || strings.Contains(id, "/") {
		s.handleAdminReconciliationItem(w, r)
		return
	}
	if u, ok := s.requireAdmin(w, r, "billing", r.Method); ok {
		if !require(w, r) {
			return
		}
		f(u, id)
	}
}
func (s *Server) handleAdminReconciliationRuleItem(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireAdmin(w, r, "billing", r.Method)
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
		if !s.requireReconciliationExecutionComposition(w, r) {
			return
		}
		s.reconciliationAdmin.RunRule(w, r, billingActor(u), parts[0])
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.requireReconciliationReadComposition(w, r) {
			return
		}
		s.reconciliationAdmin.GetRule(w, r, billingActor(u), parts[0])
	case http.MethodPatch:
		if !s.requireReconciliationExecutionComposition(w, r) {
			return
		}
		s.reconciliationAdmin.PatchRule(w, r, billingActor(u), parts[0])
	default:
		jsonMethodNotAllowed("GET, PATCH")(w, r)
	}
}
func (s *Server) handleAdminReconciliationItem(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	parts := pathPartsAfter(r.URL.Path, "/api/admin/billing/reconciliations/")
	if len(parts) == 0 || len(parts) > 2 {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "reconciliation_run_not_found", "Reconciliation run not found"))
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			jsonMethodNotAllowed(http.MethodGet)(w, r)
			return
		}
		if !s.requireReconciliationReadComposition(w, r) {
			return
		}
		s.reconciliationAdmin.GetRun(w, r, billingActor(u), parts[0])
		return
	}
	switch parts[1] {
	case "lock":
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		if !s.requireReconciliationReadComposition(w, r) {
			return
		}
		s.reconciliationAdmin.LockRun(w, r, billingActor(u), parts[0])
	case "recalculate":
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		if !s.requireReconciliationExecutionComposition(w, r) {
			return
		}
		s.reconciliationAdmin.Recalculate(w, r, billingActor(u), parts[0])
	case "export":
		if r.Method != http.MethodGet {
			jsonMethodNotAllowed(http.MethodGet)(w, r)
			return
		}
		if !s.requireReconciliationReadComposition(w, r) {
			return
		}
		s.reconciliationAdmin.Export(w, r, billingActor(u), parts[0])
	default:
		writeError(w, r, NewHTTPError(http.StatusNotFound, "reconciliation_action_not_found", "Reconciliation action not found"))
	}
}
func pathPartsAfter(path, prefix string) []string {
	v := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if v == "" {
		return nil
	}
	return strings.Split(v, "/")
}

func (s *Server) requireReconciliationReadComposition(w http.ResponseWriter, r *http.Request) bool {
	if s.reconciliationReadAvailable {
		return true
	}
	writeError(w, r, NewHTTPError(http.StatusServiceUnavailable, "reconciliation_store_unavailable", "Reconciliation persistence is unavailable"))
	return false
}

func (s *Server) requireReconciliationExecutionComposition(w http.ResponseWriter, r *http.Request) bool {
	if s.reconciliationExecutionAvailable {
		return true
	}
	writeError(w, r, NewHTTPError(http.StatusServiceUnavailable, "reconciliation_store_unavailable", "Reconciliation persistence is unavailable"))
	return false
}
