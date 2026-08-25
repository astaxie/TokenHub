package server

import (
	"sort"
	"strings"
)

func routeProviderProtocolsFromCapabilities(descriptor AdapterDescriptor) map[string]bool {
	protocols := map[string]bool{}
	if adapterSupports(descriptor, AdapterCapabilityChat) {
		protocols["chat/completions"] = true
	}
	if adapterSupports(descriptor, AdapterCapabilityResponses) {
		protocols["responses"] = true
	}
	if adapterSupports(descriptor, AdapterCapabilityEmbeddings) {
		protocols["embeddings"] = true
	}
	return protocols
}

func adapterDescriptorRouteProtocolSet(descriptor AdapterDescriptor) map[string]bool {
	if len(descriptor.ProviderPolicy.RouteProtocols) > 0 {
		return routeProtocolSet(descriptor.ProviderPolicy.RouteProtocols)
	}
	return routeProviderProtocolsFromCapabilities(descriptor)
}

func routeProtocolSet(protocols []string) map[string]bool {
	set := map[string]bool{}
	for _, protocol := range protocols {
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if protocol != "" {
			set[protocol] = true
		}
	}
	return set
}

func routeProtocolList(protocols []string) []string {
	set := map[string]bool{}
	for _, protocol := range protocols {
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if protocol != "" {
			set[protocol] = true
		}
	}
	result := make([]string, 0, len(set))
	for protocol := range set {
		result = append(result, protocol)
	}
	sort.Strings(result)
	return result
}

func routeProtocolSetList(protocols map[string]bool) []string {
	result := make([]string, 0, len(protocols))
	for protocol, supported := range protocols {
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if supported && protocol != "" {
			result = append(result, protocol)
		}
	}
	sort.Strings(result)
	return result
}
