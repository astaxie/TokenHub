package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const (
	maxAdminPluginInstallArchiveBytes   = 64 << 20
	maxAdminPluginInstallSignatureBytes = pluginmeta.MaxMarketplaceSignatureEnvelopeBytes
)

type adminPluginDescriptorResponse struct {
	pluginmeta.Descriptor

	Reason            string                           `json:"reason,omitempty"`
	RestartRequired   bool                             `json:"restart_required"`
	Health            pluginmeta.PackageHealthStatus   `json:"health,omitempty"`
	Mandatory         bool                             `json:"mandatory,omitempty"`
	RollbackAvailable bool                             `json:"rollback_available"`
	RollbackVersion   string                           `json:"rollback_version,omitempty"`
	RollbackTarget    pluginmeta.PackageRollbackTarget `json:"rollback_target,omitempty"`
	LastErrorCode     string                           `json:"last_error_code,omitempty"`
	AuditEvent        pluginmeta.PackageLifecycleEvent `json:"audit_event,omitempty"`
	Loadable          bool                             `json:"loadable"`
	Lifecycle         adminPluginLifecycleResponse     `json:"lifecycle"`
	Compatibility     adminPluginCompatibilityResponse `json:"compatibility"`
	Trust             adminPluginTrustSummaryResponse  `json:"trust"`
}

type adminPluginLifecycleResponse struct {
	Status            pluginmeta.Status                `json:"status"`
	Reason            string                           `json:"reason,omitempty"`
	RestartRequired   bool                             `json:"restart_required"`
	Health            pluginmeta.PackageHealthStatus   `json:"health"`
	Mandatory         bool                             `json:"mandatory"`
	RollbackAvailable bool                             `json:"rollback_available"`
	RollbackVersion   string                           `json:"rollback_version,omitempty"`
	RollbackTarget    pluginmeta.PackageRollbackTarget `json:"rollback_target,omitempty"`
	LastErrorCode     string                           `json:"last_error_code,omitempty"`
	AuditEvent        pluginmeta.PackageLifecycleEvent `json:"audit_event,omitempty"`
	Loadable          bool                             `json:"loadable"`
}

type adminPluginCompatibilityResponse struct {
	PluginAPI             string                     `json:"plugin_api"`
	ManifestSchemaVersion int                        `json:"manifest_schema_version"`
	CoreVersion           string                     `json:"core_version"`
	MinCore               string                     `json:"min_core,omitempty"`
	MaxCore               string                     `json:"max_core,omitempty"`
	Verdict               string                     `json:"verdict"`
	ReasonCode            pluginmeta.PluginErrorCode `json:"reason_code,omitempty"`
}

type adminPluginTrustSummaryResponse struct {
	Source             pluginmeta.Source             `json:"source"`
	Verdict            pluginmeta.PluginTrustVerdict `json:"verdict"`
	ChecksumPresent    bool                          `json:"checksum_present"`
	SignaturePresent   bool                          `json:"signature_present"`
	SignatureAlgorithm string                        `json:"signature_algorithm,omitempty"`
	SignatureKeyID     string                        `json:"signature_key_id,omitempty"`
	ReasonCode         pluginmeta.PluginErrorCode    `json:"reason_code,omitempty"`
}

type adminPluginStateResponse struct {
	PluginID          string                           `json:"plugin_id"`
	Status            pluginmeta.Status                `json:"status"`
	Reason            string                           `json:"reason,omitempty"`
	RestartRequired   bool                             `json:"restart_required"`
	Health            pluginmeta.PackageHealthStatus   `json:"health,omitempty"`
	Mandatory         bool                             `json:"mandatory,omitempty"`
	RollbackAvailable bool                             `json:"rollback_available"`
	RollbackVersion   string                           `json:"rollback_version,omitempty"`
	RollbackTarget    pluginmeta.PackageRollbackTarget `json:"rollback_target,omitempty"`
	LastErrorCode     string                           `json:"last_error_code,omitempty"`
	AuditEvent        pluginmeta.PackageLifecycleEvent `json:"audit_event,omitempty"`
	Loadable          bool                             `json:"loadable"`
	Lifecycle         adminPluginLifecycleResponse     `json:"lifecycle"`
	Plugin            *adminPluginDescriptorResponse   `json:"plugin,omitempty"`
}

type adminPluginInstallResponse struct {
	Plugin          adminPluginDescriptorResponse `json:"plugin"`
	RestartRequired bool                          `json:"restart_required"`
	Replaced        bool                          `json:"replaced"`
}

type adminPluginUninstallResponse struct {
	PluginID        string `json:"plugin_id"`
	RestartRequired bool   `json:"restart_required"`
}

