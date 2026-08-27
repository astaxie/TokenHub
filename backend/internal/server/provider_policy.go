package server

import (
	"log"
	"strings"
)

const (
	providerAuthModeOption              = "auth_mode"
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

func applyProviderDescriptorDefaults(provider *Provider, descriptor AdapterDescriptor) {
	if provider == nil || strings.TrimSpace(provider.BaseURL) != "" {
		return
	}
	if baseURL := providerDescriptorDefaultBaseURL(descriptor); baseURL != "" {
		provider.BaseURL = baseURL
	}
}

func providerDescriptorDefaultBaseURL(descriptor AdapterDescriptor) string {
	if baseURL := strings.TrimSpace(descriptor.ProviderPolicy.DefaultBaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	for _, resourceType := range descriptor.ResourceTypes {
		if !resourceType.Default {
			continue
		}
		if baseURL := strings.TrimSpace(resourceType.Defaults["base_url"]); baseURL != "" {
			return baseURL
		}
	}
	for _, resourceType := range descriptor.ResourceTypes {
		if baseURL := strings.TrimSpace(resourceType.Defaults["base_url"]); baseURL != "" {
			return baseURL
		}
	}
	return ""
}

type providerPluginPolicyReconciler interface {
	ReconcileProviderPluginPolicies(registry *AdapterRegistry) (int, error)
}

type providerResourceTypeDefaultsConfigurator interface {
	ConfigureProviderResourceTypeDefaults(defaults map[string]map[string]string)
}

func configureProviderResourceTypeDefaults(store Store, registry *AdapterRegistry) {
	configurator, ok := store.(providerResourceTypeDefaultsConfigurator)
	if !ok {
		return
	}
	configurator.ConfigureProviderResourceTypeDefaults(providerResourceTypeDefaultsFromRegistry(registry))
}

func providerResourceTypeDefaultsFromRegistry(registry *AdapterRegistry) map[string]map[string]string {
	if registry == nil {
		return nil
	}
	defaults := map[string]map[string]string{}
	for _, descriptor := range registry.List() {
		for _, resourceType := range descriptor.ResourceTypes {
			resourceTypeName := strings.ToLower(strings.TrimSpace(resourceType.Type))
			if resourceTypeName == "" || len(resourceType.Defaults) == 0 {
				continue
			}
			if _, exists := defaults[resourceTypeName]; exists && !resourceType.Default {
				continue
			}
			defaults[resourceTypeName] = cloneStringMap(resourceType.Defaults)
		}
	}
	return defaults
}

func reconcileProviderPluginPolicies(store Store, registry *AdapterRegistry) {
	reconciler, ok := store.(providerPluginPolicyReconciler)
	if !ok {
		return
	}
	updated, err := reconciler.ReconcileProviderPluginPolicies(registry)
	if err != nil {
		log.Printf("[tokenhub] failed to reconcile provider plugin policies: %v", err)
		return
	}
	if updated > 0 {
		log.Printf("[tokenhub] reconciled plugin policy options for %d providers", updated)
	}
}

func providerRouteRequiresResource(provider Provider) bool {
	if value, ok := provider.Options[providerRouteRequiresResourceOption]; ok {
		return strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return false
}

func providerUsesResourceCredentials(provider Provider) bool {
	if value, ok := provider.Options[providerCredentialsScopeOption]; ok {
		return strings.EqualFold(strings.TrimSpace(value), providerCredentialsScopeResource)
	}
	return false
}

func patchedProviderPolicy(current Provider, patch Provider) Provider {
	current.Type = firstNonEmpty(patch.Type, current.Type)
	if patch.Options != nil {
		current.Options = patch.Options
	}
	return current
}

func providerPolicyOptionsChanged(before map[string]string, after map[string]string) bool {
	for _, key := range []string{providerRouteRequiresResourceOption, providerCredentialsScopeOption} {
		if strings.TrimSpace(before[key]) != strings.TrimSpace(after[key]) {
			return true
		}
	}
	return false
}
