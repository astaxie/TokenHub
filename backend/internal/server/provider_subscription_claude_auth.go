package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type claudeSubscriptionImportSecret struct {
	ActorID     string    `json:"actor_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	AccessToken string    `json:"access_token"`
}

func registerBuiltinClaudeSubscriptionAuthorization(server *Server, actions builtinActionRegistrar) {
	for _, capability := range []string{"oauth.cancel", "oauth.exchange", "oauth.methods", "oauth.start"} {
		metadata := map[string]string{"provider_resource_type": providerResourceClaudeSubscription}
		properties := map[string]string{"session_id": "string", "state": "string", "mode": "string", "session_json": "string", "key": "string", "inputs": "object", "method_id": "string"}
		kind := pluginmeta.ActionKindMutate
		if capability == "oauth.methods" {
			kind = pluginmeta.ActionKindRead
			properties = nil
		}
		if capability == "oauth.start" {
			metadata["subscription_auth"] = "true"
			metadata["subscription_session_json"] = "true"
		}
		if capability == "oauth.exchange" {
			metadata["result_secret_policy"] = "provider_account_credentials"
		}
		descriptor := pluginmeta.ActionDescriptor{PluginID: "tokenhub.provider.claude-subscription", ActionID: providerClaudeSubscription + "." + capability, Kind: kind, Title: "Claude Code " + capability, Capability: capability, Subject: providerClaudeSubscription, InputSchema: actionObjectSchema(nil, properties), OutputSchema: actionObjectSchema(nil, nil), Metadata: metadata}
		mustRegisterPluginAction(actions, descriptor, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
			return server.executeClaudeSubscriptionAuthorization(capability, invocation)
		}))
	}
}

func (s *Server) executeClaudeSubscriptionAuthorization(capability string, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	if strings.TrimSpace(invocation.Actor.ID) == "" {
		return pluginmeta.ActionResult{}, NewHTTPError(403, "subscription_actor_required", "An authenticated administrator is required")
	}
	if capability == "oauth.methods" {
		return pluginmeta.ActionResult{Data: map[string]any{"methods": []any{map[string]any{"id": "token", "label": "Claude Code setup token", "type": "api", "prompts": []any{map[string]any{"key": "key", "label": "Claude Code subscription token", "type": "password"}}}}}}, nil
	}
	definition := builtinSubscriptionDefinition{providerType: providerClaudeSubscription}
	if capability == "oauth.cancel" {
		return s.cancelSubscriptionOAuth(definition, invocation)
	}
	var input struct {
		SessionID   string            `json:"session_id"`
		State       string            `json:"state"`
		Mode        string            `json:"mode"`
		SessionJSON string            `json:"session_json"`
		Key         string            `json:"key"`
		Inputs      map[string]string `json:"inputs"`
	}
	if json.Unmarshal(invocation.Payload, &input) != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_plugin_action_payload", "Plugin action payload is invalid")
	}
	if capability == "oauth.start" {
		token := strings.TrimSpace(firstNonEmpty(input.Key, input.Inputs["key"]))
		if token == "" || len(token) > 16<<10 {
			return pluginmeta.ActionResult{}, NewHTTPError(400, "claude_subscription_credentials_required", "Import a Claude Code subscription token or account session")
		}
		state, err := randomHex(32)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		now := time.Now().UTC()
		expires := now.Add(5 * time.Minute)
		encoded, _ := json.Marshal(claudeSubscriptionImportSecret{ActorID: invocation.Actor.ID, ExpiresAt: expires, AccessToken: token})
		session := providerAccountOAuthSession{ID: NewID("claude_import"), State: state, CodeVerifier: string(encoded), ClientID: "tokenhub:subscription:" + providerClaudeSubscription, CreatedAt: now}
		if err := s.store.SaveProviderAccountOAuthSession(session); err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: map[string]any{"flow": "auto", "session_id": session.ID, "state": state, "poll_interval": 2, "expires_at": expires.Format(time.RFC3339)}}, nil
	}
	if input.Mode == "import" {
		if err := validateClaudeSubscriptionSession(input.SessionJSON); err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: map[string]any{"status": "complete", "session_json": input.SessionJSON, "catalog": claudeSubscriptionCatalog()}}, nil
	}
	invalid := NewHTTPError(400, "invalid_oauth_state", "Subscription OAuth session is invalid or expired")
	session, ok, err := s.store.GetProviderAccountOAuthSessionByState(input.State)
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	if !ok || session.ID != input.SessionID || session.ClientID != "tokenhub:subscription:"+providerClaudeSubscription {
		return pluginmeta.ActionResult{}, invalid
	}
	var secret claudeSubscriptionImportSecret
	if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || secret.ActorID != invocation.Actor.ID || !secret.ExpiresAt.After(time.Now()) {
		return pluginmeta.ActionResult{}, invalid
	}
	if _, ok, err := s.store.ConsumeProviderAccountOAuthSession(input.SessionID, input.State); err != nil {
		return pluginmeta.ActionResult{}, err
	} else if !ok {
		return pluginmeta.ActionResult{}, invalid
	}
	return pluginmeta.ActionResult{Data: map[string]any{"status": "complete", "access_token": secret.AccessToken, "token_type": "Bearer", "catalog": claudeSubscriptionCatalog()}}, nil
}
