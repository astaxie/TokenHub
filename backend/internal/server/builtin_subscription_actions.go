package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerBuiltinSubscriptionActions(server *Server, actions builtinActionRegistrar) {
	registerBuiltinClaudeSubscriptionActions(server, actions)
	for _, definition := range builtinSubscriptionDefinitions() {
		for _, capability := range builtinSubscriptionActionCapabilities(definition.providerType) {
			kind := pluginmeta.ActionKindRead
			if capability.capability == "oauth.start" {
				kind = pluginmeta.ActionKindExternalRedirect
			}
			if capability.capability == "oauth.exchange" || capability.capability == "oauth.cancel" || capability.capability == "credentials.refresh" {
				kind = pluginmeta.ActionKindMutate
			}
			if capability.capability == "probe.run" || capability.capability == "provider.probe.run" {
				kind = pluginmeta.ActionKindTest
			}
			metadata := map[string]string{"provider_resource_type": definition.resourceType}
			schema := actionObjectSchema([]string{"resource_id"}, map[string]string{"resource_id": "string", "force": "boolean", "model": "string", "prompt": "string"})
			switch capability.capability {
			case "oauth.start":
				metadata["subscription_auth"] = "true"
				metadata["oauth_redirect_uri"] = definition.redirectURI
				metadata["notice_login_step"] = "Sign in to your subscription account, then paste the callback URL."
				metadata["callback_eyebrow"] = definition.name + " authorization"
				if definition.providerType == ProviderGitHubCopilotSubscription {
					metadata["oauth_flow"] = "device_code"
					metadata["notice_login_step"] = "Open GitHub and enter the device code to authorize Copilot."
				}
				if definition.providerType == ProviderChatGPTSubscription {
					metadata["oauth_callback_fields"] = "client_id"
				}
				schema = actionObjectSchema(nil, map[string]string{"redirect_uri": "string", "return_url": "string", "organization_id": "string", "method_id": "string", "inputs": "object", "client_id": "string", "client_secret": "string"})
			case "oauth.methods":
				schema = actionObjectSchema(nil, nil)
			case "oauth.cancel":
				schema = actionObjectSchema([]string{"session_id", "state"}, map[string]string{"session_id": "string", "state": "string"})
			case "oauth.exchange":
				metadata["result_secret_policy"] = "provider_account_credentials"
				metadata["oauth_redirect_uri"] = definition.redirectURI
				schema = actionObjectSchema([]string{"session_id", "state"}, map[string]string{"session_id": "string", "state": "string", "code": "string", "client_id": "string", "redirect_uri": "string"})
			case "probe.run":
				metadata["probe_fields"] = "model,prompt"
				metadata["default_payload_json"] = `{"prompt":"Reply with OK."}`
			case "provider.probe.run":
				schema = actionObjectSchema([]string{"provider_id"}, map[string]string{"provider_id": "string"})
			}
			descriptor := pluginmeta.ActionDescriptor{PluginID: definition.pluginID, ActionID: capability.id, Kind: kind, Title: definition.name + " " + capability.capability, Capability: capability.capability, Subject: definition.providerType, Metadata: metadata, InputSchema: schema, OutputSchema: actionObjectSchema(nil, nil)}
			mustRegisterPluginAction(actions, descriptor, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
				return server.executeBuiltinSubscriptionAction(ctx, definition, capability.capability, invocation)
			}))
		}
	}
}

func (s *Server) executeBuiltinSubscriptionAction(ctx context.Context, definition builtinSubscriptionDefinition, capability string, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	if capability == "oauth.methods" {
		return pluginmeta.ActionResult{Data: map[string]any{"methods": []any{map[string]any{"id": "oauth_client", "label": "Google OAuth client", "type": "oauth", "prompts": []any{map[string]any{"key": "client_id", "label": "Google OAuth client ID", "type": "text"}, map[string]any{"key": "client_secret", "label": "Google OAuth client secret", "type": "password"}}}}}}, nil
	}
	if capability == "oauth.start" {
		return s.startSubscriptionOAuth(ctx, definition, invocation)
	}
	if capability == "oauth.cancel" || capability == "oauth.exchange" {
		return s.runSubscriptionOAuthAction(ctx, definition, capability, invocation)
	}
	var payload struct {
		ResourceID string `json:"resource_id"`
		ProviderID string `json:"provider_id"`
		Force      bool   `json:"force"`
		Model      string `json:"model"`
		Prompt     string `json:"prompt"`
	}
	if json.Unmarshal(invocation.Payload, &payload) != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_plugin_action_payload", "Plugin action payload is invalid")
	}
	if capability == "provider.probe.run" {
		provider, ok := s.store.GetProvider(payload.ProviderID)
		if !ok || provider.Type != definition.providerType {
			return pluginmeta.ActionResult{}, NewHTTPError(404, "provider_not_found", "Provider not found")
		}
		result, err := s.integrations.TestProvider(ctx, payload.ProviderID)
		return pluginmeta.ActionResult{Data: result}, err
	}
	resource, ok := s.store.GetProviderResource(payload.ResourceID)
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(404, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.store.GetProvider(resource.ProviderID)
	if !ok || provider.Type != definition.providerType || resource.ResourceType != definition.resourceType {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "provider_resource_type_mismatch", "Resource does not belong to this subscription provider")
	}
	switch capability {
	case "models.read":
		catalog, err := s.queryProviderResourceModels(ctx, payload.ResourceID)
		return pluginmeta.ActionResult{Data: catalog}, err
	case "probe.run":
		result, err := s.integrations.TestProviderResource(ctx, payload.ResourceID, &ProviderProbeRequest{Model: payload.Model, Prompt: payload.Prompt})
		return pluginmeta.ActionResult{Data: result}, err
	case "credentials.refresh":
		credentials, err := s.store.RefreshProviderResourceCredentials(ctx, payload.ResourceID, payload.Force)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		if definition.providerType == ProviderGitHubCopilotSubscription {
			adapter, ok := resolveTypedAdapter[*CopilotSubscriptionAdapter](s.adapterRegistry, definition.providerType)
			if !ok {
				return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "Subscription adapter is unavailable")
			}
			if _, err := adapter.session(ctx, credentials, payload.Force); err != nil {
				return pluginmeta.ActionResult{}, err
			}
		}
		return pluginmeta.ActionResult{Data: map[string]any{"credential_summary": providerAccountCredentialSummary(credentials)}}, nil
	}
	return pluginmeta.ActionResult{}, NewHTTPError(http.StatusNotFound, "provider_action_not_found", "Subscription action not found")
}
