package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type providerImageCapabilityRouteProfile struct {
	ProviderType                string
	ResourceType                string
	PublicModel                 string
	UpstreamModel               string
	CapabilityOption            string
	CapabilityCheckedAtOption   string
	CapabilitySupportedValue    string
	CapabilityUnsupportedValue  string
	RouteBackfillOption         string
	RouteBackfillValue          string
	ProviderErrorCode           string
	ProviderErrorMessage        string
	UpstreamModelErrorCode      string
	UpstreamModelErrorMessage   string
	CapabilityErrorCode         string
	CapabilityErrorMessage      string
	EnabledRequiredErrorCode    string
	EnabledRequiredMessage      string
	AuditAction                 string
	OperationKeyPrefix          string
	ProbePrompt                 string
	ProbeBackground             string
	ProbeQuality                string
	ProbeSize                   string
	ProbeTimeoutErrorCode       string
	ProbeTimeoutErrorMessage    string
	RuntimeUnsupportedErrorCode string
	RequestAliasModel           string
	RequestAliasHeader          string
	RequestAliasOriginator      string
	RequestAliasResponseFormat  string
	RequestDefaultModel         bool
	RequestSupportsMask         bool
	RequestSupportsMaskSet      bool
	RequestSizePolicy           string
	RequestAllowedSizes         []string
	RequestAllowedQualities     []string
	RequestAllowedFormats       []string
	RequestMaxOutputImages      int
	ProbeErrorMessages          map[string]string
}

