package server

import (
	"net/http"
	"strings"
)

func (s *Server) handleAdminRequestDetailGet(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("request_id")
	if requestID == "" || strings.Contains(requestID, "/") {
		s.handleAdminRequestDetail(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "audit", r.Method)
	if !ok {
		return
	}
	s.serveAdminRequestDetail(w, r, user, requestID)
}

func (s *Server) handleAdminAlertDeliverPost(w http.ResponseWriter, r *http.Request) {
	alertID := r.PathValue("alert_id")
	if alertID == "" || strings.Contains(alertID, "/") {
		s.handleAdminAlertItem(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "alert", r.Method)
	if !ok {
		return
	}
	s.serveAdminAlertDelivery(w, r, user, alertID)
}

func (s *Server) handleAdminApprovalApprovePost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminApprovalActionPost(w, r, "approve")
}

func (s *Server) handleAdminApprovalRejectPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminApprovalActionPost(w, r, "reject")
}

func (s *Server) handleAdminApprovalActionPost(w http.ResponseWriter, r *http.Request, action string) {
	approvalID := r.PathValue("approval_id")
	if approvalID == "" || strings.Contains(approvalID, "/") {
		s.handleAdminApprovalItem(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "approval", r.Method)
	if !ok {
		return
	}
	s.serveAdminApprovalAction(w, r, user, approvalID, action)
}
