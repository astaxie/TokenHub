package server

import (
	"encoding/json"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type builtinProviderAdapter struct {
	providerType                     string
	adapter                          any
	supportsCustomHeaders            *bool
	managedHeaders                   []string
	apiKeyRequired                   *bool
	routeProtocols                   []string
	credentialsScope                 string
	credentialRefreshProfile         string
	routeRequiresResource            bool
	sessionAffinityKind              string
	sessionAffinityIdentifierProfile string
	claudeCodeAttributionDefault     string
	reasoningConfigurable            *bool
	preserveReasoningContent         *bool
	responsesModelAllowlist          []string
	errorProfile                     string
	storeProbeFallback               bool
	defaultCatalogProviderType       bool
	authModes                        []string
	authModeLegacyOption             string
	authModeInvalidErrorCode         string
	authModeInvalidErrorMessage      string
	modelDiscovery                   AdapterModelDiscoveryPolicy
	modelCategories                  []providerModelCategoryDefinition
	resourceTypes                    []pluginmeta.ManifestProviderResourceType
	catalogEntry                     *pluginProviderCatalogEntry
	managementActions                []builtinProviderPluginCapability
	backgroundJobs                   []builtinProviderPluginCapability
	capabilities                     []AdapterCapability
}

type builtinProviderPluginCapability struct {
	id         string
	capability string
	subject    string
}

func registerBuiltinProviderAdapters(registry *AdapterRegistry, adapters map[string]any, runtimes ...pluginmeta.Runtime) error {
	var runtime *pluginmeta.Runtime
	if len(runtimes) > 0 {
		runtime = &runtimes[0]
	}
	register := func(pluginID string, name string, adapter builtinProviderAdapter) error {
		state, err := builtInProviderPluginState(runtime, pluginID)
		if err != nil {
			return err
		}
		registerBuiltinProviderPlugin(registry, pluginID, name, adapter, state)
		return nil
	}
	if err := register("tokenhub.provider.mock", "Mock Provider", builtinProviderAdapter{
		providerType:       ProviderMock,
		adapter:            adapters[ProviderMock],
		storeProbeFallback: true,
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityResponses,
			AdapterCapabilityEmbeddings,
		},
	}); err != nil {
		return err
	}
	if err := register("tokenhub.provider.openai", "OpenAI", builtinProviderAdapter{
		providerType:          ProviderOpenAI,
		adapter:               adapters[ProviderOpenAI],
		reasoningConfigurable: boolPointer(true),
		managedHeaders:        []string{"api-key", "x-api-key", "openai-organization", "openai-project"},
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
	}); err != nil {
		return err
	}
	if err := register("tokenhub.provider.openai-compatible", "OpenAI-Compatible", builtinProviderAdapter{
		providerType:               ProviderOpenAICompatible,
		apiKeyRequired:             boolPointer(false),
		adapter:                    adapters[ProviderOpenAICompatible],
		defaultCatalogProviderType: true,
		reasoningConfigurable:      boolPointer(true),
		managedHeaders:             []string{"api-key", "x-api-key", "openai-organization", "openai-project"},
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityResponses,
			AdapterCapabilityResponseStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityProbe,
		},
	}); err != nil {
		return err
	}
	if err := register("tokenhub.provider.kronk", "Kronk", builtinProviderAdapter{
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
		managementActions: []builtinProviderPluginCapability{
			{id: "kronk.models.preview", capability: "models.preview", subject: ProviderKronk},
		},
		capabilities: []AdapterCapability{
			AdapterCapabilityChat,
			AdapterCapabilityChatStream,
			AdapterCapabilityResponses,
			AdapterCapabilityResponseStream,
			AdapterCapabilityEmbeddings,
			AdapterCapabilityModels,
			AdapterCapabilityProbe,
		},
	}); err != nil {
		return err
	}
	if err := register("tokenhub.provider.openai-codex", "OpenAI Codex Subscription", builtinProviderAdapter{
		providerType:                     ProviderOpenAICodex,
		adapter:                          adapters[ProviderOpenAICodex],
		supportsCustomHeaders:            boolPointer(false),
		apiKeyRequired:                   boolPointer(false),
		routeProtocols:                   []string{providerRouteProtocolCodexResponses, providerRouteProtocolResponses},
		credentialsScope:                 providerCredentialsScopeResource,
		credentialRefreshProfile:         openAIAccountOAuthRefreshProfile,
		routeRequiresResource:            true,
		sessionAffinityKind:              AffinityKindCodexSession,
		sessionAffinityIdentifierProfile: sessionAffinityIdentifierProfileCompatibility,
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
		managementActions: builtinOpenAICodexProviderPluginActions(),
		backgroundJobs:    builtinOpenAICodexProviderPluginBackgroundJobs(),
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
	}); err != nil {
		return err
	}
	if err := register("tokenhub.provider.azure-openai", "Azure OpenAI", builtinProviderAdapter{
		providerType:          ProviderAzureOpenAI,
		adapter:               adapters[ProviderAzureOpenAI],
		supportsCustomHeaders: boolPointer(false),
		reasoningConfigurable: boolPointer(true),
		managedHeaders:        []string{"api-key", "x-api-key", "openai-organization", "openai-project"},
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
	}); err != nil {
		return err
	}
	if err := register("tokenhub.provider.anthropic", "Anthropic", builtinProviderAdapter{
		providerType:                 ProviderAnthropic,
		adapter:                      adapters[ProviderAnthropic],
		routeProtocols:               []string{providerRouteProtocolAnthropic},
		authModes:                    []string{anthropicAuthTypeAPIKey, anthropicAuthTypeBearer},
		authModeLegacyOption:         anthropicAuthTypeOption,
		authModeInvalidErrorCode:     "provider_anthropic_auth_type_invalid",
		authModeInvalidErrorMessage:  "Anthropic authentication type must be x-api-key or bearer",
		claudeCodeAttributionDefault: claudeCodeAttributionPreserve,
		managedHeaders:               []string{"api-key", "x-api-key", "anthropic-version", "anthropic-beta"},
		modelDiscovery: AdapterModelDiscoveryPolicy{
			Path: "/v1/models",
			Auth: providerModelDiscoveryAuthProviderAuthMode,
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
	}); err != nil {
		return err
	}
	if err := register("tokenhub.provider.gemini", "Gemini", builtinProviderAdapter{
		providerType:   ProviderGemini,
		adapter:        adapters[ProviderGemini],
		routeProtocols: []string{"gemini"},
		managedHeaders: []string{"x-goog-api-key"},
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
	}); err != nil {
		return err
	}
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
		if err := register("tokenhub.provider."+adapterType, adapterType, adapter); err != nil {
			return err
		}
	}
	return nil
}