func (s *Server) applyImageCapabilityActionSideEffects(ctx context.Context, descriptor pluginmeta.ActionDescriptor, payload json.RawMessage, result pluginmeta.ActionResult) (pluginmeta.ActionResult, error) {
	profile, ok := providerImageCapabilityRouteProfileFromAction(descriptor)
	if !ok {
		return result, nil
	}
	capability, ok := providerImageCapabilityResultFromActionData(result.Data)
	if !ok {
		return result, nil
	}
	resourceID, err := imageCapabilityActionResourceID(payload, capability.ResourceID)
	if err != nil {
		return result, err
	}
	if resourceID == "" {
		return result, NewHTTPError(http.StatusBadGateway, "provider_image_capability_missing_resource", "Provider image capability action did not identify a resource")
	}
	resource, ok := s.store.GetProviderResource(resourceID)
	if !ok {
		return result, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return result, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if profile.ProviderType != "" && profile.ProviderType != provider.Type {
		return result, NewHTTPError(http.StatusForbidden, "plugin_action_subject_mismatch", "Plugin action subject does not match the Provider type")
	}
	if profile.ResourceType != "" && profile.ResourceType != resource.ResourceType {
		return result, NewHTTPError(http.StatusForbidden, "plugin_action_resource_type_mismatch", "Plugin action resource type does not match the Provider resource")
	}

	if !capability.Enabled {
		if err := s.setProviderImageCapabilityRoutesStatus(provider.ID, profile, StatusDisabled); err != nil {
			return result, err
		}
		return result, nil
	}

	if capability.Capability != "" {
		if _, err := s.updateProviderImageCapability(resource.ID, capability.Capability, profile); err != nil {
			return result, err
		}
	}
	if !profile.capabilityIsSupported(capability.Capability) {
		return result, nil
	}
	route, err := s.ensureProviderImageCapabilityRoute(provider.ID, profile)
	if err != nil {
		return result, err
	}
	capability.RouteID = route.ID
	result.Data = imageCapabilityActionResultWithRoute(result.Data, route.ID)
	return result, nil
}

func providerImageCapabilityRouteProfileFromAction(descriptor pluginmeta.ActionDescriptor) (providerImageCapabilityRouteProfile, bool) {
	profile := providerImageCapabilityProfileFromAction(descriptor)
	return profile, profile.PublicModel != "" && profile.UpstreamModel != ""
}

func providerImageCapabilityProfileFromAction(descriptor pluginmeta.ActionDescriptor) providerImageCapabilityRouteProfile {
	profile := providerImageCapabilityRouteProfile{
		ProviderType:  strings.TrimSpace(descriptor.Subject),
		ResourceType:  strings.TrimSpace(firstNonEmpty(descriptor.Metadata["provider_resource_type"], descriptor.Metadata["resource_type"])),
		PublicModel:   strings.TrimSpace(descriptor.Metadata["public_model"]),
		UpstreamModel: strings.TrimSpace(descriptor.Metadata["upstream_model"]),
		CapabilityOption: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_option"],
			descriptor.Metadata["provider_capability_option"],
		)),
		CapabilityCheckedAtOption: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_checked_at_option"],
			descriptor.Metadata["provider_capability_checked_at_option"],
		)),
		CapabilitySupportedValue: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_supported_value"],
			descriptor.Metadata["supported_value"],
		)),
		CapabilityUnsupportedValue: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_unsupported_value"],
			descriptor.Metadata["unsupported_value"],
		)),
		RouteBackfillOption: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_backfill_option"],
			descriptor.Metadata["provider_route_backfill_option"],
		)),
		RouteBackfillValue: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_backfill_value"],
			descriptor.Metadata["provider_route_backfill_value"],
		)),
		ProviderErrorCode: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_error.provider.code"],
			descriptor.Metadata["provider_error_code"],
		)),
		ProviderErrorMessage: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_error.provider.message"],
			descriptor.Metadata["provider_error_message"],
		)),
		UpstreamModelErrorCode: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_error.upstream_model.code"],
			descriptor.Metadata["upstream_model_error_code"],
		)),
		UpstreamModelErrorMessage: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_error.upstream_model.message"],
			descriptor.Metadata["upstream_model_error_message"],
		)),
		CapabilityErrorCode: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_error.capability.code"],
			descriptor.Metadata["capability_error_code"],
		)),
		CapabilityErrorMessage: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_error.capability.message"],
			descriptor.Metadata["capability_error_message"],
		)),
		EnabledRequiredErrorCode: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["enabled_required_error_code"],
			descriptor.Metadata["request_error.enabled_required.code"],
		)),
		EnabledRequiredMessage: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["enabled_required_error_message"],
			descriptor.Metadata["request_error.enabled_required.message"],
		)),
		AuditAction: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["audit_action"],
			descriptor.Metadata["admin_audit_action"],
		)),
		OperationKeyPrefix: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["operation_key_prefix"],
			descriptor.Metadata["lock_key_prefix"],
		)),
		ProbePrompt: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["probe_request.prompt"],
			descriptor.Metadata["probe_prompt"],
		)),
		ProbeBackground: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["probe_request.background"],
			descriptor.Metadata["probe_background"],
		)),
		ProbeQuality: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["probe_request.quality"],
			descriptor.Metadata["probe_quality"],
		)),
		ProbeSize: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["probe_request.size"],
			descriptor.Metadata["probe_size"],
		)),
		ProbeTimeoutErrorCode: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["probe_error.timeout.code"],
			descriptor.Metadata["probe_timeout_error_code"],
		)),
		ProbeTimeoutErrorMessage: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["probe_error.timeout.message"],
			descriptor.Metadata["probe_timeout_error_message"],
		)),
		RuntimeUnsupportedErrorCode: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["runtime_error.unsupported.code"],
			descriptor.Metadata["image_generation_unsupported_error_code"],
		)),
		RequestAliasModel: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["request_alias.model"],
			descriptor.Metadata["image_request_alias_model"],
		)),
		RequestAliasHeader: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["request_alias.header"],
			descriptor.Metadata["image_request_alias_header"],
		)),
		RequestAliasOriginator: strings.ToLower(strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["request_alias.originator_prefix"],
			descriptor.Metadata["image_request_alias_originator_prefix"],
		))),
		RequestAliasResponseFormat: strings.ToLower(strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["request_alias.response_format"],
			descriptor.Metadata["image_request_alias_response_format"],
		))),
		RequestDefaultModel: truthyString(firstNonEmpty(
			descriptor.Metadata["request.default_model"],
			descriptor.Metadata["image_request_default_model"],
		)),
		RequestSizePolicy: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["request.size_policy"],
			descriptor.Metadata["image_request_size_policy"],
		)),
		RequestAllowedSizes: stringListMetadata(firstNonEmpty(
			descriptor.Metadata["request.allowed_sizes"],
			descriptor.Metadata["image_request_allowed_sizes"],
		)),
		RequestAllowedQualities: stringListMetadata(firstNonEmpty(
			descriptor.Metadata["request.allowed_qualities"],
			descriptor.Metadata["image_request_allowed_qualities"],
		)),
		RequestAllowedFormats: stringListMetadata(firstNonEmpty(
			descriptor.Metadata["request.allowed_response_formats"],
			descriptor.Metadata["image_request_allowed_response_formats"],
		)),
		RequestMaxOutputImages: intMetadata(firstNonEmpty(
			descriptor.Metadata["request.max_output_images"],
			descriptor.Metadata["image_request_max_output_images"],
		)),
		ProbeErrorMessages: providerImageCapabilityProbeErrorMessages(descriptor.Metadata),
	}
	profile.RequestSupportsMask, profile.RequestSupportsMaskSet = boolMetadata(firstNonEmpty(
		descriptor.Metadata["request.supports_mask"],
		descriptor.Metadata["image_request_supports_mask"],
	))
	profile.withDefaults()
	return profile
}

