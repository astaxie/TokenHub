package plugin

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	PluginManifestSchemaVersion = 1
	PluginAPIV1                 = "v1"
	CurrentPluginAPI            = PluginAPIV1
	CurrentCoreVersion          = "0.7.0"
)

type PluginErrorCode string

const (
	PluginErrorManifestSchemaUnsupported PluginErrorCode = "plugin_manifest_schema_unsupported"
	PluginErrorAPIRequired               PluginErrorCode = "plugin_api_required"
	PluginErrorAPIUnsupported            PluginErrorCode = "plugin_api_unsupported"
	PluginErrorCoreVersionInvalid        PluginErrorCode = "plugin_core_version_invalid"
	PluginErrorCoreVersionUnsupported    PluginErrorCode = "plugin_core_version_unsupported"
	PluginErrorKindUnsupported           PluginErrorCode = "plugin_kind_unsupported"
	PluginErrorPlacementUnsupported      PluginErrorCode = "plugin_placement_unsupported"
	PluginErrorCapabilityUnsupported     PluginErrorCode = "plugin_capability_unsupported"
	PluginErrorCapabilityRequired        PluginErrorCode = "plugin_capability_required"
)

const (
	CapabilityKindProviderType         = "provider_type"
	CapabilityKindProvider             = "provider"
	CapabilityKindProviderPolicy       = "provider_policy"
	CapabilityKindProviderResourceType = "provider_resource_type"
	CapabilityKindProviderCatalog      = "provider_catalog"
	CapabilityKindAdminUI              = "admin_ui"
	CapabilityKindSIM                  = "sim"
	CapabilityKindGatewayChain         = "gateway_chain"
	CapabilityKindManagementAction     = "management_action"
	CapabilityKindBackgroundJob        = "background_job"
)

const (
	ProviderPolicySupportsCustomHeaders          = "supports_custom_headers"
	ProviderPolicyAPIKeyRequired                 = "api_key_required"
	ProviderPolicyRouteRequiresResource          = "route_requires_resource"
	ProviderPolicyCredentialsScope               = "credentials_scope"
	ProviderPolicySessionAffinityKind            = "session_affinity_kind"
	ProviderPolicySystemPromptTransformDefault   = "system_prompt_transform_default"
	ProviderPolicyClaudeCodeAttributionDefault   = "claude_code_attribution_default"
	ProviderPolicyReasoningConfigurable          = "reasoning_configurable"
	ProviderPolicyPreserveReasoningContent       = "preserve_reasoning_content"
	ProviderPolicyResponsesModelAllowlist        = "responses_model_allowlist"
	ProviderPolicyDefaultBaseURL                 = "default_base_url"
	ProviderPolicyDefaultCatalogProviderType     = "default_catalog_provider_type"
	ProviderPolicyErrorProfile                   = "error_profile"
	ProviderPolicyModelDiscoveryPath             = "model_discovery_path"
	ProviderPolicyModelDiscoveryAuth             = "model_discovery_auth"
	ProviderPolicyModelDiscoveryAPIKeyQueryParam = "model_discovery_api_key_query_param"
	ProviderPolicyModelDiscoveryHeaders          = "model_discovery_headers"
	ProviderPolicyRouteProtocol                  = "route_protocol"
	ProviderPolicyAuthMode                       = "auth_mode"
	ProviderPolicyAuthModeLegacyOption           = "auth_mode_legacy_option"
	ProviderPolicyAuthModeInvalidErrorCode       = "auth_mode_invalid_error_code"
	ProviderPolicyAuthModeInvalidErrorMessage    = "auth_mode_invalid_error_message"
	ProviderPolicyCredentialRefreshProfile       = "credential_refresh_profile"
	ProviderCatalogEntry                         = "entry"
	ProviderCatalogModelCategory                 = "model_category"
	SIMCapabilityThemeTokens                     = "theme_tokens"
	SIMCapabilityShellLayout                     = "shell_layout"
	SIMCapabilityPageTemplate                    = "page_template"
	SIMCapabilityDashboardComposition            = "dashboard_composition"
	AdminUICapabilityLegacyProviderForm          = "provider_form"
	AdminUICapabilityLegacyDashboardPanel        = "dashboard_panel"
	AdminUICapabilityLegacyDashboardCard         = "dashboard_card"
	AdminUICapabilityLegacyNavSection            = "nav_section"
	AdminUICapabilityLegacyProviderModelPanel    = "provider_model_panel"
	AdminUICapabilityLegacyProviderResourceForm  = "provider_resource_form"
	AdminUICapabilityLegacyProviderResourcePanel = "provider_resource_panel"
	AdminUICapabilityLegacyRouteDetailPanel      = "route_detail_panel"
	AdminUICapabilityLegacySettingsPanel         = "settings_panel"
	PluginFeatureProviderPlugins                 = "provider_plugins"
	PluginFeatureAdminUIContributions            = "admin_ui_contributions"
	PluginFeatureSIMPresentation                 = "sim_presentation"
	PluginFeatureGatewayChainHooks               = "gateway_chain_hooks"
	PluginFeatureManagementActions               = "management_actions"
	PluginFeatureBackgroundJobs                  = "background_jobs"
	PluginFeatureStdioJSONV1                     = "stdio_json_v1"
	PluginFeatureMarketplaceDistribution         = "marketplace_distribution"
)

