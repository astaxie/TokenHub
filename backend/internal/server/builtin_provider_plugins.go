package server

import (
	"encoding/json"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type builtinProviderAdapter struct {
	providerType                 string
	adapter                      any
	credentialsScope             string
	routeRequiresResource        bool
	sessionAffinityKind          string
	claudeCodeAttributionDefault string
	authModes                    []string
	resourceTypes                []pluginmeta.ManifestProviderResourceType
	catalogEntry                 *pluginProviderCatalogEntry
	capabilities                 []AdapterCapability
}

func registerBuiltinProviderAdapters(registry *AdapterRegistry, adapters map[string]ProviderAdapter, codexSubscription *CodexSubscriptionAdapter) {
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.mock", "Mock Provider", builtinProviderAdapter{
		providerType: ProviderMock,
		adapter:      adapters[ProviderMock],
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityResponses,
			AdapterCapabilityEmbeddings,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.openai", "OpenAI", builtinProviderAdapter{
		providerType: ProviderOpenAI,
		adapter:      adapters[ProviderOpenAI],
		catalogEntry: builtinProviderPluginCatalogEntry(
			"openai",
			"OpenAI",
			ProviderOpenAI,
			"https://api.openai.com/v1",
			"https://platform.openai.com/docs/models",
			[]string{"openai"},
		),
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityResponses,
			AdapterCapabilityResponseStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityProbe,
			AdapterCapabilityImageGenerate,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.openai-compatible", "OpenAI-Compatible", builtinProviderAdapter{
		providerType: ProviderOpenAICompatible,
		adapter:      adapters[ProviderOpenAICompatible],
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityResponses,
			AdapterCapabilityResponseStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityProbe,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.kronk", "Kronk", builtinProviderAdapter{
		providerType: ProviderKronk,
		adapter:      adapters[ProviderKronk],
		catalogEntry: builtinProviderPluginCatalogEntry(
			ProviderKronk,
			"Kronk",
			ProviderKronk,
			kronkDefaultBaseURL,
			kronkDocURL,
			[]string{"custom"},
		),
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityResponses,
			AdapterCapabilityResponseStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityModels,
			AdapterCapabilityProbe,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.openai-codex", "OpenAI Codex Subscription", builtinProviderAdapter{
		providerType:          ProviderOpenAICodex,
		adapter:               codexSubscription,
		credentialsScope:      providerCredentialsScopeResource,
		routeRequiresResource: true,
		sessionAffinityKind:   AffinityKindCodexSession,
		catalogEntry: &pluginProviderCatalogEntry{
			ID:          codexProviderCatalogID,
			Name:        "OpenAI Codex",
			DisplayName: "OpenAI Codex",
			Type:        ProviderOpenAICodex,
			BaseURL:     openAICodexBaseURL,
			DocURL:      "https://developers.openai.com/codex",
			Categories:  []string{"codex"},
			Source:      "openai-codex-live",
		},
		resourceTypes: []pluginmeta.ManifestProviderResourceType{{
			Type:        ProviderResourceOpenAISubscription,
			DisplayName: "OpenAI Codex Subscription",
			AuthModes:   []string{"oauth", "personal_access_token"},
			Default:     true,
			Defaults: map[string]string{
				"auth_type":       "oauth",
				"base_url":        openAICodexBaseURL,
				"max_concurrency": "3",
			},
		}},
		capabilities: []AdapterCapability{
			AdapterCapabilityResponses,
			AdapterCapabilityResponseStream,
			AdapterCapabilityModels,
			AdapterCapabilityProbe,
			AdapterCapabilityQuota,
			AdapterCapabilityOAuth,
			AdapterCapabilityAffinity,
			AdapterCapabilityCompact,
			AdapterCapabilityImageGenerate,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.azure-openai", "Azure OpenAI", builtinProviderAdapter{
		providerType: ProviderAzureOpenAI,
		adapter:      adapters[ProviderAzureOpenAI],
		catalogEntry: builtinProviderPluginCatalogEntry(
			"azure-openai",
			"Azure OpenAI",
			ProviderAzureOpenAI,
			"",
			"https://learn.microsoft.com/azure/ai-services/openai/",
			[]string{"microsoft", "openai"},
		),
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityProbe,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.anthropic", "Anthropic", builtinProviderAdapter{
		providerType:                 ProviderAnthropic,
		adapter:                      adapters[ProviderAnthropic],
		authModes:                    []string{anthropicAuthTypeAPIKey, anthropicAuthTypeBearer},
		claudeCodeAttributionDefault: claudeCodeAttributionPreserve,
		catalogEntry: builtinProviderPluginCatalogEntry(
			"anthropic",
			"Anthropic",
			ProviderAnthropic,
			"https://api.anthropic.com",
			"https://docs.anthropic.com",
			[]string{"claude"},
		),
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityProbe,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.gemini", "Gemini", builtinProviderAdapter{
		providerType: ProviderGemini,
		adapter:      adapters[ProviderGemini],
		catalogEntry: builtinProviderPluginCatalogEntry(
			"google",
			"Google Gemini",
			ProviderGemini,
			"https://generativelanguage.googleapis.com/v1beta",
			"https://ai.google.dev/gemini-api/docs",
			[]string{"gemini"},
		),
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityProbe,
		},
	})
	for _, adapterType := range []string{"deepseek", "qwen", "local"} {
		registerBuiltinProviderPlugin(registry, "tokenhub.provider."+adapterType, adapterType, builtinProviderAdapter{
			providerType: adapterType,
			adapter:      adapters[adapterType],
			catalogEntry: builtinProviderPluginCatalogEntryForType(adapterType),
			capabilities: []AdapterCapability{
				AdapterCapabilityChat,
				AdapterCapabilityChatStream,
				AdapterCapabilityResponses,
				AdapterCapabilityResponseStream,
				AdapterCapabilityEmbeddings,
				AdapterCapabilityProbe,
			},
		})
	}
}

