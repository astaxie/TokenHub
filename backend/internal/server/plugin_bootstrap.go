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

func bootstrapServerPlugins(store Store, config Config, adapters map[string]ProviderAdapter, codexSubscription *CodexSubscriptionAdapter) (serverPluginBootstrap, error) {
	pluginRegistry := pluginmeta.NewRegistry()
	gatewayChain := pluginmeta.NewGatewayChainRegistry()
	gatewayHooks := pluginmeta.NewGatewayHookRunner(gatewayChain)
	adminUI := pluginmeta.NewAdminUIRegistry()
	pluginActions := pluginmeta.NewActionBroker()
	pluginBackgroundJobs := pluginmeta.NewBackgroundJobBroker()
	pluginBackgroundRunner := pluginmeta.NewBackgroundJobRunner(pluginBackgroundJobs)
	adapterRegistry := NewAdapterRegistryWithPlugins(pluginRegistry)

	registerBuiltinProviderAdapters(adapterRegistry, adapters, codexSubscription)
	registerBuiltinProviderCatalogPlugins(pluginRegistry)
	if codexSubscription != nil {
		codexSubscription.SupportsResourceModels = func(providerType string, resourceType string) bool {
			descriptor, ok := adapterRegistry.Describe(providerType)
			return ok && adapterSupportsResourceType(descriptor, resourceType)
		}
	}
	registerBuiltinGatewayChainPlugins(pluginRegistry, gatewayChain, gatewayHooks)
	registerBuiltinAdminUIContributions(pluginRegistry, adminUI)
	packages, err := pluginmeta.NewRuntime(config.PluginDir).LoadIntoWithActionsAndBackground(pluginRegistry, gatewayChain, adminUI, pluginActions, pluginBackgroundJobs, gatewayHooks)
	if err != nil {
		return serverPluginBootstrap{}, fmt.Errorf("load TokenHub plugins: %w", err)
	}
	registerExternalProviderPluginAdapters(adapterRegistry, packages)
	configureProviderResourceTypeDefaults(store, adapterRegistry)
	reconcileProviderPluginPolicies(store, adapterRegistry)

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
	if s.codexSubscription != nil {
		s.codexSubscription.ImageCapabilityProfiles = func(providerType string) []providerImageCapabilityRouteProfile {
			profiles := []providerImageCapabilityRouteProfile{}
			for _, profile := range providerImageCapabilityRouteProfilesFromActions(s.pluginActions.List()) {
				if profile.ProviderType == strings.TrimSpace(providerType) {
					profiles = append(profiles, profile)
				}
			}
			return profiles
		}
	}
	s.syncProviderImageCapabilityRouteProfiles()
	if s.credentialRefresh != nil {
		s.credentialRefresh.pluginRefresh = s.refreshProviderResourceCredentialsWithPluginAction
		s.credentialRefresh.pluginJob = s.providerCredentialRefreshBackgroundJobRegistered
	}
}
