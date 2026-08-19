package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) StartBillingScheduler() {
	s.billing.StartScheduler(30 * time.Second)
	s.reconciliation.StartScheduler(30 * time.Second)
	s.credentialRefresh.StartScheduler(providerCredentialRefreshInterval)
	s.payloadRetention.StartScheduler(requestPayloadRetentionInterval)
}

func (s *Server) serveAdminBillingConnectorsGet(w http.ResponseWriter, _ *http.Request, _ AdminUser) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListBillingConnectors()})
}

func (s *Server) serveAdminBillingConnectorsPost(w http.ResponseWriter, r *http.Request, user AdminUser) {
	var request BillingConnectorRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		if isPayloadTooLarge(err) {
			writeError(w, r, err)
			return
		}
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_billing_connector", "Invalid billing connector payload"))
		return
	}
	connector, err := billingConnectorFromRequest(request)
	if err != nil {
		writeError(w, r, err)
		return
	}
	created, err := s.store.CreateBillingConnector(connector)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "billing_connector", created.ID, nil, created)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleAdminBillingConnectorItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/billing/connectors/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "billing_connector_not_found", "Billing connector not found"))
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		s.handleAdminBillingConnectorAction(w, r, user, id, parts[1])
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.serveAdminBillingConnectorGet(w, r, user, id)
	case http.MethodPatch:
		s.serveAdminBillingConnectorPatch(w, r, user, id)
	case http.MethodDelete:
		s.serveAdminBillingConnectorDelete(w, r, user, id)
	default:
		jsonMethodNotAllowed("GET, PATCH, DELETE")(w, r)
	}
}

func (s *Server) serveAdminBillingConnectorGet(w http.ResponseWriter, r *http.Request, _ AdminUser, connectorID string) {
	connector, err := s.store.GetBillingConnector(connectorID, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, connector)
}

func (s *Server) serveAdminBillingConnectorPatch(w http.ResponseWriter, r *http.Request, user AdminUser, connectorID string) {
	before, err := s.store.GetBillingConnector(connectorID, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var request BillingConnectorPatchRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		if isPayloadTooLarge(err) {
			writeError(w, r, err)
			return
		}
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_billing_connector", "Invalid billing connector payload"))
		return
	}
	patch, err := billingConnectorPatch(request, before)
	if err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.store.UpdateBillingConnector(connectorID, patch)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "billing_connector", connectorID, before, updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) serveAdminBillingConnectorDelete(w http.ResponseWriter, r *http.Request, user AdminUser, connectorID string) {
	before, err := s.store.GetBillingConnector(connectorID, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.store.DeleteBillingConnector(connectorID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "billing_connector", connectorID, before, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminBillingConnectorAction(w http.ResponseWriter, r *http.Request, user AdminUser, connectorID string, action string) {
	if r.Method != http.MethodPost {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	switch action {
	case "test":
		s.serveAdminBillingConnectorTest(w, r, user, connectorID)
	case "sync":
		s.serveAdminBillingConnectorSync(w, r, user, connectorID)
	default:
		writeError(w, r, NewHTTPError(http.StatusNotFound, "billing_action_not_found", "Billing connector action not found"))
	}
}

func (s *Server) serveAdminBillingConnectorTest(w http.ResponseWriter, r *http.Request, user AdminUser, connectorID string) {
	result, err := s.billing.Test(r.Context(), connectorID)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordAdminAuditWithStatus(r, user, "test", "billing_connector", connectorID, BillingSyncFailed, httpErr.Code, nil, map[string]any{"error_code": httpErr.Code})
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "test", "billing_connector", connectorID, nil, result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) serveAdminBillingConnectorSync(w http.ResponseWriter, r *http.Request, user AdminUser, connectorID string) {
	var request BillingSyncRequest
	if err := s.decodeJSONOptional(w, r, &request); err != nil {
		if isPayloadTooLarge(err) {
			writeError(w, r, err)
			return
		}
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_billing_sync", "Invalid billing sync payload"))
		return
	}
	run, err := s.billing.Sync(r.Context(), connectorID, request, "manual")
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordAdminAuditWithStatus(r, user, "sync", "billing_connector", connectorID, BillingSyncFailed, httpErr.Code, nil, run)
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "sync", "billing_connector", connectorID, nil, run)
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) serveAdminBillingRecordsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListBillingRecords(r.URL.Query().Get("connector_id"), billingListLimit(r))})
}

func (s *Server) serveAdminBillingSyncRunsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListBillingSyncRuns(r.URL.Query().Get("connector_id"), billingListLimit(r))})
}

func billingListLimit(r *http.Request) int {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return limit
}

