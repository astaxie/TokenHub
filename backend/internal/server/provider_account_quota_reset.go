package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openAIAccountQuotaResetValueMaxLength = 256
	openAIAccountQuotaResetOperationKind  = "codex-quota-reset-operations"
	openAIAccountQuotaResetPending        = "pending"
	openAIAccountQuotaResetUnknown        = "unknown"
	openAIAccountQuotaResetSucceeded      = "succeeded"
	openAIAccountQuotaResetFailed         = "failed"
	openAIAccountQuotaResetDangerHeader   = "X-TokenHub-Dangerous-Operation"
	openAIAccountQuotaResetDangerValue    = "codex-quota-reset"
)

type openAIAccountQuotaResetCredit struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

type openAIAccountQuotaResetCredits struct {
	AvailableCount   int                                      `json:"available_count"`
	Credits          []openAIAccountQuotaResetCredit          `json:"credits"`
	FetchedAt        int64                                    `json:"fetched_at"`
	PendingOperation *openAIAccountQuotaResetPendingOperation `json:"pending_operation,omitempty"`
}

type openAIAccountQuotaResetPendingOperation struct {
	IdempotencyKey         string  `json:"idempotency_key"`
	ExpectedAvailableCount int     `json:"expected_available_count"`
	CreditID               string  `json:"credit_id"`
	ExpiresAt              *string `json:"expires_at"`
	State                  string  `json:"state"`
}

type openAIAccountQuotaResetRequest struct {
	Confirm                bool    `json:"confirm"`
	IdempotencyKey         string  `json:"idempotency_key"`
	ExpectedAvailableCount *int    `json:"expected_available_count"`
	CreditID               *string `json:"credit_id,omitempty"`
}

type openAIAccountQuotaResetResult struct {
	Code         string `json:"code"`
	WindowsReset int    `json:"windows_reset"`
}

type openAIAccountQuotaResetConsumeRequest struct {
	RedeemRequestID string `json:"redeem_request_id"`
	CreditID        string `json:"credit_id,omitempty"`
}

type openAIAccountQuotaResetOperation struct {
	Resource               AdminResource
	IdempotencyKey         string
	ResourceID             string
	ExpectedAvailableCount int
	RequestedCreditID      string
	CreditID               string
	ExpiresAt              string
	State                  string
	ResultCode             string
	WindowsReset           int
	ErrorCode              string
	ErrorStatus            int
	UpstreamStatus         int
}

func (s *Server) serveAdminOpenAIAccountQuotaResetCredits(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	details, err := s.queryOpenAIAccountQuotaResetCredits(r.Context(), resourceID)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordAdminAuditWithStatus(r, user, "query_quota_reset_credits", "provider_resource", resourceID, "failed", httpErr.Code, "", map[string]any{"error_code": httpErr.Code})
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "query_quota_reset_credits", "provider_resource", resourceID, "", map[string]any{"available_count": details.AvailableCount})
	writeJSON(w, http.StatusOK, details)
}

