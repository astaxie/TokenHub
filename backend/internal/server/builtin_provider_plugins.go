package server

import (
	"encoding/json"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type builtinProviderAdapter struct {
	providerType                 string
	adapter                      any
	supportsCustomHeaders        *bool
	apiKeyRequired               *bool
	routeProtocols               []string
	credentialsScope             string
	credentialRefreshProfile     string
	routeRequiresResource        bool
	sessionAffinityKind          string
	claudeCodeAttributionDefault string
	reasoningConfigurable        *bool
	preserveReasoningContent     *bool
	responsesModelAllowlist      []string
	errorProfile                 string
	defaultCatalogProviderType   bool
	authModes                    []string
	authModeLegacyOption         string
	authModeInvalidErrorCode     string
	authModeInvalidErrorMessage  string
	modelDiscovery               AdapterModelDiscoveryPolicy
	modelCategories              []providerModelCategoryDefinition
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
		providerType:          ProviderOpenAI,
		adapter:               adapters[ProviderOpenAI],
		reasoningConfigurable: boolPointer(true),
		catalogEntry: builtinProviderPluginCatalogEntry(
			"openai",
			"OpenAI",
			ProviderOpenAI,
			"https://api.openai.com/v1",
			"https://platform.openai.com/docs/models",
			[]string{"openai"},
			[]string{"gpt-5", "gpt-5-mini", "gpt-4.1-mini", "text-embedding-3-small"},
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
		providerType:               ProviderOpenAICompatible,
		adapter:                    adapters[ProviderOpenAICompatible],
		defaultCatalogProviderType: true,
		reasoningConfigurable:      boolPointer(true),
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
		providerType:   ProviderKronk,
		adapter:        adapters[ProviderKronk],
		apiKeyRequired: boolPointer(false),
		errorProfile:   "kronk",
		catalogEntry: builtinProviderPluginCatalogEntry(
			ProviderKronk,
			"Kronk",
			ProviderKronk,
			kronkDefaultBaseURL,
			kronkDocURL,
			[]string{"custom"},
			nil,
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
		providerType:             ProviderOpenAICodex,
		adapter:                  codexSubscription,
		supportsCustomHeaders:    boolPointer(false),
		routeProtocols:           []string{providerRouteProtocolCodexResponses, providerRouteProtocolResponses},
		credentialsScope:         providerCredentialsScopeResource,
		credentialRefreshProfile: providerCredentialRefreshProfileOpenAIAccountOAuth,
		routeRequiresResource:    true,
		sessionAffinityKind:      AffinityKindCodexSession,
		catalogEntry: &pluginProviderCatalogEntry{
			ID:                                codexProviderCatalogID,
			Name:                              "OpenAI Codex",
			DisplayName:                       "OpenAI Codex",
			Type:                              ProviderOpenAICodex,
			BaseURL:                           openAICodexBaseURL,
			DocURL:                            "https://developers.openai.com/codex",
			Categories:                        []string{"codex"},
			Source:                            "openai-codex-live",
			ModelsAccountRequiredErrorCode:    "codex_account_required",
			ModelsAccountRequiredErrorMessage: "Connect an OpenAI Codex subscription account before loading its models",
		},
		resourceTypes: []pluginmeta.ManifestProviderResourceType{{
			Type:                      ProviderResourceOpenAISubscription,
			DisplayName:               "OpenAI Codex Subscription",
			AuthModes:                 []string{"oauth", "personal_access_token"},
			CredentialIdentityProfile: providerResourceIdentityProfileOpenAIIDToken,
			CredentialInputOptional:   true,
			Default:                   true,
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
		providerType:          ProviderAzureOpenAI,
		adapter:               adapters[ProviderAzureOpenAI],
		supportsCustomHeaders: boolPointer(false),
		reasoningConfigurable: boolPointer(true),
		catalogEntry: builtinProviderPluginCatalogEntry(
			"azure-openai",
			"Azure OpenAI",
			ProviderAzureOpenAI,
			"",
			"https://learn.microsoft.com/azure/ai-services/openai/",
			[]string{"microsoft", "openai"},
			nil,
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
		routeProtocols:               []string{providerRouteProtocolAnthropic},
		authModes:                    []string{anthropicAuthTypeAPIKey, anthropicAuthTypeBearer},
		authModeLegacyOption:         anthropicAuthTypeOption,
		authModeInvalidErrorCode:     "provider_anthropic_auth_type_invalid",
		authModeInvalidErrorMessage:  "Anthropic authentication type must be x-api-key or bearer",
		claudeCodeAttributionDefault: claudeCodeAttributionPreserve,
		modelDiscovery: AdapterModelDiscoveryPolicy{
			Path: "/v1/models",
			Auth: "provider_auth_mode",
			Headers: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		catalogEntry: builtinProviderPluginCatalogEntry(
			"anthropic",
			"Anthropic",
			ProviderAnthropic,
			"https://api.anthropic.com",
			"https://docs.anthropic.com",
			[]string{"claude"},
			[]string{"claude-sonnet-4.5", "claude-haiku-4.5"},
		),
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityProbe,
		},
	})
	registerBuiltinProviderPlugin(registry, "tokenhub.provider.gemini", "Gemini", builtinProviderAdapter{
		providerType:   ProviderGemini,
		adapter:        adapters[ProviderGemini],
		routeProtocols: []string{"gemini"},
		modelDiscovery: AdapterModelDiscoveryPolicy{
			Auth:             "query_param",
			APIKeyQueryParam: "key",
		},
		catalogEntry: builtinProviderPluginCatalogEntry(
			"google",
			"Google Gemini",
			ProviderGemini,
			"https://generativelanguage.googleapis.com/v1beta",
			"https://ai.google.dev/gemini-api/docs",
			[]string{"gemini"},
			[]string{"gemini-2.5-pro", "gemini-2.5-flash"},
		),
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityProbe,
		},
	})
	for _, adapterType := range []string{"deepseek", "qwen", "local"} {
		adapter := builtinProviderAdapter{
			providerType:          adapterType,
			adapter:               adapters[adapterType],
			reasoningConfigurable: boolPointer(true),
			catalogEntry:          builtinProviderPluginCatalogEntryForType(adapterType),
			capabilities: []AdapterCapability{
				AdapterCapabilityChat,
				AdapterCapabilityChatStream,
				AdapterCapabilityResponses,
				AdapterCapabilityResponseStream,
				AdapterCapabilityEmbeddings,
				AdapterCapabilityProbe,
			},
		}
		if adapterType == "deepseek" {
			adapter.preserveReasoningContent = boolPointer(true)
			adapter.responsesModelAllowlist = []string{"deepseek-v4-flash", "deepseek-v4-pro"}
		}
		registerBuiltinProviderPlugin(registry, "tokenhub.provider."+adapterType, adapterType, adapter)
	}
}

func registerBuiltinProviderCatalogPlugins(registry *pluginmeta.Registry) {
	if registry == nil {
		return
	}
	registerBuiltinProviderCatalogPlugin(registry, "tokenhub.provider-catalog.siliconflow", "SiliconFlow", builtinProviderPluginCatalogEntry(
		"siliconflow",
		"SiliconFlow",
		ProviderOpenAICompatible,
		"https://api.siliconflow.cn/v1",
		"https://cloud.siliconflow.com/models",
		[]string{"custom"},
		nil,
	))
}

func registerBuiltinProviderCatalogPlugin(registry *pluginmeta.Registry, pluginID string, name string, entry *pluginProviderCatalogEntry) {
	if entry == nil {
		return
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		panic(err)
	}
	if err := registry.Register(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    name,
		Version: "built-in",
		Source:  pluginmeta.SourceBuiltIn,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{{
			Kind:    "provider_catalog",
			Name:    "entry",
			Subject: entry.Type,
			Value:   string(encoded),
		}},
	}); err != nil {
		panic(err)
	}
}

func builtinProviderPluginCatalogSeedEntries() []ProviderCatalogEntry {
	return providerCatalogSeedEntriesFromRegistry(builtinProviderPluginCatalogRegistry())
}

func builtinProviderPluginCatalogDefaultType() string {
	return providerCatalogDefaultTypeFromRegistry(builtinProviderPluginCatalogRegistry())
}

func builtinProviderPluginCatalogRegistry() *AdapterRegistry {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	registerBuiltinProviderAdapters(registry, map[string]ProviderAdapter{}, &CodexSubscriptionAdapter{})
	registerBuiltinProviderCatalogPlugins(registry.plugins)
	return registry
}

func builtinProviderPluginCatalogEntryForType(providerType string) *pluginProviderCatalogEntry {
	switch providerType {
	case "deepseek":
		return builtinProviderPluginCatalogEntryFromProviderCatalog(deepSeekBuiltinCatalogEntry())
	case "qwen":
		return builtinProviderPluginCatalogEntry(
			"qwen",
			"Qwen",
			"qwen",
			"https://dashscope.aliyuncs.com/compatible-mode/v1",
			"https://help.aliyun.com/zh/model-studio",
			[]string{"qwen"},
			[]string{"qwen-max", "qwen-plus"},
		)
	case "local":
		return builtinProviderPluginCatalogEntry(
			"ollama",
			"Ollama",
			"local",
			"http://127.0.0.1:11434/v1",
			"https://ollama.com",
			[]string{"llama"},
			nil,
		)
	default:
		return nil
	}
}

func builtinProviderPluginCatalogEntry(id string, name string, providerType string, baseURL string, docURL string, categories []string, modelIDs []string) *pluginProviderCatalogEntry {
	entry := &pluginProviderCatalogEntry{
		ID:          id,
		Name:        name,
		DisplayName: name,
		Type:        providerType,
		BaseURL:     baseURL,
		DocURL:      docURL,
		Categories:  catalogUniqueStrings(categories),
		Source:      "plugin:built_in",
	}
	if len(modelIDs) > 0 {
		catalog := builtinCatalogEntry(id, name, providerType, baseURL, docURL, modelIDs)
		enriched := builtinProviderPluginCatalogEntryFromProviderCatalog(catalog)
		enriched.Source = "plugin:built_in"
		return enriched
	}
	return entry
}

func builtinProviderPluginCatalogEntryFromProviderCatalog(catalog ProviderCatalogEntry) *pluginProviderCatalogEntry {
	models := make([]pluginProviderCatalogModel, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		models = append(models, pluginProviderCatalogModel{
			ID:                        model.ID,
			Name:                      model.Name,
			DisplayName:               model.DisplayName,
			CanonicalName:             model.CanonicalName,
			Category:                  model.Category,
			Family:                    model.Family,
			Type:                      model.Type,
			ContextWindow:             model.ContextWindow,
			MaxOutputTokens:           model.MaxOutputTokens,
			InputPriceUSDPer1M:        model.InputPriceUSDPer1M,
			CacheReadPriceUSDPer1M:    model.CacheReadPriceUSDPer1M,
			CacheWritePriceUSDPer1M:   model.CacheWritePriceUSDPer1M,
			CacheWrite5mPriceUSDPer1M: model.CacheWrite5mPriceUSDPer1M,
			CacheWrite1hPriceUSDPer1M: model.CacheWrite1hPriceUSDPer1M,
			OutputPriceUSDPer1M:       model.OutputPriceUSDPer1M,
			InputModalities:           model.InputModalities,
			OutputModalities:          model.OutputModalities,
			Capabilities:              model.Capabilities,
			SupportedParameters:       model.SupportedParameters,
			LastUpdated:               model.LastUpdated,
			Metadata:                  model.Metadata,
		})
	}
	return &pluginProviderCatalogEntry{
		ID:          catalog.ID,
		Name:        catalog.Name,
		DisplayName: catalog.DisplayName,
		Type:        catalog.Type,
		BaseURL:     catalog.BaseURL,
		DocURL:      catalog.DocURL,
		Categories:  catalogUniqueStrings(catalog.Categories),
		ModelsCount: catalog.ModelsCount,
		Source:      "plugin:built_in",
		ETag:        catalog.ETag,
		Models:      models,
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
	if adapter.supportsCustomHeaders != nil {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "supports_custom_headers",
			Subject: adapter.providerType,
			Value:   boolString(*adapter.supportsCustomHeaders),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.apiKeyRequired != nil {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    providerAPIKeyRequiredOption,
			Subject: adapter.providerType,
			Value:   boolString(*adapter.apiKeyRequired),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	for _, protocol := range adapter.routeProtocols {
		protocol = strings.TrimSpace(protocol)
		if protocol == "" {
			continue
		}
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "route_protocol",
			Subject: adapter.providerType,
			Value:   protocol,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
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
	if adapter.credentialRefreshProfile != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    providerCredentialRefreshProfileOption,
			Subject: adapter.providerType,
			Value:   adapter.credentialRefreshProfile,
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
			Name:    systemPromptTransformDefaultPolicy,
			Subject: adapter.providerType,
			Value:   adapter.claudeCodeAttributionDefault,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.reasoningConfigurable != nil {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    providerReasoningConfigurablePolicy,
			Subject: adapter.providerType,
			Value:   boolString(*adapter.reasoningConfigurable),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.preserveReasoningContent != nil {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    reasoningContentOption,
			Subject: adapter.providerType,
			Value:   boolString(*adapter.preserveReasoningContent),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	for _, model := range adapter.responsesModelAllowlist {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "responses_model_allowlist",
			Subject: adapter.providerType,
			Value:   model,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.catalogEntry != nil && adapter.catalogEntry.BaseURL != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "default_base_url",
			Subject: adapter.providerType,
			Value:   strings.TrimRight(strings.TrimSpace(adapter.catalogEntry.BaseURL), "/"),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.errorProfile != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "error_profile",
			Subject: adapter.providerType,
			Value:   adapter.errorProfile,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.defaultCatalogProviderType {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "default_catalog_provider_type",
			Subject: adapter.providerType,
			Value:   "true",
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
	if option := strings.TrimSpace(adapter.authModeLegacyOption); option != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    providerAuthModeLegacyOptionPolicy,
			Subject: adapter.providerType,
			Value:   option,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if code := strings.TrimSpace(adapter.authModeInvalidErrorCode); code != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    providerAuthModeInvalidErrorCodePolicy,
			Subject: adapter.providerType,
			Value:   code,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if message := strings.TrimSpace(adapter.authModeInvalidErrorMessage); message != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    providerAuthModeInvalidErrorMessagePolicy,
			Subject: adapter.providerType,
			Value:   message,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.modelDiscovery.Path != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "model_discovery_path",
			Subject: adapter.providerType,
			Value:   adapter.modelDiscovery.Path,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.modelDiscovery.Auth != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "model_discovery_auth",
			Subject: adapter.providerType,
			Value:   adapter.modelDiscovery.Auth,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.modelDiscovery.APIKeyQueryParam != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "model_discovery_api_key_query_param",
			Subject: adapter.providerType,
			Value:   adapter.modelDiscovery.APIKeyQueryParam,
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if len(adapter.modelDiscovery.Headers) > 0 {
		if data, err := json.Marshal(normalizedStringMap(adapter.modelDiscovery.Headers)); err == nil {
			descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
				Kind:    "provider_policy",
				Name:    "model_discovery_headers",
				Subject: adapter.providerType,
				Value:   string(data),
			})
			descriptor = pluginmeta.NormalizeDescriptor(descriptor)
		}
	}
	if adapter.catalogEntry != nil {
		categoryDefinitions := append([]providerModelCategoryDefinition(nil), adapter.modelCategories...)
		categoryDefinitions = append(categoryDefinitions, providerModelCategoryDefinitionsForKeys(adapter.catalogEntry.Categories)...)
		for _, category := range categoryDefinitions {
			if data, err := json.Marshal(category); err == nil {
				descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
					Kind:    "provider_catalog",
					Name:    "model_category",
					Subject: adapter.providerType,
					Value:   string(data),
				})
				descriptor = pluginmeta.NormalizeDescriptor(descriptor)
			}
		}
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

func boolPointer(value bool) *bool {
	return &value
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
