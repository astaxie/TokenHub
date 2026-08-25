package server

import pluginmeta "tokenhub/backend/internal/plugin"

func registerBuiltinAdminUIContributions(registry *pluginmeta.Registry, adminUI *pluginmeta.AdminUIRegistry) {
	mustRegisterPlugin(registry, pluginmeta.Descriptor{
		ID:         "tokenhub.admin.plugin-ecosystem",
		Name:       "TokenHub Plugin Ecosystem Dashboard",
		Version:    "built-in",
		Source:     pluginmeta.SourceBuiltIn,
		Kinds:      []pluginmeta.Kind{pluginmeta.KindAdminUI},
		Placements: []pluginmeta.Placement{pluginmeta.PlacementPresentation},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "admin_ui", Name: "dashboard_card", Subject: "plugin_ecosystem"},
		},
	})
	mustRegisterAdminUIContribution(adminUI, pluginmeta.AdminUIContribution{
		PluginID: "tokenhub.admin.plugin-ecosystem",
		ID:       "overview",
		Slot:     pluginmeta.SlotDashboardCard,
		Title:    "Plugin Ecosystem",
		Schema: map[string]any{
			"fields": []any{
				map[string]any{"name": "registered_plugins", "type": "metric", "label": "Registered plugins", "source": "plugins.length", "format": "number"},
				map[string]any{"name": "gateway_hooks", "type": "metric", "label": "Gateway hooks", "source": "pluginChain.hooks.length", "format": "number"},
				map[string]any{"name": "admin_ui_contributions", "type": "metric", "label": "UI contributions", "source": "pluginUI.length", "format": "number"},
				map[string]any{"name": "plugin_actions", "type": "metric", "label": "Plugin actions", "source": "pluginActions.length", "format": "number"},
			},
		},
	})

	mustRegisterPlugin(registry, pluginmeta.Descriptor{
		ID:         "tokenhub.provider.openai-codex",
		Name:       "OpenAI Codex Subscription",
		Version:    "built-in",
		Source:     pluginmeta.SourceBuiltIn,
		Kinds:      []pluginmeta.Kind{pluginmeta.KindAdminUI},
		Placements: []pluginmeta.Placement{pluginmeta.PlacementPresentation},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "admin_ui", Name: "provider_form", Subject: ProviderOpenAICodex},
			{Kind: "admin_ui", Name: "provider_resource_panel", Subject: ProviderOpenAICodex},
		},
	})
	for _, contribution := range []pluginmeta.AdminUIContribution{
		{
			PluginID:      "tokenhub.provider.openai-codex",
			ID:            "provider-setup",
			Slot:          pluginmeta.SlotProviderFormSection,
			Title:         "OpenAI Codex account setup",
			ProviderTypes: []string{ProviderOpenAICodex},
			Action:        "openai_codex.oauth.start",
		},
		{
			PluginID:      "tokenhub.provider.openai-codex",
			ID:            "quota",
			Slot:          pluginmeta.SlotProviderResourcePanel,
			Title:         "OpenAI Codex quota",
			ProviderTypes: []string{ProviderOpenAICodex},
			Action:        "openai_codex.quota.read",
		},
	} {
		mustRegisterAdminUIContribution(adminUI, contribution)
	}
}

func mustRegisterAdminUIContribution(registry *pluginmeta.AdminUIRegistry, contribution pluginmeta.AdminUIContribution) {
	if err := registry.Register(contribution); err != nil {
		panic(err)
	}
}
