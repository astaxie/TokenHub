package server

import "strings"

const providerRouteRequiresResourceOption = "route_requires_resource"

func applyProviderPluginPolicy(provider *Provider, descriptor AdapterDescriptor) {
	if provider == nil {
		return
	}
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	if descriptor.ProviderPolicy.RouteRequiresResource {
		provider.Options[providerRouteRequiresResourceOption] = "true"
	} else {
		delete(provider.Options, providerRouteRequiresResourceOption)
	}
}

func providerRouteRequiresResource(provider Provider) bool {
	if value, ok := provider.Options[providerRouteRequiresResourceOption]; ok {
		return strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return provider.Type == ProviderOpenAICodex
}
