package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type AdapterCapability string

const (
	AdapterCapabilityChat           AdapterCapability = "chat"
	AdapterCapabilityChatStream     AdapterCapability = "chat_stream"
	AdapterCapabilityResponses      AdapterCapability = "responses"
	AdapterCapabilityResponseStream AdapterCapability = "responses_stream"
	AdapterCapabilityEmbeddings     AdapterCapability = "embeddings"
	AdapterCapabilityModels         AdapterCapability = "models"
	AdapterCapabilityProbe          AdapterCapability = "probe"
	AdapterCapabilityQuota          AdapterCapability = "quota"
	AdapterCapabilityOAuth          AdapterCapability = "oauth"
	AdapterCapabilityAffinity       AdapterCapability = "session_affinity"
	AdapterCapabilityCompact        AdapterCapability = "responses_compact"
	AdapterCapabilityWebSocket      AdapterCapability = "responses_websocket"
	AdapterCapabilityImageGenerate  AdapterCapability = "image_generation"
)

type AdapterDescriptor struct {
	Type           string                                    `json:"type"`
	Capabilities   []AdapterCapability                       `json:"capabilities"`
	PluginID       string                                    `json:"plugin_id,omitempty"`
	ResourceTypes  []pluginmeta.ManifestProviderResourceType `json:"resource_types,omitempty"`
	ProviderPolicy AdapterProviderPolicy                     `json:"provider_policy"`
}

type AdapterProviderPolicy struct {
	RouteProtocols               []string                    `json:"route_protocols,omitempty"`
	AuthModes                    []string                    `json:"auth_modes,omitempty"`
	AuthModeLegacyOption         string                      `json:"auth_mode_legacy_option,omitempty"`
	AuthModeInvalidErrorCode     string                      `json:"auth_mode_invalid_error_code,omitempty"`
	AuthModeInvalidErrorMessage  string                      `json:"auth_mode_invalid_error_message,omitempty"`
	SupportsCustomHeaders        bool                        `json:"supports_custom_headers"`
	APIKeyRequired               bool                        `json:"api_key_required"`
	RouteRequiresResource        bool                        `json:"route_requires_resource"`
	CredentialsScope             string                      `json:"credentials_scope,omitempty"`
	SessionAffinityKind          string                      `json:"session_affinity_kind,omitempty"`
	ClaudeCodeAttributionDefault string                      `json:"claude_code_attribution_default,omitempty"`
	PreserveReasoningContent     *bool                       `json:"preserve_reasoning_content,omitempty"`
	ResponsesModelAllowlist      []string                    `json:"responses_model_allowlist,omitempty"`
	DefaultBaseURL               string                      `json:"default_base_url,omitempty"`
	DefaultCatalogProviderType   bool                        `json:"default_catalog_provider_type,omitempty"`
	ErrorProfile                 string                      `json:"error_profile,omitempty"`
	CredentialRefreshProfile     string                      `json:"credential_refresh_profile,omitempty"`
	ModelDiscovery               AdapterModelDiscoveryPolicy `json:"model_discovery,omitempty"`
	ModelCategories              []AdapterModelCategory      `json:"model_categories,omitempty"`
}