func providerImageCapabilityProbeErrorMessages(metadata map[string]string) map[string]string {
	messages := map[string]string{}
	for key, value := range metadata {
		code := ""
		switch {
		case strings.HasPrefix(key, "probe_error_message."):
			code = strings.TrimPrefix(key, "probe_error_message.")
		case strings.HasPrefix(key, "public_error_message."):
			code = strings.TrimPrefix(key, "public_error_message.")
		default:
			continue
		}
		code = strings.TrimSpace(code)
		value = strings.TrimSpace(value)
		if code != "" && value != "" {
			messages[code] = value
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return messages
}

func providerImageCapabilityRouteProfilesFromActions(actions []pluginmeta.ActionDescriptor) []providerImageCapabilityRouteProfile {
	profiles := make([]providerImageCapabilityRouteProfile, 0, len(actions))
	for _, action := range actions {
		if action.Capability != "image.capability.configure" {
			continue
		}
		profile, ok := providerImageCapabilityRouteProfileFromAction(action)
		if !ok {
			continue
		}
		profiles = append(profiles, profile)
	}
	return dedupeProviderImageCapabilityRouteProfiles(profiles)
}

type providerImageCapabilityProfileStore interface {
	setProviderImageCapabilityRouteProfiles([]providerImageCapabilityRouteProfile)
}

func (s *Server) syncProviderImageCapabilityRouteProfiles() {
	if s == nil || s.pluginActions == nil {
		return
	}
	store, ok := s.store.(providerImageCapabilityProfileStore)
	if !ok {
		return
	}
	store.setProviderImageCapabilityRouteProfiles(providerImageCapabilityRouteProfilesFromActions(s.pluginActions.List()))
}

func imageCapabilityActionResourceID(payload json.RawMessage, fallback string) (string, error) {
	resourceID := strings.TrimSpace(fallback)
	if resourceID != "" {
		return resourceID, nil
	}
	var request struct {
		ResourceID string `json:"resource_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &request); err != nil {
			return "", NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
		}
	}
	return strings.TrimSpace(request.ResourceID), nil
}

func imageCapabilityActionResultWithRoute(data any, routeID string) any {
	raw, err := json.Marshal(data)
	if err != nil {
		return data
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return data
	}
	result["route_id"] = routeID
	return result
}

func (s *Server) updateProviderImageCapability(resourceID string, capability string, profile providerImageCapabilityRouteProfile) (ProviderResource, error) {
	profile.withDefaults()
	options := map[string]string{
		profile.CapabilityOption:          capability,
		profile.CapabilityCheckedAtOption: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if profile.capabilityIsSupported(capability) {
		options[profile.RouteBackfillOption] = profile.RouteBackfillValue
	}
	return s.store.UpdateProviderResourceOptions(resourceID, options)
}

func (s *Server) ensureProviderImageCapabilityRoute(providerID string, profile providerImageCapabilityRouteProfile) (ModelRoute, error) {
	var disabled *ModelRoute
	for _, route := range s.store.ListRoutes() {
		if !providerImageCapabilityRouteMatches(route, providerID, profile) {
			continue
		}
		if route.Status == StatusActive {
			return route, nil
		}
		if disabled == nil {
			copy := route
			disabled = &copy
		}
	}
	if disabled != nil {
		disabled.Status = StatusActive
		return s.store.UpdateRoute(disabled.ID, *disabled)
	}
	return s.store.CreateRoute(defaultProviderImageCapabilityRoute(providerID, profile))
}

func (s *Server) setProviderImageCapabilityRoutesStatus(providerID string, profile providerImageCapabilityRouteProfile, status string) error {
	for _, route := range s.store.ListRoutes() {
		if !providerImageCapabilityRouteMatches(route, providerID, profile) || route.Status == status {
			continue
		}
		route.Status = status
		if _, err := s.store.UpdateRoute(route.ID, route); err != nil {
			return err
		}
	}
	return nil
}

func providerImageCapabilityRouteMatches(route ModelRoute, providerID string, profile providerImageCapabilityRouteProfile) bool {
	profile.withDefaults()
	return strings.TrimSpace(route.ModelName) == profile.PublicModel &&
		strings.TrimSpace(route.ProviderID) == strings.TrimSpace(providerID) &&
		strings.TrimSpace(route.ProviderModel) == profile.UpstreamModel
}

func defaultProviderImageCapabilityRoute(providerID string, profile providerImageCapabilityRouteProfile) ModelRoute {
	return ModelRoute{
		ModelName:     profile.PublicModel,
		ProviderID:    strings.TrimSpace(providerID),
		ProviderModel: profile.UpstreamModel,
		Priority:      1,
		Weight:        100,
		QualityScore:  50,
		CostScore:     50,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityWeighted,
		ProjectScope:  RouteProjectScopeAll,
	}
}

func (p *providerImageCapabilityRouteProfile) withDefaults() {
	if p.CapabilityOption == "" {
		p.CapabilityOption = "image_capability"
	}
	if p.CapabilityCheckedAtOption == "" {
		p.CapabilityCheckedAtOption = "image_capability_checked_at"
	}
	if p.CapabilitySupportedValue == "" {
		p.CapabilitySupportedValue = "supported"
	}
	if p.CapabilityUnsupportedValue == "" {
		p.CapabilityUnsupportedValue = "unsupported"
	}
	if p.RouteBackfillOption == "" {
		p.RouteBackfillOption = "image_capability_route_backfill_v1"
	}
	if p.RouteBackfillValue == "" {
		p.RouteBackfillValue = "completed"
	}
	if p.ProviderErrorCode == "" {
		p.ProviderErrorCode = "provider_image_capability_provider_required"
	}
	if p.ProviderErrorMessage == "" {
		p.ProviderErrorMessage = "The image capability route must use the Provider declared by its plugin"
	}
	if p.UpstreamModelErrorCode == "" {
		p.UpstreamModelErrorCode = "provider_image_capability_upstream_model_invalid"
	}
	if p.UpstreamModelErrorMessage == "" {
		p.UpstreamModelErrorMessage = "The image capability route must use the upstream model declared by its plugin"
	}
	if p.CapabilityErrorCode == "" {
		p.CapabilityErrorCode = "provider_image_capability_required"
	}
	if p.CapabilityErrorMessage == "" {
		p.CapabilityErrorMessage = "Test image generation with an eligible provider resource before activating this route"
	}
	if p.EnabledRequiredErrorCode == "" {
		p.EnabledRequiredErrorCode = "provider_image_capability_enabled_required"
	}
	if p.EnabledRequiredMessage == "" {
		p.EnabledRequiredMessage = "The enabled field is required"
	}
	if p.AuditAction == "" {
		p.AuditAction = "configure_provider_image_capability"
	}
	if p.OperationKeyPrefix == "" {
		p.OperationKeyPrefix = "provider-image-capability"
	}
	if p.ProbePrompt == "" {
		p.ProbePrompt = "A simple solid color test image centered on a plain background."
	}
	if p.ProbeBackground == "" {
		p.ProbeBackground = "auto"
	}
	if p.ProbeQuality == "" {
		p.ProbeQuality = "low"
	}
	if p.ProbeSize == "" {
		p.ProbeSize = "1024x1024"
	}
	if p.ProbeTimeoutErrorCode == "" {
		p.ProbeTimeoutErrorCode = "provider_image_capability_upstream_timeout"
	}
	if p.ProbeTimeoutErrorMessage == "" {
		p.ProbeTimeoutErrorMessage = "Provider image capability test timed out"
	}
	if !p.RequestSupportsMaskSet {
		p.RequestSupportsMask = true
	}
	if p.RequestSizePolicy == "" {
		p.RequestSizePolicy = imageRequestSizePolicyGPTImage2
	}
	if len(p.RequestAllowedQualities) == 0 {
		p.RequestAllowedQualities = defaultImageRequestQualities()
	}
	if len(p.RequestAllowedFormats) == 0 {
		p.RequestAllowedFormats = defaultImageResponseFormats()
	}
	if p.RequestMaxOutputImages <= 0 {
		p.RequestMaxOutputImages = currentImageOutputLimit
	}
}

func (p providerImageCapabilityRouteProfile) capabilityIsSupported(capability string) bool {
	p.withDefaults()
	return strings.TrimSpace(capability) == p.CapabilitySupportedValue
}

func (p providerImageCapabilityRouteProfile) capabilityIsUnsupported(capability string) bool {
	p.withDefaults()
	return strings.TrimSpace(capability) == p.CapabilityUnsupportedValue
}

func (p providerImageCapabilityRouteProfile) key() string {
	p.withDefaults()
	key := strings.Join([]string{
		p.ProviderType,
		p.ResourceType,
		p.PublicModel,
		p.UpstreamModel,
		p.CapabilityOption,
		p.CapabilityCheckedAtOption,
		p.CapabilitySupportedValue,
		p.CapabilityUnsupportedValue,
		p.RouteBackfillOption,
		p.RouteBackfillValue,
		p.ProviderErrorCode,
		p.ProviderErrorMessage,
		p.UpstreamModelErrorCode,
		p.UpstreamModelErrorMessage,
		p.CapabilityErrorCode,
		p.CapabilityErrorMessage,
		p.EnabledRequiredErrorCode,
		p.EnabledRequiredMessage,
		p.AuditAction,
		p.OperationKeyPrefix,
		p.ProbePrompt,
		p.ProbeBackground,
		p.ProbeQuality,
		p.ProbeSize,
		p.ProbeTimeoutErrorCode,
		p.ProbeTimeoutErrorMessage,
		p.RuntimeUnsupportedErrorCode,
		p.RequestAliasModel,
		p.RequestAliasHeader,
		p.RequestAliasOriginator,
		p.RequestAliasResponseFormat,
		boolString(p.RequestDefaultModel),
		boolString(p.RequestSupportsMask),
		p.RequestSizePolicy,
		strings.Join(p.RequestAllowedSizes, ","),
		strings.Join(p.RequestAllowedQualities, ","),
		strings.Join(p.RequestAllowedFormats, ","),
		strconv.Itoa(p.RequestMaxOutputImages),
	}, "\x00")
	for _, entry := range sortedStringMapEntries(p.ProbeErrorMessages) {
		key += "\x00" + entry
	}
	return key
}

func sortedStringMapEntries(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	entries := make([]string, 0, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			entries = append(entries, key+"="+value)
		}
	}
	sort.Strings(entries)
	return entries
}

func truthyString(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func boolMetadata(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y", "on", "enabled":
		return true, true
	case "false", "0", "no", "n", "off", "disabled":
		return false, true
	default:
		return false, false
	}
}

func stringListMetadata(value string) []string {
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func intMetadata(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func dedupeProviderImageCapabilityRouteProfiles(profiles []providerImageCapabilityRouteProfile) []providerImageCapabilityRouteProfile {
	if len(profiles) == 0 {
		return nil
	}
	seen := map[string]bool{}
	deduped := make([]providerImageCapabilityRouteProfile, 0, len(profiles))
	for _, profile := range profiles {
		profile.withDefaults()
		key := profile.key()
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, profile)
	}
	return deduped
}

func providerImageCapabilityProfileMatchesResource(profile providerImageCapabilityRouteProfile, provider Provider, resource ProviderResource) bool {
	profile.withDefaults()
	if profile.ProviderType != "" && strings.TrimSpace(provider.Type) != profile.ProviderType {
		return false
	}
	if profile.ResourceType != "" && strings.TrimSpace(resource.ResourceType) != profile.ResourceType {
		return false
	}
	return true
}
