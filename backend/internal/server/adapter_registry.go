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
)

type AdapterDescriptor struct {
	Type         string              `json:"type"`
	Capabilities []AdapterCapability `json:"capabilities"`
}

type AdapterRegistry struct {
	adapters    map[string]any
	legacy      map[string]ProviderAdapter
	descriptors map[string]AdapterDescriptor
}

func NewAdapterRegistry(adapters map[string]ProviderAdapter) *AdapterRegistry {
	if adapters == nil {
		adapters = map[string]ProviderAdapter{}
	}
	return &AdapterRegistry{
		adapters:    map[string]any{},
		legacy:      adapters,
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
		adapter, ok = r.legacy[adapterType]
	}
	if !ok {
		return nil, NewHTTPError(503, "provider_adapter_missing", "Provider adapter is not registered")
	}
	return adapter, nil
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