type adminPluginRollbackResponse struct {
	Plugin          adminPluginDescriptorResponse    `json:"plugin"`
	RestartRequired bool                             `json:"restart_required"`
	RollbackVersion string                           `json:"rollback_version"`
	RollbackTarget  pluginmeta.PackageRollbackTarget `json:"rollback_target,omitempty"`
}

type adminPluginInstallTrustPayload struct {
	TrustPolicy        pluginmeta.PluginTrustPolicy `json:"trust_policy"`
	SignatureURL       string                       `json:"signature_url"`
	SignatureKeyID     string                       `json:"signature_key_id"`
	SignaturePublicKey string                       `json:"signature_public_key"`
}

type adminPluginInstallPayload struct {
	DownloadURL    string            `json:"download_url"`
	ChecksumSHA256 string            `json:"checksum_sha256"`
	Replace        bool              `json:"replace"`
	Enable         bool              `json:"enable"`
	Reason         string            `json:"reason"`
	Status         pluginmeta.Status `json:"status"`
	adminPluginInstallTrustPayload
}

func (s *Server) adminPluginDescriptors() ([]adminPluginDescriptorResponse, error) {
	installed := map[string]pluginmeta.Package{}
	if s != nil && strings.TrimSpace(s.config.PluginDir) != "" {
		packages, err := pluginmeta.NewRuntime(s.config.PluginDir).DiscoverRecoverable()
		if err != nil {
			return nil, err
		}
		for _, pkg := range packages {
			installed[pkg.Manifest.ID] = pkg
		}
	}
	descriptors := s.pluginRegistry.List()
	response := make([]adminPluginDescriptorResponse, 0, len(descriptors))
	seen := map[string]bool{}
	for _, descriptor := range descriptors {
		pkg, ok := installed[descriptor.ID]
		if !ok && descriptor.Source == pluginmeta.SourceBuiltIn && strings.TrimSpace(s.config.PluginDir) != "" {
			state, found, err := pluginmeta.NewRuntime(s.config.PluginDir).ReadBuiltInPackageState(descriptor.ID)
			if err != nil {
				return nil, err
			}
			if found {
				pkg.State = state
				descriptor.Status = state.Status
			}
		}
		response = append(response, adminPluginDescriptorForPackage(descriptor, pkg, ok))
		seen[descriptor.ID] = true
	}
	for _, pkg := range installed {
		if seen[pkg.Manifest.ID] {
			continue
		}
		descriptor := pkg.Manifest.Descriptor()
		descriptor.Status = pkg.State.Status
		response = append(response, adminPluginDescriptorForPackage(descriptor, pkg, true))
	}
	return response, nil
}

func adminPluginDescriptorForPackage(descriptor pluginmeta.Descriptor, pkg pluginmeta.Package, installed bool) adminPluginDescriptorResponse {
	state := pluginmeta.PackageState{Status: descriptor.Status}
	compatibility := pluginmeta.ManifestCompatibility{
		PluginAPI: pluginmeta.CurrentPluginAPI,
	}
	manifestSchemaVersion := pluginmeta.PluginManifestSchemaVersion
	if installed {
		state = pkg.State
		compatibility = pkg.Manifest.TokenHub
		manifestSchemaVersion = pkg.Manifest.SchemaVersion
		descriptor.Status = state.Status
	} else if strings.TrimSpace(string(pkg.State.Status)) != "" {
		state = pkg.State
		descriptor.Status = state.Status
	} else if normalized, err := pluginmeta.NormalizePackageState(state); err == nil {
		state = normalized
	}
	trust := adminPluginTrustSummaryForDescriptor(descriptor)
	descriptor.Distribution = sanitizeAdminPluginDistribution(descriptor.Distribution)
	descriptor.Marketplace = sanitizeAdminPluginMarketplaceMetadata(descriptor.Marketplace)
	lifecycle := adminPluginLifecycleForState(state)
	return adminPluginDescriptorResponse{
		Descriptor:        descriptor,
		Reason:            lifecycle.Reason,
		RestartRequired:   lifecycle.RestartRequired,
		Health:            lifecycle.Health,
		Mandatory:         lifecycle.Mandatory,
		RollbackAvailable: lifecycle.RollbackAvailable,
		RollbackVersion:   lifecycle.RollbackVersion,
		RollbackTarget:    lifecycle.RollbackTarget,
		LastErrorCode:     lifecycle.LastErrorCode,
		AuditEvent:        lifecycle.AuditEvent,
		Loadable:          lifecycle.Loadable,
		Lifecycle:         lifecycle,
		Compatibility:     adminPluginCompatibilityForManifest(compatibility, manifestSchemaVersion),
		Trust:             trust,
	}
}

