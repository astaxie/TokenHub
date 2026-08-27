package server

import (
	"log"
	"strings"
)

const (
	providerAuthModeOption                             = "auth_mode"
	providerAPIKeyRequiredOption                       = "api_key_required"
	providerCredentialsScopeOption                     = "credentials_scope"
	providerCredentialsScopeProvider                   = "provider"
	providerCredentialsScopeResource                   = "resource"
	providerCredentialRefreshProfileOption             = "credential_refresh_profile"
	providerCredentialRefreshProfileOpenAIAccountOAuth = "openai_account_oauth"
	providerErrorProfileOption                         = "error_profile"
	providerRouteRequiresResourceOption                = "route_requires_resource"
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
	if errorProfile := strings.TrimSpace(descriptor.ProviderPolicy.ErrorProfile); errorProfile != "" {
		provider.Options[providerErrorProfileOption] = errorProfile
	} else {
		delete(provider.Options, providerErrorProfileOption)
	}
	if refreshProfile := strings.TrimSpace(descriptor.ProviderPolicy.CredentialRefreshProfile); refreshProfile != "" {
		provider.Options[providerCredentialRefreshProfileOption] = refreshProfile
	} else {
		delete(provider.Options, providerCredentialRefreshProfileOption)
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

type providerTypeDefaultsConfigurator interface {
	ConfigureProviderTypeDefaults(defaultBaseURLs map[string]string)
}

type providerResourceTypePolicyConfigurator interface {
	ConfigureProviderResourceTypePolicy(resourceTypes map[string][]string)
}

func configureProviderResourceTypeDefaults(store Store, registry *AdapterRegistry) {
	providerConfigurator, ok := store.(providerTypeDefaultsConfigurator)
	if ok {
		providerConfigurator.ConfigureProviderTypeDefaults(providerTypeDefaultBaseURLsFromRegistry(registry))
	}
	configurator, ok := store.(providerResourceTypeDefaultsConfigurator)
	if ok {
		configurator.ConfigureProviderResourceTypeDefaults(providerResourceTypeDefaultsFromRegistry(registry))
	}
	policyConfigurator, ok := store.(providerResourceTypePolicyConfigurator)
	if ok {
		policyConfigurator.ConfigureProviderResourceTypePolicy(providerResourceTypePolicyFromRegistry(registry))
	}
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

func providerTypeDefaultBaseURLsFromRegistry(registry *AdapterRegistry) map[string]string {
	if registry == nil {
		return nil
	}
	defaults := map[string]string{}
	for _, descriptor := range registry.List() {
		providerType := strings.ToLower(strings.TrimSpace(descriptor.Type))
		baseURL := strings.TrimRight(strings.TrimSpace(descriptor.ProviderPolicy.DefaultBaseURL), "/")
		if providerType == "" || baseURL == "" {
			continue
		}
		defaults[providerType] = baseURL
	}
	return defaults
}

func providerResourceTypePolicyFromRegistry(registry *AdapterRegistry) map[string][]string {
	if registry == nil {
		return nil
	}
	policy := map[string][]string{}
	for _, descriptor := range registry.List() {
		providerType := strings.ToLower(strings.TrimSpace(descriptor.Type))
		if providerType == "" {
			continue
		}
		resourceTypes := []string{}
		hasResourceTypeMetadata := false
		for _, resourceType := range descriptor.ResourceTypes {
			resourceTypeName := strings.ToLower(strings.TrimSpace(resourceType.Type))
			if resourceTypeName == "" {
				continue
			}
			hasResourceTypeMetadata = true
			if resourceTypeName == ProviderResourceAPIKey {
				continue
			}
			resourceTypes = append(resourceTypes, resourceTypeName)
		}
		if !hasResourceTypeMetadata {
			continue
		}
		policy[providerType] = sortedUniqueStrings(resourceTypes)
	}
	return policy
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

func providerConfiguredErrorProfile(provider Provider) string {
	if value, ok := provider.Options[providerErrorProfileOption]; ok {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

func providerCredentialRefreshProfile(provider Provider) string {
	if value, ok := provider.Options[providerCredentialRefreshProfileOption]; ok {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}

func patchedProviderPolicy(current Provider, patch Provider) Provider {
	current.Type = firstNonEmpty(patch.Type, current.Type)
	if patch.Options != nil {
		current.Options = patch.Options
	}
	return current
}

func providerPolicyOptionsChanged(before map[string]string, after map[string]string) bool {
	for _, key := range []string{providerRouteRequiresResourceOption, providerCredentialsScopeOption, providerErrorProfileOption, providerCredentialRefreshProfileOption} {
		if strings.TrimSpace(before[key]) != strings.TrimSpace(after[key]) {
			return true
		}
	}
	return false
}