type PluginContractError struct {
	Code    PluginErrorCode
	Message string
}

func (e PluginContractError) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return e.Message
}

type PluginAPICompatibility struct {
	PluginAPI             string   `json:"plugin_api"`
	ManifestSchemaVersion int      `json:"manifest_schema_version"`
	MinCore               string   `json:"min_core"`
	MaxCore               string   `json:"max_core,omitempty"`
	CapabilityKinds       []string `json:"capability_kinds"`
	FeatureFlags          []string `json:"feature_flags"`
}

func SupportedPluginAPICompatibility() []PluginAPICompatibility {
	return []PluginAPICompatibility{{
		PluginAPI:             PluginAPIV1,
		ManifestSchemaVersion: PluginManifestSchemaVersion,
		MinCore:               CurrentCoreVersion,
		CapabilityKinds: []string{
			CapabilityKindAdminUI,
			CapabilityKindBackgroundJob,
			CapabilityKindGatewayChain,
			CapabilityKindManagementAction,
			CapabilityKindProvider,
			CapabilityKindProviderCatalog,
			CapabilityKindProviderPolicy,
			CapabilityKindProviderResourceType,
			CapabilityKindProviderType,
			CapabilityKindSIM,
		},
		FeatureFlags: []string{
			PluginFeatureProviderPlugins,
			PluginFeatureAdminUIContributions,
			PluginFeatureSIMPresentation,
			PluginFeatureGatewayChainHooks,
			PluginFeatureManagementActions,
			PluginFeatureBackgroundJobs,
			PluginFeatureStdioJSONV1,
			PluginFeatureMarketplaceDistribution,
		},
	}}
}

func PluginErrorCodeOf(err error) (PluginErrorCode, bool) {
	var contractErr PluginContractError
	if errors.As(err, &contractErr) {
		return contractErr.Code, true
	}
	return "", false
}

func ValidateManifestCompatibility(compat ManifestCompatibility) error {
	pluginAPI := strings.TrimSpace(compat.PluginAPI)
	if pluginAPI == "" {
		return pluginContractErrorf(PluginErrorAPIRequired, "tokenhub.plugin_api is required")
	}
	if pluginAPI != CurrentPluginAPI {
		return pluginContractErrorf(PluginErrorAPIUnsupported, "unsupported tokenhub.plugin_api %q", compat.PluginAPI)
	}
	if err := validateCoreVersionRange(compat.MinCore, compat.MaxCore); err != nil {
		return err
	}
	return nil
}