func builtinProviderPluginCatalogEntryForType(providerType string) *pluginProviderCatalogEntry {
	switch providerType {
	case "deepseek":
		return builtinProviderPluginCatalogEntry(
			"deepseek",
			"DeepSeek",
			"deepseek",
			"https://api.deepseek.com",
			"https://api-docs.deepseek.com",
			[]string{"deepseek"},
		)
	case "qwen":
		return builtinProviderPluginCatalogEntry(
			"qwen",
			"Qwen",
			"qwen",
			"https://dashscope.aliyuncs.com/compatible-mode/v1",
			"https://help.aliyun.com/zh/model-studio",
			[]string{"qwen"},
		)
	case "local":
		return builtinProviderPluginCatalogEntry(
			"ollama",
			"Ollama",
			"local",
			"http://127.0.0.1:11434/v1",
			"https://ollama.com",
			[]string{"llama"},
		)
	default:
		return nil
	}
}

func builtinProviderPluginCatalogEntry(id string, name string, providerType string, baseURL string, docURL string, categories []string) *pluginProviderCatalogEntry {
	return &pluginProviderCatalogEntry{
		ID:          id,
		Name:        name,
		DisplayName: name,
		Type:        providerType,
		BaseURL:     baseURL,
		DocURL:      docURL,
		Categories:  categories,
		Source:      "plugin:built_in",
	}
}

func registerBuiltinProviderPlugin(registry *AdapterRegistry, pluginID string, name string, adapter builtinProviderAdapter) {
	registrations := []AdapterRegistration{{
		Type:         adapter.providerType,
		Adapter:      adapter.adapter,
		Capabilities: adapter.capabilities,
	}}
	if err := registry.RegisterPlugin(builtinProviderDescriptor(pluginID, name, adapter), registrations...); err != nil {
		panic(err)
	}
}

func builtinProviderDescriptor(pluginID string, name string, adapter builtinProviderAdapter) pluginmeta.Descriptor {
	capabilities := make([]string, 0, len(adapter.capabilities))
	for _, capability := range adapter.capabilities {
		capabilities = append(capabilities, string(capability))
	}
	descriptor := pluginmeta.BuiltInProviderWithResourceTypeMetadata(pluginID, name, []string{adapter.providerType}, adapter.resourceTypes, capabilities)
	if adapter.routeRequiresResource {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "route_requires_resource",
			Subject: adapter.providerType,
			Value:   "true",
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.credentialsScope != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "credentials_scope",
			Subject: adapter.providerType,
			Value:   adapter.credentialsScope,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.sessionAffinityKind != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "session_affinity_kind",
			Subject: adapter.providerType,
			Value:   adapter.sessionAffinityKind,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.claudeCodeAttributionDefault != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    claudeCodeAttributionDefaultPolicy,
			Subject: adapter.providerType,
			Value:   adapter.claudeCodeAttributionDefault,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	for _, authMode := range adapter.authModes {
		if authMode == "" {
			continue
		}
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    providerAuthModeOption,
			Subject: adapter.providerType,
			Value:   authMode,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.catalogEntry != nil {
		if data, err := json.Marshal(adapter.catalogEntry); err == nil {
			descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
				Kind:    "provider_catalog",
				Name:    "entry",
				Subject: adapter.providerType,
				Value:   string(data),
			})
			descriptor = pluginmeta.NormalizeDescriptor(descriptor)
		}
	}
	return descriptor
}
