package server

import (
	"context"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func addBuiltinClaudeSubscriptionRuntime(adapters map[string]any, deps builtinProviderRuntimeDependencies) {
	adapter := &ClaudeSubscriptionAdapter{StreamIdleTimeout: deps.StreamIdleTimeout}
	if deps.Store != nil {
		adapter.RefreshCredentials = providerCredentialLoaderForType(deps.Store, providerClaudeSubscription)
		adapter.RunClusterOperation = deps.Store.RunClusterOperation
	}
	if store, ok := deps.Store.(interface {
		ReplaceProviderSession(context.Context, string, string, string, string) error
	}); ok {
		adapter.SaveSession = store.ReplaceProviderSession
	}
	adapters[providerClaudeSubscription] = adapter
}

func registerBuiltinClaudeSubscriptionProvider(register func(string, string, builtinProviderAdapter) error, adapters map[string]any) error {
	registration := builtinProviderAdapter{providerType: providerClaudeSubscription, adapter: adapters[providerClaudeSubscription], supportsCustomHeaders: boolPointer(false), apiKeyRequired: boolPointer(false), credentialsScope: providerCredentialsScopeResource, routeRequiresResource: true, routeProtocols: []string{providerRouteProtocolChatCompletions},
		capabilities:  []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityModels, AdapterCapabilityOAuth, AdapterCapabilityProbe},
		resourceTypes: []pluginmeta.ManifestProviderResourceType{{Type: providerResourceClaudeSubscription, DisplayName: "Claude Code Account", AuthModes: []string{"oauth"}, Default: true, Defaults: map[string]string{"auth_type": "oauth", "max_concurrency": "1"}}},
		catalogEntry:  &pluginProviderCatalogEntry{ID: "claude-subscription", Name: "Claude Code Subscription", DisplayName: "Claude Code Subscription", Type: providerClaudeSubscription, DocURL: "https://code.claude.com/docs/en/overview", Categories: []string{"anthropic"}, Source: "claude-code-model-aliases", ModelsAccountRequiredErrorCode: "subscription_account_required", ModelsAccountRequiredErrorMessage: "Import a Claude Code account before loading its model aliases"}}
	for _, capability := range []string{"models.read", "oauth.cancel", "oauth.exchange", "oauth.methods", "oauth.start", "probe.run", "provider.probe.run"} {
		registration.managementActions = append(registration.managementActions, builtinProviderPluginCapability{id: providerClaudeSubscription + "." + capability, capability: capability, subject: providerClaudeSubscription})
	}
	return register("tokenhub.provider.claude-subscription", "Claude Code Subscription", registration)
}

func registerBuiltinClaudeSubscriptionActions(server *Server, actions builtinActionRegistrar) {
	registerBuiltinClaudeSubscriptionAuthorization(server, actions)
	definition := builtinSubscriptionDefinition{providerType: providerClaudeSubscription, pluginID: "tokenhub.provider.claude-subscription", name: "Claude Code Subscription", resourceType: providerResourceClaudeSubscription}
	for _, capability := range []string{"models.read", "probe.run", "provider.probe.run"} {
		schema := actionObjectSchema([]string{"resource_id"}, map[string]string{"resource_id": "string", "model": "string", "prompt": "string"})
		kind := pluginmeta.ActionKindRead
		if capability != "models.read" {
			kind = pluginmeta.ActionKindTest
		}
		if capability == "provider.probe.run" {
			schema = actionObjectSchema([]string{"provider_id"}, map[string]string{"provider_id": "string"})
		}
		descriptor := pluginmeta.ActionDescriptor{PluginID: definition.pluginID, ActionID: providerClaudeSubscription + "." + capability, Capability: capability, Subject: providerClaudeSubscription, Kind: kind, Title: definition.name + " " + capability, InputSchema: schema, OutputSchema: actionObjectSchema(nil, nil), Metadata: map[string]string{"provider_resource_type": providerResourceClaudeSubscription, "probe_fields": "model,prompt", "default_payload_json": `{"prompt":"Reply with OK."}`}}
		mustRegisterPluginAction(actions, descriptor, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
			return server.executeBuiltinSubscriptionAction(ctx, definition, capability, invocation)
		}))
	}
}

func (a *ClaudeSubscriptionAdapter) ResourceModels(ctx context.Context, provider Provider, resource ProviderResource, _ string) (ProviderCatalogEntry, int, error) {
	provider.Options = cloneStringMap(provider.Options)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options["resource_id"] = resource.ID
	credentials, err := subscriptionCredentials(ctx, provider, a.RefreshCredentials, false)
	if err != nil {
		return ProviderCatalogEntry{}, 0, err
	}
	if credentials.SessionJSON != "" {
		if err := validateClaudeSubscriptionSession(credentials.SessionJSON); err != nil {
			return ProviderCatalogEntry{}, 0, err
		}
	} else if strings.TrimSpace(credentials.AccessToken) == "" {
		return ProviderCatalogEntry{}, 0, NewHTTPError(400, "claude_subscription_credentials_required", "Import a Claude Code subscription token or account session")
	}
	return claudeSubscriptionCatalog(), 200, nil
}

func claudeSubscriptionCatalog() ProviderCatalogEntry {
	models := []ProviderCatalogModel{}
	for _, alias := range []string{"sonnet", "opus", "haiku", "fable"} {
		models = append(models, ProviderCatalogModel{ID: alias, Name: alias, DisplayName: "Claude " + alias, Type: "chat", Capabilities: []string{"chat", "streaming", "tools"}, Metadata: map[string]string{"source": "claude-code-model-aliases", "availability": "checked-by-cli-at-runtime"}})
	}
	return subscriptionCatalog(providerClaudeSubscription, "Claude Code Subscription", "", "claude-code-model-aliases", "", models)
}

func (a *ClaudeSubscriptionAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{Model: "sonnet", Prompt: "Reply with OK."}
}
func (a *ClaudeSubscriptionAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
	start := time.Now()
	provider.Options = cloneStringMap(provider.Options)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options["resource_id"] = resource.ID
	model := firstNonEmpty(strings.TrimSpace(request.Model), "sonnet")
	body, usage, err := a.Chat(ctx, provider, model, ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: firstNonEmpty(strings.TrimSpace(request.Prompt), "Reply with OK.")}}})
	if err != nil {
		return ProviderProbeResult{}, err
	}
	response := body.(map[string]any)
	message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	text, _ := message["content"].(string)
	return ProviderProbeResult{ResourceID: resource.ID, Model: model, OutputText: text, Usage: usage, LatencyMS: time.Since(start).Milliseconds(), Response: response}, nil
}
