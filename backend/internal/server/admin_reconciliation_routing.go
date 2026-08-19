package server

import (
	"net/http"
	"strings"
)

type adminReconciliationRuleHandler func(http.ResponseWriter, *http.Request, AdminUser, string)
type adminReconciliationRunHandler func(http.ResponseWriter, *http.Request, AdminUser, string)

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

func (s *Server) adminReconciliationRuleMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	reject := s.adminMethodNotAllowed("billing", allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		if ruleID := r.PathValue("rule_id"); ruleID == "" || strings.Contains(ruleID, "/") {
			s.handleAdminReconciliationRuleItem(w, r)
			return
		}
		reject(w, r)
	}
}

func (s *Server) adminReconciliationRunMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	reject := s.adminMethodNotAllowed("billing", allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		if runID := r.PathValue("run_id"); runID == "" || strings.Contains(runID, "/") {
			s.handleAdminReconciliationItem(w, r)
			return
		}
		reject(w, r)
	}
}

func (s *Server) handleAdminReconciliationRulesGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	s.serveAdminReconciliationRulesGet(w, r, user)
}

func (s *Server) handleAdminReconciliationRulesPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	s.serveAdminReconciliationRulesPost(w, r, user)
}

func (s *Server) handleAdminReconciliationRuleGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRuleRoute(w, r, s.serveAdminReconciliationRuleGet)
}

func (s *Server) handleAdminReconciliationRulePatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRuleRoute(w, r, s.serveAdminReconciliationRulePatch)
}

func (s *Server) handleAdminReconciliationRuleRunPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRuleRoute(w, r, s.serveAdminReconciliationRuleRun)
}

func (s *Server) handleAdminReconciliationRuleRoute(w http.ResponseWriter, r *http.Request, handler adminReconciliationRuleHandler) {
	ruleID := r.PathValue("rule_id")
	if ruleID == "" || strings.Contains(ruleID, "/") {
		s.handleAdminReconciliationRuleItem(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	handler(w, r, user, ruleID)
}

func (s *Server) handleAdminReconciliationsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	s.serveAdminReconciliationsGet(w, r)
}

func (s *Server) handleAdminReconciliationGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.serveAdminReconciliationGet)
}

func (s *Server) handleAdminReconciliationLockPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.serveAdminReconciliationLock)
}

func (s *Server) handleAdminReconciliationRecalculatePost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.serveAdminReconciliationRecalculate)
}

func (s *Server) handleAdminReconciliationExportGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReconciliationRoute(w, r, s.serveAdminReconciliationExport)
}

func (s *Server) handleAdminReconciliationRoute(w http.ResponseWriter, r *http.Request, handler adminReconciliationRunHandler) {
	runID := r.PathValue("run_id")
	if runID == "" || strings.Contains(runID, "/") {
		s.handleAdminReconciliationItem(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	handler(w, r, user, runID)
}
