package server

import "net/http"

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
	s.serveAdminBillingConnectorsGet(w, r, user)
}

func (s *Server) handleAdminBillingConnectorsPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	s.serveAdminBillingConnectorsPost(w, r, user)
}

func (s *Server) handleAdminBillingConnectorGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, s.serveAdminBillingConnectorGet)
}

func (s *Server) handleAdminBillingConnectorPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, s.serveAdminBillingConnectorPatch)
}

func (s *Server) handleAdminBillingConnectorDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, s.serveAdminBillingConnectorDelete)
}

func (s *Server) handleAdminBillingConnectorTestPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, s.serveAdminBillingConnectorTest)
}

func (s *Server) handleAdminBillingConnectorSyncPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminBillingConnectorRoute(w, r, s.serveAdminBillingConnectorSync)
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
	handler(w, r, user, connectorID)
}

func (s *Server) handleAdminBillingRecordsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	s.serveAdminBillingRecordsGet(w, r)
}

func (s *Server) handleAdminBillingSyncRunsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	s.serveAdminBillingSyncRunsGet(w, r)
}
