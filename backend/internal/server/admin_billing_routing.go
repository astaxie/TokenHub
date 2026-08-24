package server

import (
	"net/http"

	"tokenhub/backend/internal/admin"
)

type adminBillingConnectorHandler func(http.ResponseWriter, *http.Request, AdminUser, string)

func (s *Server) registerBillingRoutes() {
	billingMethodNotAllowed := func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("billing", allowedMethods)
	}
	s.registerMethodRoutes("/api/admin/billing/connectors", billingMethodNotAllowed,
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminBillingConnectorsGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminBillingConnectorsPost},
	)
	s.registerMethodRoutes("/api/admin/billing/connectors/{connector_id}", billingMethodNotAllowed,
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminBillingConnectorGet},
		methodRoute{Method: http.MethodPatch, Handler: s.handleAdminBillingConnectorPatch},
		methodRoute{Method: http.MethodDelete, Handler: s.handleAdminBillingConnectorDelete},
	)
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/billing/connectors/{connector_id}/test", s.handleAdminBillingConnectorTestPost, s.adminMethodNotAllowed("billing", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/billing/connectors/{connector_id}/sync", s.handleAdminBillingConnectorSyncPost, s.adminMethodNotAllowed("billing", http.MethodPost))
	s.mux.HandleFunc("/api/admin/billing/connectors/", s.handleAdminBillingConnectorItem)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/billing/records", s.handleAdminBillingRecordsGet, s.adminMethodNotAllowed("billing", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/billing/sync-runs", s.handleAdminBillingSyncRunsGet, s.adminMethodNotAllowed("billing", http.MethodGet))
	s.registerReconciliationRoutes()
}

func (s *Server) handleAdminBillingConnectorsGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	if !s.requireBillingComposition(w, r) {
		return
	}
	s.billingAdmin.ListConnectors(w, r, billingActor(user))
}

func (s *Server) handleAdminBillingConnectorsPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	if !s.requireBillingComposition(w, r) {
		return
	}
	s.billingAdmin.CreateConnector(w, r, billingActor(user))
}

func (s *Server) handleAdminBillingConnectorGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, func(w http.ResponseWriter, r *http.Request, user AdminUser, id string) {
		s.billingAdmin.GetConnector(w, r, billingActor(user), id)
	})
}

func (s *Server) handleAdminBillingConnectorPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, func(w http.ResponseWriter, r *http.Request, user AdminUser, id string) {
		s.billingAdmin.PatchConnector(w, r, billingActor(user), id)
	})
}

func (s *Server) handleAdminBillingConnectorDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, func(w http.ResponseWriter, r *http.Request, user AdminUser, id string) {
		s.billingAdmin.DeleteConnector(w, r, billingActor(user), id)
	})
}

func (s *Server) handleAdminBillingConnectorTestPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, func(w http.ResponseWriter, r *http.Request, user AdminUser, id string) {
		s.billingAdmin.TestConnector(w, r, billingActor(user), id)
	})
}

func (s *Server) handleAdminBillingConnectorSyncPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, func(w http.ResponseWriter, r *http.Request, user AdminUser, id string) {
		s.billingAdmin.SyncConnector(w, r, billingActor(user), id)
	})
}

func (s *Server) handleAdminBillingConnectorRoute(w http.ResponseWriter, r *http.Request, handler adminBillingConnectorHandler) {
	connectorID := r.PathValue("connector_id")
	if connectorID == "" {
		s.handleAdminBillingConnectorItem(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	if !s.requireBillingComposition(w, r) {
		return
	}
	handler(w, r, user, connectorID)
}

func (s *Server) handleAdminBillingConnectorItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	if !s.requireBillingComposition(w, r) {
		return
	}
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/billing/connectors/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "billing_connector_not_found", "Billing connector not found"))
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		switch parts[1] {
		case "test":
			s.billingAdmin.TestConnector(w, r, billingActor(user), id)
		case "sync":
			s.billingAdmin.SyncConnector(w, r, billingActor(user), id)
		default:
			writeError(w, r, NewHTTPError(http.StatusNotFound, "billing_action_not_found", "Billing connector action not found"))
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.billingAdmin.GetConnector(w, r, billingActor(user), id)
	case http.MethodPatch:
		s.billingAdmin.PatchConnector(w, r, billingActor(user), id)
	case http.MethodDelete:
		s.billingAdmin.DeleteConnector(w, r, billingActor(user), id)
	default:
		jsonMethodNotAllowed("GET, PATCH, DELETE")(w, r)
	}
}

func (s *Server) handleAdminBillingRecordsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	if !s.requireBillingComposition(w, r) {
		return
	}
	s.billingAdmin.ListRecords(w, r)
}

func (s *Server) handleAdminBillingSyncRunsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	if !s.requireBillingComposition(w, r) {
		return
	}
	s.billingAdmin.ListSyncRuns(w, r)
}

func (s *Server) requireBillingComposition(w http.ResponseWriter, r *http.Request) bool {
	if s.billingAvailable {
		return true
	}
	writeError(w, r, NewHTTPError(http.StatusServiceUnavailable, "billing_repository_unavailable", "Billing persistence is unavailable"))
	return false
}

func billingActor(user AdminUser) admin.BillingActor {
	return admin.BillingActor{ID: user.ID, Name: user.Name, Role: user.Role}
}