type AdapterModelDiscoveryPolicy struct {
	Path             string            `json:"path,omitempty"`
	Auth             string            `json:"auth,omitempty"`
	APIKeyQueryParam string            `json:"api_key_query_param,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
}

type AdapterModelCategory struct {
	Key               string   `json:"key"`
	Label             string   `json:"label,omitempty"`
	Order             int      `json:"order,omitempty"`
	Aliases           []string `json:"aliases,omitempty"`
	FamilyPrefixes    []string `json:"family_prefixes,omitempty"`
	CanonicalPrefixes []string `json:"canonical_prefixes,omitempty"`
}

// AdapterRegistry is the single source of truth for which adapter serves a
// provider type and which capabilities that adapter advertises. An adapter may
// be present without a descriptor: capabilities are only declared through
// Register, so Describe reporting "unknown" means "capabilities undeclared",
// not "adapter missing".
type AdapterRegistry struct {
	adapters    map[string]any
	descriptors map[string]AdapterDescriptor
	plugins     *pluginmeta.Registry
}

func NewAdapterRegistry() *AdapterRegistry {
	return NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
}

func NewAdapterRegistryWithPlugins(plugins *pluginmeta.Registry) *AdapterRegistry {
	if plugins == nil {
		plugins = pluginmeta.NewRegistry()
	}
	return &AdapterRegistry{
		adapters:    map[string]any{},
		descriptors: map[string]AdapterDescriptor{},
		plugins:     plugins,
	}
}

func (r *AdapterRegistry) Register(adapterType string, adapter any, capabilities ...AdapterCapability) {
	r.register(adapterType, adapter, "", capabilities...)
}

type AdapterRegistration struct {
	Type         string
	Adapter      any
	Capabilities []AdapterCapability
}

func (r *AdapterRegistry) RegisterPlugin(descriptor pluginmeta.Descriptor, registrations ...AdapterRegistration) error {
	if r == nil {
		return fmt.Errorf("adapter registry is not configured")
	}
	if err := r.plugins.Register(descriptor); err != nil {
		return err
	}
	for _, registration := range registrations {
		r.register(registration.Type, registration.Adapter, descriptor.ID, registration.Capabilities...)
	}
	return nil
}

func (r *AdapterRegistry) register(adapterType string, adapter any, pluginID string, capabilities ...AdapterCapability) {
	if r == nil {
		return
	}
	r.adapters[adapterType] = adapter
	unique := make(map[AdapterCapability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if capability != "" {
			unique[capability] = struct{}{}
		}
	}
	normalized := make([]AdapterCapability, 0, len(unique))
	for capability := range unique {
		normalized = append(normalized, capability)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	r.descriptors[adapterType] = AdapterDescriptor{Type: adapterType, Capabilities: normalized, PluginID: pluginID}
}

func (r *AdapterRegistry) Resolve(adapterType string) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("adapter registry is not configured")
	}
	adapter, ok := r.adapters[adapterType]
	if !ok {
		return nil, NewHTTPError(503, "provider_adapter_missing", "Provider adapter is not registered")
	}
	return adapter, nil
}

// resolveTypedAdapter resolves adapterType and narrows it to T. It reports
// false when the type is unregistered or the registered adapter is not a T.
func resolveTypedAdapter[T any](r *AdapterRegistry, adapterType string) (T, bool) {
	var zero T
	adapter, err := r.Resolve(adapterType)
	if err != nil {
		return zero, false
	}
	typed, ok := adapter.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

func (r *AdapterRegistry) Describe(adapterType string) (AdapterDescriptor, bool) {
	if r == nil {
		return AdapterDescriptor{}, false
	}
	descriptor, ok := r.descriptors[adapterType]
	if ok {
		descriptor = r.withProviderPolicy(descriptor)
	}
	return descriptor, ok
}

func adapterSupports(descriptor AdapterDescriptor, capability AdapterCapability) bool {
	for _, supported := range descriptor.Capabilities {
		if supported == capability {
			return true
		}
	}
	return false
}

func adapterSupportsResourceType(descriptor AdapterDescriptor, resourceType string) bool {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if resourceType == "" {
		return false
	}
	for _, supported := range descriptor.ResourceTypes {
		if strings.ToLower(strings.TrimSpace(supported.Type)) == resourceType {
			return true
		}
	}
	return false
}

func (r *AdapterRegistry) List() []AdapterDescriptor {
	if r == nil {
		return nil
	}
	items := make([]AdapterDescriptor, 0, len(r.descriptors))
	for _, descriptor := range r.descriptors {
		items = append(items, r.withProviderPolicy(descriptor))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Type < items[j].Type })
	return items
}

func (r *AdapterRegistry) withProviderPolicy(descriptor AdapterDescriptor) AdapterDescriptor {
	descriptor.ResourceTypes = adapterProviderResourceTypes(r, descriptor.Type)
	descriptor.ProviderPolicy = AdapterProviderPolicy{
		RouteProtocols:               adapterRouteProtocols(r, descriptor),
		AuthModes:                    adapterAuthModes(r, descriptor.Type),
		AuthModeLegacyOption:         adapterAuthModeLegacyOption(r, descriptor.Type),
		AuthModeInvalidErrorCode:     adapterAuthModeInvalidErrorCode(r, descriptor.Type),
		AuthModeInvalidErrorMessage:  adapterAuthModeInvalidErrorMessage(r, descriptor.Type),
		SupportsCustomHeaders:        adapterSupportsProviderHeaders(r, descriptor.Type),
		APIKeyRequired:               adapterAPIKeyRequired(r, descriptor.Type),
		RouteRequiresResource:        adapterRequiresRouteResource(r, descriptor.Type),
		CredentialsScope:             adapterCredentialsScope(r, descriptor.Type),
		SessionAffinityKind:          adapterSessionAffinityKind(r, descriptor.Type),
		ClaudeCodeAttributionDefault: adapterClaudeCodeAttributionDefault(r, descriptor.Type),
		PreserveReasoningContent:     adapterPreserveReasoningContent(r, descriptor.Type),
		ResponsesModelAllowlist:      adapterResponsesModelAllowlist(r, descriptor.Type),
		DefaultBaseURL:               adapterDefaultBaseURL(r, descriptor.Type),
		DefaultCatalogProviderType:   adapterDefaultCatalogProviderType(r, descriptor.Type),
		ErrorProfile:                 adapterErrorProfile(r, descriptor.Type),
		CredentialRefreshProfile:     adapterCredentialRefreshProfile(r, descriptor.Type),
		ModelDiscovery:               adapterModelDiscovery(r, descriptor.Type),
		ModelCategories:              adapterModelCategories(r, descriptor.Type),
	}
	return descriptor
}

func adapterRouteProtocols(registry *AdapterRegistry, descriptor AdapterDescriptor) []string {
	if protocols := providerPolicyStringValues(registry, descriptor.Type, "route_protocol"); len(protocols) > 0 {
		return routeProtocolList(protocols)
	}
	return routeProtocolSetList(routeProviderProtocolsFromCapabilities(descriptor))
}

func adapterAuthModes(registry *AdapterRegistry, providerType string) []string {
	return providerPolicyStringValues(registry, providerType, providerAuthModeOption)
}

func adapterAuthModeLegacyOption(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, providerAuthModeLegacyOptionPolicy); ok {
		return value
	}
	return ""
}

func adapterAuthModeInvalidErrorCode(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, providerAuthModeInvalidErrorCodePolicy); ok {
		return value
	}
	return ""
}

func adapterAuthModeInvalidErrorMessage(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, providerAuthModeInvalidErrorMessagePolicy); ok {
		return value
	}
	return ""
}

func adapterSupportsProviderHeaders(registry *AdapterRegistry, providerType string) bool {
	if value, ok := providerPolicyBoolCapability(registry, providerType, "supports_custom_headers"); ok {
		return value
	}
	return defaultProviderTypeSupportsHeaders(providerType)
}

func adapterAPIKeyRequired(registry *AdapterRegistry, providerType string) bool {
	if value, ok := providerPolicyBoolCapability(registry, providerType, providerAPIKeyRequiredOption); ok {
		return value
	}
	return true
}

func adapterRequiresRouteResource(registry *AdapterRegistry, providerType string) bool {
	if value, ok := providerPolicyBoolCapability(registry, providerType, "route_requires_resource"); ok {
		return value
	}
	return false
}

func adapterCredentialsScope(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, "credentials_scope"); ok {
		return value
	}
	return providerCredentialsScopeProvider
}

func adapterSessionAffinityKind(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, "session_affinity_kind"); ok && validProviderSessionAffinityKind(value) {
		return value
	}
	return AffinityKindProviderSession
}

func adapterClaudeCodeAttributionDefault(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, claudeCodeAttributionDefaultPolicy); ok {
		return normalizeClaudeCodeAttributionPolicyOrEmpty(value)
	}
	return ""
}

func adapterPreserveReasoningContent(registry *AdapterRegistry, providerType string) *bool {
	if value, ok := providerPolicyBoolCapability(registry, providerType, reasoningContentOption); ok {
		return boolPointer(value)
	}
	return nil
}

func adapterResponsesModelAllowlist(registry *AdapterRegistry, providerType string) []string {
	return providerPolicyStringValues(registry, providerType, "responses_model_allowlist")
}

func adapterDefaultBaseURL(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, "default_base_url"); ok {
		return strings.TrimRight(strings.TrimSpace(value), "/")
	}
	return ""
}

func adapterDefaultCatalogProviderType(registry *AdapterRegistry, providerType string) bool {
	if value, ok := providerPolicyBoolCapability(registry, providerType, "default_catalog_provider_type"); ok {
		return value
	}
	return false
}

func adapterErrorProfile(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, "error_profile"); ok {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

func adapterCredentialRefreshProfile(registry *AdapterRegistry, providerType string) string {
	if value, ok := providerPolicyStringCapability(registry, providerType, providerCredentialRefreshProfileOption); ok {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

func adapterModelDiscovery(registry *AdapterRegistry, providerType string) AdapterModelDiscoveryPolicy {
	policy := AdapterModelDiscoveryPolicy{}
	if value, ok := providerPolicyStringCapability(registry, providerType, "model_discovery_path"); ok {
		policy.Path = strings.TrimSpace(value)
	}
	if value, ok := providerPolicyStringCapability(registry, providerType, "model_discovery_auth"); ok {
		policy.Auth = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := providerPolicyStringCapability(registry, providerType, "model_discovery_api_key_query_param"); ok {
		policy.APIKeyQueryParam = strings.TrimSpace(value)
	}
	if value, ok := providerPolicyStringCapability(registry, providerType, "model_discovery_headers"); ok {
		var headers map[string]string
		if err := json.Unmarshal([]byte(value), &headers); err == nil {
			policy.Headers = normalizedStringMap(headers)
		}
	}
	return policy
}

func adapterModelCategories(registry *AdapterRegistry, providerType string) []AdapterModelCategory {
	plugin, ok := adapterPluginDescriptor(registry, providerType)
	if !ok {
		return nil
	}
	categories := []AdapterModelCategory{}
	for _, capability := range plugin.Capabilities {
		if capability.Kind != "provider_catalog" || capability.Name != "model_category" {
			continue
		}
		if capability.Subject != "" && capability.Subject != providerType {
			continue
		}
		category, ok := adapterModelCategoryFromCapability(capability)
		if ok {
			categories = append(categories, category)
		}
	}
	return sortedAdapterModelCategories(categories)
}

func adapterModelCategoryFromCapability(capability pluginmeta.CapabilityDescriptor) (AdapterModelCategory, bool) {
	var category AdapterModelCategory
	if err := json.Unmarshal([]byte(strings.TrimSpace(capability.Value)), &category); err != nil {
		return AdapterModelCategory{}, false
	}
	category.Key = strings.ToLower(strings.TrimSpace(category.Key))
	if category.Key == "" {
		return AdapterModelCategory{}, false
	}
	category.Label = strings.TrimSpace(category.Label)
	category.Aliases = sortedUniqueStrings(category.Aliases)
	category.FamilyPrefixes = sortedUniqueStrings(category.FamilyPrefixes)
	category.CanonicalPrefixes = sortedUniqueStrings(category.CanonicalPrefixes)
	return category, true
}

func sortedAdapterModelCategories(categories []AdapterModelCategory) []AdapterModelCategory {
	byKey := map[string]AdapterModelCategory{}
	for _, category := range categories {
		if category.Key == "" {
			continue
		}
		if existing, ok := byKey[category.Key]; !ok || categoryHasMoreMetadata(category, existing) {
			byKey[category.Key] = category
		}
	}
	result := make([]AdapterModelCategory, 0, len(byKey))
	for _, category := range byKey {
		result = append(result, category)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].Key < result[j].Key
	})
	return result
}

func categoryHasMoreMetadata(candidate AdapterModelCategory, existing AdapterModelCategory) bool {
	score := 0
	if candidate.Label != "" {
		score++
	}
	score += len(candidate.Aliases) + len(candidate.FamilyPrefixes) + len(candidate.CanonicalPrefixes)
	existingScore := 0
	if existing.Label != "" {
		existingScore++
	}
	existingScore += len(existing.Aliases) + len(existing.FamilyPrefixes) + len(existing.CanonicalPrefixes)
	return score >= existingScore
}

func providerPolicyBoolCapability(registry *AdapterRegistry, providerType string, name string) (bool, bool) {
	value, ok := providerPolicyStringCapability(registry, providerType, name)
	if !ok {
		return false, false
	}
	return strings.EqualFold(value, "true"), true
}

func providerPolicyStringCapability(registry *AdapterRegistry, providerType string, name string) (string, bool) {
	values := providerPolicyStringValues(registry, providerType, name)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func providerPolicyStringValues(registry *AdapterRegistry, providerType string, name string) []string {
	plugin, ok := adapterPluginDescriptor(registry, providerType)
	if !ok {
		return nil
	}
	values := []string{}
	for _, capability := range plugin.Capabilities {
		if capability.Kind != "provider_policy" || capability.Name != name {
			continue
		}
		if capability.Subject != "" && capability.Subject != providerType {
			continue
		}
		if value := strings.TrimSpace(capability.Value); value != "" {
			values = append(values, value)
		}
	}
	return sortedUniqueStrings(values)
}

func adapterProviderResourceTypes(registry *AdapterRegistry, providerType string) []pluginmeta.ManifestProviderResourceType {
	plugin, ok := adapterPluginDescriptor(registry, providerType)
	if !ok {
		return nil
	}
	itemsByType := map[string]pluginmeta.ManifestProviderResourceType{}
	for _, capability := range plugin.Capabilities {
		if capability.Kind != "provider_resource_type" {
			continue
		}
		if capability.Subject != "" && capability.Subject != providerType {
			continue
		}
		resourceType := adapterResourceTypeFromCapability(capability)
		if resourceType.Type == "" {
			continue
		}
		if current, exists := itemsByType[resourceType.Type]; !exists || adapterResourceTypeHasMetadata(resourceType) || !adapterResourceTypeHasMetadata(current) {
			itemsByType[resourceType.Type] = resourceType
		}
	}
	items := make([]pluginmeta.ManifestProviderResourceType, 0, len(itemsByType))
	for _, item := range itemsByType {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Type < items[j].Type })
	return items
}

func adapterPluginDescriptor(registry *AdapterRegistry, providerType string) (pluginmeta.Descriptor, bool) {
	if registry == nil || registry.plugins == nil {
		return pluginmeta.Descriptor{}, false
	}
	descriptor, ok := registry.descriptors[providerType]
	if !ok || descriptor.PluginID == "" {
		return pluginmeta.Descriptor{}, false
	}
	return registry.plugins.Describe(descriptor.PluginID)
}

func adapterResourceTypeFromCapability(capability pluginmeta.CapabilityDescriptor) pluginmeta.ManifestProviderResourceType {
	resourceType := pluginmeta.ManifestProviderResourceType{Type: strings.TrimSpace(capability.Name)}
	if value := strings.TrimSpace(capability.Value); value != "" {
		var parsed pluginmeta.ManifestProviderResourceType
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			resourceType = parsed
			if strings.TrimSpace(resourceType.Type) == "" {
				resourceType.Type = capability.Name
			}
		}
	}
	resourceType.Type = strings.TrimSpace(resourceType.Type)
	resourceType.DisplayName = strings.TrimSpace(resourceType.DisplayName)
	resourceType.AuthModes = sortedUniqueStrings(resourceType.AuthModes)
	resourceType.Defaults = normalizedStringMap(resourceType.Defaults)
	resourceType.CredentialIdentityProfile = strings.TrimSpace(resourceType.CredentialIdentityProfile)
	return resourceType
}

func adapterResourceTypeHasMetadata(resourceType pluginmeta.ManifestProviderResourceType) bool {
	return resourceType.DisplayName != "" || len(resourceType.AuthModes) > 0 || len(resourceType.Defaults) > 0 || resourceType.CredentialIdentityProfile != "" || resourceType.CredentialInputOptional || resourceType.Default
}

func sortedUniqueStrings(items []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func normalizedStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	result := make(map[string]string, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (r *AdapterRegistry) ListPlugins() []pluginmeta.Descriptor {
	if r == nil {
		return nil
	}
	return r.plugins.List()
}
