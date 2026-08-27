package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) handleAdminPluginActionPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "providers", r.Method)
	if !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	actionID := strings.TrimSpace(r.PathValue("action_id"))
	if pluginID == "" || actionID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_action_not_found", "Plugin action not found"))
		return
	}
	if _, ok := s.pluginRegistry.Describe(pluginID); !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	var payload json.RawMessage
	if err := s.decodeJSONOptional(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := s.executeRawPluginAction(r.Context(), user, pluginID, actionID, payload)
	if err != nil {
		s.recordPluginActionAudit(r, user, pluginID, actionID, "failed", err.Error())
		writeError(w, r, pluginActionHTTPError(err))
		return
	}
	if descriptor, ok := s.pluginActions.Describe(pluginID, actionID); ok {
		result, err = s.applyPluginActionSideEffects(r.Context(), descriptor, payload, result)
		if err != nil {
			s.recordPluginActionAudit(r, user, pluginID, actionID, "failed", err.Error())
			writeError(w, r, err)
			return
		}
	}
	result = sanitizePluginActionResult(result)
	s.recordPluginActionAudit(r, user, pluginID, actionID, "success", "")
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminPluginBackgroundJobRunPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "providers", r.Method)
	if !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	jobID := strings.TrimSpace(r.PathValue("job_id"))
	if pluginID == "" || jobID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_background_job_not_found", "Plugin background job not found"))
		return
	}
	if _, ok := s.pluginRegistry.Describe(pluginID); !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	var payload json.RawMessage
	if err := s.decodeJSONOptional(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	record, err := s.pluginBackgroundRunner.Run(r.Context(), pluginmeta.BackgroundJobInvocation{
		PluginID: pluginID,
		JobID:    jobID,
		Trigger:  "manual",
		Actor:    pluginActionActor(user),
		Payload:  payload,
	})
	record = sanitizePluginBackgroundJobRunRecord(record)
	if err != nil {
		s.recordPluginBackgroundJobAudit(r, user, pluginID, jobID, "failed", err.Error())
		writeError(w, r, pluginBackgroundJobHTTPError(err))
		return
	}
	s.recordPluginBackgroundJobAudit(r, user, pluginID, jobID, "success", "")
	writeJSON(w, http.StatusOK, map[string]any{"data": record})
}

func (s *Server) applyPluginActionSideEffects(ctx context.Context, descriptor pluginmeta.ActionDescriptor, payload json.RawMessage, result pluginmeta.ActionResult) (pluginmeta.ActionResult, error) {
	switch strings.TrimSpace(descriptor.Capability) {
	case "credentials.refresh":
		return s.applyCredentialsRefreshActionSideEffects(ctx, descriptor, payload, result)
	case "image.capability.configure":
		return s.applyImageCapabilityActionSideEffects(ctx, descriptor, payload, result)
	default:
		return result, nil
	}
}

