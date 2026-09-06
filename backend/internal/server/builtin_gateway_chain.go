package server

import pluginmeta "tokenhub/backend/internal/plugin"

const tokenHubCoreGatewayChainPluginID = "tokenhub.chain.core"

func mustRegisterPlugin(registry *pluginmeta.Registry, descriptor pluginmeta.Descriptor) {
	if err := registry.Register(descriptor); err != nil {
		panic(err)
	}
}
