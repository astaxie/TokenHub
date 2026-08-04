package server

import (
	"fmt"
	"sort"
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
	Type         string              `json:"type"`
	Capabilities []AdapterCapability `json:"capabilities"`
}

// AdapterRegistry is the single source of truth for which adapter serves a
// provider type and which capabilities that adapter advertises. An adapter may
// be present without a descriptor: capabilities are only declared through
// Register, so Describe reporting "unknown" means "capabilities undeclared",
// not "adapter missing".
type AdapterRegistry struct {
	adapters    map[string]any
	descriptors map[string]AdapterDescriptor
}

func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		adapters:    map[string]any{},
		descriptors: map[string]AdapterDescriptor{},
	}
}

func (r *AdapterRegistry) Register(adapterType string, adapter any, capabilities ...AdapterCapability) {
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
	r.descriptors[adapterType] = AdapterDescriptor{Type: adapterType, Capabilities: normalized}
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
		items = append(items, descriptor)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Type < items[j].Type })
	return items
}