func adminPluginLifecycleForState(state pluginmeta.PackageState) adminPluginLifecycleResponse {
	normalized, err := pluginmeta.NormalizePackageState(state)
	if err == nil {
		state = normalized
	}
	return adminPluginLifecycleResponse{
		Status:            state.Status,
		Reason:            state.Reason,
		RestartRequired:   state.PendingRestart(),
		Health:            state.Health,
		Mandatory:         state.Mandatory,
		RollbackAvailable: state.RollbackAvailable(),
		RollbackVersion:   state.RollbackVersion,
		RollbackTarget:    state.RollbackTarget,
		LastErrorCode:     state.LastErrorCode,
		AuditEvent:        state.AuditEvent,
		Loadable:          state.Loadable(),
	}
}

func adminPluginCompatibilityForManifest(compatibility pluginmeta.ManifestCompatibility, manifestSchemaVersion int) adminPluginCompatibilityResponse {
	response := adminPluginCompatibilityResponse{
		PluginAPI:             firstNonEmpty(strings.TrimSpace(compatibility.PluginAPI), pluginmeta.CurrentPluginAPI),
		ManifestSchemaVersion: manifestSchemaVersion,
		CoreVersion:           pluginmeta.CurrentCoreVersion,
		MinCore:               strings.TrimSpace(compatibility.MinCore),
		MaxCore:               strings.TrimSpace(compatibility.MaxCore),
		Verdict:               "compatible",
	}
	if response.ManifestSchemaVersion == 0 {
		response.ManifestSchemaVersion = pluginmeta.PluginManifestSchemaVersion
	}
	if err := pluginmeta.ValidateManifestCompatibility(compatibility); err != nil {
		response.Verdict = "incompatible"
		if code, ok := pluginmeta.PluginErrorCodeOf(err); ok {
			response.ReasonCode = code
		}
	}
	return response
}

func adminPluginTrustSummaryForDescriptor(descriptor pluginmeta.Descriptor) adminPluginTrustSummaryResponse {
	response := adminPluginTrustSummaryResponse{
		Source:  descriptor.Source,
		Verdict: pluginmeta.TrustVerdictUnverified,
	}
	if descriptor.Source == pluginmeta.SourceBuiltIn {
		response.Verdict = pluginmeta.TrustVerdictTrusted
	}
	if descriptor.Distribution != nil {
		response.ChecksumPresent = strings.TrimSpace(descriptor.Distribution.ChecksumSHA256) != ""
		response.SignaturePresent = strings.TrimSpace(descriptor.Distribution.SignatureURL) != "" ||
			strings.TrimSpace(descriptor.Distribution.SignatureAlgorithm) != "" ||
			strings.TrimSpace(descriptor.Distribution.SignatureKeyID) != ""
		response.SignatureAlgorithm = strings.TrimSpace(descriptor.Distribution.SignatureAlgorithm)
		response.SignatureKeyID = strings.TrimSpace(descriptor.Distribution.SignatureKeyID)
	}
	return response
}

func sanitizeAdminPluginDistribution(distribution *pluginmeta.Distribution) *pluginmeta.Distribution {
	if distribution == nil {
		return nil
	}
	safe := &pluginmeta.Distribution{
		MarketplaceURL:     sanitizeAdminPluginPublicHTTPSURL(distribution.MarketplaceURL),
		RepositoryURL:      sanitizeAdminPluginPublicHTTPSURL(distribution.RepositoryURL),
		SignatureAlgorithm: strings.TrimSpace(distribution.SignatureAlgorithm),
		SignatureKeyID:     strings.TrimSpace(distribution.SignatureKeyID),
		HomepageURL:        sanitizeAdminPluginPublicHTTPSURL(distribution.HomepageURL),
		License:            strings.TrimSpace(distribution.License),
	}
	if safe.MarketplaceURL == "" && safe.RepositoryURL == "" && safe.SignatureAlgorithm == "" &&
		safe.SignatureKeyID == "" && safe.HomepageURL == "" && safe.License == "" {
		return nil
	}
	return safe
}

