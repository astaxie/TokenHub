package plugin

import "sort"

type Kind string

const (
	KindProvider  Kind = "provider"
	KindAdminUI   Kind = "admin_ui"
	KindSIM       Kind = "sim"
	KindExtension Kind = "extension"
)

type Placement string

const (
	PlacementPresentation     Placement = "presentation"
	PlacementGatewayChain     Placement = "gateway_chain"
	PlacementBackground       Placement = "background"
	PlacementManagementAction Placement = "management_action"
)

type Source string

const (
	SourceBuiltIn     Source = "built_in"
	SourceMarketplace Source = "marketplace"
	SourceLocalFile   Source = "local_file"
)

type CapabilityDescriptor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Subject string `json:"subject,omitempty"`
}

type Descriptor struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Source       Source                 `json:"source"`
	Kinds        []Kind                 `json:"kinds"`
	Placements   []Placement            `json:"placements"`
	Capabilities []CapabilityDescriptor `json:"capabilities"`
}

func BuiltInProvider(id string, name string, providerTypes []string, capabilities []string) Descriptor {
	return BuiltInProviderWithResourceTypes(id, name, providerTypes, nil, capabilities)
}

func BuiltInProviderWithResourceTypes(id string, name string, providerTypes []string, resourceTypes []string, capabilities []string) Descriptor {
	descriptors := make([]CapabilityDescriptor, 0, len(providerTypes)*len(capabilities))
	for _, providerType := range providerTypes {
		for _, resourceType := range resourceTypes {
			descriptors = append(descriptors, CapabilityDescriptor{
				Kind:    "provider_resource_type",
				Name:    resourceType,
				Subject: providerType,
			})
		}
		for _, capability := range capabilities {
			descriptors = append(descriptors, CapabilityDescriptor{
				Kind:    "provider",
				Name:    capability,
				Subject: providerType,
			})
		}
	}
	return Descriptor{
		ID:           id,
		Name:         name,
		Version:      "built-in",
		Source:       SourceBuiltIn,
		Kinds:        []Kind{KindProvider},
		Placements:   []Placement{PlacementGatewayChain, PlacementManagementAction},
		Capabilities: descriptors,
	}
}

func NormalizeDescriptor(descriptor Descriptor) Descriptor {
	descriptor.Kinds = normalizeKinds(descriptor.Kinds)
	descriptor.Placements = normalizePlacements(descriptor.Placements)
	descriptor.Capabilities = normalizeCapabilities(descriptor.Capabilities)
	return descriptor
}

func normalizeKinds(items []Kind) []Kind {
	seen := map[Kind]struct{}{}
	normalized := make([]Kind, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func normalizePlacements(items []Placement) []Placement {
	seen := map[Placement]struct{}{}
	normalized := make([]Placement, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func normalizeCapabilities(items []CapabilityDescriptor) []CapabilityDescriptor {
	seen := map[CapabilityDescriptor]struct{}{}
	normalized := make([]CapabilityDescriptor, 0, len(items))
	for _, item := range items {
		if item.Kind == "" || item.Name == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Kind != normalized[j].Kind {
			return normalized[i].Kind < normalized[j].Kind
		}
		if normalized[i].Subject != normalized[j].Subject {
			return normalized[i].Subject < normalized[j].Subject
		}
		return normalized[i].Name < normalized[j].Name
	})
	return normalized
}
