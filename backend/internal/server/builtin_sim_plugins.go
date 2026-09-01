package server

import (
	"encoding/json"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerBuiltinDefaultSIMPlugin(registry *pluginmeta.Registry) {
	descriptor := builtinSIMDescriptor(builtinSIMDescriptorOptions{
		id:      "tokenhub.sim.default",
		name:    "TokenHub Default Interface Template",
		summary: "The built-in TokenHub admin console interface template.",
		categories: []string{
			"interface-template",
			"official",
		},
		localizations: map[string]pluginmeta.MarketplaceLocalization{
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
		capabilities: []pluginmeta.CapabilityDescriptor{
			simThemeCapability("default-light", map[string]any{
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
			simThemeCapability("default-dark", map[string]any{
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
			simLayoutCapability("default-sidebar", map[string]any{
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
			simDashboardCapability("default-dashboard", map[string]any{
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
	})
	mustRegisterPlugin(registry, descriptor)
}

func registerBuiltinAntDSIMPlugin(registry *pluginmeta.Registry) {
	descriptor := builtinSIMDescriptor(builtinSIMDescriptorOptions{
		id:      "tokenhub.sim.antd",
		name:    "Ant Design Style Interface Template",
		summary: "An Ant Design style TokenHub admin console interface template.",
		categories: []string{
			"interface-template",
			"official",
			"antd",
		},
		localizations: map[string]pluginmeta.MarketplaceLocalization{
			"zh-CN": {
				Name:        "Ant Design 风格界面模板",
				Summary:     "基于 Ant Design 视觉语言的 TokenHub 管理端界面模板。",
				Description: "提供 Ant Design 风格的主题 token、紧凑侧边栏布局和工作台编排，适合偏企业后台、表格和表单密集的管理场景。",
			},
			"en-US": {
				Name:        "Ant Design Style Interface Template",
				Summary:     "A TokenHub admin console interface template based on the Ant Design visual language.",
				Description: "Provides Ant Design style theme tokens, a compact sidebar layout, and workspace composition for enterprise admin scenarios with dense tables and forms.",
			},
			"ja-JP": {
				Name:        "Ant Design スタイルインターフェイステンプレート",
				Summary:     "Ant Design の視覚言語に基づく TokenHub 管理コンソール用インターフェイステンプレートです。",
				Description: "密度の高いテーブルやフォームを扱う企業管理画面向けに、Ant Design スタイルのテーマ Token、コンパクトなサイドバーレイアウト、ワークスペース構成を提供します。",
			},
		},
		capabilities: []pluginmeta.CapabilityDescriptor{
			simThemeCapability("antd-light", map[string]any{
				"id":       "antd-light",
				"title":    "Ant Design Light",
				"mode":     "light",
				"order":    20,
				"priority": 90,
				"localizations": map[string]any{
					"zh-CN": map[string]any{"title": "Ant Design 浅色"},
					"en-US": map[string]any{"title": "Ant Design Light"},
					"ja-JP": map[string]any{"title": "Ant Design ライト"},
				},
				"tokens": antDLightThemeTokens(),
			}),
			simThemeCapability("antd-dark", map[string]any{
				"id":       "antd-dark",
				"title":    "Ant Design Dark",
				"mode":     "dark",
				"order":    20,
				"priority": 90,
				"localizations": map[string]any{
					"zh-CN": map[string]any{"title": "Ant Design 深色"},
					"en-US": map[string]any{"title": "Ant Design Dark"},
					"ja-JP": map[string]any{"title": "Ant Design ダーク"},
				},
				"tokens": antDDarkThemeTokens(),
			}),
			simLayoutCapability("antd-compact-sidebar", map[string]any{
				"id":            "antd-compact-sidebar",
				"title":         "Ant Design Compact Sidebar",
				"navigation":    "sidebar",
				"density":       "compact",
				"content_width": "fluid",
				"order":         20,
				"priority":      90,
				"localizations": map[string]any{
					"zh-CN": map[string]any{"title": "Ant Design 紧凑侧边栏"},
					"en-US": map[string]any{"title": "Ant Design Compact Sidebar"},
					"ja-JP": map[string]any{"title": "Ant Design コンパクトサイドバー"},
				},
			}),
			simDashboardCapability("antd-dashboard", map[string]any{
				"id":       "antd-dashboard",
				"title":    "Ant Design Dashboard",
				"layout":   "compact_grid",
				"order":    20,
				"priority": 90,
				"localizations": map[string]any{
					"zh-CN": map[string]any{"title": "Ant Design 工作台"},
					"en-US": map[string]any{"title": "Ant Design Dashboard"},
					"ja-JP": map[string]any{"title": "Ant Design ワークスペース"},
				},
				"cards": []any{
					map[string]any{"plugin_id": "tokenhub.admin.plugin-ecosystem", "contribution_id": "overview", "region": "main", "size": "wide", "order": 100},
				},
			}),
		},
	})
	mustRegisterPlugin(registry, descriptor)
}

type builtinSIMDescriptorOptions struct {
	id            string
	name          string
	summary       string
	categories    []string
	localizations map[string]pluginmeta.MarketplaceLocalization
	capabilities  []pluginmeta.CapabilityDescriptor
}

func builtinSIMDescriptor(options builtinSIMDescriptorOptions) pluginmeta.Descriptor {
	return pluginmeta.NormalizeDescriptor(pluginmeta.Descriptor{
		ID:      options.id,
		Name:    options.name,
		Version: "built-in",
		Source:  pluginmeta.SourceBuiltIn,
		Status:  pluginmeta.StatusEnabled,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindSIM},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementPresentation,
		},
		Marketplace: &pluginmeta.MarketplaceMetadata{
			Summary:       options.summary,
			Categories:    options.categories,
			Localizations: options.localizations,
		},
		Capabilities: options.capabilities,
	})
}

func simThemeCapability(subject string, payload map[string]any) pluginmeta.CapabilityDescriptor {
	return simCapability(pluginmeta.SIMCapabilityThemeTokens, subject, payload)
}

func simLayoutCapability(subject string, payload map[string]any) pluginmeta.CapabilityDescriptor {
	return simCapability(pluginmeta.SIMCapabilityShellLayout, subject, payload)
}

func simDashboardCapability(subject string, payload map[string]any) pluginmeta.CapabilityDescriptor {
	return simCapability(pluginmeta.SIMCapabilityDashboardComposition, subject, payload)
}

func simCapability(name string, subject string, payload map[string]any) pluginmeta.CapabilityDescriptor {
	return pluginmeta.CapabilityDescriptor{
		Kind:    pluginmeta.CapabilityKindSIM,
		Name:    name,
		Subject: subject,
		Value:   simCapabilityValue(payload),
	}
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

func antDLightThemeTokens() map[string]string {
	return map[string]string{
		"bg":            "#f5f5f5",
		"surface":       "#ffffff",
		"surface-2":     "#fafafa",
		"surface-3":     "#f0f0f0",
		"border":        "#d9d9d9",
		"border-2":      "#bfbfbf",
		"border-strong": "#8c8c8c",
		"ink":           "#000000",
		"ink-2":         "#595959",
		"ink-3":         "#8c8c8c",
		"ink-4":         "#bfbfbf",
		"accent":        "#1677ff",
		"accent-ink":    "#ffffff",
		"pos":           "#52c41a",
		"warn":          "#faad14",
		"red":           "#ff4d4f",
		"chart-grid":    "#f0f0f0",
	}
}

func antDDarkThemeTokens() map[string]string {
	return map[string]string{
		"bg":            "#000000",
		"surface":       "#141414",
		"surface-2":     "#1f1f1f",
		"surface-3":     "#262626",
		"border":        "#303030",
		"border-2":      "#434343",
		"border-strong": "#595959",
		"ink":           "#ffffff",
		"ink-2":         "#d9d9d9",
		"ink-3":         "#8c8c8c",
		"ink-4":         "#595959",
		"accent":        "#1668dc",
		"accent-ink":    "#ffffff",
		"pos":           "#49aa19",
		"warn":          "#d89614",
		"red":           "#dc4446",
		"chart-grid":    "#303030",
	}
}