func (s *Server) serveAdminOpenAIAccountQuotaReset(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	if strings.TrimSpace(r.Header.Get(openAIAccountQuotaResetDangerHeader)) != openAIAccountQuotaResetDangerValue {
		s.recordOpenAIAccountQuotaResetFailure(r, user, resourceID, "quota_reset_danger_confirmation_required")
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "quota_reset_danger_confirmation_required", "The dangerous operation confirmation header is required"))
		return
	}
	var req openAIAccountQuotaResetRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.recordOpenAIAccountQuotaResetFailure(r, user, resourceID, AsHTTPError(err).Code)
		writeError(w, r, err)
		return
	}
	if err := validateOpenAIAccountQuotaResetRequest(req, r.Header.Get("Idempotency-Key")); err != nil {
		s.recordOpenAIAccountQuotaResetFailure(r, user, resourceID, AsHTTPError(err).Code)
		writeError(w, r, err)
		return
	}
	result, err := s.resetOpenAIAccountQuota(r.Context(), resourceID, req)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordOpenAIAccountQuotaResetFailure(r, user, resourceID, httpErr.Code)
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "reset_quota", "provider_resource", resourceID, "", map[string]any{
		"code":          result.Code,
		"windows_reset": result.WindowsReset,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordOpenAIAccountQuotaResetFailure(r *http.Request, user AdminUser, resourceID string, errorCode string) {
	status := "failed"
	if errorCode == "openai_quota_reset_outcome_unknown" {
		status = "unknown"
	}
	s.recordAdminAuditWithStatus(r, user, "reset_quota", "provider_resource", resourceID, status, errorCode, "", map[string]any{"error_code": errorCode})
}

func validateOpenAIAccountQuotaResetRequest(req openAIAccountQuotaResetRequest, idempotencyHeader string) error {
	if !req.Confirm {
		return NewHTTPError(http.StatusBadRequest, "quota_reset_confirmation_required", "confirm must be true")
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		return NewHTTPError(http.StatusBadRequest, "quota_reset_idempotency_key_required", "idempotency_key is required")
	}
	if len(key) > openAIAccountQuotaResetValueMaxLength {
		return NewHTTPError(http.StatusBadRequest, "quota_reset_idempotency_key_invalid", "idempotency_key is too long")
	}
	if strings.TrimSpace(idempotencyHeader) == "" || strings.TrimSpace(idempotencyHeader) != key {
		return NewHTTPError(http.StatusBadRequest, "quota_reset_idempotency_header_mismatch", "Idempotency-Key header must be present and match idempotency_key")
	}
	if req.ExpectedAvailableCount == nil || *req.ExpectedAvailableCount < 0 {
		return NewHTTPError(http.StatusBadRequest, "quota_reset_expected_count_required", "expected_available_count must be a non-negative integer")
	}
	if req.CreditID == nil {
		return NewHTTPError(http.StatusBadRequest, "quota_reset_credit_id_required", "credit_id is required")
	}
	creditID := strings.TrimSpace(*req.CreditID)
	if creditID == "" || len(creditID) > openAIAccountQuotaResetValueMaxLength {
		return NewHTTPError(http.StatusBadRequest, "quota_reset_credit_id_invalid", "credit_id must be a non-empty value of at most 256 characters")
	}
	return nil
}

func (s *Server) queryOpenAIAccountQuotaResetCredits(ctx context.Context, resourceID string) (openAIAccountQuotaResetCredits, error) {
	if _, _, err := s.openAIAccountQuotaResetResource(resourceID); err != nil {
		return openAIAccountQuotaResetCredits{}, err
	}
	details, _, err := s.fetchOpenAIAccountQuotaResetCredits(ctx, resourceID)
	if err == nil {
		details.PendingOperation = s.openAIAccountQuotaResetPendingOperation(resourceID)
	}
	return details, err
}

func (s *Server) resetOpenAIAccountQuota(ctx context.Context, resourceID string, req openAIAccountQuotaResetRequest) (openAIAccountQuotaResetResult, error) {
	var result openAIAccountQuotaResetResult
	err := s.store.RunClusterOperation(ctx, "provider-quota-reset:"+resourceID, func(leaseCtx context.Context) error {
		if _, _, validateErr := s.openAIAccountQuotaResetResource(resourceID); validateErr != nil {
			return validateErr
		}

		operationID := openAIAccountQuotaResetOperationID(resourceID, req.IdempotencyKey)
		resources := s.store.ListResources(openAIAccountQuotaResetOperationKind)
		operation, found := openAIAccountQuotaResetOperationByID(resources, operationID)
		var creds ProviderResourceCredentials
		endpoint := ""
		if found {
			if payloadErr := openAIAccountQuotaResetOperationPayloadError(operation, resourceID, req); payloadErr != nil {
				return payloadErr
			}
			switch operation.State {
			case openAIAccountQuotaResetSucceeded:
				if openAIAccountQuotaResetResultError(operation.ResultCode) != nil || operation.WindowsReset < 0 {
					return NewHTTPError(http.StatusInternalServerError, "quota_reset_operation_invalid", "Stored quota reset operation is invalid")
				}
				result = openAIAccountQuotaResetResult{Code: operation.ResultCode, WindowsReset: operation.WindowsReset}
				return nil
			case openAIAccountQuotaResetFailed:
				return openAIAccountQuotaResetStoredError(operation)
			case openAIAccountQuotaResetPending, openAIAccountQuotaResetUnknown:
				var credentialErr error
				creds, credentialErr = s.store.RefreshProviderResourceCredentials(leaseCtx, resourceID, false)
				if credentialErr != nil {
					return credentialErr
				}
			default:
				return NewHTTPError(http.StatusInternalServerError, "quota_reset_operation_invalid", "Stored quota reset operation is invalid")
			}
		} else {
			if openAIAccountQuotaResetHasUnresolvedOperation(resources, resourceID) {
				return NewHTTPError(http.StatusConflict, "quota_reset_operation_in_progress", "Another quota reset operation must be resolved before starting a new one")
			}
			var endpointErr error
			endpoint, endpointErr = openAIAccountQuotaResetEndpoint(firstNonEmpty(s.codexSubscription.QuotaURL, openAIAccountQuotaURL), true)
			if endpointErr != nil {
				return endpointErr
			}
			preflight, freshCreds, preflightErr := s.fetchOpenAIAccountQuotaResetCredits(leaseCtx, resourceID)
			if preflightErr != nil {
				return preflightErr
			}
			if preflight.AvailableCount != *req.ExpectedAvailableCount {
				return NewHTTPError(http.StatusConflict, "quota_reset_available_count_changed", "Available reset credits changed; reload the account quota before confirming")
			}
			if preflight.AvailableCount == 0 {
				return NewHTTPError(http.StatusConflict, "quota_reset_no_credit", "No reset credit is currently available")
			}
			requestedCreditID := openAIAccountQuotaResetRequestedCreditID(req)
			if requestedCreditID == "" {
				return NewHTTPError(http.StatusBadRequest, "quota_reset_credit_id_required", "credit_id is required for a new quota reset operation")
			}
			selected, selectedOK := selectOpenAIAccountQuotaResetCredit(preflight.Credits, requestedCreditID, time.Now().UTC())
			if !selectedOK {
				return NewHTTPError(http.StatusConflict, "quota_reset_credit_unavailable", "The selected reset credit is no longer available")
			}
			operation, preflightErr = s.createOpenAIAccountQuotaResetOperation(operationID, resourceID, req, selected)
			if preflightErr != nil {
				return preflightErr
			}
			creds = freshCreds
		}
		if endpoint == "" {
			var endpointErr error
			endpoint, endpointErr = openAIAccountQuotaResetEndpoint(firstNonEmpty(s.codexSubscription.QuotaURL, openAIAccountQuotaURL), true)
			if endpointErr != nil {
				return endpointErr
			}
		}

		fetched, executeErr := s.executeOpenAIAccountQuotaResetOperation(leaseCtx, resourceID, endpoint, creds, operation)
		if executeErr != nil {
			return executeErr
		}
		result = fetched
		return nil
	})
	return result, err
}

func openAIAccountQuotaResetRequestedCreditID(req openAIAccountQuotaResetRequest) string {
	if req.CreditID == nil {
		return ""
	}
	return strings.TrimSpace(*req.CreditID)
}

func openAIAccountQuotaResetOperationID(resourceID string, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(resourceID) + "\x00" + strings.TrimSpace(idempotencyKey)))
	return fmt.Sprintf("codex_quota_reset_%x", digest[:])
}

