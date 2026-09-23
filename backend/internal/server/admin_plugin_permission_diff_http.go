package server

import (
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type adminPluginPermissionDiffPreviewRequest struct {
	DownloadURL    string `json:"download_url"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	adminPluginInstallTrustPayload
}

type adminPluginPermissionDiffPreviewResponse struct {
	Operation        string                            `json:"operation"`
	PluginID         string                            `json:"plugin_id"`
	CurrentVersion   string                            `json:"current_version,omitempty"`
	CandidateVersion string                            `json:"candidate_version"`
	PermissionDiff   pluginmeta.PermissionDiff         `json:"permission_diff"`
	Trust            adminPluginPermissionTrustSummary `json:"trust"`
	Compatibility    adminPluginCompatibilityResponse  `json:"compatibility"`
}

type adminPluginPermissionTrustSummary struct {
	Verdict            pluginmeta.PluginTrustVerdict `json:"verdict"`
	ChecksumPresent    bool                          `json:"checksum_present"`
	SignaturePresent   bool                          `json:"signature_present"`
	SignatureAlgorithm string                        `json:"signature_algorithm,omitempty"`
	SignatureKeyID     string                        `json:"signature_key_id,omitempty"`
	ReasonCode         pluginmeta.PluginErrorCode    `json:"reason_code,omitempty"`
}

func (s *Server) handleAdminPluginPermissionDiffInstallPost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	var payload adminPluginPermissionDiffPreviewRequest
	if err := s.decodeJSON(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	response, httpErr := s.previewAdminPluginPermissionDiff(r, "", payload)
	if httpErr != nil {
		writeError(w, r, httpErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": response})
}

func (s *Server) handleAdminPluginPermissionDiffUpdatePost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	if pluginID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	var payload adminPluginPermissionDiffPreviewRequest
	if err := s.decodeJSONOptional(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	response, httpErr := s.previewAdminPluginPermissionDiff(r, pluginID, payload)
	if httpErr != nil {
		writeError(w, r, httpErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": response})
}

func (s *Server) previewAdminPluginPermissionDiff(r *http.Request, pluginID string, payload adminPluginPermissionDiffPreviewRequest) (adminPluginPermissionDiffPreviewResponse, error) {
	distribution := &pluginmeta.Distribution{}
	var current pluginmeta.Package
	operation := "install"
	if pluginID != "" {
		operation = "update"
		pkg, found, err := pluginmeta.NewRuntime(s.config.PluginDir).DescribeInstalledPackage(pluginID)
		if err != nil {
			return adminPluginPermissionDiffPreviewResponse{}, NewHTTPError(http.StatusInternalServerError, "plugin_permission_diff_failed", "Plugin package could not be inspected")
		}
		if !found {
			return adminPluginPermissionDiffPreviewResponse{}, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found")
		}
		current = pkg
		if descriptor, ok := s.pluginRegistry.Describe(pluginID); ok && descriptor.Distribution != nil {
			distribution = descriptor.Distribution
		}
	}
	downloadURL := firstNonEmpty(strings.TrimSpace(payload.DownloadURL), distribution.DownloadURL)
	checksum := firstNonEmpty(strings.ToLower(strings.TrimSpace(payload.ChecksumSHA256)), distribution.ChecksumSHA256)
	if downloadURL == "" || checksum == "" {
		return adminPluginPermissionDiffPreviewResponse{}, NewHTTPError(http.StatusBadRequest, "plugin_distribution_unavailable", "Plugin distribution metadata is unavailable")
	}
	if err := validatePluginInstallDownloadURL(downloadURL); err != nil {
		return adminPluginPermissionDiffPreviewResponse{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_download_url", err.Error())
	}
	archive, err := s.downloadPluginInstallArchive(r, downloadURL)
	if err != nil {
		return adminPluginPermissionDiffPreviewResponse{}, err
	}
	options := pluginmeta.InstallOptions{
		ChecksumSHA256: checksum,
		TrustPolicy:    payload.TrustPolicy,
	}
	options, err = s.applyAdminPluginInstallTrust(r, archive, options, payload.adminPluginInstallTrustPayload, distribution)
	if err != nil {
		return adminPluginPermissionDiffPreviewResponse{}, err
	}
	trust, err := pluginmeta.ValidateInstallTrust(archive, options)
	if err != nil {
		return adminPluginPermissionDiffPreviewResponse{}, pluginInstallHTTPError(err)
	}
	candidate, err := pluginmeta.InspectInstallZipArchive(archive)
	if err != nil {
		return adminPluginPermissionDiffPreviewResponse{}, NewHTTPError(http.StatusBadRequest, "plugin_package_inspection_failed", "Plugin package could not be inspected")
	}
	if pluginID != "" && candidate.ID != pluginID {
		return adminPluginPermissionDiffPreviewResponse{}, NewHTTPError(http.StatusBadRequest, "plugin_id_mismatch", "Candidate plugin ID does not match the requested plugin")
	}
	var previous []pluginmeta.PermissionDescriptor
	var currentVersion string
	if current.Manifest.ID != "" {
		previous = pluginmeta.ManifestPermissionDescriptors(current.Manifest.Permissions)
		currentVersion = current.Manifest.Version
	}
	diff := pluginmeta.DiffPermissions(previous, pluginmeta.ManifestPermissionDescriptors(candidate.Permissions))
	return adminPluginPermissionDiffPreviewResponse{
		Operation:        operation,
		PluginID:         candidate.ID,
		CurrentVersion:   currentVersion,
		CandidateVersion: candidate.Version,
		PermissionDiff:   diff,
		Trust:            adminPluginPermissionTrustForDecision(trust),
		Compatibility:    adminPluginCompatibilityForManifest(candidate.TokenHub, candidate.SchemaVersion),
	}, nil
}

func adminPluginPermissionTrustForDecision(decision pluginmeta.PluginTrustDecision) adminPluginPermissionTrustSummary {
	response := adminPluginPermissionTrustSummary{
		Verdict:         decision.Verdict,
		ChecksumPresent: strings.TrimSpace(decision.ChecksumSHA256) != "",
		SignaturePresent: strings.TrimSpace(decision.SignatureURL) != "" ||
			strings.TrimSpace(decision.SignatureKeyID) != "",
		SignatureKeyID: strings.TrimSpace(decision.SignatureKeyID),
		ReasonCode:     decision.Reason,
	}
	if response.SignaturePresent {
		response.SignatureAlgorithm = pluginmeta.PluginSignatureAlgorithmEd25519
	}
	return response
}
