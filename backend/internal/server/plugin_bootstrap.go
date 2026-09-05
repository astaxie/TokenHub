package server

import (
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

func bootstrapServerPlugins(store Store, config Config, adapters map[string]any) (serverPluginBootstrap, error) {
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
	registerBuiltinGatewayChainPlugins(pluginRegistry, gatewayChain, gatewayHooks)
	registerBuiltinAdminUIContributions(pluginRegistry, adminUI)
	packages, err := pluginRuntime.LoadIntoWithActionsAndBackground(pluginRegistry, gatewayChain, adminUI, pluginActions, pluginBackgroundJobs, gatewayHooks)
	if err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("load TokenHub plugins: %w", err)
	}
	registerExternalProviderPluginAdapters(adapterRegistry, packages)
	configureProviderResourceModelSupport(adapterRegistry.adapters, adapterRegistry)
	configureProviderResourceTypeDefaults(store, adapterRegistry)
	if err := configureProviderCredentialIdentityProfileHandlers(store, adapterRegistry); err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("configure provider credential identity profiles: %w", err)
	}
	if err := configureProviderCredentialRefreshHandlers(store, adapterRegistry); err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("configure provider credential refresh handlers: %w", err)
	}
	reconcileProviderPluginPolicies(store, adapterRegistry)
	if err := pluginRuntime.CompleteRuntimeRestart(); err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("complete TokenHub plugin runtime restart: %w", err)
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

func (s *Server) installServerPluginHandlers() {
	if s == nil {
		return
	}
	registerBuiltinPluginActions(s)
	registerBuiltinPluginBackgroundJobs(s)
	if s.adapterRegistry != nil {
		configureProviderImageCapabilityProfiles(s.adapterRegistry.adapters, func(providerType string) []providerImageCapabilityRouteProfile {
			profiles := []providerImageCapabilityRouteProfile{}
			for _, profile := range providerImageCapabilityRouteProfilesFromActions(s.pluginActions.List()) {
				if profile.ProviderType == strings.TrimSpace(providerType) {
					profiles = append(profiles, profile)
				}
			}
			return profiles
		})
	}
	s.syncProviderImageCapabilityRouteProfiles()
	if s.credentialRefresh != nil {
		s.credentialRefresh.pluginRefresh = s.refreshProviderResourceCredentialsWithPluginAction
		s.credentialRefresh.pluginJob = s.providerCredentialRefreshBackgroundJobRegistered
	}
}
