package server

import "context"

func (s *Server) runProviderAdminOperation(ctx context.Context, provider Provider, operation ProviderAdminOperation, fn func() error) error {
	key, ok := s.providerAdminOperationKey(provider, operation)
	if !ok {
		return fn()
	}
	return s.store.RunClusterOperation(ctx, key, func(context.Context) error {
		return fn()
	})
}

func (s *Server) runProviderResourceAdminOperation(ctx context.Context, provider Provider, resource ProviderResource, operation ProviderAdminOperation, fn func() error) error {
	key, ok := s.providerResourceAdminOperationKey(provider, resource, operation)
	if !ok {
		return fn()
	}
	return s.store.RunClusterOperation(ctx, key, func(context.Context) error {
		return fn()
	})
}

func (s *Server) providerAdminOperationKey(provider Provider, operation ProviderAdminOperation) (string, bool) {
	lifecycle, ok := resolveTypedAdapter[ProviderAdminLifecycle](s.adapterRegistry, provider.Type)
	if !ok {
		return "", false
	}
	return lifecycle.ProviderOperationKey(provider, operation)
}

func (s *Server) providerResourceAdminOperationKey(provider Provider, resource ProviderResource, operation ProviderAdminOperation) (string, bool) {
	lifecycle, ok := resolveTypedAdapter[ProviderAdminLifecycle](s.adapterRegistry, provider.Type)
	if !ok {
		return "", false
	}
	return lifecycle.ProviderResourceOperationKey(provider, resource, operation)
}