func ValidateCapabilityDescriptor(descriptor CapabilityDescriptor) error {
	kind := strings.TrimSpace(descriptor.Kind)
	name := strings.TrimSpace(descriptor.Name)
	if kind == "" || name == "" {
		return pluginContractErrorf(PluginErrorCapabilityRequired, "plugin capability kind and name are required")
	}
	if !safePluginContractToken(kind) || !validCapabilityKind(kind) {
		return pluginContractErrorf(PluginErrorCapabilityUnsupported, "unsupported plugin capability kind %q", descriptor.Kind)
	}
	if !validCapabilityName(kind, name) {
		return pluginContractErrorf(PluginErrorCapabilityUnsupported, "unsupported plugin capability %s.%s", kind, name)
	}
	return nil
}

func ValidateDescriptorContract(descriptor Descriptor) error {
	for _, kind := range descriptor.Kinds {
		if !validKind(kind) {
			return pluginContractErrorf(PluginErrorKindUnsupported, "unsupported plugin kind %q", kind)
		}
	}
	for _, placement := range descriptor.Placements {
		if !validPlacement(placement) {
			return pluginContractErrorf(PluginErrorPlacementUnsupported, "unsupported plugin placement %q", placement)
		}
	}
	for _, capability := range descriptor.Capabilities {
		if err := ValidateCapabilityDescriptor(capability); err != nil {
			return err
		}
	}
	return nil
}

func pluginContractErrorf(code PluginErrorCode, format string, args ...any) error {
	return PluginContractError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func validateCoreVersionRange(minCore string, maxCore string) error {
	current, err := parsePluginCoreVersion(CurrentCoreVersion)
	if err != nil {
		return fmt.Errorf("invalid current core version %q: %w", CurrentCoreVersion, err)
	}
	if strings.TrimSpace(minCore) != "" {
		minimum, err := parsePluginCoreVersion(minCore)
		if err != nil {
			return pluginContractErrorf(PluginErrorCoreVersionInvalid, "tokenhub.min_core is invalid: %v", err)
		}
		if comparePluginCoreVersion(current, minimum) < 0 {
			return pluginContractErrorf(PluginErrorCoreVersionUnsupported, "plugin requires TokenHub core %s or newer", strings.TrimSpace(minCore))
		}
	}
	if strings.TrimSpace(maxCore) != "" {
		maximum, err := parsePluginCoreVersion(maxCore)
		if err != nil {
			return pluginContractErrorf(PluginErrorCoreVersionInvalid, "tokenhub.max_core is invalid: %v", err)
		}
		if comparePluginCoreVersion(current, maximum) > 0 {
			return pluginContractErrorf(PluginErrorCoreVersionUnsupported, "plugin supports TokenHub core up to %s", strings.TrimSpace(maxCore))
		}
		if strings.TrimSpace(minCore) != "" {
			minimum, _ := parsePluginCoreVersion(minCore)
			if comparePluginCoreVersion(minimum, maximum) > 0 {
				return pluginContractErrorf(PluginErrorCoreVersionInvalid, "tokenhub.min_core cannot be greater than tokenhub.max_core")
			}
		}
	}
	return nil
}

type pluginCoreVersion struct {
	major int
	minor int
	patch int
}

func parsePluginCoreVersion(value string) (pluginCoreVersion, error) {
	normalized := strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(normalized, ".")
	if len(parts) != 3 {
		return pluginCoreVersion{}, fmt.Errorf("expected MAJOR.MINOR.PATCH")
	}
	parsed := make([]int, 3)
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return pluginCoreVersion{}, fmt.Errorf("expected non-negative numeric version component")
		}
		parsed[index] = number
	}
	return pluginCoreVersion{major: parsed[0], minor: parsed[1], patch: parsed[2]}, nil
}

func comparePluginCoreVersion(left pluginCoreVersion, right pluginCoreVersion) int {
	if left.major != right.major {
		return compareInt(left.major, right.major)
	}
	if left.minor != right.minor {
		return compareInt(left.minor, right.minor)
	}
	return compareInt(left.patch, right.patch)
}

