package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type subscriptionWorkerDefinition struct{ ID, Name string }

func subscriptionWorkerDefinitions() []subscriptionWorkerDefinition {
	return []subscriptionWorkerDefinition{
		{"cursor", "Cursor"}, {"kiro", "Kiro"}, {"qoder", "Qoder"}, {"qoder-cn", "Qoder China"},
		{"workbuddy", "WorkBuddy"}, {"workbuddy-ai", "WorkBuddy AI"}, {"mimo-app", "MiMo"}, {"zcode", "ZCode"},
		{"factory", "Factory"}, {"grok", "Grok"}, {"devin", "Devin"}, {"commandcode-plan", "Command Code"}, {"zed", "Zed"},
		{"kimi-work", "Kimi Work"}, {"minimax-code", "MiniMax Coding Plan"}, {"minimax-code-global", "MiniMax Coding Plan Global"},
		{"cline", "Cline"}, {"trae-cn", "Trae China"}, {"trae-global", "Trae Global"}, {"opendesign", "OpenDesign"},
		{"nous-portal", "Nous Portal"}, {"raccoon", "Raccoon"}, {"autoclaw", "AutoClaw"}, {"autoclaw-cn", "AutoClaw China"},
		{"codearts", "CodeArts"}, {"joycode", "JoyCode"},
	}
}
func (d subscriptionWorkerDefinition) providerType() string {
	return strings.ReplaceAll(d.ID, "-", "_") + "_subscription"
}
func (d subscriptionWorkerDefinition) resourceType() string {
	return strings.ReplaceAll(d.ID, "-", "_") + "_account"
}
func (d subscriptionWorkerDefinition) pluginID() string {
	return "tokenhub.provider." + d.ID + "-subscription"
}

type SubscriptionWorkerAdapter struct {
	Definition          subscriptionWorkerDefinition
	Pool                *subscriptionWorkerPool
	RefreshCredentials  func(context.Context, string, bool) (ProviderResourceCredentials, error)
	SaveSession         func(context.Context, string, string, string, string) error
	RunClusterOperation func(context.Context, string, func(context.Context) error) error
	StreamIdleTimeout   time.Duration
}

func addBuiltinSubscriptionWorkerRuntime(adapters map[string]any, deps builtinProviderRuntimeDependencies) {
	client := deps.StreamClient
	if client == nil {
		client = deps.Client
	}
	pool := newSubscriptionWorkerPool(client)
	for _, definition := range subscriptionWorkerDefinitions() {
		adapter := &SubscriptionWorkerAdapter{Definition: definition, Pool: pool, StreamIdleTimeout: deps.StreamIdleTimeout}
		if deps.Store != nil {
			adapter.RefreshCredentials = deps.Store.RefreshProviderResourceCredentials
			adapter.RunClusterOperation = deps.Store.RunClusterOperation
		}
		if store, ok := deps.Store.(interface {
			ReplaceProviderSession(context.Context, string, string, string, string) error
		}); ok {
			adapter.SaveSession = store.ReplaceProviderSession
		}
		adapters[definition.providerType()] = adapter
	}
}

