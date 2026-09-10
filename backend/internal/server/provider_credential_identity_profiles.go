package server

import (
	"fmt"
	"strings"
)

type providerResourceCredentialIdentityResolver func(ProviderResourceCredentials) ProviderResourceCredentials

type providerResourceCredentialIdentityRegistration struct {
	ProviderType string
	Profile      string
	Resolve      providerResourceCredentialIdentityResolver
}

func (registration providerResourceCredentialIdentityRegistration) normalized(defaultProviderType string) providerResourceCredentialIdentityRegistration {
	registration.ProviderType = strings.ToLower(strings.TrimSpace(firstNonEmpty(registration.ProviderType, defaultProviderType)))
	registration.Profile = strings.ToLower(strings.TrimSpace(registration.Profile))
	return registration
}

func (registration providerResourceCredentialIdentityRegistration) key() string {
	return providerResourceScopedKey(registration.ProviderType, registration.Profile)
}

type providerCredentialIdentityProfileProvider interface {
	ProviderResourceCredentialIdentityProfiles() []providerResourceCredentialIdentityRegistration
}

type providerCredentialIdentityProfileConfigurator interface {
	ConfigureProviderCredentialIdentityProfileHandlers([]providerResourceCredentialIdentityRegistration)
}

func configureProviderCredentialIdentityProfileHandlers(store Store, registry *AdapterRegistry) error {
	configurator, ok := store.(providerCredentialIdentityProfileConfigurator)
	if !ok {
		return nil
	}
	registrations, err := registry.ProviderCredentialIdentityProfileRegistrations()
	if err != nil {
		return err
	}
	configurator.ConfigureProviderCredentialIdentityProfileHandlers(registrations)
	return nil
}

func (r *AdapterRegistry) ProviderCredentialIdentityProfileRegistrations() ([]providerResourceCredentialIdentityRegistration, error) {
	if r == nil {
		return nil, nil
	}
	registrations := []providerResourceCredentialIdentityRegistration{}
	seen := map[string]string{}
	for _, descriptor := range r.List() {
		adapter, err := r.Resolve(descriptor.Type)
		if err != nil {
			return nil, err
		}
		provider, ok := adapter.(providerCredentialIdentityProfileProvider)
		if !ok {
			continue
		}
		for _, registration := range provider.ProviderResourceCredentialIdentityProfiles() {
			registration = registration.normalized(descriptor.Type)
			if registration.Profile == "" || registration.Resolve == nil {
				continue
			}
			if registration.ProviderType != strings.ToLower(strings.TrimSpace(descriptor.Type)) {
				return nil, fmt.Errorf("provider credential identity profile %q is registered by provider %q for provider %q", registration.Profile, descriptor.Type, registration.ProviderType)
			}
			if !descriptorUsesCredentialIdentityProfile(descriptor, registration.Profile) {
				return nil, fmt.Errorf("provider credential identity profile %q for provider %q is not declared by its resource types", registration.Profile, descriptor.Type)
			}
			key := registration.key()
			if owner, exists := seen[key]; exists {
				return nil, fmt.Errorf("provider credential identity profile %q for provider %q is already registered by %s", registration.Profile, registration.ProviderType, owner)
			}
			seen[key] = descriptor.PluginID
			registrations = append(registrations, registration)
		}
	}
	return registrations, nil
}

func descriptorUsesCredentialIdentityProfile(descriptor AdapterDescriptor, profile string) bool {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		return false
	}
	for _, resourceType := range descriptor.ResourceTypes {
		if strings.ToLower(strings.TrimSpace(resourceType.CredentialIdentityProfile)) == profile {
			return true
		}
	}
	return false
}

func (s *GormStore) ConfigureProviderCredentialIdentityProfileHandlers(registrations []providerResourceCredentialIdentityRegistration) {
	if s == nil {
		return
	}
	normalized := make(map[string]providerResourceCredentialIdentityRegistration, len(registrations))
	for _, registration := range registrations {
		registration = registration.normalized("")
		if registration.ProviderType != "" && registration.Profile != "" && registration.Resolve != nil {
			normalized[registration.key()] = registration
		}
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.providerCredentialIdentities = normalized
}

func (s *GormStore) providerCredentialIdentityRegistration(providerType string, resourceType string) (providerResourceCredentialIdentityRegistration, bool) {
	if s == nil {
		return providerResourceCredentialIdentityRegistration{}, false
	}
	profile := s.providerResourceCredentialIdentityProfile(providerType, resourceType)
	if profile == "" {
		return providerResourceCredentialIdentityRegistration{}, false
	}
	registration, ok := s.providerCredentialIdentities[providerResourceScopedKey(providerType, profile)]
	return registration, ok
}