func compareInt(left int, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func validCapabilityKind(kind string) bool {
	switch kind {
	case CapabilityKindProviderType,
		CapabilityKindProvider,
		CapabilityKindProviderPolicy,
		CapabilityKindProviderResourceType,
		CapabilityKindProviderCatalog,
		CapabilityKindAdminUI,
		CapabilityKindSIM,
		CapabilityKindGatewayChain,
		CapabilityKindManagementAction,
		CapabilityKindBackgroundJob:
		return true
	default:
		return false
	}
}

func validCapabilityName(kind string, name string) bool {
	if !safePluginContractToken(name) {
		return false
	}
	switch kind {
	case CapabilityKindProviderPolicy:
		return validProviderPolicyCapabilityName(name)
	case CapabilityKindProviderCatalog:
		return name == ProviderCatalogEntry || name == ProviderCatalogModelCategory
	case CapabilityKindAdminUI:
		return validAdminUICapabilityName(name)
	case CapabilityKindSIM:
		return validSIMCapabilityName(name)
	case CapabilityKindGatewayChain:
		return validGatewayHookStage(GatewayHookStage(name))
	default:
		return true
	}
}

func validProviderPolicyCapabilityName(name string) bool {
	switch name {
	case ProviderPolicySupportsCustomHeaders,
		ProviderPolicyAPIKeyRequired,
		ProviderPolicyRouteRequiresResource,
		ProviderPolicyCredentialsScope,
		ProviderPolicySessionAffinityKind,
		ProviderPolicySystemPromptTransformDefault,
		ProviderPolicyClaudeCodeAttributionDefault,
		ProviderPolicyReasoningConfigurable,
		ProviderPolicyPreserveReasoningContent,
		ProviderPolicyResponsesModelAllowlist,
		ProviderPolicyDefaultBaseURL,
		ProviderPolicyDefaultCatalogProviderType,
		ProviderPolicyErrorProfile,
		ProviderPolicyModelDiscoveryPath,
		ProviderPolicyModelDiscoveryAuth,
		ProviderPolicyModelDiscoveryAPIKeyQueryParam,
		ProviderPolicyModelDiscoveryHeaders,
		ProviderPolicyRouteProtocol,
		ProviderPolicyAuthMode,
		ProviderPolicyAuthModeLegacyOption,
		ProviderPolicyAuthModeInvalidErrorCode,
		ProviderPolicyAuthModeInvalidErrorMessage,
		ProviderPolicyCredentialRefreshProfile:
		return true
	default:
		return false
	}
}

func validAdminUICapabilityName(name string) bool {
	if validLegacyAdminUICapabilityName(name) {
		return true
	}
	return validAdminUISlot(AdminUISlot(name))
}

func validLegacyAdminUICapabilityName(name string) bool {
	switch name {
	case AdminUICapabilityLegacyProviderForm,
		AdminUICapabilityLegacyDashboardPanel,
		AdminUICapabilityLegacyDashboardCard,
		AdminUICapabilityLegacyNavSection,
		AdminUICapabilityLegacyProviderModelPanel,
		AdminUICapabilityLegacyProviderResourceForm,
		AdminUICapabilityLegacyProviderResourcePanel,
		AdminUICapabilityLegacyRouteDetailPanel,
		AdminUICapabilityLegacySettingsPanel:
		return true
	default:
		return false
	}
}

func validSIMCapabilityName(name string) bool {
	switch name {
	case SIMCapabilityThemeTokens,
		SIMCapabilityShellLayout,
		SIMCapabilityPageTemplate,
		SIMCapabilityDashboardComposition:
		return true
	default:
		return false
	}
}

func safePluginContractToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "..") || strings.Contains(value, "\\") || strings.Contains(value, "<") {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') ||
			char == '_' || char == '-' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}
