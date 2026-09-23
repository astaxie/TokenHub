package server

import (
	"net/http"
	"time"
)

type builtinProviderRuntimeDependencies struct {
	Store               Store
	Client              *http.Client
	StreamClient        *http.Client
	StreamIdleTimeout   time.Duration
	SyntheticDNSPolicy  *providerSyntheticDNSPolicy
	ProviderProxyPolicy *providerProxyPolicy
}

type builtinProviderRuntime struct {
	adapters map[string]any
}

type providerResourceModelSupportConfigurer interface {
	ConfigureProviderResourceModelSupport(func(providerType string, resourceType string) bool)
}

type providerImageCapabilityProfileConfigurer interface {
	ConfigureProviderImageCapabilityProfiles(func(providerType string) []providerImageCapabilityRouteProfile)
}

func configureProviderResourceModelSupport(adapters map[string]any, registry *AdapterRegistry) {
	for _, adapter := range adapters {
		configurer, ok := adapter.(providerResourceModelSupportConfigurer)
		if !ok {
			continue
		}
		configurer.ConfigureProviderResourceModelSupport(func(providerType string, resourceType string) bool {
			descriptor, ok := registry.Describe(providerType)
			return ok && adapterSupportsResourceType(descriptor, resourceType)
		})
	}
}

func configureProviderImageCapabilityProfiles(adapters map[string]any, profiles func(providerType string) []providerImageCapabilityRouteProfile) {
	for _, adapter := range adapters {
		configurer, ok := adapter.(providerImageCapabilityProfileConfigurer)
		if !ok {
			continue
		}
		configurer.ConfigureProviderImageCapabilityProfiles(profiles)
	}
}
