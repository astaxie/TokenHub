package server

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const maxAdminPluginInstallArchiveBytes = 64 << 20

type adminPluginStateResponse struct {
	PluginID        string            `json:"plugin_id"`
	Status          pluginmeta.Status `json:"status"`
	Reason          string            `json:"reason,omitempty"`
	RestartRequired bool              `json:"restart_required"`
}

type adminPluginInstallResponse struct {
	Plugin          pluginmeta.Descriptor `json:"plugin"`
	RestartRequired bool                  `json:"restart_required"`
	Replaced        bool                  `json:"replaced"`
}

func (s *Server) handleAdminPluginInstallPost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	var payload struct {
		DownloadURL    string            `json:"download_url"`
		ChecksumSHA256 string            `json:"checksum_sha256"`
		Replace        bool              `json:"replace"`
		Enable         bool              `json:"enable"`
		Reason         string            `json:"reason"`
		Status         pluginmeta.Status `json:"status"`
	}
	if err := s.decodeJSON(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	downloadURL := strings.TrimSpace(payload.DownloadURL)
	if err := validatePluginInstallDownloadURL(downloadURL); err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_download_url", err.Error()))
		return
	}
	checksum := strings.ToLower(strings.TrimSpace(payload.ChecksumSHA256))
	if checksum == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "plugin_checksum_required", "Plugin package checksum_sha256 is required"))
		return
	}
	state := pluginmeta.PackageState{Status: pluginmeta.StatusDisabled, Reason: payload.Reason}
	if payload.Enable {
		state.Status = pluginmeta.StatusEnabled
	}
	if strings.TrimSpace(string(payload.Status)) != "" {
		state.Status = payload.Status
	}
	archive, err := s.downloadPluginInstallArchive(r, downloadURL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	pkg, err := s.installPluginArchive(archive, pluginmeta.InstallOptions{
		ChecksumSHA256: checksum,
		Replace:        payload.Replace,
		InitialState:   state,
	})
	if err != nil {
		writeError(w, r, pluginInstallHTTPError(err))
		return
	}
	descriptor := pkg.Manifest.Descriptor()
	descriptor.Status = pkg.State.Status
	writeJSON(w, http.StatusCreated, map[string]any{"data": adminPluginInstallResponse{
		Plugin:          descriptor,
		RestartRequired: true,
		Replaced:        payload.Replace,
	}})
}

func (s *Server) handleAdminPluginUpdatePost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	var payload struct {
		DownloadURL    string `json:"download_url"`
		ChecksumSHA256 string `json:"checksum_sha256"`
	}
	if err := s.decodeJSONOptional(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	if pluginID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	descriptor, ok := s.pluginRegistry.Describe(pluginID)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	distribution := descriptor.Distribution
	if distribution == nil {
		distribution = &pluginmeta.Distribution{}
	}
	if strings.TrimSpace(distribution.DownloadURL) == "" && strings.TrimSpace(payload.DownloadURL) == "" ||
		strings.TrimSpace(distribution.ChecksumSHA256) == "" && strings.TrimSpace(payload.ChecksumSHA256) == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "plugin_distribution_unavailable", "Plugin distribution metadata is unavailable"))
		return
	}
	downloadURL := firstNonEmpty(strings.TrimSpace(payload.DownloadURL), distribution.DownloadURL)
	checksum := firstNonEmpty(strings.ToLower(strings.TrimSpace(payload.ChecksumSHA256)), distribution.ChecksumSHA256)
	if err := validatePluginInstallDownloadURL(downloadURL); err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_download_url", err.Error()))
		return
	}
	archive, err := s.downloadPluginInstallArchive(r, downloadURL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	current, _, err := pluginmeta.NewRuntime(s.config.PluginDir).DescribeInstalledPackage(pluginID)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_install_failed", "Plugin package could not be inspected"))
		return
	}
	if current.Manifest.ID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	pkg, err := s.installPluginArchive(archive, pluginmeta.InstallOptions{
		ChecksumSHA256: checksum,
		Replace:        true,
		InitialState:   current.State,
	})
	if err != nil {
		writeError(w, r, pluginInstallHTTPError(err))
		return
	}
	updated := pkg.Manifest.Descriptor()
	updated.Status = pkg.State.Status
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginInstallResponse{
		Plugin:          updated,
		RestartRequired: true,
		Replaced:        true,
	}})
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

func validatePluginInstallDownloadURL(raw string) error {
	if raw == "" {
		return errors.New("Plugin package download_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("Plugin package download_url must be an absolute URL")
	}
	if parsed.Scheme != "https" {
		return errors.New("Plugin package download_url must use HTTPS")
	}
	return nil
}

func (s *Server) downloadPluginInstallArchive(r *http.Request, downloadURL string) ([]byte, error) {
	client := s.pluginInstallClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_plugin_download_url", "Plugin package download_url is invalid")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "plugin_download_failed", "Plugin package could not be downloaded")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewHTTPError(http.StatusBadGateway, "plugin_download_failed", "Plugin package download failed")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAdminPluginInstallArchiveBytes+1))
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "plugin_download_failed", "Plugin package download failed")
	}
	if len(data) > maxAdminPluginInstallArchiveBytes {
		return nil, NewHTTPError(http.StatusBadRequest, "plugin_archive_too_large", "Plugin package archive is too large")
	}
	return data, nil
}

func (s *Server) installPluginArchive(archive []byte, options pluginmeta.InstallOptions) (pluginmeta.Package, error) {
	return pluginmeta.NewRuntime(s.config.PluginDir).InstallZipArchive(archive, options)
}

func pluginInstallHTTPError(err error) error {
	switch {
	case errors.Is(err, pluginmeta.ErrInstallChecksumMismatch):
		return NewHTTPError(http.StatusBadRequest, "plugin_checksum_mismatch", "Plugin package checksum verification failed")
	case errors.Is(err, pluginmeta.ErrInstallPackageExists):
		return NewHTTPError(http.StatusConflict, "plugin_package_exists", "Plugin package is already installed")
	default:
		return NewHTTPError(http.StatusBadRequest, "plugin_install_failed", err.Error())
	}
}
