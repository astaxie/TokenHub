package server

import (
	"context"
	"fmt"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type serverPluginBootstrap struct {
	pluginRegistry         *pluginmeta.Registry
	gatewayChain           *pluginmeta.GatewayChainRegistry
	gatewayHooks           *pluginmeta.GatewayHookRunner
	adminUI                *pluginmeta.AdminUIRegistry
	pluginActions          *pluginmeta.ActionBroker
	pluginBackgroundJobs   *pluginmeta.BackgroundJobBroker
	pluginBackgroundRunner *pluginmeta.BackgroundJobRunner
	adapterRegistry        *AdapterRegistry
}

func bootstrapServerPlugins(config Config, adapters map[string]any) (serverPluginBootstrap, error) {
	pluginRegistry := pluginmeta.NewRegistry()
	gatewayChain := pluginmeta.NewGatewayChainRegistry()
	gatewayHooks := pluginmeta.NewGatewayHookRunner(gatewayChain)
	adminUI := pluginmeta.NewAdminUIRegistry()
	pluginActions := pluginmeta.NewActionBroker()
	pluginBackgroundJobs := pluginmeta.NewBackgroundJobBroker()
	pluginBackgroundRunner := pluginmeta.NewBackgroundJobRunner(pluginBackgroundJobs)
	adapterRegistry := NewAdapterRegistryWithPlugins(pluginRegistry)
	pluginRuntime := pluginmeta.NewRuntime(config.PluginDir)

	if err := registerBuiltinProviderAdapters(adapterRegistry, adapters, pluginRuntime); err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("register built-in provider plugins: %w", err)
	}
	registerBuiltinProviderCatalogPlugins(pluginRegistry)
	if err := registerBuiltinProviderCatalogFilePlugins(pluginRegistry, adapterRegistry, config.ProviderCatalogFile, pluginRuntime); err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("register provider catalog plugins: %w", err)
	}
	registerBuiltinAdminUIContributions(pluginRegistry, adminUI)
	packages, err := pluginRuntime.LoadIntoWithActionsAndBackground(pluginRegistry, gatewayChain, adminUI, pluginActions, pluginBackgroundJobs, gatewayHooks)
	if err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("load TokenHub plugins: %w", err)
	}
	registerExternalProviderPluginAdapters(adapterRegistry, packages)
	if _, err := adapterRegistry.ProviderCredentialIdentityProfileRegistrations(); err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("configure provider credential identity profiles: %w", err)
	}
	if _, err := adapterRegistry.ProviderCredentialRefreshRegistrations(); err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("configure provider credential refresh handlers: %w", err)
	}

	return serverPluginBootstrap{
		pluginRegistry:         pluginRegistry,
		gatewayChain:           gatewayChain,
		gatewayHooks:           gatewayHooks,
		adminUI:                adminUI,
		pluginActions:          pluginActions,
		pluginBackgroundJobs:   pluginBackgroundJobs,
		pluginBackgroundRunner: pluginBackgroundRunner,
		adapterRegistry:        adapterRegistry,
	}, nil
}

func (s *Server) installServerPluginHandlers(bootstrap *serverPluginBootstrap) {
	if s == nil || bootstrap == nil {
		return
	}
	registerBuiltinPluginActions(s, bootstrap.pluginActions)
	registerBuiltinPluginBackgroundJobs(s, bootstrap.pluginBackgroundJobs)
	if s.credentialRefresh != nil && s.credentialRefresh.pluginRefresh == nil {
		s.credentialRefresh.pluginRefresh = func(ctx context.Context, resource ProviderResource) (bool, error) {
			s.pluginRuntimeMu.RLock()
			defer s.pluginRuntimeMu.RUnlock()
			return s.refreshProviderResourceCredentialsWithPluginAction(ctx, resource)
		}
		s.credentialRefresh.pluginJob = func(providerType string) bool {
			s.pluginRuntimeMu.RLock()
			defer s.pluginRuntimeMu.RUnlock()
			return s.providerCredentialRefreshBackgroundJobRegistered(providerType)
		}
	}
}

func (s *Server) publishServerPluginStoreConfiguration(bootstrap *serverPluginBootstrap) error {
	if s == nil || bootstrap == nil {
		return nil
	}
	configureProviderResourceModelSupport(bootstrap.adapterRegistry.adapters, bootstrap.adapterRegistry)
	configureProviderImageCapabilityProfiles(bootstrap.adapterRegistry.adapters, func(providerType string) []providerImageCapabilityRouteProfile {
		profiles := []providerImageCapabilityRouteProfile{}
		for _, profile := range providerImageCapabilityRouteProfilesFromActions(bootstrap.pluginActions.List()) {
			if profile.ProviderType == strings.TrimSpace(providerType) {
				profiles = append(profiles, profile)
			}
		}
		return profiles
	})
	configureProviderResourceTypeDefaults(s.store, bootstrap.adapterRegistry)
	if err := configureProviderCredentialIdentityProfileHandlers(s.store, bootstrap.adapterRegistry); err != nil {
		return err
	}
	if err := configureProviderCredentialRefreshHandlers(s.store, bootstrap.adapterRegistry); err != nil {
		return err
	}
	reconcileProviderPluginPolicies(s.store, bootstrap.adapterRegistry)
	if store, ok := s.store.(providerImageCapabilityProfileStore); ok {
		store.setProviderImageCapabilityRouteProfiles(providerImageCapabilityRouteProfilesFromActions(bootstrap.pluginActions.List()))
	}
	return nil
}