func sanitizeAdminPluginMarketplaceMetadata(metadata *pluginmeta.MarketplaceMetadata) *pluginmeta.MarketplaceMetadata {
	metadata = metadata.Normalized()
	if metadata == nil {
		return nil
	}
	safe := &pluginmeta.MarketplaceMetadata{
		Summary:       metadata.Summary,
		Categories:    append([]string(nil), metadata.Categories...),
		Localizations: copyAdminPluginMarketplaceLocalizations(metadata.Localizations),
	}
	for _, screenshot := range metadata.Screenshots {
		screenshot.URL = sanitizeAdminPluginPublicHTTPSURL(screenshot.URL)
		screenshot.ThumbnailURL = sanitizeAdminPluginPublicHTTPSURL(screenshot.ThumbnailURL)
		if normalized, ok := screenshot.Normalized(); ok {
			safe.Screenshots = append(safe.Screenshots, normalized)
		}
	}
	if metadata.Compatibility != nil {
		compatibility := &pluginmeta.MarketplaceCompatibility{
			Verdict: metadata.Compatibility.Verdict,
		}
		for _, badge := range metadata.Compatibility.Badges {
			badge.URL = sanitizeAdminPluginPublicHTTPSURL(badge.URL)
			if normalized, ok := badge.Normalized(); ok {
				compatibility.Badges = append(compatibility.Badges, normalized)
			}
		}
		safe.Compatibility = compatibility.Normalized()
	}
	if metadata.Publisher != nil {
		publisher := *metadata.Publisher
		publisher.URL = sanitizeAdminPluginPublicHTTPSURL(publisher.URL)
		publisher.SupportURL = sanitizeAdminPluginPublicHTTPSURL(publisher.SupportURL)
		publisher.ContactURL = sanitizeAdminPluginPublicHTTPSURL(publisher.ContactURL)
		safe.Publisher = publisher.Normalized()
	}
	for _, advisory := range metadata.Advisories {
		advisory.URL = sanitizeAdminPluginPublicHTTPSURL(advisory.URL)
		if normalized, ok := advisory.Normalized(); ok {
			safe.Advisories = append(safe.Advisories, normalized)
		}
	}
	for _, note := range metadata.ReleaseNotes {
		note.URL = sanitizeAdminPluginPublicHTTPSURL(note.URL)
		if normalized, ok := note.Normalized(); ok {
			safe.ReleaseNotes = append(safe.ReleaseNotes, normalized)
		}
	}
	return safe.Normalized()
}

func copyAdminPluginMarketplaceLocalizations(items map[string]pluginmeta.MarketplaceLocalization) map[string]pluginmeta.MarketplaceLocalization {
	if len(items) == 0 {
		return nil
	}
	copied := make(map[string]pluginmeta.MarketplaceLocalization, len(items))
	for locale, localization := range items {
		copied[locale] = localization
	}
	return copied
}

func sanitizeAdminPluginPublicHTTPSURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	parsed.Scheme = "https"
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func (s *Server) handleAdminPluginInstallPost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	payload, archive, err := s.readAdminPluginInstallRequest(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	downloadURL := strings.TrimSpace(payload.DownloadURL)
	checksum := strings.ToLower(strings.TrimSpace(payload.ChecksumSHA256))
	if archive == nil {
		if err := validatePluginInstallDownloadURL(downloadURL); err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_download_url", err.Error()))
			return
		}
		if checksum == "" {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "plugin_checksum_required", "Plugin package checksum_sha256 is required"))
			return
		}
		archive, err = s.downloadPluginInstallArchive(r, downloadURL)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	state := pluginmeta.PackageState{
		Status:          pluginmeta.StatusDisabled,
		Reason:          payload.Reason,
		RestartRequired: true,
		AuditEvent:      pluginmeta.PackageLifecycleInstalled,
	}
	if payload.Enable {
		state.Status = pluginmeta.StatusEnabled
	}
	if strings.TrimSpace(string(payload.Status)) != "" {
		state.Status = payload.Status
	}
	options := pluginmeta.InstallOptions{
		ChecksumSHA256: checksum,
		TrustPolicy:    payload.TrustPolicy,
		Replace:        payload.Replace,
		InitialState:   state,
	}
	options, err = s.applyAdminPluginInstallTrust(r, archive, options, payload.adminPluginInstallTrustPayload, nil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	pkg, err := s.installPluginArchive(archive, options)
	if err != nil {
		writeError(w, r, pluginInstallHTTPError(err))
		return
	}
	descriptor := pkg.Manifest.Descriptor()
	descriptor.Status = pkg.State.Status
	writeJSON(w, http.StatusCreated, map[string]any{"data": adminPluginInstallResponse{
		Plugin:          adminPluginDescriptorForPackage(descriptor, pkg, true),
		RestartRequired: true,
		Replaced:        payload.Replace,
	}})
}

