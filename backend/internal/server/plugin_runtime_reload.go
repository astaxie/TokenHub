package server

import (
	"context"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) reloadPluginRuntime(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.store.RunClusterOperation(ctx, "plugin-runtime-reload", func(context.Context) error {
		bootstrap, err := bootstrapServerPlugins(s.config, s.builtinProviderAdapters)
		if err != nil {
			return err
		}
		s.installServerPluginHandlers(&bootstrap)

		s.pluginRuntimeMu.Lock()
		defer s.pluginRuntimeMu.Unlock()
		if err := pluginmeta.NewRuntime(s.config.PluginDir).CompleteRuntimeRestart(); err != nil {
			return err
		}
		if err := s.publishServerPluginStoreConfiguration(&bootstrap); err != nil {
			return err
		}
		s.pluginRegistry = bootstrap.pluginRegistry
		s.gatewayChain = bootstrap.gatewayChain
		s.gatewayHooks = bootstrap.gatewayHooks
		s.adminUI = bootstrap.adminUI
		s.pluginActions = bootstrap.pluginActions
		s.pluginBackgroundJobs = bootstrap.pluginBackgroundJobs
		if s.pluginBackgroundRunner != nil {
			s.pluginBackgroundRunner.SetBroker(bootstrap.pluginBackgroundJobs)
		} else {
			s.pluginBackgroundRunner = bootstrap.pluginBackgroundRunner
		}
		s.adapterRegistry = bootstrap.adapterRegistry
		s.integrations = NewIntegrationService(s.store, bootstrap.adapterRegistry, s.upstreamClient)
		if s.providerCatalog != nil {
			s.providerCatalog.UsePluginCatalogTypes(bootstrap.adapterRegistry)
		}
		return nil
	})
}
