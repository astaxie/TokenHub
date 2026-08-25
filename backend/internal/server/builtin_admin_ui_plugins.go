package server

import pluginmeta "tokenhub/backend/internal/plugin"

func registerBuiltinAdminUIContributions(registry *pluginmeta.Registry, adminUI *pluginmeta.AdminUIRegistry) {
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