func registerBuiltinProviderCatalogPlugins(registry *pluginmeta.Registry) {
	if registry == nil {
		return
	}
	registerBuiltinProviderCatalogCategoryPlugin(registry)
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

func registerBuiltinProviderCatalogCategoryPlugin(registry *pluginmeta.Registry) {
	capabilities := make([]pluginmeta.CapabilityDescriptor, 0, len(builtinProviderModelCategoryDefinitionSeed()))
	for _, category := range builtinProviderModelCategoryDefinitionSeed() {
		data, err := json.Marshal(category)
		if err != nil {
			panic(err)
		}
		capabilities = append(capabilities, pluginmeta.CapabilityDescriptor{
			Kind:  pluginmeta.CapabilityKindProviderCatalog,
			Name:  pluginmeta.ProviderCatalogModelCategory,
			Value: string(data),
		})
	}
	if err := registry.Register(pluginmeta.Descriptor{
		ID:           "tokenhub.provider-catalog.model-categories",
		Name:         "Built-in Provider Model Categories",
		Version:      "built-in",
		Source:       pluginmeta.SourceBuiltIn,
		Kinds:        []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements:   []pluginmeta.Placement{pluginmeta.PlacementGatewayChain},
		Capabilities: capabilities,
	}); err != nil {
		panic(err)
	}
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

func builtinProviderModelCategoryDefinitions() []providerModelCategoryDefinition {
	return normalizeProviderModelCategoryDefinitions(builtinProviderModelCategoryDefinitionSeed())
}

func builtinProviderModelCategoryDefinitionsForKeys(keys []string) []providerModelCategoryDefinition {
	definitionsByKey := map[string]providerModelCategoryDefinition{}
	for _, definition := range builtinProviderModelCategoryDefinitionSeed() {
		definition = normalizeProviderModelCategoryDefinition(definition)
		definitionsByKey[definition.Key] = definition
	}
	result := make([]providerModelCategoryDefinition, 0, len(keys))
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if definition, ok := definitionsByKey[key]; ok {
			result = append(result, definition)
			continue
		}
		result = append(result, providerModelCategoryDefinition{Key: key, Label: key, Aliases: []string{key}})
	}
	return normalizeProviderModelCategoryDefinitions(result)
}

func builtinProviderModelCategoryDefinitionSeed() []providerModelCategoryDefinition {
	return []providerModelCategoryDefinition{
		{Key: "codex", Label: "OpenAI Codex", Order: 10, Aliases: []string{"codex"}, FamilyPrefixes: []string{"codex"}},
		{Key: "openai", Label: "OpenAI", Order: 20, Aliases: []string{"gpt", "openai", "o1", "o3", "o4"}, FamilyPrefixes: []string{"gpt"}, CanonicalPrefixes: []string{"gpt"}},
		{Key: "claude", Label: "Claude", Order: 30, Aliases: []string{"anthropic", "claude"}, FamilyPrefixes: []string{"claude"}, CanonicalPrefixes: []string{"claude"}},
		{Key: "deepseek", Label: "DeepSeek", Order: 40, Aliases: []string{"deepseek"}, FamilyPrefixes: []string{"deepseek"}, CanonicalPrefixes: []string{"deepseek"}},
		{Key: "gemini", Label: "Gemini", Order: 50, Aliases: []string{"gemini", "google", "google/"}, FamilyPrefixes: []string{"gemini"}, CanonicalPrefixes: []string{"gemini"}},
		{Key: "qwen", Label: "Qwen", Order: 60, Aliases: []string{"alibaba", "dashscope", "qwen"}, FamilyPrefixes: []string{"qwen"}, CanonicalPrefixes: []string{"qwen"}},
		{Key: "glm", Label: "GLM", Order: 70, Aliases: []string{"glm", "zhipu"}, FamilyPrefixes: []string{"glm"}, CanonicalPrefixes: []string{"glm"}},
		{Key: "kimi", Label: "Kimi", Order: 80, Aliases: []string{"kimi", "moonshot"}, FamilyPrefixes: []string{"kimi"}},
		{Key: "doubao", Label: "Doubao", Order: 90, Aliases: []string{"doubao", "volcengine"}, FamilyPrefixes: []string{"doubao"}},
		{Key: "ernie", Label: "ERNIE", Order: 100, Aliases: []string{"ernie"}},
		{Key: "baichuan", Label: "Baichuan", Order: 110, Aliases: []string{"baichuan"}},
		{Key: "minimax", Label: "MiniMax", Order: 120, Aliases: []string{"hailuo", "minimax"}},
		{Key: "stepfun", Label: "StepFun", Order: 130, Aliases: []string{"step-", "stepaudio"}},
		{Key: "wanx", Label: "WanX", Order: 140, Aliases: []string{"wanx"}},
		{Key: "grok", Label: "Grok", Order: 150, Aliases: []string{"grok", "xai", "xai/"}},
		{Key: "paddlepaddle", Label: "PaddlePaddle", Order: 160, Aliases: []string{"paddleocr"}},
		{Key: "microsoft", Label: "Microsoft", Order: 170, Aliases: []string{"phi-"}},
		{Key: "llama", Label: "Llama", Order: 180, Aliases: []string{"llama", "meta", "meta/"}, FamilyPrefixes: []string{"llama"}},
		{Key: "mistral", Label: "Mistral", Order: 190, Aliases: []string{"mistral"}, FamilyPrefixes: []string{"mistral"}},
		{Key: "custom", Label: "自定义", Order: 1000, Aliases: []string{"custom"}, FamilyPrefixes: []string{"custom"}, CanonicalPrefixes: []string{"custom"}},
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
	if err := registerBuiltinProviderAdapters(registry, map[string]any{ProviderOpenAICodex: &CodexSubscriptionAdapter{}}); err != nil {
		panic(err)
	}
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

func builtInProviderPluginState(runtime *pluginmeta.Runtime, pluginID string) (pluginmeta.PackageState, error) {
	state, err := pluginmeta.NormalizePackageState(pluginmeta.PackageState{Status: pluginmeta.StatusEnabled})
	if err != nil {
		return pluginmeta.PackageState{}, err
	}
	if runtime == nil {
		return state, nil
	}
	if configured, ok, err := runtime.ReadBuiltInPackageState(pluginID); err != nil {
		return pluginmeta.PackageState{}, err
	} else if ok {
		state = configured
	}
	return state, nil
}

func registerBuiltinProviderPlugin(registry *AdapterRegistry, pluginID string, name string, adapter builtinProviderAdapter, state pluginmeta.PackageState) {
	state, err := pluginmeta.NormalizePackageState(state)
	if err != nil {
		panic(err)
	}
	descriptor := builtinProviderDescriptor(pluginID, name, adapter)
	descriptor.Status = state.Status
	if !state.Enabled() {
		if err := registry.plugins.Register(descriptor); err != nil {
			panic(err)
		}
		return
	}
	registrations := []AdapterRegistration{{
		Type:         adapter.providerType,
		Adapter:      adapter.adapter,
		Capabilities: adapter.capabilities,
	}}
	if err := registry.RegisterPlugin(descriptor, registrations...); err != nil {
		panic(err)
	}
}

func builtinProviderDescriptor(pluginID string, name string, adapter builtinProviderAdapter) pluginmeta.Descriptor {
	capabilities := make([]string, 0, len(adapter.capabilities))
	for _, capability := range adapter.capabilities {
		capabilities = append(capabilities, string(capability))
	}
	descriptor := pluginmeta.BuiltInProviderWithResourceTypeMetadata(pluginID, name, []string{adapter.providerType}, adapter.resourceTypes, capabilities)
	descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
		Kind: pluginmeta.CapabilityKindProviderType,
		Name: adapter.providerType,
	})
	descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	if adapter.supportsCustomHeaders != nil {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "supports_custom_headers",
			Subject: adapter.providerType,
			Value:   boolString(*adapter.supportsCustomHeaders),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	for _, header := range sortedUniqueStrings(adapter.managedHeaders) {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    pluginmeta.ProviderPolicyManagedHeader,
			Subject: adapter.providerType,
			Value:   strings.ToLower(header),
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
	if adapter.storeProbeFallback {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    pluginmeta.ProviderPolicyStoreProbeFallback,
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
	if adapter.sessionAffinityIdentifierProfile != "" {
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    "provider_policy",
			Name:    "session_affinity_identifier_profile",
			Subject: adapter.providerType,
			Value:   adapter.sessionAffinityIdentifierProfile,
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
	for _, action := range adapter.managementActions {
		action.id = strings.TrimSpace(action.id)
		if action.id == "" {
			continue
		}
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    pluginmeta.CapabilityKindManagementAction,
			Name:    action.id,
			Subject: firstNonEmpty(strings.TrimSpace(action.subject), adapter.providerType),
			Value:   strings.TrimSpace(action.capability),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if len(adapter.backgroundJobs) > 0 {
		descriptor.Placements = append(descriptor.Placements, pluginmeta.PlacementBackground)
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	for _, job := range adapter.backgroundJobs {
		job.id = strings.TrimSpace(job.id)
		if job.id == "" {
			continue
		}
		descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
			Kind:    pluginmeta.CapabilityKindBackgroundJob,
			Name:    job.id,
			Subject: firstNonEmpty(strings.TrimSpace(job.subject), adapter.providerType),
			Value:   strings.TrimSpace(job.capability),
		})
		descriptor = pluginmeta.NormalizeDescriptor(descriptor)
	}
	if adapter.catalogEntry != nil {
		categoryDefinitions := append([]providerModelCategoryDefinition(nil), adapter.modelCategories...)
		categoryDefinitions = append(categoryDefinitions, builtinProviderModelCategoryDefinitionsForKeys(adapter.catalogEntry.Categories)...)
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

func builtinOpenAICodexProviderPluginActions() []builtinProviderPluginCapability {
	return []builtinProviderPluginCapability{
		{id: "openai_codex.credentials.refresh", capability: "credentials.refresh", subject: ProviderOpenAICodex},
		{id: "openai_codex.image_capability.configure", capability: "image.capability.configure", subject: ProviderOpenAICodex},
		{id: "openai_codex.models.preview", capability: "models.preview", subject: ProviderOpenAICodex},
		{id: "openai_codex.models.read", capability: "models.read", subject: ProviderOpenAICodex},
		{id: "openai_codex.oauth.exchange", capability: "oauth.exchange", subject: ProviderOpenAICodex},
		{id: "openai_codex.oauth.start", capability: "oauth.start", subject: ProviderOpenAICodex},
		{id: "openai_codex.probe.run", capability: "probe.run", subject: ProviderOpenAICodex},
		{id: "openai_codex.provider.probe.run", capability: "provider.probe.run", subject: ProviderOpenAICodex},
		{id: "openai_codex.quota.read", capability: "quota.read", subject: ProviderOpenAICodex},
		{id: "openai_codex.quota.reset", capability: "quota.reset", subject: ProviderOpenAICodex},
		{id: "openai_codex.quota.reset_credits.read", capability: "quota.reset_credits.read", subject: ProviderOpenAICodex},
	}
}

func builtinOpenAICodexProviderPluginBackgroundJobs() []builtinProviderPluginCapability {
	return []builtinProviderPluginCapability{
		{id: "openai_codex.credentials.refresh_due", capability: providerCredentialRefreshDueJobCapability, subject: ProviderOpenAICodex},
		{id: "openai_codex.quota.refresh_due", capability: providerQuotaRefreshDueJobCapability, subject: ProviderOpenAICodex},
	}
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
