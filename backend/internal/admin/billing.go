package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tokenhub/backend/internal/billing"
)

type BillingTransport struct {
	DecodeJSON         func(http.ResponseWriter, *http.Request, any) error
	DecodeJSONOptional func(http.ResponseWriter, *http.Request, any) error
	IsPayloadTooLarge  func(error) bool
	NewError           func(int, string, string) error
	MapError           func(error) error
	WriteJSON          func(http.ResponseWriter, int, any)
	WriteError         func(http.ResponseWriter, *http.Request, error)
	Audit              func(*http.Request, BillingActor, BillingAudit)
}

type BillingActor struct {
	ID   string
	Name string
	Role string
}

type BillingAudit struct {
	Action     string
	ResourceID string
	Status     string
	Message    string
	Before     any
	After      any
}

type BillingHandler struct {
	repository billing.ManagementStore
	service    *billing.Service
	transport  BillingTransport
}

func NewBillingHandler(repository billing.ManagementStore, service *billing.Service, transport BillingTransport) *BillingHandler {
	return &BillingHandler{repository: repository, service: service, transport: transport}
}

type billingConnectorRequest struct {
	Name                    string            `json:"name"`
	Type                    string            `json:"type"`
	BaseURL                 string            `json:"base_url"`
	Status                  string            `json:"status"`
	ScheduleIntervalMinutes int               `json:"schedule_interval_minutes"`
	Config                  map[string]string `json:"config"`
	Credentials             map[string]string `json:"credentials"`
}

type billingConnectorPatchRequest struct {
	Name                    *string           `json:"name"`
	BaseURL                 *string           `json:"base_url"`
	Status                  *string           `json:"status"`
	ScheduleIntervalMinutes *int              `json:"schedule_interval_minutes"`
	Config                  map[string]string `json:"config"`
	Credentials             map[string]string `json:"credentials"`
}

type billingSyncRequest struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type billingConnectorResponse struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	Type                    string            `json:"type"`
	BaseURL                 string            `json:"base_url"`
	Status                  string            `json:"status"`
	ScheduleIntervalMinutes int               `json:"schedule_interval_minutes"`
	Config                  map[string]string `json:"config,omitempty"`
	CredentialsConfigured   bool              `json:"credentials_configured"`
	CredentialFields        []string          `json:"credential_fields,omitempty"`
	LastSyncedThrough       *time.Time        `json:"last_synced_through,omitempty"`
	LastSyncStatus          string            `json:"last_sync_status,omitempty"`
	LastSyncMessage         string            `json:"last_sync_message,omitempty"`
	LastSyncAt              *time.Time        `json:"last_sync_at,omitempty"`
	NextSyncAt              *time.Time        `json:"next_sync_at,omitempty"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

type billingRecordResponse struct {
	ID                string            `json:"id"`
	ConnectorID       string            `json:"connector_id"`
	ExternalID        string            `json:"external_id"`
	SourceType        string            `json:"source_type"`
	AccountID         string            `json:"account_id,omitempty"`
	Service           string            `json:"service,omitempty"`
	Product           string            `json:"product,omitempty"`
	Model             string            `json:"model,omitempty"`
	Currency          string            `json:"currency"`
	GrossAmount       string            `json:"gross_amount"`
	DiscountAmount    string            `json:"discount_amount"`
	TaxAmount         string            `json:"tax_amount"`
	RefundAmount      string            `json:"refund_amount"`
	NetAmount         string            `json:"net_amount"`
	UsageQuantity     int64             `json:"usage_quantity,omitempty"`
	UsageUnit         string            `json:"usage_unit,omitempty"`
	UsageStartAt      time.Time         `json:"usage_start_at"`
	UsageEndAt        time.Time         `json:"usage_end_at"`
	SourceTimezone    string            `json:"source_timezone"`
	BillingPeriod     string            `json:"billing_period"`
	ExternalRequestID string            `json:"external_request_id,omitempty"`
	RawSnapshotID     string            `json:"raw_snapshot_id"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

