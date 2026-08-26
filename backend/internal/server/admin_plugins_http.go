package server

import (
	"errors"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type adminPluginStateResponse struct {
	PluginID        string            `json:"plugin_id"`
	Status          pluginmeta.Status `json:"status"`
	Reason          string            `json:"reason,omitempty"`
	RestartRequired bool              `json:"restart_required"`
}

func (s *Server) handleAdminPluginStatePatch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	if pluginID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	var payload struct {
		Status pluginmeta.Status `json:"status"`
		Reason string            `json:"reason"`
	}
	if err := s.decodeJSON(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(string(payload.Status)) == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_state", "Plugin status is required"))
		return
	}
	state, err := pluginmeta.NormalizePackageState(pluginmeta.PackageState{
		Status: payload.Status,
		Reason: payload.Reason,
	})
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_state", err.Error()))
		return
	}
	pkg, err := pluginmeta.NewRuntime(s.config.PluginDir).UpdatePackageState(pluginID, state)
	if err != nil {
		if errors.Is(err, pluginmeta.ErrPackageNotFound) {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
			return
		}
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_state_update_failed", "Plugin state could not be updated"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginStateResponse{
		PluginID:        pkg.Manifest.ID,
		Status:          pkg.State.Status,
		Reason:          pkg.State.Reason,
		RestartRequired: true,
	}})
}
