package server

import pluginmeta "tokenhub/backend/internal/plugin"

type builtinProviderAdapter struct {
	providerType  string
	adapter       any
	resourceTypes []string
	capabilities  []AdapterCapability
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
		providerType:  ProviderOpenAICodex,
		adapter:       codexSubscription,
		resourceTypes: []string{ProviderResourceOpenAISubscription},
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
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityProbe,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.anthropic", "Anthropic", builtinProviderAdapter{
		providerType: ProviderAnthropic,
		adapter:      adapters[ProviderAnthropic],
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityProbe,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.gemini", "Gemini", builtinProviderAdapter{
		providerType: ProviderGemini,
		adapter:      adapters[ProviderGemini],
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
	return pluginmeta.BuiltInProviderWithResourceTypes(pluginID, name, []string{adapter.providerType}, adapter.resourceTypes, capabilities)
}
