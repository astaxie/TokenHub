package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerBuiltinSubscriptionWorkerActions(server *Server, actions builtinActionRegistrar) {
	for _, definition := range subscriptionWorkerDefinitions() {
		capabilities := []string{"oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel", "models.read", "credentials.refresh", "probe.run", "provider.probe.run"}
		if subscriptionWorkerHasQuota(definition.ID) {
			capabilities = append(capabilities, "quota.read")
		}
		if subscriptionWorkerHasCheckin(definition.ID) {
			capabilities = append(capabilities, "checkin.run")
		}
		for _, capability := range capabilities {
			kind := pluginmeta.ActionKindRead
			if capability == "oauth.start" {
				kind = pluginmeta.ActionKindExternalRedirect
			}
			if capability == "oauth.exchange" || capability == "oauth.cancel" || capability == "credentials.refresh" || capability == "checkin.run" {
				kind = pluginmeta.ActionKindMutate
			}
			if capability == "probe.run" || capability == "provider.probe.run" {
				kind = pluginmeta.ActionKindTest
			}
			metadata := map[string]string{"provider_resource_type": definition.resourceType()}
			properties := map[string]string{"resource_id": "string", "provider_id": "string", "model": "string", "prompt": "string", "refresh": "boolean"}
			required := []string{"resource_id"}
			switch capability {
			case "checkin.run":
				properties = map[string]string{"resource_id": "string"}
			case "quota.read":
				properties = map[string]string{"resource_id": "string", "refresh": "boolean"}
			case "oauth.methods":
				required = nil
				properties = nil
			case "oauth.start":
				required = nil
				properties = map[string]string{"method_id": "string", "inputs": "object", "key": "string"}
				metadata["subscription_auth"] = "true"
				metadata["subscription_session_json"] = "true"
			case "oauth.exchange":
				required = nil
				properties = map[string]string{"session_id": "string", "state": "string", "code": "string", "callback_url": "string", "mode": "string", "session_json": "string"}
				metadata["result_secret_policy"] = "provider_account_credentials"
			case "oauth.cancel":
				required = []string{"session_id", "state"}
				properties = map[string]string{"session_id": "string", "state": "string"}
			case "provider.probe.run":
				required = []string{"provider_id"}
			case "probe.run":
				metadata["probe_fields"] = "model,prompt"
				metadata["default_payload_json"] = `{"prompt":"Reply with OK."}`
			}
			descriptor := pluginmeta.ActionDescriptor{PluginID: definition.pluginID(), ActionID: definition.providerType() + "." + capability, Kind: kind, Title: definition.Name + " " + capability, Capability: capability, Subject: definition.providerType(), Metadata: metadata, InputSchema: actionObjectSchema(required, properties), OutputSchema: actionObjectSchema(nil, nil)}
			mustRegisterPluginAction(actions, descriptor, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
				return server.executeSubscriptionWorkerAction(ctx, definition, capability, invocation)
			}))
		}
	}
}

func (s *Server) executeSubscriptionWorkerAction(ctx context.Context, definition subscriptionWorkerDefinition, capability string, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	adapter, ok := resolveTypedAdapter[*SubscriptionWorkerAdapter](s.adapterRegistry, definition.providerType())
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "Subscription adapter is unavailable")
	}
	var input struct {
		subscriptionWorkerAuthInput
		Mode        string `json:"mode"`
		SessionJSON string `json:"session_json"`
		ResourceID  string `json:"resource_id"`
		ProviderID  string `json:"provider_id"`
		Model       string `json:"model"`
		Prompt      string `json:"prompt"`
		Refresh     bool   `json:"refresh"`
	}
	if len(invocation.Payload) > 0 && json.Unmarshal(invocation.Payload, &input) != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_plugin_action_payload", "Plugin action payload is invalid")
	}
	var data any
	var err error
	switch capability {
	case "oauth.methods":
		data, err = adapter.authorizationMethods(ctx)
	case "oauth.start":
		data, err = adapter.startAuthorization(ctx, invocation.Actor.ID, input.subscriptionWorkerAuthInput)
	case "oauth.exchange":
		if input.Mode == "import" {
			data, err = adapter.importAuthorization(ctx, invocation.Actor.ID, input.SessionJSON)
		} else {
			data, err = adapter.continueAuthorization(ctx, invocation.Actor.ID, input.subscriptionWorkerAuthInput, false)
		}
	case "oauth.cancel":
		data, err = adapter.continueAuthorization(ctx, invocation.Actor.ID, input.subscriptionWorkerAuthInput, true)
	case "provider.probe.run":
		provider, exists := s.store.GetProvider(input.ProviderID)
		if !exists || provider.Type != definition.providerType() {
			return pluginmeta.ActionResult{}, NewHTTPError(404, "provider_not_found", "Provider not found")
		}
		data, err = s.integrations.TestProvider(ctx, input.ProviderID)
	default:
		resource, exists := s.store.GetProviderResource(input.ResourceID)
		if !exists {
			return pluginmeta.ActionResult{}, NewHTTPError(404, "provider_resource_not_found", "Provider resource not found")
		}
		provider, exists := s.store.GetProvider(resource.ProviderID)
		if !exists || provider.Type != definition.providerType() || resource.ResourceType != definition.resourceType() {
			return pluginmeta.ActionResult{}, NewHTTPError(400, "provider_resource_type_mismatch", "Resource does not belong to this subscription provider")
		}
		switch capability {
		case "quota.read":
			if !input.Refresh {
				if cached, ok := s.cachedProviderMonitoringQuota(resource.ID, time.Minute); ok {
					return pluginmeta.ActionResult{Data: cached}, nil
				}
			}
			data, err = adapter.accountManagement(ctx, provider, resource, "usage")
		case "checkin.run":
			data, err = adapter.accountManagement(ctx, provider, resource, "checkin")
		case "models.read":
			data, err = s.queryProviderResourceModels(ctx, input.ResourceID)
		case "probe.run":
			data, err = s.integrations.TestProviderResource(ctx, input.ResourceID, &ProviderProbeRequest{Model: input.Model, Prompt: input.Prompt})
		case "credentials.refresh":
			err = adapter.withAccountLease(ctx, provider, &resource, func(leaseCtx context.Context) error {
				process, release, id, accountErr := adapter.account(leaseCtx, provider, &resource)
				if accountErr != nil {
					return accountErr
				}
				defer release()
				_, invokeErr := process.invoke(leaseCtx, adapter.command(id, "refresh"), nil)
				return invokeErr
			})
			if err == nil {
				data = map[string]any{"status": "complete"}
			}

		default:
			return pluginmeta.ActionResult{}, NewHTTPError(404, "provider_action_not_found", "Subscription action not found")
		}
	}
	return pluginmeta.ActionResult{Data: data}, err
}

func (a *SubscriptionWorkerAdapter) importAuthorization(ctx context.Context, actorID, auth string) (map[string]any, error) {
	if strings.TrimSpace(actorID) == "" {
		return nil, NewHTTPError(403, "subscription_actor_required", "An authenticated administrator is required")
	}
	if err := validateSubscriptionSession(auth); err != nil {
		return nil, err
	}
	key := NewID("subscription_import")
	process, release, err := a.Pool.acquire(ctx, key, auth, nil)
	if err != nil {
		return nil, err
	}
	defer release()
	defer process.cancel()
	if _, err := process.invoke(ctx, a.command(key, "import"), nil); err != nil {
		return nil, err
	}
	return a.authorizationCredentials(ctx, process, key)
}
