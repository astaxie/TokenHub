package server

import "strings"

const (
	providerCredentialsScopeOption      = "credentials_scope"
	providerCredentialsScopeProvider    = "provider"
	providerCredentialsScopeResource    = "resource"
	providerRouteRequiresResourceOption = "route_requires_resource"
)

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
	switch descriptor.ProviderPolicy.CredentialsScope {
	case providerCredentialsScopeResource:
		provider.Options[providerCredentialsScopeOption] = providerCredentialsScopeResource
	case providerCredentialsScopeProvider:
		delete(provider.Options, providerCredentialsScopeOption)
	}
}

func providerRouteRequiresResource(provider Provider) bool {
	if value, ok := provider.Options[providerRouteRequiresResourceOption]; ok {
		return strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return provider.Type == ProviderOpenAICodex
}

func providerUsesResourceCredentials(provider Provider) bool {
	if value, ok := provider.Options[providerCredentialsScopeOption]; ok {
		return strings.EqualFold(strings.TrimSpace(value), providerCredentialsScopeResource)
	}
	return provider.Type == ProviderOpenAICodex
}

func patchedProviderPolicy(current Provider, patch Provider) Provider {
	current.Type = firstNonEmpty(patch.Type, current.Type)
	if patch.Options != nil {
		current.Options = patch.Options
	}
	return current
}
