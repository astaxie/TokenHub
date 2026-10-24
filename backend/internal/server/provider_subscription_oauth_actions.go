package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type subscriptionOAuthSecret struct {
	ActorID        string    `json:"actor_id"`
	Verifier       string    `json:"verifier,omitempty"`
	Nonce          string    `json:"nonce,omitempty"`
	OrganizationID string    `json:"organization_id,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	ClientSecret   string    `json:"client_secret,omitempty"`
	DeviceCode     string    `json:"device_code,omitempty"`
	Interval       int       `json:"interval,omitempty"`
	NextPollAt     time.Time `json:"next_poll_at,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func (s *Server) startSubscriptionOAuth(ctx context.Context, definition builtinSubscriptionDefinition, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	var request struct {
		RedirectURI    string            `json:"redirect_uri"`
		OrganizationID string            `json:"organization_id"`
		ClientID       string            `json:"client_id"`
		ClientSecret   string            `json:"client_secret"`
		Inputs         map[string]string `json:"inputs"`
	}
	if len(invocation.Payload) > 0 && json.Unmarshal(invocation.Payload, &request) != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_plugin_action_payload", "Plugin action payload is invalid")
	}
	state, err := randomHex(32)
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	verifier, err := randomHex(32)
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	nonce, err := randomHex(32)
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	now := time.Now().UTC()
	secret := subscriptionOAuthSecret{ActorID: invocation.Actor.ID, Verifier: verifier, Nonce: nonce, OrganizationID: strings.TrimSpace(request.OrganizationID), ExpiresAt: now.Add(15 * time.Minute)}
	session := providerAccountOAuthSession{ID: NewID("subscription_oauth"), State: state, ClientID: "tokenhub:subscription:" + definition.providerType, RedirectURI: firstNonEmpty(strings.TrimSpace(request.RedirectURI), definition.redirectURI), CreatedAt: now}
	var authURL string
	result := map[string]any{"flow": "authorization_code", "session_id": session.ID, "state": state, "redirect_uri": session.RedirectURI, "expires_at": secret.ExpiresAt.Format(time.RFC3339)}
	switch definition.providerType {
	case ProviderGitHubCopilotSubscription:
		adapter, ok := resolveTypedAdapter[*CopilotSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "Copilot adapter is unavailable")
		}
		authorization, err := adapter.StartDeviceAuthorization(ctx)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		secret.Verifier, secret.Nonce = "", ""
		secret.DeviceCode, secret.Interval = authorization.DeviceCode, authorization.Interval
		secret.ExpiresAt = now.Add(time.Duration(authorization.ExpiresIn) * time.Second)
		secret.NextPollAt = now.Add(time.Duration(secret.Interval) * time.Second)
		authURL = authorization.VerificationURI
		result["flow"], result["user_code"], result["poll_interval"] = "device_code", authorization.UserCode, secret.Interval
		result["expires_at"] = secret.ExpiresAt.Format(time.RFC3339)
	case ProviderChatGPTSubscription:
		host, hostErr := randomHex(16)
		if hostErr != nil {
			return pluginmeta.ActionResult{}, hostErr
		}
		hostID := "urn:uuid:" + host[:8] + "-" + host[8:12] + "-4" + host[13:16] + "-a" + host[17:20] + "-" + host[20:]
		authURL, err = ChatGPTSubscriptionAuthorizeURL(session.RedirectURI, state, nonce, verifier, hostID)
	default:
		secret.ClientID = strings.TrimSpace(firstNonEmpty(request.ClientID, request.Inputs["client_id"]))
		secret.ClientSecret = strings.TrimSpace(firstNonEmpty(request.ClientSecret, request.Inputs["client_secret"]))
		if !googleSubscriptionClientCredentialsValid(secret.ClientID, secret.ClientSecret) {
			return pluginmeta.ActionResult{}, googleSubscriptionClientCredentialsRequired()
		}
		authURL, err = GoogleSubscriptionAuthorizeURL(definition.providerType, session.RedirectURI, state, verifier, secret.ClientID)
	}
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	encoded, err := json.Marshal(secret)
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	session.CodeVerifier = string(encoded)
	if err := s.store.SaveProviderAccountOAuthSession(session); err != nil {
		return pluginmeta.ActionResult{}, err
	}
	result["auth_url"] = authURL
	return pluginmeta.ActionResult{Data: result, RedirectURL: authURL}, nil
}