func openAIAccountQuotaResetOperationByID(resources []AdminResource, operationID string) (openAIAccountQuotaResetOperation, bool) {
	for _, resource := range resources {
		if resource.ID == operationID {
			return openAIAccountQuotaResetOperationFromResource(resource), true
		}
	}
	return openAIAccountQuotaResetOperation{}, false
}

func openAIAccountQuotaResetOperationFromResource(resource AdminResource) openAIAccountQuotaResetOperation {
	return openAIAccountQuotaResetOperation{
		Resource:               resource,
		IdempotencyKey:         strings.TrimSpace(stringField(resource.Fields, "idempotency_key")),
		ResourceID:             strings.TrimSpace(stringField(resource.Fields, "resource_id")),
		ExpectedAvailableCount: int(int64Field(resource.Fields, "expected_available_count")),
		RequestedCreditID:      strings.TrimSpace(stringField(resource.Fields, "requested_credit_id")),
		CreditID:               strings.TrimSpace(stringField(resource.Fields, "credit_id")),
		ExpiresAt:              strings.TrimSpace(stringField(resource.Fields, "expires_at")),
		State:                  strings.ToLower(strings.TrimSpace(stringField(resource.Fields, "state"))),
		ResultCode:             strings.ToLower(strings.TrimSpace(stringField(resource.Fields, "result_code"))),
		WindowsReset:           int(int64Field(resource.Fields, "windows_reset")),
		ErrorCode:              strings.TrimSpace(stringField(resource.Fields, "error_code")),
		ErrorStatus:            int(int64Field(resource.Fields, "error_status")),
		UpstreamStatus:         int(int64Field(resource.Fields, "upstream_status")),
	}
}

func openAIAccountQuotaResetHasUnresolvedOperation(resources []AdminResource, resourceID string) bool {
	for _, resource := range resources {
		operation := openAIAccountQuotaResetOperationFromResource(resource)
		if operation.ResourceID == resourceID && (operation.State == openAIAccountQuotaResetPending || operation.State == openAIAccountQuotaResetUnknown) {
			return true
		}
	}
	return false
}

