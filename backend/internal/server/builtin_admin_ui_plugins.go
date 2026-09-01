package server

import (
	"encoding/json"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerBuiltinAdminUIContributions(registry *pluginmeta.Registry, adminUI *pluginmeta.AdminUIRegistry) {
	registerBuiltinDefaultSIMPlugin(registry)

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

func registerBuiltinDefaultSIMPlugin(registry *pluginmeta.Registry) {
	descriptor := pluginmeta.NormalizeDescriptor(pluginmeta.Descriptor{
		ID:      "tokenhub.sim.default",
		Name:    "TokenHub Default Interface Template",
		Version: "built-in",
		Source:  pluginmeta.SourceBuiltIn,
		Status:  pluginmeta.StatusEnabled,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindSIM},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementPresentation,
		},
		Marketplace: &pluginmeta.MarketplaceMetadata{
			Summary:    "The built-in TokenHub admin console interface template.",
			Categories: []string{"interface-template", "official"},
			Localizations: map[string]pluginmeta.MarketplaceLocalization{
				"zh-CN": {
					Name:        "TokenHub 默认界面模板",
					Summary:     "TokenHub 管理端内置的默认界面模板。",
					Description: "提供默认主题 token、侧边栏布局和工作台编排，是第三方界面模板插件的基准实现。",
				},
				"en-US": {
					Name:        "TokenHub Default Interface Template",
					Summary:     "The built-in TokenHub admin console interface template.",
					Description: "Provides the default theme tokens, sidebar layout, and workspace composition as the baseline implementation for third-party interface template plugins.",
				},
				"ja-JP": {
					Name:        "TokenHub 既定インターフェイステンプレート",
					Summary:     "TokenHub 管理コンソール組み込みの既定インターフェイステンプレートです。",
					Description: "既定のテーマ Token、サイドバーレイアウト、ワークスペース構成を提供し、サードパーティ製インターフェイステンプレートプラグインの基準実装になります。",
				},
			},
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{
				Kind:    pluginmeta.CapabilityKindSIM,
				Name:    pluginmeta.SIMCapabilityThemeTokens,
				Subject: "default-light",
				Value: simCapabilityValue(map[string]any{
					"id":       "default-light",
					"title":    "TokenHub Default Light",
					"mode":     "light",
					"default":  true,
					"order":    10,
					"priority": 100,
					"localizations": map[string]any{
						"zh-CN": map[string]any{"title": "TokenHub 默认浅色"},
						"en-US": map[string]any{"title": "TokenHub Default Light"},
						"ja-JP": map[string]any{"title": "TokenHub 既定ライト"},
					},
					"tokens": defaultLightThemeTokens(),
				}),
			},
			{
				Kind:    pluginmeta.CapabilityKindSIM,
				Name:    pluginmeta.SIMCapabilityThemeTokens,
				Subject: "default-dark",
				Value: simCapabilityValue(map[string]any{
					"id":       "default-dark",
					"title":    "TokenHub Default Dark",
					"mode":     "dark",
					"default":  true,
					"order":    10,
					"priority": 100,
					"localizations": map[string]any{
						"zh-CN": map[string]any{"title": "TokenHub 默认深色"},
						"en-US": map[string]any{"title": "TokenHub Default Dark"},
						"ja-JP": map[string]any{"title": "TokenHub 既定ダーク"},
					},
					"tokens": defaultDarkThemeTokens(),
				}),
			},
			{
				Kind:    pluginmeta.CapabilityKindSIM,
				Name:    pluginmeta.SIMCapabilityShellLayout,
				Subject: "default-sidebar",
				Value: simCapabilityValue(map[string]any{
					"id":            "default-sidebar",
					"title":         "TokenHub Default Sidebar",
					"navigation":    "sidebar",
					"density":       "comfortable",
					"content_width": "fluid",
					"default":       true,
					"order":         10,
					"priority":      100,
					"localizations": map[string]any{
						"zh-CN": map[string]any{"title": "TokenHub 默认侧边栏"},
						"en-US": map[string]any{"title": "TokenHub Default Sidebar"},
						"ja-JP": map[string]any{"title": "TokenHub 既定サイドバー"},
					},
				}),
			},
			{
				Kind:    pluginmeta.CapabilityKindSIM,
				Name:    pluginmeta.SIMCapabilityDashboardComposition,
				Subject: "default-dashboard",
				Value: simCapabilityValue(map[string]any{
					"id":       "default-dashboard",
					"title":    "TokenHub Default Dashboard",
					"layout":   "grid",
					"default":  true,
					"order":    10,
					"priority": 100,
					"localizations": map[string]any{
						"zh-CN": map[string]any{"title": "TokenHub 默认工作台"},
						"en-US": map[string]any{"title": "TokenHub Default Dashboard"},
						"ja-JP": map[string]any{"title": "TokenHub 既定ワークスペース"},
					},
					"cards": []any{
						map[string]any{"plugin_id": "tokenhub.admin.plugin-ecosystem", "contribution_id": "overview", "region": "main", "size": "wide", "order": 100},
					},
				}),
			},
		},
	})
	mustRegisterPlugin(registry, descriptor)
}

func simCapabilityValue(payload map[string]any) string {
	value, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(value)
}

func defaultLightThemeTokens() map[string]string {
	return map[string]string{
		"bg":            "#f5f5f7",
		"surface":       "#ffffff",
		"surface-2":     "#fafafb",
		"surface-3":     "#f2f2f5",
		"border":        "#eaeaee",
		"border-2":      "#e0e0e5",
		"border-strong": "#d4d4dc",
		"ink":           "#16161a",
		"ink-2":         "#5c5c66",
		"ink-3":         "#9c9ca6",
		"ink-4":         "#c4c4cc",
		"accent":        "#3e7bf6",
		"accent-ink":    "#ffffff",
		"pos":           "#0fa478",
		"warn":          "#d98a23",
		"red":           "#dc2626",
		"chart-grid":    "#efeff2",
	}
}

func defaultDarkThemeTokens() map[string]string {
	return map[string]string{
		"bg":            "#0b0b0f",
		"surface":       "#141419",
		"surface-2":     "#18181e",
		"surface-3":     "#1e1e25",
		"border":        "#26262e",
		"border-2":      "#30303a",
		"border-strong": "#3c3c46",
		"ink":           "#f2f2f5",
		"ink-2":         "#a0a0ab",
		"ink-3":         "#6e6e79",
		"ink-4":         "#48484f",
		"accent":        "#5b92ff",
		"accent-ink":    "#0b0b0f",
		"pos":           "#2fd8a8",
		"warn":          "#e5a84b",
		"red":           "#dc2626",
		"chart-grid":    "#23232b",
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