func billingConnectorFromRequest(request BillingConnectorRequest) (BillingConnector, error) {
	connector := BillingConnector{
		Name:                    strings.TrimSpace(request.Name),
		Type:                    strings.ToLower(strings.TrimSpace(request.Type)),
		BaseURL:                 strings.TrimRight(strings.TrimSpace(request.BaseURL), "/"),
		Status:                  strings.ToLower(strings.TrimSpace(request.Status)),
		ScheduleIntervalMinutes: request.ScheduleIntervalMinutes,
		Config:                  request.Config,
		Credentials:             request.Credentials,
	}
	if connector.Status == "" {
		connector.Status = StatusActive
	}
	if err := validateBillingConnector(connector); err != nil {
		return BillingConnector{}, err
	}
	return connector, nil
}

func billingConnectorPatch(request BillingConnectorPatchRequest, before BillingConnector) (BillingConnector, error) {
	patch := BillingConnector{ScheduleIntervalMinutes: -1}
	candidate := before
	if request.Name != nil {
		patch.Name = strings.TrimSpace(*request.Name)
		candidate.Name = patch.Name
	}
	if request.BaseURL != nil {
		patch.BaseURL = strings.TrimRight(strings.TrimSpace(*request.BaseURL), "/")
		candidate.BaseURL = patch.BaseURL
	}
	if request.Status != nil {
		patch.Status = strings.ToLower(strings.TrimSpace(*request.Status))
		candidate.Status = patch.Status
	}
	if request.ScheduleIntervalMinutes != nil {
		patch.ScheduleIntervalMinutes = *request.ScheduleIntervalMinutes
		candidate.ScheduleIntervalMinutes = patch.ScheduleIntervalMinutes
	}
	if request.Config != nil {
		patch.Config = request.Config
		candidate.Config = request.Config
	}
	if request.Credentials != nil {
		patch.Credentials = request.Credentials
	}
	if err := validateBillingConnector(candidate); err != nil {
		return BillingConnector{}, err
	}
	return patch, nil
}

func validateBillingConnector(connector BillingConnector) error {
	if connector.Name == "" || connector.BaseURL == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_billing_connector", "name and base_url are required")
	}
	switch connector.Type {
	case BillingConnectorAliyun, BillingConnectorNewAPI, BillingConnectorOneAPI:
	default:
		return NewHTTPError(http.StatusBadRequest, "invalid_billing_connector_type", "type must be aliyun, newapi, or oneapi")
	}
	if connector.Status != StatusActive && connector.Status != StatusDisabled {
		return NewHTTPError(http.StatusBadRequest, "invalid_billing_connector_status", "status must be active or disabled")
	}
	if connector.ScheduleIntervalMinutes < 0 {
		return NewHTTPError(http.StatusBadRequest, "invalid_billing_schedule", "schedule_interval_minutes cannot be negative")
	}
	baseURL, err := url.Parse(connector.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_billing_base_url", "base_url must be an HTTP(S) URL without credentials, query parameters, or fragments")
	}
	allowedConfig := billingConnectorAllowedConfig(connector.Type)
	for key := range connector.Config {
		if _, ok := allowedConfig[key]; !ok {
			return NewHTTPError(http.StatusBadRequest, "invalid_billing_config", "Billing connector config contains an unsupported field")
		}
	}
	if endpoint := strings.TrimSpace(connector.Config["endpoint"]); endpoint != "" {
		if parsed, parseErr := url.Parse(endpoint); parseErr != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return NewHTTPError(http.StatusBadRequest, "invalid_billing_endpoint", "Billing connector endpoint is invalid")
		}
	}
	if connector.Type == BillingConnectorNewAPI {
		userID, parseErr := strconv.ParseInt(strings.TrimSpace(connector.Config["user_id"]), 10, 64)
		if parseErr != nil || userID <= 0 {
			return NewHTTPError(http.StatusBadRequest, "invalid_billing_config", "NewAPI user_id must be a positive integer")
		}
	}
	return nil
}

func billingConnectorAllowedConfig(connectorType string) map[string]struct{} {
	allowed := map[string]struct{}{
		"currency":              {},
		"max_retries":           {},
		"page_size":             {},
		"provider_id":           {},
		"provider_resource_id":  {},
		"rate_limit_per_second": {},
		"retry_base_ms":         {},
	}
	switch connectorType {
	case BillingConnectorAliyun:
		allowed["product_code"] = struct{}{}
		allowed["source_timezone"] = struct{}{}
	case BillingConnectorNewAPI:
		allowed["quota_per_unit"] = struct{}{}
		allowed["user_id"] = struct{}{}
	case BillingConnectorOneAPI:
		allowed["endpoint"] = struct{}{}
		allowed["quota_per_unit"] = struct{}{}
	}
	return allowed
}