func openAIAccountQuotaResetOperationPayloadError(operation openAIAccountQuotaResetOperation, resourceID string, req openAIAccountQuotaResetRequest) error {
	requestedCreditID := openAIAccountQuotaResetRequestedCreditID(req)
	creditMatches := operation.RequestedCreditID == requestedCreditID
	if operation.RequestedCreditID == "" && requestedCreditID == operation.CreditID {
		creditMatches = true
	}
	if operation.ResourceID != resourceID ||
		operation.IdempotencyKey != strings.TrimSpace(req.IdempotencyKey) ||
		operation.ExpectedAvailableCount != *req.ExpectedAvailableCount ||
		!creditMatches {
		return NewHTTPError(http.StatusConflict, "quota_reset_idempotency_payload_mismatch", "The idempotency_key was already used with a different quota reset request")
	}
	return nil
}

func openAIAccountQuotaResetStoredError(operation openAIAccountQuotaResetOperation) error {
	status := operation.ErrorStatus
	if status < http.StatusBadRequest || status > 599 {
		status = http.StatusBadGateway
	}
	code := operation.ErrorCode
	if code == "" {
		return NewHTTPError(http.StatusInternalServerError, "quota_reset_operation_invalid", "Stored quota reset operation is invalid")
	}
	message := "The stored quota reset operation failed"
	switch code {
	case "quota_reset_nothing_to_reset":
		message = "No active usage window can be reset"
	case "quota_reset_no_credit":
		message = "No reset credit is currently available"
	case "quota_reset_ineligible":
		message = "The Codex account or current usage windows are not eligible for reset"
	case "openai_quota_reset_rate_limited":
		message = "OpenAI quota reset endpoint is rate limited"
	case "openai_quota_reset_unauthorized":
		message = "OpenAI quota reset authorization was rejected"
	case "openai_quota_reset_forbidden":
		message = "OpenAI quota reset request was forbidden"
	}
	result := NewHTTPError(status, code, message)
	result.UpstreamStatus = operation.UpstreamStatus
	return result
}

func selectOpenAIAccountQuotaResetCredit(credits []openAIAccountQuotaResetCredit, requestedCreditID string, now time.Time) (openAIAccountQuotaResetCredit, bool) {
	for _, credit := range credits {
		if requestedCreditID != "" && credit.ID != requestedCreditID {
			continue
		}
		if openAIAccountQuotaResetCreditAvailable([]openAIAccountQuotaResetCredit{credit}, credit.ID, now) {
			return credit, true
		}
	}
	return openAIAccountQuotaResetCredit{}, false
}

func (s *Server) createOpenAIAccountQuotaResetOperation(operationID string, resourceID string, req openAIAccountQuotaResetRequest, credit openAIAccountQuotaResetCredit) (openAIAccountQuotaResetOperation, error) {
	expiresAt := ""
	if credit.ExpiresAt != nil {
		expiresAt = strings.TrimSpace(*credit.ExpiresAt)
	}
	fields := map[string]any{
		"idempotency_key":          strings.TrimSpace(req.IdempotencyKey),
		"resource_id":              resourceID,
		"expected_available_count": *req.ExpectedAvailableCount,
		"requested_credit_id":      openAIAccountQuotaResetRequestedCreditID(req),
		"credit_id":                credit.ID,
		"expires_at":               expiresAt,
		"state":                    openAIAccountQuotaResetPending,
		"result_code":              "",
		"windows_reset":            0,
		"error_code":               "",
		"error_status":             0,
		"upstream_status":          0,
	}
	s.store.CreateResource(openAIAccountQuotaResetOperationKind, AdminResource{
		ID:     operationID,
		Name:   "Codex quota reset operation",
		Status: StatusActive,
		Fields: fields,
	})
	persisted, ok := openAIAccountQuotaResetOperationByID(s.store.ListResources(openAIAccountQuotaResetOperationKind), operationID)
	if !ok || persisted.State != openAIAccountQuotaResetPending ||
		persisted.IdempotencyKey != strings.TrimSpace(req.IdempotencyKey) ||
		persisted.ResourceID != resourceID ||
		persisted.ExpectedAvailableCount != *req.ExpectedAvailableCount ||
		persisted.RequestedCreditID != openAIAccountQuotaResetRequestedCreditID(req) ||
		persisted.CreditID != credit.ID || persisted.ExpiresAt != expiresAt {
		return openAIAccountQuotaResetOperation{}, NewHTTPError(http.StatusInternalServerError, "quota_reset_operation_persist_failed", "Failed to persist the quota reset operation")
	}
	return persisted, nil
}

