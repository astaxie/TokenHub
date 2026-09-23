package server

import "net/http"

type adminResourceCollectionHandler func(http.ResponseWriter, *http.Request, AdminUser, string)
type adminResourceItemHandler func(http.ResponseWriter, *http.Request, AdminUser, string, string)

func (s *Server) handleAdminResources(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, adminResourcePermission(r.URL.Path), r.Method)
	if !ok {
		return
	}
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/resources/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	kind := parts[0]
	if kind == openAIAccountQuotaResetOperationKind {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.serveAdminResourceCollectionGet(w, r, user, kind)
		case http.MethodPost:
			s.serveAdminResourceCollectionPost(w, r, user, kind)
		default:
			jsonMethodNotAllowed(http.MethodGet+", "+http.MethodPost)(w, r)
		}
		return
	}
	if kind == "invoices" && len(parts) == 3 && parts[1] != "" {
		s.handleAdminInvoiceAction(w, r, user, parts[1], parts[2])
		return
	}
	if kind == "monitors" && len(parts) == 3 && parts[1] != "" && parts[2] == "run" {
		s.handleAdminMonitorRun(w, r, user, parts[1])
		return
	}
	if len(parts) != 2 || parts[1] == "" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.serveAdminResourcePatch(w, r, user, kind, parts[1])
	case http.MethodDelete:
		s.serveAdminResourceDelete(w, r, user, kind, parts[1])
	default:
		jsonMethodNotAllowed(http.MethodPatch+", "+http.MethodDelete)(w, r)
	}
}

