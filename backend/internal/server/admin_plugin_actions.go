package server

import (
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
	s.recordPluginActionAudit(r, user, pluginID, actionID, "success", "")
	writeJSON(w, http.StatusOK, result)
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
	httpErr := AsHTTPError(err)
	if httpErr.Code != "internal_error" {
		return httpErr
	}
	return NewHTTPError(http.StatusInternalServerError, "plugin_action_failed", "Plugin action failed")
}