func (s *Server) exchangeSubscriptionOAuth(ctx context.Context, definition builtinSubscriptionDefinition, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	var request struct {
		SessionID   string `json:"session_id"`
		State       string `json:"state"`
		Code        string `json:"code"`
		ClientID    string `json:"client_id"`
		RedirectURI string `json:"redirect_uri"`
	}
	if json.Unmarshal(invocation.Payload, &request) != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_plugin_action_payload", "Plugin action payload is invalid")
	}
	session, ok, err := s.store.GetProviderAccountOAuthSessionByState(strings.TrimSpace(request.State))
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	invalid := NewHTTPError(400, "invalid_oauth_state", "Subscription OAuth session is invalid or expired")
	if !ok || session.ID != request.SessionID || session.ClientID != "tokenhub:subscription:"+definition.providerType {
		return pluginmeta.ActionResult{}, invalid
	}
	var secret subscriptionOAuthSecret
	if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || secret.ActorID != invocation.Actor.ID || !secret.ExpiresAt.After(time.Now()) {
		return pluginmeta.ActionResult{}, invalid
	}
	if request.RedirectURI != "" && request.RedirectURI != session.RedirectURI {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "subscription_oauth_redirect_mismatch", "OAuth callback URL does not match the session")
	}
	if (definition.providerType == providerGeminiCLISubscription || definition.providerType == providerAntigravitySubscription) && request.ClientID != "" && request.ClientID != secret.ClientID {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "subscription_oauth_client_mismatch", "OAuth client does not match the session")
	}
	if definition.providerType == ProviderGitHubCopilotSubscription && time.Now().Before(secret.NextPollAt) {
		return subscriptionOAuthPending("pending", secret.Interval), nil
	}
	if definition.providerType != ProviderGitHubCopilotSubscription && strings.TrimSpace(request.Code) == "" {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "missing_oauth_code", "OAuth authorization code is required")
	}
	session, ok, err = s.store.ConsumeProviderAccountOAuthSession(request.SessionID, request.State)
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	if !ok {
		return pluginmeta.ActionResult{}, invalid
	}
	// Re-read the atomically consumed value. A concurrent device poll may have
	// updated its interval before this exchange acquired the session.
	if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || secret.ActorID != invocation.Actor.ID || !secret.ExpiresAt.After(time.Now()) {
		return pluginmeta.ActionResult{}, invalid
	}
	if definition.providerType == ProviderGitHubCopilotSubscription && time.Now().Before(secret.NextPollAt) {
		if err := s.store.SaveProviderAccountOAuthSession(session); err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return subscriptionOAuthPending("pending", secret.Interval), nil
	}
	var credentials ProviderResourceCredentials
	switch definition.providerType {
	case ProviderGitHubCopilotSubscription:
		adapter, ok := resolveTypedAdapter[*CopilotSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "Copilot adapter is unavailable")
		}
		var status string
		credentials, status, err = adapter.PollDeviceAuthorization(ctx, secret.DeviceCode)
		if err == nil && (status == "pending" || status == "slow_down") {
			if status == "slow_down" {
				secret.Interval += 5
			}
			secret.NextPollAt = time.Now().Add(time.Duration(secret.Interval) * time.Second)
			encoded, _ := json.Marshal(secret)
			session.CodeVerifier = string(encoded)
			if err := s.store.SaveProviderAccountOAuthSession(session); err != nil {
				return pluginmeta.ActionResult{}, err
			}
			return subscriptionOAuthPending(status, secret.Interval), nil
		}
	case ProviderChatGPTSubscription:
		adapter, ok := resolveTypedAdapter[*ChatGPTSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "ChatGPT adapter is unavailable")
		}
		credentials, err = adapter.ExchangeAuthorization(ctx, ChatGPTSubscriptionAuthorization{Code: request.Code, ClientID: request.ClientID, Verifier: secret.Verifier, Nonce: secret.Nonce, RedirectURI: session.RedirectURI})
	default:
		adapter, ok := resolveTypedAdapter[*GoogleSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "Google subscription adapter is unavailable")
		}
		credentials, err = adapter.ExchangeAuthorization(ctx, request.Code, secret.Verifier, session.RedirectURI, secret.ClientID, secret.ClientSecret)
		credentials.OrganizationID = secret.OrganizationID
	}
	if err != nil {
		return pluginmeta.ActionResult{}, err
	}
	return pluginmeta.ActionResult{Data: s.subscriptionOAuthExchangeCatalog(ctx, definition, credentials)}, nil
}

func subscriptionOAuthPending(status string, interval int) pluginmeta.ActionResult {
	return pluginmeta.ActionResult{Data: map[string]any{"status": status, "poll_interval": interval}}
}