func (s *Server) executeOpenAIAccountQuotaResetOperation(ctx context.Context, resourceID string, endpoint string, creds ProviderResourceCredentials, operation openAIAccountQuotaResetOperation) (openAIAccountQuotaResetResult, error) {
	if operation.CreditID == "" || operation.IdempotencyKey == "" {
		return openAIAccountQuotaResetResult{}, NewHTTPError(http.StatusInternalServerError, "quota_reset_operation_invalid", "Stored quota reset operation is invalid")
	}
	payload := openAIAccountQuotaResetConsumeRequest{
		RedeemRequestID: operation.IdempotencyKey,
		CreditID:        operation.CreditID,
	}
	result, status, consumeErr := consumeOpenAIAccountQuotaResetWithClient(ctx, s.codexSubscription.Client, endpoint, creds, payload)
	if status == http.StatusUnauthorized {
		refreshed, refreshErr := s.store.RefreshProviderResourceCredentials(ctx, resourceID, true)
		if refreshErr != nil {
			return openAIAccountQuotaResetResult{}, refreshErr
		}
		result, status, consumeErr = consumeOpenAIAccountQuotaResetWithClient(ctx, s.codexSubscription.Client, endpoint, refreshed, payload)
	}
	if consumeErr != nil {
		return openAIAccountQuotaResetResult{}, s.finishOpenAIAccountQuotaResetOperationError(operation, openAIAccountQuotaResetResult{}, consumeErr)
	}
	if mappedErr := openAIAccountQuotaResetResultError(result.Code); mappedErr != nil {
		AsHTTPError(mappedErr).UpstreamStatus = status
		return openAIAccountQuotaResetResult{}, s.finishOpenAIAccountQuotaResetOperationError(operation, result, mappedErr)
	}
	if _, err := s.updateOpenAIAccountQuotaResetOperation(operation, openAIAccountQuotaResetSucceeded, result, nil); err != nil {
		return openAIAccountQuotaResetResult{}, openAIAccountQuotaResetOutcomeUnknownError(http.StatusBadGateway)
	}
	return result, nil
}

func (s *Server) finishOpenAIAccountQuotaResetOperationError(operation openAIAccountQuotaResetOperation, result openAIAccountQuotaResetResult, err error) error {
	httpErr := AsHTTPError(err)
	if openAIAccountQuotaResetOutcomeIsUnknown(httpErr) {
		unknownErr := openAIAccountQuotaResetOutcomeUnknownError(httpErr.Status)
		unknownErr.UpstreamStatus = httpErr.UpstreamStatus
		if _, updateErr := s.updateOpenAIAccountQuotaResetOperation(operation, openAIAccountQuotaResetUnknown, result, unknownErr); updateErr != nil {
			return unknownErr
		}
		return unknownErr
	}
	if _, updateErr := s.updateOpenAIAccountQuotaResetOperation(operation, openAIAccountQuotaResetFailed, result, httpErr); updateErr != nil {
		return NewHTTPError(http.StatusInternalServerError, "quota_reset_operation_update_failed", "Failed to store the quota reset failure")
	}
	return err
}

func openAIAccountQuotaResetOutcomeIsUnknown(err *HTTPError) bool {
	if err == nil {
		return false
	}
	if err.UpstreamStatus >= http.StatusInternalServerError {
		return true
	}
	switch err.Code {
	case "quota_reset_nothing_to_reset", "quota_reset_no_credit", "quota_reset_ineligible":
		return false
	case "openai_quota_reset_outcome_unknown", "openai_quota_reset_invalid_response":
		return true
	}
	return err.UpstreamStatus > 0 && err.UpstreamStatus < http.StatusBadRequest
}

func openAIAccountQuotaResetOutcomeUnknownError(status int) *HTTPError {
	if status != http.StatusGatewayTimeout {
		status = http.StatusBadGateway
	}
	return NewHTTPError(status, "openai_quota_reset_outcome_unknown", "The reset result may be unknown; retry only with the same idempotency_key and credit_id")
}

