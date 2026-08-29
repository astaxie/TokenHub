package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
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

type adminPluginInstallTrustPayload struct {
	TrustPolicy        pluginmeta.PluginTrustPolicy `json:"trust_policy"`
	SignatureURL       string                       `json:"signature_url"`
	SignatureKeyID     string                       `json:"signature_key_id"`
	SignaturePublicKey string                       `json:"signature_public_key"`
}

func (s *Server) adminPluginDescriptors() ([]adminPluginDescriptorResponse, error) {
	installed := map[string]pluginmeta.Package{}
	if s != nil && strings.TrimSpace(s.config.PluginDir) != "" {
		packages, err := pluginmeta.NewRuntime(s.config.PluginDir).Discover()
		if err != nil {
			return nil, err
		}
		for _, pkg := range packages {
			installed[pkg.Manifest.ID] = pkg
		}
	}
	descriptors := s.pluginRegistry.List()
	response := make([]adminPluginDescriptorResponse, 0, len(descriptors))
	for _, descriptor := range descriptors {
		pkg, ok := installed[descriptor.ID]
		response = append(response, adminPluginDescriptorForPackage(descriptor, pkg, ok))
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
	var payload struct {
		DownloadURL    string            `json:"download_url"`
		ChecksumSHA256 string            `json:"checksum_sha256"`
		Replace        bool              `json:"replace"`
		Enable         bool              `json:"enable"`
		Reason         string            `json:"reason"`
		Status         pluginmeta.Status `json:"status"`
		adminPluginInstallTrustPayload
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
	options := pluginmeta.InstallOptions{
		ChecksumSHA256: checksum,
		TrustPolicy:    payload.TrustPolicy,
		Replace:        true,
		InitialState:   current.State,
	}
	options, err = s.applyAdminPluginInstallTrust(r, archive, options, payload.adminPluginInstallTrustPayload, distribution)
	if err != nil {
		writeError(w, r, err)
		return
	}
	pkg, err := s.installPluginArchive(archive, options)
	if err != nil {
		writeError(w, r, pluginInstallHTTPError(err))
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
		LastErrorCode:     pkg.State.LastErrorCode,
		AuditEvent:        pkg.State.AuditEvent,
		Loadable:          pkg.State.Loadable(),
		Lifecycle:         plugin.Lifecycle,
		Plugin:            &plugin,
	}})
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