func (s *Server) readAdminPluginInstallRequest(w http.ResponseWriter, r *http.Request) (adminPluginInstallPayload, []byte, error) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("content-type")), "multipart/form-data") {
		var payload adminPluginInstallPayload
		if err := s.decodeJSON(w, r, &payload); err != nil {
			return adminPluginInstallPayload{}, nil, err
		}
		return payload, nil, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxAdminPluginInstallArchiveBytes)+(1<<20))
	if err := r.ParseMultipartForm(int64(maxAdminPluginInstallArchiveBytes)); err != nil {
		return adminPluginInstallPayload{}, nil, NewHTTPError(http.StatusBadRequest, "invalid_plugin_upload", "Plugin package upload is invalid")
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, err := firstAdminPluginUploadFile(r, "package", "plugin_package", "file")
	if err != nil {
		return adminPluginInstallPayload{}, nil, err
	}
	defer file.Close()
	archive, err := io.ReadAll(io.LimitReader(file, int64(maxAdminPluginInstallArchiveBytes)+1))
	if err != nil {
		return adminPluginInstallPayload{}, nil, NewHTTPError(http.StatusBadRequest, "invalid_plugin_upload", "Plugin package upload could not be read")
	}
	if len(archive) > maxAdminPluginInstallArchiveBytes {
		return adminPluginInstallPayload{}, nil, NewHTTPError(http.StatusBadRequest, "plugin_upload_too_large", "Plugin package is too large")
	}
	payload := adminPluginInstallPayload{
		ChecksumSHA256: strings.TrimSpace(r.FormValue("checksum_sha256")),
		Replace:        adminPluginFormBool(r.FormValue("replace")),
		Enable:         adminPluginFormBool(r.FormValue("enable")),
		Reason:         strings.TrimSpace(r.FormValue("reason")),
		Status:         pluginmeta.Status(strings.TrimSpace(r.FormValue("status"))),
		adminPluginInstallTrustPayload: adminPluginInstallTrustPayload{
			TrustPolicy:        pluginmeta.PluginTrustPolicy(strings.TrimSpace(r.FormValue("trust_policy"))),
			SignatureURL:       strings.TrimSpace(r.FormValue("signature_url")),
			SignatureKeyID:     strings.TrimSpace(r.FormValue("signature_key_id")),
			SignaturePublicKey: strings.TrimSpace(r.FormValue("signature_public_key")),
		},
	}
	return payload, archive, nil
}

func firstAdminPluginUploadFile(r *http.Request, names ...string) (io.ReadCloser, error) {
	for _, name := range names {
		file, _, err := r.FormFile(name)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, http.ErrMissingFile) {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_plugin_upload", "Plugin package upload is invalid")
		}
	}
	return nil, NewHTTPError(http.StatusBadRequest, "plugin_package_required", "Plugin package file is required")
}

func adminPluginFormBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) handleAdminPluginUpdatePost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	var payload struct {
		DownloadURL    string `json:"download_url"`
		ChecksumSHA256 string `json:"checksum_sha256"`
		adminPluginInstallTrustPayload
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
	updateState := current.State
	updateState.RestartRequired = true
	updateState.RollbackVersion = current.Manifest.Version
	updateState.AuditEvent = pluginmeta.PackageLifecyclePendingRestart
	options := pluginmeta.InstallOptions{
		ChecksumSHA256:   checksum,
		TrustPolicy:      payload.TrustPolicy,
		Replace:          true,
		PreserveRollback: true,
		InitialState:     updateState,
	}
	options, err = s.applyAdminPluginInstallTrust(r, archive, options, payload.adminPluginInstallTrustPayload, distribution)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := pluginmeta.NewRuntime(s.config.PluginDir).PreserveRollbackPackage(pluginID, current.Dir); err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_rollback_prepare_failed", "Plugin rollback package could not be prepared"))
		return
	}
	pkg, err := s.installPluginArchive(archive, options)
	if err != nil {
		writeError(w, r, pluginInstallHTTPError(err))
		return
	}
	if err := removeSupersededPluginPackageDir(s.config.PluginDir, current.Dir, pkg.Dir); err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_update_cleanup_failed", "Plugin package update cleanup failed"))
		return
	}
	updated := pkg.Manifest.Descriptor()
	updated.Status = pkg.State.Status
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginInstallResponse{
		Plugin:          adminPluginDescriptorForPackage(updated, pkg, true),
		RestartRequired: true,
		Replaced:        true,
	}})
}

func (s *Server) handleAdminPluginRollbackPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "providers", r.Method)
	if !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	if pluginID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	var payload struct {
		Reason string `json:"reason"`
	}
	if err := s.decodeJSONOptional(w, r, &payload); err != nil {
		writeError(w, r, err)
		return
	}
	runtime := pluginmeta.NewRuntime(s.config.PluginDir)
	pkg, err := runtime.RollbackPackage(pluginID, payload.Reason)
	if err != nil {
		if errors.Is(err, pluginmeta.ErrPackageRollbackUnavailable) {
			if s.handleAdminPluginBuiltInFallbackRollback(w, r, user, runtime, pluginID, payload.Reason) {
				return
			}
		}
		httpErr := pluginRollbackHTTPError(err)
		if httpErr.Status != http.StatusNotFound {
			s.recordPluginRollbackAudit(r, user, pluginID, "failed", httpErr.Code)
		}
		writeError(w, r, httpErr)
		return
	}
	descriptor := pkg.Manifest.Descriptor()
	descriptor.Status = pkg.State.Status
	plugin := adminPluginDescriptorForPackage(descriptor, pkg, true)
	s.recordPluginRollbackAudit(r, user, pluginID, "success", string(pluginmeta.PackageLifecycleRollbackStarted))
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginRollbackResponse{
		Plugin:          plugin,
		RestartRequired: true,
		RollbackVersion: pkg.Manifest.Version,
		RollbackTarget:  pluginmeta.PackageRollbackTargetPreviousPackage,
	}})
}