func (s *Server) updateOpenAIAccountQuotaResetOperation(operation openAIAccountQuotaResetOperation, state string, result openAIAccountQuotaResetResult, operationErr *HTTPError) (openAIAccountQuotaResetOperation, error) {
	fields := make(map[string]any, len(operation.Resource.Fields))
	for key, value := range operation.Resource.Fields {
		fields[key] = value
	}
	fields["state"] = state
	fields["result_code"] = result.Code
	fields["windows_reset"] = result.WindowsReset
	fields["error_code"] = ""
	fields["error_status"] = 0
	fields["upstream_status"] = 0
	if operationErr != nil {
		fields["error_code"] = operationErr.Code
		fields["error_status"] = operationErr.Status
		fields["upstream_status"] = operationErr.UpstreamStatus
	}
	updated, err := s.store.UpdateResource(openAIAccountQuotaResetOperationKind, operation.Resource.ID, AdminResource{Fields: fields})
	if err != nil {
		return openAIAccountQuotaResetOperation{}, err
	}
	return openAIAccountQuotaResetOperationFromResource(updated), nil
}

func (s *Server) openAIAccountQuotaResetPendingOperation(resourceID string) *openAIAccountQuotaResetPendingOperation {
	var selected *openAIAccountQuotaResetOperation
	for _, resource := range s.store.ListResources(openAIAccountQuotaResetOperationKind) {
		operation := openAIAccountQuotaResetOperationFromResource(resource)
		if operation.ResourceID != resourceID || (operation.State != openAIAccountQuotaResetPending && operation.State != openAIAccountQuotaResetUnknown) {
			continue
		}
		if selected == nil || operation.Resource.UpdatedAt.After(selected.Resource.UpdatedAt) {
			copy := operation
			selected = &copy
		}
	}
	if selected == nil || selected.IdempotencyKey == "" || selected.CreditID == "" {
		return nil
	}
	var expiresAt *string
	if selected.ExpiresAt != "" {
		value := selected.ExpiresAt
		expiresAt = &value
	}
	return &openAIAccountQuotaResetPendingOperation{
		IdempotencyKey:         selected.IdempotencyKey,
		ExpectedAvailableCount: selected.ExpectedAvailableCount,
		CreditID:               selected.CreditID,
		ExpiresAt:              expiresAt,
		State:                  selected.State,
	}
}

func (s *Server) openAIAccountQuotaResetResource(resourceID string) (ProviderResource, Provider, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if provider.Type != ProviderOpenAICodex || resource.ResourceType != ProviderResourceOpenAISubscription {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusBadRequest, "provider_resource_quota_reset_unsupported", "Quota reset is only available for Codex OpenAI subscription resources")
	}
	if provider.Status != StatusActive || resource.Status != StatusActive {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusConflict, "provider_resource_inactive", "Provider and subscription resource must be active")
	}
	return resource, provider, nil
}

func (s *Server) fetchOpenAIAccountQuotaResetCredits(ctx context.Context, resourceID string) (openAIAccountQuotaResetCredits, ProviderResourceCredentials, error) {
	creds, err := s.store.RefreshProviderResourceCredentials(ctx, resourceID, false)
	if err != nil {
		return openAIAccountQuotaResetCredits{}, ProviderResourceCredentials{}, err
	}
	endpoint, err := openAIAccountQuotaResetEndpoint(firstNonEmpty(s.codexSubscription.QuotaURL, openAIAccountQuotaURL), false)
	if err != nil {
		return openAIAccountQuotaResetCredits{}, ProviderResourceCredentials{}, err
	}
	details, status, err := fetchOpenAIAccountQuotaResetCreditsWithClient(ctx, s.codexSubscription.Client, endpoint, creds)
	if status != http.StatusUnauthorized {
		return details, creds, err
	}
	refreshed, refreshErr := s.store.RefreshProviderResourceCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return openAIAccountQuotaResetCredits{}, ProviderResourceCredentials{}, refreshErr
	}
	details, _, err = fetchOpenAIAccountQuotaResetCreditsWithClient(ctx, s.codexSubscription.Client, endpoint, refreshed)
	return details, refreshed, err
}

