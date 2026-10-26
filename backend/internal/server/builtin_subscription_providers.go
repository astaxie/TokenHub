package server

import pluginmeta "tokenhub/backend/internal/plugin"

type builtinSubscriptionDefinition struct {
	providerType, pluginID, name, resourceType, redirectURI string
}

func builtinSubscriptionDefinitions() []builtinSubscriptionDefinition {
	return []builtinSubscriptionDefinition{
		{ProviderGitHubCopilotSubscription, "tokenhub.provider.github-copilot-subscription", "GitHub Copilot Subscription", ProviderResourceGitHubCopilotSubscription, ""},
		{ProviderChatGPTSubscription, "tokenhub.provider.chatgpt-subscription", "ChatGPT API", ProviderResourceChatGPTSubscription, "http://127.0.0.1:1455/auth/callback"},
		{providerGeminiCLISubscription, "tokenhub.provider.gemini-cli", "Gemini CLI", "gemini_cli_account", "http://127.0.0.1:8085/oauth2callback"},
		{providerAntigravitySubscription, "tokenhub.provider.antigravity", "Antigravity", "antigravity_account", "http://localhost:51121/oauth-callback"},
	}
}

func addBuiltinSubscriptionRuntime(adapters map[string]any, deps builtinProviderRuntimeDependencies) {
	addBuiltinClaudeSubscriptionRuntime(adapters, deps)
	copilot := &CopilotSubscriptionAdapter{Client: deps.Client, StreamClient: deps.StreamClient, CredentialRefreshClient: deps.Client, StreamIdleTimeout: deps.StreamIdleTimeout}
	chatgpt := &ChatGPTSubscriptionAdapter{Client: deps.Client, StreamClient: deps.StreamClient, CredentialRefreshClient: deps.Client, StreamIdleTimeout: deps.StreamIdleTimeout}
	if deps.Store != nil {
		copilot.RefreshCredentials = providerCredentialLoaderForType(deps.Store, ProviderGitHubCopilotSubscription)
		chatgpt.RefreshCredentials = providerCredentialLoaderForType(deps.Store, ProviderChatGPTSubscription)
	}
	adapters[ProviderGitHubCopilotSubscription], adapters[ProviderChatGPTSubscription] = copilot, chatgpt
	for _, kind := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		adapters[kind] = newGoogleSubscriptionAdapter(kind, deps)
	}
}

func registerBuiltinSubscriptionProviders(register func(string, string, builtinProviderAdapter) error, adapters map[string]any) error {
	for _, definition := range builtinSubscriptionDefinitions() {
		var registration builtinProviderAdapter
		switch definition.providerType {
		case providerGeminiCLISubscription, providerAntigravitySubscription:
			registration = googleSubscriptionRegistration(definition.providerType, adapters[definition.providerType])
		default:
			base, doc, source := copilotSubscriptionBaseURL, "https://github.com/features/copilot", "github-copilot-live"
			protocols := []string{providerRouteProtocolChatCompletions, providerRouteProtocolResponses}
			capabilities := []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityModels, AdapterCapabilityProbe, AdapterCapabilityOAuth}
			refresh := ""
			if definition.providerType == ProviderChatGPTSubscription {
				base, doc, source = chatGPTSubscriptionBaseURL, "https://chatgpt.com", "chatgpt-api-live"
				protocols = []string{providerRouteProtocolCodexResponses, providerRouteProtocolResponses}
				capabilities = []AdapterCapability{AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityModels, AdapterCapabilityProbe, AdapterCapabilityOAuth}
				refresh = chatGPTSubscriptionRefreshProfile
			}
			registration = builtinProviderAdapter{providerType: definition.providerType, adapter: adapters[definition.providerType], supportsCustomHeaders: boolPointer(false), apiKeyRequired: boolPointer(false),
				credentialsScope: providerCredentialsScopeResource, credentialRefreshProfile: refresh, routeRequiresResource: true, routeProtocols: protocols, capabilities: capabilities,
				resourceTypes: []pluginmeta.ManifestProviderResourceType{{Type: definition.resourceType, DisplayName: definition.name, AuthModes: []string{"oauth"}, Default: true, Defaults: map[string]string{"auth_type": "oauth", "base_url": base, "max_concurrency": "3"}}},
				catalogEntry:  &pluginProviderCatalogEntry{ID: definition.providerType, Name: definition.name, DisplayName: definition.name, Type: definition.providerType, BaseURL: base, DocURL: doc, Categories: []string{"openai"}, Source: source, ModelsAccountRequiredErrorCode: "subscription_account_required", ModelsAccountRequiredErrorMessage: "Connect a subscription account before loading its models"}}
		}
		registration.managementActions = builtinSubscriptionActionCapabilities(definition.providerType)
		if err := register(definition.pluginID, definition.name, registration); err != nil {
			return err
		}
	}
	return registerBuiltinClaudeSubscriptionProvider(register, adapters)
}

func builtinSubscriptionActionCapabilities(providerType string) []builtinProviderPluginCapability {
	var actions []builtinProviderPluginCapability
	for _, capability := range []string{"credentials.refresh", "models.read", "oauth.cancel", "oauth.exchange", "oauth.start", "probe.run", "provider.probe.run"} {
		if capability == "oauth.start" && (providerType == ProviderGitHubCopilotSubscription || providerType == providerGeminiCLISubscription || providerType == providerAntigravitySubscription) {
			actions = append(actions, builtinProviderPluginCapability{id: providerType + ".oauth.methods", capability: "oauth.methods", subject: providerType})
		}
		actions = append(actions, builtinProviderPluginCapability{id: providerType + "." + capability, capability: capability, subject: providerType})
	}
	return actions
}