func (s *Server) handleAdminPluginBuiltInFallbackRollback(w http.ResponseWriter, r *http.Request, user AdminUser, runtime pluginmeta.Runtime, pluginID string, reason string) bool {
	descriptor, ok := s.pluginRegistry.Describe(pluginID)
	if !ok || descriptor.Source != pluginmeta.SourceBuiltIn {
		return false
	}
	pkg, err := runtime.RollbackPackageToBuiltInFallback(pluginID, reason)
	if err != nil {
		return false
	}
	descriptor.Status = pkg.State.Status
	plugin := adminPluginDescriptorForPackage(descriptor, pluginmeta.Package{}, false)
	s.recordPluginRollbackAudit(r, user, pluginID, "success", string(pluginmeta.PackageRollbackTargetBuiltIn))
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginRollbackResponse{
		Plugin:          plugin,
		RestartRequired: false,
		RollbackVersion: "built-in",
		RollbackTarget:  pluginmeta.PackageRollbackTargetBuiltIn,
	}})
	return true
}

func pluginRollbackHTTPError(err error) *HTTPError {
	if errors.Is(err, pluginmeta.ErrPackageNotFound) {
		return NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found")
	}
	if errors.Is(err, pluginmeta.ErrPackageRollbackUnavailable) {
		return NewHTTPError(http.StatusConflict, "plugin_rollback_unavailable", "Plugin rollback is unavailable")
	}
	return NewHTTPError(http.StatusInternalServerError, "plugin_rollback_failed", "Plugin package could not be rolled back")
}

func (s *Server) recordPluginRollbackAudit(r *http.Request, user AdminUser, pluginID string, status string, message string) {
	s.store.RecordAuditEvent(AuditEvent{
		ActorUserID:  user.ID,
		ActorName:    user.Name,
		ActorRole:    user.Role,
		Action:       "plugin.rollback",
		ResourceType: "plugin",
		ResourceID:   pluginID,
		Status:       status,
		Message:      message,
		IP:           s.clientIP(r),
		UserAgent:    r.UserAgent(),
	})
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
	if !adminPluginStatePatchStatusAllowed(payload.Status) {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_state", "unsupported plugin package status "+string(payload.Status)))
		return
	}
	runtime := pluginmeta.NewRuntime(s.config.PluginDir)
	current, found, err := runtime.DescribeInstalledPackage(pluginID)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_state_update_failed", "Plugin state could not be inspected"))
		return
	}
	if !found {
		s.handleAdminBuiltInPluginStatePatch(w, r, runtime, pluginID, payload.Status, payload.Reason)
		return
	}
	state, err := adminPluginStatePatchState(current.State, payload.Status, payload.Reason)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_state", err.Error()))
		return
	}
	pkg, err := runtime.UpdatePackageState(pluginID, state)
	if err != nil {
		if errors.Is(err, pluginmeta.ErrPackageNotFound) {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
			return
		}
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_state_update_failed", "Plugin state could not be updated"))
		return
	}
	descriptor := pkg.Manifest.Descriptor()
	descriptor.Status = pkg.State.Status
	plugin := adminPluginDescriptorForPackage(descriptor, pkg, true)
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginStateResponse{
		PluginID:          pkg.Manifest.ID,
		Status:            pkg.State.Status,
		Reason:            pkg.State.Reason,
		RestartRequired:   true,
		Health:            pkg.State.Health,
		Mandatory:         pkg.State.Mandatory,
		RollbackAvailable: pkg.State.RollbackAvailable(),
		RollbackVersion:   pkg.State.RollbackVersion,
		RollbackTarget:    pkg.State.RollbackTarget,
		LastErrorCode:     pkg.State.LastErrorCode,
		AuditEvent:        pkg.State.AuditEvent,
		Loadable:          pkg.State.Loadable(),
		Lifecycle:         plugin.Lifecycle,
		Plugin:            &plugin,
	}})
}

