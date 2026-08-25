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
	result, err := s.pluginActions.Execute(r.Context(), pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
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

func (s *Server) applyPluginActionSideEffects(ctx context.Context, descriptor pluginmeta.ActionDescriptor, payload json.RawMessage, result pluginmeta.ActionResult) (pluginmeta.ActionResult, error) {
	if strings.TrimSpace(descriptor.Capability) != "credentials.refresh" {
		return result, nil
	}
	credentials, ok := pluginActionResultCredentials(result.Data)
	if !ok {
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
