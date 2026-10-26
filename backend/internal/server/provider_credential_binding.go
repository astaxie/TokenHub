package server

import "context"

type providerCredentialBindingKey struct{}

// Bind credential loads to the adapter that will use them. The store verifies
// this binding against its current resource/provider rows before reading secrets,
// including when a queued request resumes after an administrator moves an account.
func providerCredentialLoaderForType(store Store, providerType string) func(context.Context, string, bool) (ProviderResourceCredentials, error) {
	return func(ctx context.Context, resourceID string, force bool) (ProviderResourceCredentials, error) {
		return store.RefreshProviderResourceCredentials(context.WithValue(ctx, providerCredentialBindingKey{}, providerType), resourceID, force)
	}
}

// The caller holds the store lock. Refresh releases it while contacting the
// upstream, so inspect the current provider again before consuming its secrets.
func (s *GormStore) validateProviderCredentialBinding(ctx context.Context, resource ProviderResource) error {
	expected, _ := ctx.Value(providerCredentialBindingKey{}).(string)
	if expected == "" {
		return nil
	}
	var provider Provider
	if err := s.db.WithContext(ctx).First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
		return notFound(err, "provider_not_found", "Provider not found")
	}
	if provider.Type != expected || !s.IsProviderAccountResourceType(provider.Type, resource.ResourceType) {
		return NewHTTPError(409, "provider_resource_changed", "Provider resource no longer belongs to the selected subscription adapter")
	}
	return nil
}