func (s *Server) handleAdminBuiltInPluginStatePatch(w http.ResponseWriter, r *http.Request, runtime pluginmeta.Runtime, pluginID string, status pluginmeta.Status, reason string) {
	descriptor, ok := s.pluginRegistry.Describe(pluginID)
	if !ok || descriptor.Source != pluginmeta.SourceBuiltIn {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	currentState := pluginmeta.PackageState{Status: descriptor.Status}
	if state, found, err := runtime.ReadBuiltInPackageState(pluginID); err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_state_update_failed", "Plugin state could not be inspected"))
		return
	} else if found {
		currentState = state
	}
	currentState, err := pluginmeta.NormalizePackageState(currentState)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_state_update_failed", "Plugin state could not be inspected"))
		return
	}
	state, err := adminPluginStatePatchState(currentState, status, reason)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_plugin_state", err.Error()))
		return
	}
	state, err = runtime.UpdateBuiltInPackageState(pluginID, state)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_state_update_failed", "Plugin state could not be updated"))
		return
	}
	descriptor.Status = state.Status
	plugin := adminPluginDescriptorForPackage(descriptor, pluginmeta.Package{State: state}, false)
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginStateResponse{
		PluginID:          descriptor.ID,
		Status:            state.Status,
		Reason:            state.Reason,
		RestartRequired:   true,
		Health:            state.Health,
		Mandatory:         state.Mandatory,
		RollbackAvailable: state.RollbackAvailable(),
		RollbackVersion:   state.RollbackVersion,
		RollbackTarget:    state.RollbackTarget,
		LastErrorCode:     state.LastErrorCode,
		AuditEvent:        state.AuditEvent,
		Loadable:          state.Loadable(),
		Lifecycle:         plugin.Lifecycle,
		Plugin:            &plugin,
	}})
}

func adminPluginStatePatchState(current pluginmeta.PackageState, status pluginmeta.Status, reason string) (pluginmeta.PackageState, error) {
	state := current
	state.Status = status
	state.Reason = reason
	state.RestartRequired = true
	state.AuditEvent = adminPluginLifecycleEventForStatus(status)
	return pluginmeta.NormalizePackageState(state)
}

func adminPluginStatePatchStatusAllowed(status pluginmeta.Status) bool {
	switch status {
	case pluginmeta.StatusEnabled,
		pluginmeta.StatusDisabled,
		pluginmeta.StatusPendingRestart,
		pluginmeta.StatusFailedValidation,
		pluginmeta.StatusFailedStartup,
		pluginmeta.StatusRollbackAvailable,
		pluginmeta.StatusMandatory:
		return true
	default:
		return false
	}
}

func adminPluginLifecycleEventForStatus(status pluginmeta.Status) pluginmeta.PackageLifecycleEvent {
	switch status {
	case pluginmeta.StatusEnabled, pluginmeta.StatusMandatory:
		return pluginmeta.PackageLifecycleEnabled
	case pluginmeta.StatusDisabled:
		return pluginmeta.PackageLifecycleDisabled
	case pluginmeta.StatusPendingRestart:
		return pluginmeta.PackageLifecyclePendingRestart
	case pluginmeta.StatusFailedValidation:
		return pluginmeta.PackageLifecycleValidationFailed
	case pluginmeta.StatusFailedStartup:
		return pluginmeta.PackageLifecycleStartupFailed
	case pluginmeta.StatusRollbackAvailable:
		return pluginmeta.PackageLifecycleRollbackAvailable
	default:
		return ""
	}
}

func (s *Server) handleAdminPluginDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	pluginID := strings.TrimSpace(r.PathValue("plugin_id"))
	if pluginID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
		return
	}
	pkg, err := pluginmeta.NewRuntime(s.config.PluginDir).UninstallPackage(pluginID)
	if err != nil {
		if errors.Is(err, pluginmeta.ErrPackageNotFound) {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "plugin_not_found", "Plugin not found"))
			return
		}
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_uninstall_failed", "Plugin package could not be uninstalled"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginUninstallResponse{
		PluginID:        pkg.Manifest.ID,
		RestartRequired: true,
	}})
}

func validatePluginInstallDownloadURL(raw string) error {
	return validatePluginInstallHTTPSURL("Plugin package download_url", raw)
}

func validatePluginInstallSignatureURL(raw string) error {
	return validatePluginInstallHTTPSURL("Plugin package signature_url", raw)
}

