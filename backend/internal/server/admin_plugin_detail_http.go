package server

import (
	"errors"
	"net/http"
	"os"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type adminPluginDetailResponse struct {
	Plugin  adminPluginDescriptorResponse `json:"plugin"`
	Package *pluginmeta.PackageInspection `json:"package,omitempty"`
}

func (s *Server) handleAdminPluginDetailGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	descriptors, err := s.adminPluginDescriptors()
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_discovery_failed", "Plugin packages could not be inspected"))
		return
	}
	var descriptor adminPluginDescriptorResponse
	found := false
	for _, candidate := range descriptors {
		if candidate.ID == pluginID {
			descriptor = candidate
			found = true
			break
		}
	}
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin was not found"))
		return
	}
	response := adminPluginDetailResponse{Plugin: descriptor}
	inspection, inspectErr := pluginmeta.NewRuntime(s.config.PluginDir).InspectPackage(pluginID)
	if errors.Is(inspectErr, pluginmeta.ErrPackageNotFound) {
		inspection, inspectErr = inspectBuiltInPluginPackage(s.config.ProviderCatalogFile, pluginID)
	}
	switch {
	case inspectErr == nil:
		response.Package = &inspection
	case errors.Is(inspectErr, pluginmeta.ErrPackageNotFound):
	default:
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_package_inspection_failed", "Plugin package files could not be inspected"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": response})
}

func (s *Server) handleAdminPluginFileGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	content, err := pluginmeta.NewRuntime(s.config.PluginDir).ReadPackageFile(pluginID, path)
	if errors.Is(err, pluginmeta.ErrPackageNotFound) {
		content, err = readBuiltInPluginPackageFile(s.config.ProviderCatalogFile, pluginID, path)
	}
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"data": content})
	case errors.Is(err, pluginmeta.ErrPackageFilePreviewUnavailable):
		writeError(w, r, NewHTTPError(http.StatusUnprocessableEntity, "plugin_file_preview_unavailable", "Plugin package file is not available for text preview"))
	case errors.Is(err, pluginmeta.ErrPackageNotFound), errors.Is(err, os.ErrNotExist):
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_file_not_found", "Plugin package file was not found"))
	default:
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_file_read_failed", "Plugin package file could not be read"))
	}
}
