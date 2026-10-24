package server

import (
	"encoding/json"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) cancelSubscriptionOAuth(definition builtinSubscriptionDefinition, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	var input struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	}
	if json.Unmarshal(invocation.Payload, &input) != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_plugin_action_payload", "Plugin action payload is invalid")
	}
	invalid := NewHTTPError(400, "invalid_oauth_state", "Subscription OAuth session is invalid or expired")
	session, ok, err := s.store.GetProviderAccountOAuthSessionByState(strings.TrimSpace(input.State))
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	if !ok || session.ID != input.SessionID || session.ClientID != "tokenhub:subscription:"+definition.providerType {
		return pluginmeta.ActionResult{}, invalid
	}
	var secret subscriptionOAuthSecret
	if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || secret.ActorID != invocation.Actor.ID || !secret.ExpiresAt.After(time.Now()) {
		return pluginmeta.ActionResult{}, invalid
	}
	if _, ok, err := s.store.ConsumeProviderAccountOAuthSession(input.SessionID, input.State); err != nil {
		return pluginmeta.ActionResult{}, err
	} else if !ok {
		return pluginmeta.ActionResult{}, invalid
	}
	return pluginmeta.ActionResult{Data: map[string]any{"status": "cancelled"}}, nil
}
