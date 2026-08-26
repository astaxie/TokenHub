package server

import (
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
	Type           string                `json:"type"`
	Capabilities   []AdapterCapability   `json:"capabilities"`
	PluginID       string                `json:"plugin_id,omitempty"`
	ProviderPolicy AdapterProviderPolicy `json:"provider_policy"`
}

type AdapterProviderPolicy struct {
	RouteProtocols        []string `json:"route_protocols,omitempty"`
	SupportsCustomHeaders bool     `json:"supports_custom_headers"`
	RouteRequiresResource bool     `json:"route_requires_resource"`
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
	descriptor.ProviderPolicy = AdapterProviderPolicy{
		RouteProtocols:        adapterRouteProtocols(r, descriptor),
		SupportsCustomHeaders: adapterSupportsProviderHeaders(r, descriptor.Type),
		RouteRequiresResource: adapterRequiresRouteResource(r, descriptor.Type),
	}
	return descriptor
}

func adapterRouteProtocols(registry *AdapterRegistry, descriptor AdapterDescriptor) []string {
	if protocoler, ok := resolveTypedAdapter[ProviderRouteProtocoler](registry, descriptor.Type); ok {
		return routeProtocolList(protocoler.RouteProtocols())
	}
	return routeProtocolSetList(routeProviderProtocolsFromCapabilities(descriptor))
}

func adapterSupportsProviderHeaders(registry *AdapterRegistry, providerType string) bool {
	if policyer, ok := resolveTypedAdapter[ProviderHeaderPolicyer](registry, providerType); ok {
		return policyer.SupportsProviderHeaders()
	}
	return legacyProviderTypeSupportsHeaders(providerType)
}

func adapterRequiresRouteResource(registry *AdapterRegistry, providerType string) bool {
	if value, ok := providerPolicyBoolCapability(registry, providerType, "route_requires_resource"); ok {
		return value
	}
	return providerType == ProviderOpenAICodex
}

func providerPolicyBoolCapability(registry *AdapterRegistry, providerType string, name string) (bool, bool) {
	if registry == nil || registry.plugins == nil {
		return false, false
	}
	descriptor, ok := registry.descriptors[providerType]
	if !ok || descriptor.PluginID == "" {
		return false, false
	}
	plugin, ok := registry.plugins.Describe(descriptor.PluginID)
	if !ok {
		return false, false
	}
	for _, capability := range plugin.Capabilities {
		if capability.Kind != "provider_policy" || capability.Name != name {
			continue
		}
		if capability.Subject != "" && capability.Subject != providerType {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(capability.Value), "true"), true
	}
	return false, false
}

func (r *AdapterRegistry) ListPlugins() []pluginmeta.Descriptor {
	if r == nil {
		return nil
	}
	return r.plugins.List()
}