func fetchOpenAIAccountQuotaResetCreditsWithClient(ctx context.Context, client *http.Client, endpoint string, creds ProviderResourceCredentials) (openAIAccountQuotaResetCredits, int, error) {
	accessToken, accountID, err := openAIAccountQuotaResetCredentials(creds)
	if err != nil {
		return openAIAccountQuotaResetCredits{}, 0, err
	}
	callCtx, cancel := context.WithTimeout(ctx, openAIAccountQuotaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return openAIAccountQuotaResetCredits{}, 0, NewHTTPError(http.StatusBadGateway, "openai_quota_reset_request_failed", "Failed to create OpenAI reset credit request")
	}
	setOpenAIAccountQuotaHeaders(req, accessToken, accountID)
	resp, err := openAIAccountQuotaResetDo(client, req)
	if err != nil {
		return openAIAccountQuotaResetCredits{}, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return openAIAccountQuotaResetCredits{}, resp.StatusCode, openAIAccountQuotaResetUpstreamError(resp.StatusCode, resp.Body)
	}
	var upstream struct {
		AvailableCount *int                            `json:"available_count"`
		Credits        []openAIAccountQuotaResetCredit `json:"credits"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&upstream); err != nil || upstream.AvailableCount == nil || *upstream.AvailableCount < 0 {
		return openAIAccountQuotaResetCredits{}, resp.StatusCode, NewHTTPError(http.StatusBadGateway, "openai_quota_reset_invalid_response", "OpenAI reset credit endpoint returned an invalid response")
	}
	details := openAIAccountQuotaResetCredits{
		AvailableCount: *upstream.AvailableCount,
		Credits:        minimalOpenAIAccountQuotaResetCredits(upstream.Credits),
		FetchedAt:      time.Now().UTC().Unix(),
	}
	return details, resp.StatusCode, nil
}

func consumeOpenAIAccountQuotaResetWithClient(ctx context.Context, client *http.Client, endpoint string, creds ProviderResourceCredentials, payload openAIAccountQuotaResetConsumeRequest) (openAIAccountQuotaResetResult, int, error) {
	accessToken, accountID, err := openAIAccountQuotaResetCredentials(creds)
	if err != nil {
		return openAIAccountQuotaResetResult{}, 0, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return openAIAccountQuotaResetResult{}, 0, NewHTTPError(http.StatusInternalServerError, "openai_quota_reset_request_failed", "Failed to encode OpenAI quota reset request")
	}
	callCtx, cancel := context.WithTimeout(ctx, openAIAccountQuotaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return openAIAccountQuotaResetResult{}, 0, NewHTTPError(http.StatusBadGateway, "openai_quota_reset_request_failed", "Failed to create OpenAI quota reset request")
	}
	setOpenAIAccountQuotaHeaders(req, accessToken, accountID)
	req.Header.Set("content-type", "application/json")
	resp, err := openAIAccountQuotaResetDo(client, req)
	if err != nil {
		return openAIAccountQuotaResetResult{}, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return openAIAccountQuotaResetResult{}, resp.StatusCode, openAIAccountQuotaResetUpstreamError(resp.StatusCode, resp.Body)
	}
	var result openAIAccountQuotaResetResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || result.WindowsReset < 0 {
		return openAIAccountQuotaResetResult{}, resp.StatusCode, NewHTTPError(http.StatusBadGateway, "openai_quota_reset_invalid_response", "OpenAI quota reset endpoint returned an invalid response")
	}
	result.Code = strings.ToLower(strings.TrimSpace(result.Code))
	return result, resp.StatusCode, nil
}

func openAIAccountQuotaResetDo(client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = &http.Client{Timeout: openAIAccountQuotaTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		if req.Method == http.MethodPost {
			status := http.StatusBadGateway
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(req.Context().Err(), context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			return nil, NewHTTPError(status, "openai_quota_reset_outcome_unknown", "The reset result may be unknown; retry only with the same idempotency_key and credit_id")
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(req.Context().Err(), context.DeadlineExceeded) {
			return nil, NewHTTPError(http.StatusGatewayTimeout, "openai_quota_reset_timeout", "OpenAI reset credit request timed out")
		}
		return nil, NewHTTPError(http.StatusBadGateway, "openai_quota_reset_request_failed", fmt.Sprintf("OpenAI reset credit request failed: %v", err))
	}
	return resp, nil
}

func openAIAccountQuotaResetCredentials(creds ProviderResourceCredentials) (string, string, error) {
	accessToken := strings.TrimSpace(creds.AccessToken)
	if accessToken == "" {
		return "", "", NewHTTPError(http.StatusBadRequest, "openai_account_token_missing", "OpenAI account access token is missing")
	}
	accountID := strings.TrimSpace(creds.AccountID)
	if accountID == "" {
		return "", "", NewHTTPError(http.StatusBadRequest, "openai_account_id_missing", "OpenAI ChatGPT account ID is missing")
	}
	return accessToken, accountID, nil
}

func openAIAccountQuotaResetEndpoint(quotaEndpoint string, consume bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(quotaEndpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", NewHTTPError(http.StatusInternalServerError, "openai_quota_reset_url_invalid", "OpenAI quota reset endpoint is invalid")
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/usage"):
		path = strings.TrimSuffix(path, "/usage") + "/rate-limit-reset-credits"
	case !strings.HasSuffix(path, "/rate-limit-reset-credits"):
		path += "/rate-limit-reset-credits"
	}
	if consume {
		path += "/consume"
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func minimalOpenAIAccountQuotaResetCredits(credits []openAIAccountQuotaResetCredit) []openAIAccountQuotaResetCredit {
	result := make([]openAIAccountQuotaResetCredit, 0, len(credits))
	for _, credit := range credits {
		credit.ID = strings.TrimSpace(credit.ID)
		credit.Status = strings.ToLower(strings.TrimSpace(credit.Status))
		if credit.ID == "" || credit.Status != "available" {
			continue
		}
		if credit.ExpiresAt != nil {
			expiresAt := strings.TrimSpace(*credit.ExpiresAt)
			credit.ExpiresAt = &expiresAt
		}
		result = append(result, credit)
	}
	return result
}

func openAIAccountQuotaResetCreditAvailable(credits []openAIAccountQuotaResetCredit, creditID string, now time.Time) bool {
	for _, credit := range credits {
		if credit.ID != creditID || credit.Status != "available" {
			continue
		}
		if credit.ExpiresAt == nil || strings.TrimSpace(*credit.ExpiresAt) == "" {
			return true
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*credit.ExpiresAt))
		return err == nil && expiresAt.After(now)
	}
	return false
}

func openAIAccountQuotaResetResultError(code string) error {
	switch code {
	case "reset", "already_redeemed":
		return nil
	case "nothing_to_reset":
		return NewHTTPError(http.StatusConflict, "quota_reset_nothing_to_reset", "No active usage window can be reset")
	case "no_credit":
		return NewHTTPError(http.StatusConflict, "quota_reset_no_credit", "No reset credit is currently available")
	default:
		return NewHTTPError(http.StatusBadGateway, "openai_quota_reset_invalid_response", "OpenAI quota reset endpoint returned an unknown result")
	}
}

func openAIAccountQuotaResetUpstreamError(status int, body io.Reader) error {
	code, detailCode, _ := openAIQuotaUpstreamErrorDetails(body)
	var result *HTTPError
	switch {
	case status == http.StatusTooManyRequests:
		result = NewHTTPError(http.StatusTooManyRequests, "openai_quota_reset_rate_limited", "OpenAI quota reset endpoint is rate limited")
	case status == http.StatusUnauthorized:
		result = NewHTTPError(http.StatusBadGateway, "openai_quota_reset_unauthorized", "OpenAI quota reset authorization was rejected")
	case detailCode == "rate_limit_reset_ineligible":
		result = NewHTTPError(http.StatusConflict, "quota_reset_ineligible", "The Codex account or current usage windows are not eligible for reset")
	case code == "nothing_to_reset":
		result = NewHTTPError(http.StatusConflict, "quota_reset_nothing_to_reset", "No active usage window can be reset")
	case code == "no_credit":
		result = NewHTTPError(http.StatusConflict, "quota_reset_no_credit", "No reset credit is currently available")
	case status == http.StatusForbidden:
		result = NewHTTPError(http.StatusBadGateway, "openai_quota_reset_forbidden", "OpenAI quota reset request was forbidden")
	default:
		result = NewHTTPError(http.StatusBadGateway, "openai_quota_reset_upstream_error", fmt.Sprintf("OpenAI quota reset endpoint returned %d", status))
	}
	result.UpstreamStatus = status
	return result
}

func openAIQuotaUpstreamErrorDetails(body io.Reader) (string, string, string) {
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&payload); err != nil {
		return "", "", ""
	}
	code := openAIQuotaUpstreamString(payload, "code")
	detailCode := ""
	message := openAIQuotaUpstreamString(payload, "message")
	for _, key := range []string{"detail", "error"} {
		switch value := payload[key].(type) {
		case string:
			if message == "" {
				message = strings.TrimSpace(value)
			}
		case map[string]any:
			nestedCode := openAIQuotaUpstreamString(value, "code")
			if key == "detail" {
				detailCode = nestedCode
			}
			if code == "" {
				code = nestedCode
			}
			if message == "" {
				message = openAIQuotaUpstreamString(value, "message")
			}
		}
	}
	return strings.ToLower(strings.TrimSpace(code)), strings.ToLower(strings.TrimSpace(detailCode)), strings.TrimSpace(message)
}

func openAIQuotaUpstreamString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}