func (s *Server) applyCredentialsRefreshActionSideEffects(ctx context.Context, descriptor pluginmeta.ActionDescriptor, payload json.RawMessage, result pluginmeta.ActionResult) (pluginmeta.ActionResult, error) {
	credentials, hasCredentials := pluginActionResultCredentials(result.Data)
	reauthorizationRequired := pluginActionResultReauthorizationRequired(result.Data)
	if !hasCredentials && !reauthorizationRequired {
		return result, nil
	}
	var request struct {
		ResourceID string `json:"resource_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &request); err != nil {
			return result, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
		}
	}
	resourceID := strings.TrimSpace(request.ResourceID)
	if resourceID == "" {
		return result, NewHTTPError(http.StatusBadGateway, "plugin_credentials_refresh_missing_resource", "Credentials refresh action returned credentials without a resource_id target")
	}
	resource, ok := s.store.GetProviderResource(resourceID)
	if !ok {
		return result, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return result, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if descriptor.Subject != "" && descriptor.Subject != provider.Type {
		return result, NewHTTPError(http.StatusForbidden, "plugin_action_subject_mismatch", "Plugin action subject does not match the Provider type")
	}
	expectedResourceType := strings.TrimSpace(firstNonEmpty(descriptor.Metadata["provider_resource_type"], descriptor.Metadata["resource_type"]))
	if expectedResourceType != "" && resource.ResourceType != expectedResourceType {
		return result, NewHTTPError(http.StatusForbidden, "plugin_action_resource_type_mismatch", "Plugin action resource type does not match the Provider resource")
	}
	if reauthorizationRequired && !hasCredentials {
		updated, err := s.store.UpdateProviderResourceOptions(resourceID, map[string]string{providerResourceReauthorizationRequiredOption: "true"})
		if err != nil {
			return result, err
		}
		result.Data = pluginActionResultWithCredentialSummary(result.Data, updated.CredentialSummary)
		return result, nil
	}
	updated, err := s.store.UpdateProviderResource(resourceID, providerResourceCredentialPatch(resource, credentials))
	if err != nil {
		return result, err
	}
	result.Data = pluginActionResultWithCredentialSummary(result.Data, updated.CredentialSummary)
	return result, nil
}

func pluginActionResultCredentials(data any) (*ProviderResourceCredentials, bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var envelope struct {
		Credentials *ProviderResourceCredentials `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Credentials == nil {
		return nil, false
	}
	return envelope.Credentials, true
}

func pluginActionResultReauthorizationRequired(data any) bool {
	raw, err := json.Marshal(data)
	if err != nil {
		return false
	}
	var envelope struct {
		ReauthorizationRequired bool `json:"reauthorization_required"`
		CredentialSummary       struct {
			ReauthorizationRequired string `json:"oauth_reauthorization_required"`
		} `json:"credential_summary"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	return envelope.ReauthorizationRequired || strings.EqualFold(strings.TrimSpace(envelope.CredentialSummary.ReauthorizationRequired), "true")
}

func providerResourceCredentialPatch(resource ProviderResource, credentials *ProviderResourceCredentials) ProviderResource {
	return ProviderResource{
		ProviderID:     resource.ProviderID,
		Name:           resource.Name,
		ResourceType:   resource.ResourceType,
		BaseURL:        resource.BaseURL,
		Group:          resource.Group,
		Region:         resource.Region,
		Environment:    resource.Environment,
		Status:         resource.Status,
		Healthy:        resource.Healthy,
		Priority:       resource.Priority,
		Weight:         resource.Weight,
		RateLimitRPM:   resource.RateLimitRPM,
		TokenLimitTPM:  resource.TokenLimitTPM,
		MaxConcurrency: resource.MaxConcurrency,
		Options:        resource.Options,
		Credentials:    credentials,
	}
}

func pluginActionResultWithCredentialSummary(data any, summary map[string]string) any {
	raw, err := json.Marshal(data)
	if err != nil {
		return map[string]any{"credential_summary": summary}
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{"credential_summary": summary}
	}
	delete(result, "credentials")
	result["credential_summary"] = summary
	return result
}

func sanitizePluginActionResult(result pluginmeta.ActionResult) pluginmeta.ActionResult {
	result.Data = sanitizePluginActionValue(result.Data)
	if len(result.Metadata) > 0 {
		metadata := map[string]string{}
		for key, value := range result.Metadata {
			if sensitivePluginActionResultKey(key) {
				metadata[key] = "[redacted]"
				continue
			}
			metadata[key] = value
		}
		result.Metadata = metadata
	}
	return result
}

func sanitizePluginActionValue(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return value
	}
	return redactPluginActionValue(decoded)
}

func redactPluginActionValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, child := range typed {
			if sensitivePluginActionResultKey(key) {
				result[key] = "[redacted]"
				continue
			}
			result[key] = redactPluginActionValue(child)
		}
		return result
	case []any:
		for index := range typed {
			typed[index] = redactPluginActionValue(typed[index])
		}
		return typed
	default:
		return value
	}
}

func sensitivePluginActionResultKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "reauthorization_required" || normalized == providerResourceReauthorizationRequiredOption {
		return false
	}
	return normalized == "access_token" ||
		normalized == "refresh_token" ||
		normalized == "id_token" ||
		strings.Contains(normalized, "secret") ||
		normalized == "api_key" ||
		strings.HasSuffix(normalized, "_api_key") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "cookie") ||
		normalized == "credential" ||
		normalized == "credentials" ||
		normalized == "credential_blob" ||
		strings.Contains(normalized, "private_key")
}

func sanitizePluginBackgroundJobRunRecord(record pluginmeta.BackgroundJobRunRecord) pluginmeta.BackgroundJobRunRecord {
	record.Result.Data = sanitizePluginActionValue(record.Result.Data)
	if len(record.Result.Metadata) > 0 {
		metadata := map[string]string{}
		for key, value := range record.Result.Metadata {
			if sensitivePluginActionResultKey(key) {
				metadata[key] = "[redacted]"
				continue
			}
			metadata[key] = value
		}
		record.Result.Metadata = metadata
	}
	return record
}

func sanitizePluginBackgroundJobRunRecords(records []pluginmeta.BackgroundJobRunRecord) []pluginmeta.BackgroundJobRunRecord {
	sanitized := make([]pluginmeta.BackgroundJobRunRecord, 0, len(records))
	for _, record := range records {
		sanitized = append(sanitized, sanitizePluginBackgroundJobRunRecord(record))
	}
	return sanitized
}

func (s *Server) recordPluginBackgroundJobAudit(r *http.Request, user AdminUser, pluginID string, jobID string, status string, message string) {
	s.store.RecordAuditEvent(AuditEvent{
		ActorUserID:  user.ID,
		ActorName:    user.Name,
		ActorRole:    user.Role,
		Action:       "plugin.background_job." + jobID,
		ResourceType: "plugin",
		ResourceID:   pluginID,
		Status:       status,
		Message:      message,
		IP:           s.clientIP(r),
		UserAgent:    r.UserAgent(),
	})
}

func pluginBackgroundJobHTTPError(err error) error {
	if errors.Is(err, pluginmeta.ErrPluginBackgroundJobNotFound) {
		return NewHTTPError(http.StatusNotFound, "plugin_background_job_not_found", "Plugin background job not found")
	}
	if errors.Is(err, pluginmeta.ErrPluginBackgroundJobUnavailable) {
		return NewHTTPError(http.StatusNotImplemented, "plugin_background_job_unavailable", "Plugin background job handler is unavailable")
	}
	if errors.Is(err, pluginmeta.ErrPluginBackgroundJobInvalidPayload) {
		return NewHTTPError(http.StatusBadRequest, "invalid_plugin_background_job_payload", err.Error())
	}
	if errors.Is(err, pluginmeta.ErrPluginBackgroundJobInvalidResult) {
		return NewHTTPError(http.StatusBadGateway, "invalid_plugin_background_job_result", err.Error())
	}
	if errors.Is(err, pluginmeta.ErrPluginBackgroundJobBusy) {
		return NewHTTPError(http.StatusConflict, "plugin_background_job_busy", "Plugin background job concurrency limit reached")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "internal_error" {
		return httpErr
	}
	return NewHTTPError(http.StatusInternalServerError, "plugin_background_job_failed", "Plugin background job failed")
}

func (s *Server) recordPluginActionAudit(r *http.Request, user AdminUser, pluginID string, actionID string, status string, message string) {
	s.store.RecordAuditEvent(AuditEvent{
		ActorUserID:  user.ID,
		ActorName:    user.Name,
		ActorRole:    user.Role,
		Action:       "plugin.action." + actionID,
		ResourceType: "plugin",
		ResourceID:   pluginID,
		Status:       status,
		Message:      message,
		IP:           s.clientIP(r),
		UserAgent:    r.UserAgent(),
	})
}

func pluginActionHTTPError(err error) error {
	if errors.Is(err, pluginmeta.ErrPluginActionNotFound) {
		return NewHTTPError(http.StatusNotFound, "plugin_action_not_found", "Plugin action not found")
	}
	if errors.Is(err, pluginmeta.ErrPluginActionUnavailable) {
		return NewHTTPError(http.StatusNotImplemented, "plugin_action_unavailable", "Plugin action handler is unavailable")
	}
	if errors.Is(err, pluginmeta.ErrPluginActionInvalidPayload) {
		return NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", err.Error())
	}
	if errors.Is(err, pluginmeta.ErrPluginActionInvalidResult) {
		return NewHTTPError(http.StatusBadGateway, "invalid_plugin_action_result", err.Error())
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "internal_error" {
		return httpErr
	}
	return NewHTTPError(http.StatusInternalServerError, "plugin_action_failed", "Plugin action failed")
}