type billingSyncRunResponse struct {
	ID              string     `json:"id"`
	ConnectorID     string     `json:"connector_id"`
	Trigger         string     `json:"trigger"`
	Status          string     `json:"status"`
	RangeStart      time.Time  `json:"range_start"`
	RangeEnd        time.Time  `json:"range_end"`
	CursorStart     string     `json:"cursor_start,omitempty"`
	CursorEnd       string     `json:"cursor_end,omitempty"`
	PagesFetched    int        `json:"pages_fetched"`
	Attempts        int        `json:"attempts"`
	RecordsSeen     int        `json:"records_seen"`
	RecordsInserted int        `json:"records_inserted"`
	RecordsUpdated  int        `json:"records_updated"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

func (h *BillingHandler) ListConnectors(w http.ResponseWriter, _ *http.Request, _ BillingActor) {
	connectors := h.repository.ListBillingConnectors()
	responses := make([]billingConnectorResponse, len(connectors))
	for index, connector := range connectors {
		responses[index] = connectorResponse(connector)
	}
	h.transport.WriteJSON(w, http.StatusOK, map[string]any{"data": responses})
}

func (h *BillingHandler) CreateConnector(w http.ResponseWriter, r *http.Request, actor BillingActor) {
	var request billingConnectorRequest
	if err := h.transport.DecodeJSON(w, r, &request); err != nil {
		if h.transport.IsPayloadTooLarge(err) {
			h.transport.WriteError(w, r, err)
			return
		}
		h.transport.WriteError(w, r, h.transport.NewError(http.StatusBadRequest, "invalid_billing_connector", "Invalid billing connector payload"))
		return
	}
	connector, err := connectorFromRequest(request, h.transport.NewError)
	if err != nil {
		h.transport.WriteError(w, r, err)
		return
	}
	created, err := h.repository.CreateBillingConnector(connector)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	response := connectorResponse(created)
	h.audit(r, actor, BillingAudit{Action: "create", ResourceID: created.ID, Status: "success", After: response})
	h.transport.WriteJSON(w, http.StatusCreated, response)
}

func (h *BillingHandler) GetConnector(w http.ResponseWriter, r *http.Request, _ BillingActor, connectorID string) {
	connector, err := h.repository.GetBillingConnector(connectorID, false)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.transport.WriteJSON(w, http.StatusOK, connectorResponse(connector))
}

func (h *BillingHandler) PatchConnector(w http.ResponseWriter, r *http.Request, actor BillingActor, connectorID string) {
	before, err := h.repository.GetBillingConnector(connectorID, false)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	var request billingConnectorPatchRequest
	if err := h.transport.DecodeJSON(w, r, &request); err != nil {
		if h.transport.IsPayloadTooLarge(err) {
			h.transport.WriteError(w, r, err)
			return
		}
		h.transport.WriteError(w, r, h.transport.NewError(http.StatusBadRequest, "invalid_billing_connector", "Invalid billing connector payload"))
		return
	}
	patch, err := connectorPatch(request, before, h.transport.NewError)
	if err != nil {
		h.transport.WriteError(w, r, err)
		return
	}
	updated, err := h.repository.UpdateBillingConnector(connectorID, patch)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	beforeResponse := connectorResponse(before)
	updatedResponse := connectorResponse(updated)
	h.audit(r, actor, BillingAudit{Action: "update", ResourceID: connectorID, Status: "success", Before: beforeResponse, After: updatedResponse})
	h.transport.WriteJSON(w, http.StatusOK, updatedResponse)
}

func (h *BillingHandler) DeleteConnector(w http.ResponseWriter, r *http.Request, actor BillingActor, connectorID string) {
	before, err := h.repository.GetBillingConnector(connectorID, false)
	if err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	if err := h.repository.DeleteBillingConnector(connectorID); err != nil {
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.audit(r, actor, BillingAudit{Action: "delete", ResourceID: connectorID, Status: "success", Before: connectorResponse(before)})
	w.WriteHeader(http.StatusNoContent)
}

func (h *BillingHandler) TestConnector(w http.ResponseWriter, r *http.Request, actor BillingActor, connectorID string) {
	result, err := h.service.Test(r.Context(), connectorID)
	if err != nil {
		mapped := h.transport.MapError(err)
		h.audit(r, actor, BillingAudit{Action: "test", ResourceID: connectorID, Status: billing.SyncFailed, Message: errorCode(err), After: map[string]any{"error_code": errorCode(err)}})
		h.transport.WriteError(w, r, mapped)
		return
	}
	h.audit(r, actor, BillingAudit{Action: "test", ResourceID: connectorID, Status: "success", After: result})
	h.transport.WriteJSON(w, http.StatusOK, result)
}

func (h *BillingHandler) SyncConnector(w http.ResponseWriter, r *http.Request, actor BillingActor, connectorID string) {
	var request billingSyncRequest
	if err := h.transport.DecodeJSONOptional(w, r, &request); err != nil {
		if h.transport.IsPayloadTooLarge(err) {
			h.transport.WriteError(w, r, err)
			return
		}
		h.transport.WriteError(w, r, h.transport.NewError(http.StatusBadRequest, "invalid_billing_sync", "Invalid billing sync payload"))
		return
	}
	run, err := h.service.Sync(r.Context(), connectorID, billing.SyncRequest{From: request.From, To: request.To}, "manual")
	response := syncRunResponse(run)
	if err != nil {
		h.audit(r, actor, BillingAudit{Action: "sync", ResourceID: connectorID, Status: billing.SyncFailed, Message: errorCode(err), After: response})
		h.transport.WriteError(w, r, h.transport.MapError(err))
		return
	}
	h.audit(r, actor, BillingAudit{Action: "sync", ResourceID: connectorID, Status: "success", After: response})
	h.transport.WriteJSON(w, http.StatusOK, response)
}

func (h *BillingHandler) ListRecords(w http.ResponseWriter, r *http.Request) {
	records := h.repository.ListBillingRecords(r.URL.Query().Get("connector_id"), listLimit(r))
	responses := make([]billingRecordResponse, len(records))
	for index, record := range records {
		responses[index] = recordResponse(record)
	}
	h.transport.WriteJSON(w, http.StatusOK, map[string]any{"data": responses})
}

func (h *BillingHandler) ListSyncRuns(w http.ResponseWriter, r *http.Request) {
	runs := h.repository.ListBillingSyncRuns(r.URL.Query().Get("connector_id"), listLimit(r))
	responses := make([]billingSyncRunResponse, len(runs))
	for index, run := range runs {
		responses[index] = syncRunResponse(run)
	}
	h.transport.WriteJSON(w, http.StatusOK, map[string]any{"data": responses})
}

func (h *BillingHandler) audit(r *http.Request, actor BillingActor, event BillingAudit) {
	if h.transport.Audit != nil {
		h.transport.Audit(r, actor, event)
	}
}

func connectorFromRequest(request billingConnectorRequest, newError func(int, string, string) error) (billing.Connector, error) {
	connector := billing.Connector{
		Name: strings.TrimSpace(request.Name), Type: strings.ToLower(strings.TrimSpace(request.Type)),
		BaseURL: strings.TrimRight(strings.TrimSpace(request.BaseURL), "/"), Status: strings.ToLower(strings.TrimSpace(request.Status)),
		ScheduleIntervalMinutes: request.ScheduleIntervalMinutes, Config: request.Config, Credentials: request.Credentials,
	}
	if connector.Status == "" {
		connector.Status = billing.StatusActive
	}
	return connector, validateConnector(connector, newError)
}

func connectorPatch(request billingConnectorPatchRequest, before billing.Connector, newError func(int, string, string) error) (billing.Connector, error) {
	patch := billing.Connector{ScheduleIntervalMinutes: -1}
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
	return patch, validateConnector(candidate, newError)
}

func validateConnector(connector billing.Connector, newError func(int, string, string) error) error {
	if connector.Name == "" || connector.BaseURL == "" {
		return newError(http.StatusBadRequest, "invalid_billing_connector", "name and base_url are required")
	}
	switch connector.Type {
	case billing.ConnectorAliyun, billing.ConnectorNewAPI, billing.ConnectorOneAPI:
	default:
		return newError(http.StatusBadRequest, "invalid_billing_connector_type", "type must be aliyun, newapi, or oneapi")
	}
	if connector.Status != billing.StatusActive && connector.Status != "disabled" {
		return newError(http.StatusBadRequest, "invalid_billing_connector_status", "status must be active or disabled")
	}
	if connector.ScheduleIntervalMinutes < 0 {
		return newError(http.StatusBadRequest, "invalid_billing_schedule", "schedule_interval_minutes cannot be negative")
	}
	baseURL, err := url.Parse(connector.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return newError(http.StatusBadRequest, "invalid_billing_base_url", "base_url must be an HTTP(S) URL without credentials, query parameters, or fragments")
	}
	allowed := allowedConfig(connector.Type)
	for key := range connector.Config {
		if _, ok := allowed[key]; !ok {
			return newError(http.StatusBadRequest, "invalid_billing_config", "Billing connector config contains an unsupported field")
		}
	}
	if endpoint := strings.TrimSpace(connector.Config["endpoint"]); endpoint != "" {
		if parsed, parseErr := url.Parse(endpoint); parseErr != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return newError(http.StatusBadRequest, "invalid_billing_endpoint", "Billing connector endpoint is invalid")
		}
	}
	if connector.Type == billing.ConnectorNewAPI {
		userID, parseErr := strconv.ParseInt(strings.TrimSpace(connector.Config["user_id"]), 10, 64)
		if parseErr != nil || userID <= 0 {
			return newError(http.StatusBadRequest, "invalid_billing_config", "NewAPI user_id must be a positive integer")
		}
	}
	return nil
}

func allowedConfig(connectorType string) map[string]struct{} {
	allowed := map[string]struct{}{"currency": {}, "max_retries": {}, "page_size": {}, "provider_id": {}, "provider_resource_id": {}, "rate_limit_per_second": {}, "retry_base_ms": {}}
	switch connectorType {
	case billing.ConnectorAliyun:
		allowed["product_code"] = struct{}{}
		allowed["source_timezone"] = struct{}{}
	case billing.ConnectorNewAPI:
		allowed["quota_per_unit"] = struct{}{}
		allowed["user_id"] = struct{}{}
	case billing.ConnectorOneAPI:
		allowed["endpoint"] = struct{}{}
		allowed["quota_per_unit"] = struct{}{}
	}
	return allowed
}

func listLimit(r *http.Request) int {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return limit
}

func errorCode(err error) string {
	_, code, _, ok := billing.ErrorInfo(err)
	if !ok {
		return "internal_error"
	}
	return code
}

func connectorResponse(value billing.Connector) billingConnectorResponse {
	return billingConnectorResponse{ID: value.ID, Name: value.Name, Type: value.Type, BaseURL: value.BaseURL, Status: value.Status,
		ScheduleIntervalMinutes: value.ScheduleIntervalMinutes, Config: value.Config, CredentialsConfigured: value.CredentialsConfigured,
		CredentialFields: value.CredentialFields, LastSyncedThrough: value.LastSyncedThrough, LastSyncStatus: value.LastSyncStatus,
		LastSyncMessage: value.LastSyncMessage, LastSyncAt: value.LastSyncAt, NextSyncAt: value.NextSyncAt,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func recordResponse(value billing.Record) billingRecordResponse {
	return billingRecordResponse{ID: value.ID, ConnectorID: value.ConnectorID, ExternalID: value.ExternalID, SourceType: value.SourceType,
		AccountID: value.AccountID, Service: value.Service, Product: value.Product, Model: value.Model, Currency: value.Currency,
		GrossAmount: value.GrossAmount, DiscountAmount: value.DiscountAmount, TaxAmount: value.TaxAmount, RefundAmount: value.RefundAmount,
		NetAmount: value.NetAmount, UsageQuantity: value.UsageQuantity, UsageUnit: value.UsageUnit, UsageStartAt: value.UsageStartAt,
		UsageEndAt: value.UsageEndAt, SourceTimezone: value.SourceTimezone, BillingPeriod: value.BillingPeriod,
		ExternalRequestID: value.ExternalRequestID, RawSnapshotID: value.RawSnapshotID, Metadata: value.Metadata,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func syncRunResponse(value billing.SyncRun) billingSyncRunResponse {
	return billingSyncRunResponse{ID: value.ID, ConnectorID: value.ConnectorID, Trigger: value.Trigger, Status: value.Status,
		RangeStart: value.RangeStart, RangeEnd: value.RangeEnd, CursorStart: value.CursorStart, CursorEnd: value.CursorEnd,
		PagesFetched: value.PagesFetched, Attempts: value.Attempts, RecordsSeen: value.RecordsSeen,
		RecordsInserted: value.RecordsInserted, RecordsUpdated: value.RecordsUpdated, ErrorCode: value.ErrorCode,
		ErrorMessage: value.ErrorMessage, StartedAt: value.StartedAt, FinishedAt: value.FinishedAt}
}
