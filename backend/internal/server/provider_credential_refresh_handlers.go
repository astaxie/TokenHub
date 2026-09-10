package server

import (
	"fmt"
	"strings"
)

type providerCredentialRefreshHandlerProvider interface {
	ProviderResourceCredentialRefreshHandlers() []providerResourceCredentialRefreshRegistration
}

type providerCredentialRefreshHandlerConfigurator interface {
	ConfigureProviderCredentialRefreshHandlers([]providerResourceCredentialRefreshRegistration)
}

func configureProviderCredentialRefreshHandlers(store Store, registry *AdapterRegistry) error {
	configurator, ok := store.(providerCredentialRefreshHandlerConfigurator)
	if !ok {
		return nil
	}
	registrations, err := registry.ProviderCredentialRefreshRegistrations()
	if err != nil {
		return err
	}
	configurator.ConfigureProviderCredentialRefreshHandlers(registrations)
	return nil
}

func (r *AdapterRegistry) ProviderCredentialRefreshRegistrations() ([]providerResourceCredentialRefreshRegistration, error) {
	if r == nil {
		return nil, nil
	}
	registrations := []providerResourceCredentialRefreshRegistration{}
	seen := map[string]string{}
	for _, descriptor := range r.List() {
		adapter, err := r.Resolve(descriptor.Type)
		if err != nil {
			return nil, err
		}
		provider, ok := adapter.(providerCredentialRefreshHandlerProvider)
		if !ok {
			continue
		}
		for _, registration := range provider.ProviderResourceCredentialRefreshHandlers() {
			registration = registration.normalized(descriptor.Type)
			if registration.Profile == "" || registration.Refresh == nil {
				continue
			}
			if registration.ProviderType != strings.ToLower(strings.TrimSpace(descriptor.Type)) {
				return nil, fmt.Errorf("provider credential refresh handler %q is registered by provider %q for provider %q", registration.Profile, descriptor.Type, registration.ProviderType)
			}
			if descriptor.ProviderPolicy.CredentialRefreshProfile != "" && registration.Profile != descriptor.ProviderPolicy.CredentialRefreshProfile {
				return nil, fmt.Errorf("provider credential refresh handler %q for provider %q does not match descriptor profile %q", registration.Profile, descriptor.Type, descriptor.ProviderPolicy.CredentialRefreshProfile)
			}
			key := registration.key()
			if owner, exists := seen[key]; exists {
				return nil, fmt.Errorf("provider credential refresh profile %q for provider %q is already registered by %s", registration.Profile, registration.ProviderType, owner)
			}
			seen[key] = descriptor.PluginID
			registrations = append(registrations, registration)
		}
	}
	return registrations, nil
}