func (s *Server) serveAdminResourceCollectionGet(w http.ResponseWriter, _ *http.Request, user AdminUser, kind string) {
	if kind == "monitors" {
		s.ensureDefaultMonitors()
	}
	if kind == "alert-rules" {
		s.ensureDefaultAlertRules()
	}
	items := s.filterResourcesForUser(user, kind, s.store.ListResources(kind))
	if kind == "quota-policies" {
		items = s.attachQuotaPolicyUsage(items)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": redactAdminResourcesForResponse(kind, items)})
}

func (s *Server) attachQuotaPolicyUsage(items []AdminResource) []AdminResource {
	for index := range items {
		scope := firstStringField(items[index].Fields, "scope", "scope_type")
		scopeID := firstStringField(items[index].Fields, "scope_id")
		usage, supported, err := s.store.GetQuotaPolicyUsage(scope, scopeID)
		if err != nil || !supported {
			continue
		}
		items[index].CurrentUsage = &usage
	}
	return items
}

func (s *Server) serveAdminResourceCollectionPost(w http.ResponseWriter, r *http.Request, user AdminUser, kind string) {
	if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" {
		writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader cannot create teams"))
		return
	}
	var req AdminResource
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Name == "" {
		writeError(w, r, NewHTTPError(400, "invalid_resource", "name is required"))
		return
	}
	req.Fields = preserveAdminResourceSecrets(kind, nil, req.Fields)
	if kind == "settings" {
		req.Fields = normalizeProviderProxySettingsFields(req.Fields)
	}
	if err := s.validateScopedResourceMutation(user, kind, "", req); err != nil {
		writeError(w, r, err)
		return
	}
	if kind == "settings" && req.ID == gatewaySettingsID {
		s.syntheticDNSSetting.Lock()
		defer s.syntheticDNSSetting.Unlock()
		if err := validateProviderSyntheticDNSSettings(req); err != nil {
			writeError(w, r, err)
			return
		}
		if err := validateProviderProxySettings(req, s.store); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if kind == "settings" {
		var err error
		req.Fields, err = protectAdminResourceSecretsForStorage(s.store, kind, req.Fields)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	if approval, required := s.adminResourceApproval(user, kind, "", req); required {
		s.recordAdminAudit(r, user, "request_approval", kind, approval.ID, "", approval)
		writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
		return
	}
	var resource AdminResource
	var err error
	if kind == routingPolicyResourceKind {
		resource, err = s.store.CreateRoutingPolicy(req)
	} else {
		resource, err = s.store.CreateResourceChecked(kind, req)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if kind == "settings" && resource.ID == gatewaySettingsID {
		s.syntheticDNSPolicy.applySetting(&resource)
		s.providerProxyPolicy.applySetting(&resource)
	}
	s.recordAdminAudit(r, user, "create", kind, resource.ID, "", resource)
	writeJSON(w, http.StatusCreated, redactAdminResourceForResponse(kind, resource))
}

func (s *Server) serveAdminResourcePatch(w http.ResponseWriter, r *http.Request, user AdminUser, kind string, resourceID string) {
	if kind == "settings" && resourceID == gatewaySettingsID {
		s.syntheticDNSSetting.Lock()
		defer s.syntheticDNSSetting.Unlock()
	}
	if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" && resourceID != user.TeamID {
		writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only update own team"))
		return
	}
	var req AdminResource
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Fields != nil && len(sensitiveAdminResourceFields[kind]) > 0 {
		existing, err := s.findResource(kind, resourceID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		req.Fields = preserveAdminResourceSecrets(kind, existing.Fields, req.Fields)
	}
	if kind == "settings" {
		req.Fields = normalizeProviderProxySettingsFields(req.Fields)
	}
	if err := s.validateScopedResourceMutation(user, kind, resourceID, req); err != nil {
		writeError(w, r, err)
		return
	}
	if kind == "settings" && resourceID == gatewaySettingsID {
		req.ID = resourceID
		if err := validateProviderSyntheticDNSSettings(req); err != nil {
			writeError(w, r, err)
			return
		}
		if err := validateProviderProxySettings(req, s.store); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if kind == "settings" {
		var err error
		req.Fields, err = protectAdminResourceSecretsForStorage(s.store, kind, req.Fields)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	if approval, required := s.adminResourceApproval(user, kind, resourceID, req); required {
		s.recordAdminAudit(r, user, "request_approval", kind, approval.ID, "", approval)
		writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
		return
	}
	resource, err := s.store.UpdateResource(kind, resourceID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if kind == "settings" && resourceID == gatewaySettingsID {
		s.syntheticDNSPolicy.applySetting(&resource)
		s.providerProxyPolicy.applySetting(&resource)
	}
	s.recordAdminAudit(r, user, "update", kind, resourceID, "", resource)
	writeJSON(w, http.StatusOK, redactAdminResourceForResponse(kind, resource))
}

func (s *Server) serveAdminResourceDelete(w http.ResponseWriter, r *http.Request, user AdminUser, kind string, resourceID string) {
	if kind == "settings" && resourceID == gatewaySettingsID {
		s.syntheticDNSSetting.Lock()
		defer s.syntheticDNSSetting.Unlock()
	}
	if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" {
		writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader cannot delete teams"))
		return
	}
	if kind == "teams" {
		if err := s.store.DeleteTeam(resourceID); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", kind, resourceID, "", nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.DeleteResource(kind, resourceID); err != nil {
		writeError(w, r, err)
		return
	}
	if kind == "settings" && resourceID == gatewaySettingsID {
		s.syntheticDNSPolicy.applySetting(nil)
		s.providerProxyPolicy.applySetting(nil)
	}
	s.recordAdminAudit(r, user, "delete", kind, resourceID, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminResourceCollectionGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCollectionRoute(w, r, s.serveAdminResourceCollectionGet)
}

func (s *Server) handleAdminResourceCollectionPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceCollectionRoute(w, r, s.serveAdminResourceCollectionPost)
}

func (s *Server) handleAdminResourceCollectionRoute(w http.ResponseWriter, r *http.Request, handler adminResourceCollectionHandler) {
	kind := r.PathValue("kind")
	if kind == "" || kind == openAIAccountQuotaResetOperationKind {
		s.handleAdminResources(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, adminResourcePermission(r.URL.Path), r.Method)
	if !ok {
		return
	}
	handler(w, r, user, kind)
}

func (s *Server) handleAdminResourcePatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceItemRoute(w, r, s.serveAdminResourcePatch)
}

func (s *Server) handleAdminResourceDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminResourceItemRoute(w, r, s.serveAdminResourceDelete)
}

func (s *Server) handleAdminResourceItemRoute(w http.ResponseWriter, r *http.Request, handler adminResourceItemHandler) {
	kind := r.PathValue("kind")
	resourceID := r.PathValue("resource_id")
	if kind == "" || resourceID == "" || kind == openAIAccountQuotaResetOperationKind {
		s.handleAdminResources(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, adminResourcePermission(r.URL.Path), r.Method)
	if !ok {
		return
	}
	handler(w, r, user, kind, resourceID)
}

func (s *Server) adminResourceMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		if kind == "" || kind == openAIAccountQuotaResetOperationKind {
			s.handleAdminResources(w, r)
			return
		}
		if _, ok := s.requireAdmin(w, r, adminResourcePermission(r.URL.Path), r.Method); !ok {
			return
		}
		reject(w, r)
	}
}

func (s *Server) handleAdminInvoiceConfirmPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminInvoiceActionPost(w, r, "confirm")
}

func (s *Server) handleAdminInvoiceRejectPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminInvoiceActionPost(w, r, "reject")
}

func (s *Server) handleAdminInvoiceActionPost(w http.ResponseWriter, r *http.Request, action string) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	invoiceID := r.PathValue("invoice_id")
	if invoiceID == "" {
		s.handleAdminResources(w, r)
		return
	}
	s.serveAdminInvoiceAction(w, r, user, invoiceID, action)
}

func (s *Server) handleAdminMonitorRunPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	monitorID := r.PathValue("monitor_id")
	if monitorID == "" {
		s.handleAdminResources(w, r)
		return
	}
	s.serveAdminMonitorRun(w, r, user, monitorID)
}