func registerBuiltinSubscriptionWorkerProviders(register func(string, string, builtinProviderAdapter) error, adapters map[string]any) error {
	for _, definition := range subscriptionWorkerDefinitions() {
		registration := builtinProviderAdapter{
			providerType: definition.providerType(), adapter: adapters[definition.providerType()], supportsCustomHeaders: boolPointer(false), apiKeyRequired: boolPointer(false),
			credentialsScope: providerCredentialsScopeResource, routeRequiresResource: true, routeProtocols: []string{providerRouteProtocolChatCompletions},
			capabilities:  []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityModels, AdapterCapabilityProbe, AdapterCapabilityOAuth},
			resourceTypes: []pluginmeta.ManifestProviderResourceType{{Type: definition.resourceType(), DisplayName: definition.Name + " Account", AuthModes: []string{"oauth"}, Default: true, Defaults: map[string]string{"auth_type": "oauth", "max_concurrency": "3"}}},
			catalogEntry:  &pluginProviderCatalogEntry{ID: definition.ID, Name: definition.Name, DisplayName: definition.Name, Type: definition.providerType(), DocURL: "https://github.com/yetone/magpie", Source: "subscription-account-live", Categories: []string{"custom"}, ModelsAccountRequiredErrorCode: "subscription_account_required", ModelsAccountRequiredErrorMessage: "Connect a subscription account before loading its models"},
		}
		for _, capability := range []string{"oauth.methods", "oauth.cancel", "models.read", "oauth.start", "oauth.exchange", "credentials.refresh", "probe.run", "provider.probe.run"} {
			registration.managementActions = append(registration.managementActions, builtinProviderPluginCapability{id: definition.providerType() + "." + capability, capability: capability, subject: definition.providerType()})
		}
		if err := register(definition.pluginID(), definition.Name, registration); err != nil {
			return err
		}
	}
	return nil
}

func (a *SubscriptionWorkerAdapter) Close() {
	if a.Pool != nil {
		a.Pool.Close()
	}
}

func (a *SubscriptionWorkerAdapter) account(ctx context.Context, provider Provider, resource *ProviderResource) (*subscriptionWorkerProcess, func(), string, error) {
	id := provider.Options["resource_id"]
	if resource != nil {
		id = resource.ID
	}
	if id == "" || a.RefreshCredentials == nil || a.SaveSession == nil || a.Pool == nil {
		return nil, nil, "", NewHTTPError(400, "subscription_account_required", "A saved subscription account is required")
	}
	unlock, err := a.Pool.enterAccount(ctx, a.Definition.ID+":"+id)
	if err != nil {
		return nil, nil, "", err
	}
	credentials, err := a.RefreshCredentials(ctx, id, false)
	if err != nil {
		unlock()
		return nil, nil, "", err
	}
	if err = validateSubscriptionSession(credentials.SessionJSON); err != nil {
		unlock()
		return nil, nil, "", err
	}
	caller, _ := ctx.Value(subscriptionCallerKey{}).(string)
	ephemeral := caller == ""
	if ephemeral {
		caller = NewID("probe")
	}
	sum := sha256.Sum256([]byte(caller))
	process, release, err := a.Pool.acquire(ctx, a.Definition.ID+":"+id+":"+hex.EncodeToString(sum[:]), credentials.SessionJSON, func(ctx context.Context, previous, next string) error {
		return a.SaveSession(ctx, id, a.Definition.providerType(), previous, next)
	})
	if err != nil {
		unlock()
		return nil, nil, "", err
	}
	return process, func() {
		if ephemeral {
			process.cancel()
		}
		release()
		unlock()
	}, id, nil
}

func validateSubscriptionSession(auth string) error {
	var object map[string]json.RawMessage
	if len(auth) > 512<<10 || json.Unmarshal([]byte(auth), &object) != nil || object == nil || len(object) == 0 {
		return NewHTTPError(400, "subscription_session_required", "Import the subscription account session JSON or sign in")
	}
	var kind string
	if json.Unmarshal(object["type"], &kind) != nil || (kind != "api" && kind != "oauth") {
		return NewHTTPError(400, "subscription_session_invalid", "Subscription session type must be api or oauth")
	}
	return nil
}

func (a *SubscriptionWorkerAdapter) command(resourceID, operation string) map[string]any {
	return map[string]any{"operation": operation, "provider": a.Definition.ID, "resource_id": resourceID}
}

func (a *SubscriptionWorkerAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, unsupportedCapabilityError("Subscription accounts expose chat completions")
}
func (a *SubscriptionWorkerAdapter) Embeddings(context.Context, Provider, string, EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, unsupportedCapabilityError("Subscription accounts do not expose embeddings")
}