func validatePluginInstallHTTPSURL(label string, raw string) error {
	if raw == "" {
		return errors.New(label + " is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New(label + " must be an absolute URL")
	}
	if parsed.Scheme != "https" {
		return errors.New(label + " must use HTTPS")
	}
	return nil
}

func (s *Server) downloadPluginInstallArchive(r *http.Request, downloadURL string) ([]byte, error) {
	return s.downloadAdminPluginInstallAsset(r, downloadURL, maxAdminPluginInstallArchiveBytes, "plugin_download_failed", "Plugin package")
}

func (s *Server) downloadPluginInstallSignature(r *http.Request, signatureURL string) ([]byte, error) {
	return s.downloadAdminPluginInstallAsset(r, signatureURL, maxAdminPluginInstallSignatureBytes, "plugin_signature_download_failed", "Plugin package signature")
}

func (s *Server) downloadAdminPluginInstallAsset(r *http.Request, downloadURL string, maxBytes int, errorCode string, label string) ([]byte, error) {
	client := s.pluginInstallClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, errorCode, label+" URL is invalid")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, errorCode, label+" could not be downloaded")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewHTTPError(http.StatusBadGateway, errorCode, label+" download failed")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, errorCode, label+" download failed")
	}
	if len(data) > maxBytes {
		return nil, NewHTTPError(http.StatusBadRequest, errorCode, label+" is too large")
	}
	return data, nil
}

func (s *Server) applyAdminPluginInstallTrust(r *http.Request, archive []byte, options pluginmeta.InstallOptions, payload adminPluginInstallTrustPayload, distribution *pluginmeta.Distribution) (pluginmeta.InstallOptions, error) {
	if pluginmeta.NormalizePluginTrustPolicy(payload.TrustPolicy) != pluginmeta.TrustPolicySignedMarketplace {
		return options, nil
	}
	signatureURL := firstNonEmpty(strings.TrimSpace(payload.SignatureURL), adminPluginDistributionSignatureURL(distribution))
	signatureKeyID := firstNonEmpty(strings.TrimSpace(payload.SignatureKeyID), adminPluginDistributionSignatureKeyID(distribution))
	if err := validatePluginInstallSignatureURL(signatureURL); err != nil {
		return options, NewHTTPError(http.StatusBadRequest, "plugin_signature_required", err.Error())
	}
	if strings.TrimSpace(signatureKeyID) == "" {
		return options, NewHTTPError(http.StatusBadRequest, "plugin_signature_required", "Plugin package signature_key_id is required")
	}
	publicKey, err := decodeAdminPluginSignaturePublicKey(payload.SignaturePublicKey)
	if err != nil {
		return options, err
	}
	signature, err := s.downloadPluginInstallSignature(r, signatureURL)
	if err != nil {
		return options, err
	}
	verification, err := pluginmeta.VerifyMarketplaceArtifactSignature(pluginmeta.MarketplaceArtifactVerificationInput{
		Artifact:  archive,
		Signature: signature,
		KeyID:     signatureKeyID,
		TrustedKeys: []pluginmeta.MarketplaceTrustedKey{{
			KeyID:     signatureKeyID,
			PublicKey: publicKey,
		}},
	})
	if err != nil {
		return options, NewHTTPError(http.StatusBadRequest, "plugin_signature_verification_failed", err.Error())
	}
	options.TrustPolicy = pluginmeta.TrustPolicySignedMarketplace
	options.SignatureURL = signatureURL
	options.SignatureKeyID = verification.KeyID
	options.SignatureVerified = true
	return options, nil
}

func adminPluginDistributionSignatureURL(distribution *pluginmeta.Distribution) string {
	if distribution == nil {
		return ""
	}
	return strings.TrimSpace(distribution.SignatureURL)
}

func adminPluginDistributionSignatureKeyID(distribution *pluginmeta.Distribution) string {
	if distribution == nil {
		return ""
	}
	return strings.TrimSpace(distribution.SignatureKeyID)
}

func decodeAdminPluginSignaturePublicKey(encoded string) (ed25519.PublicKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "plugin_signature_required", "Plugin package signature_public_key is required")
	}
	publicKey, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_plugin_signature_key", "Plugin package signature_public_key must be a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(publicKey), nil
}

func removeSupersededPluginPackageDir(root string, previousDir string, installedDir string) error {
	root = strings.TrimSpace(root)
	previousDir = strings.TrimSpace(previousDir)
	installedDir = strings.TrimSpace(installedDir)
	if root == "" || previousDir == "" || installedDir == "" {
		return nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	previousAbs, err := filepath.Abs(previousDir)
	if err != nil {
		return err
	}
	installedAbs, err := filepath.Abs(installedDir)
	if err != nil {
		return err
	}
	if previousAbs == installedAbs || previousAbs == rootAbs {
		return nil
	}
	relative, err := filepath.Rel(rootAbs, previousAbs)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return nil
	}
	if installedInsidePrevious, err := filepath.Rel(previousAbs, installedAbs); err == nil &&
		(installedInsidePrevious == "." || !strings.HasPrefix(installedInsidePrevious, ".."+string(os.PathSeparator)) && !filepath.IsAbs(installedInsidePrevious)) {
		return nil
	}
	return os.RemoveAll(previousAbs)
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
