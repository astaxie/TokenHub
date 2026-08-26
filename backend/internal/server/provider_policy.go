package server

import (
	"log"
	"strings"
)

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

type providerPluginPolicyReconciler interface {
	ReconcileProviderPluginPolicies(registry *AdapterRegistry) (int, error)
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

func providerPolicyOptionsChanged(before map[string]string, after map[string]string) bool {
	for _, key := range []string{providerRouteRequiresResourceOption, providerCredentialsScopeOption} {
		if strings.TrimSpace(before[key]) != strings.TrimSpace(after[key]) {
			return true
		}
	}
	return false
}
