package server

import pluginmeta "tokenhub/backend/internal/plugin"

func registerBuiltinAdminUIContributions(registry *pluginmeta.Registry, adminUI *pluginmeta.AdminUIRegistry) {
	registerBuiltinDefaultSIMPlugin(registry)
	registerBuiltinAntDSIMPlugin(registry)
	registerBuiltinKnowledgeSidebarSIMPlugin(registry)

	ecosystemContributions := []pluginmeta.AdminUIContribution{
		{
			PluginID: "tokenhub.admin.plugin-ecosystem",
			ID:       "ecosystem-page",
			Slot:     pluginmeta.SlotNavigationSection,
			Title:    "Plugin Ecosystem",
			Schema: map[string]any{
				"description": "Inspect the plugin registry, Admin UI contributions, gateway hooks, and plugin actions.",
				"fields": []any{
					map[string]any{"name": "registered_plugins", "type": "metric", "label": "Registered plugins", "source": "plugins.length", "format": "number"},
					map[string]any{"name": "gateway_hooks", "type": "metric", "label": "Gateway hooks", "source": "pluginChain.hooks.length", "format": "number"},
					map[string]any{"name": "admin_ui_contributions", "type": "metric", "label": "UI contributions", "source": "pluginUI.length", "format": "number"},
					map[string]any{"name": "plugin_actions", "type": "metric", "label": "Plugin actions", "source": "pluginActions.length", "format": "number"},
				},
			},
		},
		{
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
		},
		{
			PluginID: "tokenhub.admin.plugin-ecosystem",
			ID:       "route-context",
			Slot:     pluginmeta.SlotRouteDetailPanel,
			Title:    "Route Plugin Context",
			Schema: map[string]any{
				"fields": []any{
					map[string]any{"name": "model", "type": "text", "label": "External model", "source": "route.model_name"},
					map[string]any{"name": "provider_model", "type": "text", "label": "Provider model", "source": "route.provider_model"},
					map[string]any{"name": "route_status", "type": "text", "label": "Route status", "source": "route.status"},
				},
			},
		},
		{
			PluginID: "tokenhub.admin.plugin-ecosystem",
			ID:       "runtime",
			Slot:     pluginmeta.SlotSettingsPanel,
			Title:    "Plugin Runtime",
			Schema: map[string]any{
				"fields": []any{
					map[string]any{"name": "schema_version", "type": "text", "label": "Admin UI schema", "value": "v1"},
					map[string]any{"name": "registered_plugins", "type": "metric", "label": "Registered plugins", "source": "plugins.length", "format": "number"},
					map[string]any{"name": "gateway_hooks", "type": "metric", "label": "Gateway hooks", "source": "pluginChain.hooks.length", "format": "number"},
				},
			},
		},
	}
	mustRegisterPlugin(registry, builtinAdminUIDescriptor(pluginmeta.Descriptor{
		ID:         "tokenhub.admin.plugin-ecosystem",
		Name:       "TokenHub Plugin Ecosystem Dashboard",
		Version:    "built-in",
		Source:     pluginmeta.SourceBuiltIn,
		Kinds:      []pluginmeta.Kind{pluginmeta.KindAdminUI},
		Placements: []pluginmeta.Placement{pluginmeta.PlacementPresentation},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "admin_ui", Name: "dashboard_card", Subject: "plugin_ecosystem"},
			{Kind: "admin_ui", Name: "nav_section", Subject: "plugin_ecosystem"},
			{Kind: "admin_ui", Name: "route_detail_panel", Subject: "plugin_ecosystem"},
			{Kind: "admin_ui", Name: "settings_panel", Subject: "plugin_ecosystem"},
		},
	}, ecosystemContributions))
	for _, contribution := range ecosystemContributions {
		mustRegisterAdminUIContribution(adminUI, contribution)
	}

	coreProviderContributions := []pluginmeta.AdminUIContribution{
		{
			PluginID: "tokenhub.admin.core-provider",
			ID:       "provider-advanced-settings",
			Slot:     pluginmeta.SlotProviderFormSection,
			Title:    "Provider advanced settings",
			Schema: map[string]any{
				"placement": "advanced",
				"fields": []any{
					map[string]any{
						"name":    "system_prompt_transform_policy",
						"type":    "select",
						"label":   "System prompt transform",
						"target":  "provider",
						"options": []string{"preserve", "strip"},
						"help":    "Provider plugins may declare a default policy; providers without one strip client attribution blocks by default.",
					},
				},
			},
		},
		{
			PluginID: "tokenhub.admin.core-provider",
			ID:       "resource-system-prompt-transform",
			Slot:     pluginmeta.SlotProviderResourcePanel,
			Title:    "Provider resource system prompt transform",
			Schema: map[string]any{
				"layout": "resource_system_prompt_transform",
			},
		},
	}
	mustRegisterPlugin(registry, builtinAdminUIDescriptor(pluginmeta.Descriptor{
		ID:         "tokenhub.admin.core-provider",
		Name:       "TokenHub Core Provider Settings",
		Version:    "built-in",
		Source:     pluginmeta.SourceBuiltIn,
		Kinds:      []pluginmeta.Kind{pluginmeta.KindAdminUI},
		Placements: []pluginmeta.Placement{pluginmeta.PlacementPresentation},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "admin_ui", Name: "provider_form", Subject: "core_provider_settings"},
			{Kind: "admin_ui", Name: "provider_resource_panel", Subject: "core_provider_settings"},
		},
	}, coreProviderContributions))
	for _, contribution := range coreProviderContributions {
		mustRegisterAdminUIContribution(adminUI, contribution)
	}

	codexContributions := []pluginmeta.AdminUIContribution{
		{
			PluginID:      "tokenhub.provider.openai-codex",
			ID:            "provider-setup",
			Slot:          pluginmeta.SlotProviderFormSection,
			Title:         "OpenAI Codex account setup",
			ProviderTypes: []string{ProviderOpenAICodex},
			Action:        "openai_codex.oauth.start",
			Schema: map[string]any{
				"fields": []any{
					map[string]any{"name": "oauth_start", "type": "oauth_button", "label": "Start OpenAI Codex OAuth", "action": "openai_codex.oauth.start"},
				},
			},
		},
		{
			PluginID:      "tokenhub.provider.openai-codex",
			ID:            "fingerprint",
			Slot:          pluginmeta.SlotProviderResourceFormSection,
			Title:         "OpenAI Codex fingerprint convergence",
			ProviderTypes: []string{ProviderOpenAICodex},
			ResourceTypes: []string{ProviderResourceOpenAISubscription},
			Schema: map[string]any{
				"fields": []any{
					map[string]any{
						"name":    "codex_fingerprint_mode",
						"type":    "select",
						"label":   "Codex fingerprint convergence",
						"options": []string{"off", "device", "session", "full"},
						"default": "session",
						"help":    "Rewrite client device and session identifiers to stable account-level values when sharing a subscription account.",
					},
				},
			},
		},
		{
			PluginID:      "tokenhub.provider.openai-codex",
			ID:            "image-capability",
			Slot:          pluginmeta.SlotProviderModelPanel,
			Title:         "OpenAI Codex image capability",
			ProviderTypes: []string{ProviderOpenAICodex},
			Action:        "openai_codex.image_capability.configure",
			Schema: map[string]any{
				"layout": "image_capability",
			},
		},
		{
			PluginID:      "tokenhub.provider.openai-codex",
			ID:            "quota",
			Slot:          pluginmeta.SlotProviderResourcePanel,
			Title:         "OpenAI Codex quota",
			ProviderTypes: []string{ProviderOpenAICodex},
			ResourceTypes: []string{ProviderResourceOpenAISubscription},
			Action:        "openai_codex.quota.read",
			Schema: map[string]any{
				"fields": []any{
					map[string]any{"name": "resource_status", "type": "text", "label": "Resource status", "source": "resource.status"},
					map[string]any{"name": "account_email", "type": "text", "label": "Account email", "source": "resource.credential_summary.account_email"},
					map[string]any{"name": "image_generation", "type": "text", "label": "Image generation", "source": "resource.options.image_generation_capability"},
					map[string]any{"name": "resource_count", "type": "metric", "label": "Provider resources", "source": "resources.length", "format": "number"},
				},
			},
		},
	}
	mustRegisterPlugin(registry, builtinAdminUIDescriptor(pluginmeta.Descriptor{
		ID:         "tokenhub.provider.openai-codex",
		Name:       "OpenAI Codex Subscription",
		Version:    "built-in",
		Source:     pluginmeta.SourceBuiltIn,
		Kinds:      []pluginmeta.Kind{pluginmeta.KindAdminUI},
		Placements: []pluginmeta.Placement{pluginmeta.PlacementPresentation},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "admin_ui", Name: "provider_form", Subject: ProviderOpenAICodex},
			{Kind: "admin_ui", Name: "provider_model_panel", Subject: ProviderOpenAICodex},
			{Kind: "admin_ui", Name: "provider_resource_form", Subject: ProviderOpenAICodex},
			{Kind: "admin_ui", Name: "provider_resource_panel", Subject: ProviderOpenAICodex},
		},
	}, codexContributions))
	for _, contribution := range codexContributions {
		mustRegisterAdminUIContribution(adminUI, contribution)
	}
}

func builtinAdminUIDescriptor(descriptor pluginmeta.Descriptor, contributions []pluginmeta.AdminUIContribution) pluginmeta.Descriptor {
	for _, contribution := range contributions {
		if contribution.ID == "" || contribution.Slot == "" {
			continue
		}
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "admin_ui",
			Name:    string(contribution.Slot),
			Subject: contribution.ID,
			Value:   contribution.Action,
		})
	}
	return pluginmeta.NormalizeDescriptor(descriptor)
}

func mustRegisterAdminUIContribution(registry *pluginmeta.AdminUIRegistry, contribution pluginmeta.AdminUIContribution) {
	if err := registry.Register(contribution); err != nil {
		panic(err)
	}
}
