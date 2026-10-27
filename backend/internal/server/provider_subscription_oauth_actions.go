package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type subscriptionOAuthSecret struct {
	ActorID           string                       `json:"actor_id"`
	Verifier          string                       `json:"verifier,omitempty"`
	Nonce             string                       `json:"nonce,omitempty"`
	OrganizationID    string                       `json:"organization_id,omitempty"`
	ClientID          string                       `json:"client_id,omitempty"`
	ClientSecret      string                       `json:"client_secret,omitempty"`
	GitHubHost        string                       `json:"github_host,omitempty"`
	DeviceCode        string                       `json:"device_code,omitempty"`
	Interval          int                          `json:"interval,omitempty"`
	NextPollAt        time.Time                    `json:"next_poll_at,omitempty"`
	ExpiresAt         time.Time                    `json:"expires_at"`
	AutomaticCallback bool                         `json:"automatic_callback,omitempty"`
	Callback          *chatGPTSubscriptionCallback `json:"callback,omitempty"`
	RegistrationID    string                       `json:"registration_id,omitempty"`
	HostID            string                       `json:"host_id,omitempty"`
}

func (s *Server) startSubscriptionOAuth(ctx context.Context, definition builtinSubscriptionDefinition, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	var request struct {
		MethodID          string            `json:"method_id"`
		RedirectURI       string            `json:"redirect_uri"`
		OrganizationID    string            `json:"organization_id"`
		ClientID          string            `json:"client_id"`
		ClientSecret      string            `json:"client_secret"`
		GitHubHost        string            `json:"github_host"`
		Inputs            map[string]string `json:"inputs"`
		AutomaticCallback bool              `json:"automatic_callback"`
		RegistrationID    string            `json:"registration_id"`
		ResourceID        string            `json:"resource_id"`
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
	secret := subscriptionOAuthSecret{ActorID: invocation.Actor.ID, Verifier: verifier, Nonce: nonce, ExpiresAt: now.Add(15 * time.Minute)}
	session := providerAccountOAuthSession{ID: NewID("subscription_oauth"), State: state, ClientID: "tokenhub:subscription:" + definition.providerType, RedirectURI: firstNonEmpty(strings.TrimSpace(request.RedirectURI), definition.redirectURI), CreatedAt: now}
	var authURL string
	result := map[string]any{"flow": "authorization_code", "session_id": session.ID, "state": state, "redirect_uri": session.RedirectURI, "expires_at": secret.ExpiresAt.Format(time.RFC3339)}
	switch definition.providerType {
	case ProviderGitHubCopilotSubscription:
		adapter, ok := resolveTypedAdapter[*CopilotSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "Copilot adapter is unavailable")
		}
		secret.GitHubHost, err = normalizeCopilotSubscriptionHost(firstNonEmpty(request.GitHubHost, request.Inputs["github_host"]))
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		authorization, err := adapter.StartDeviceAuthorizationAt(ctx, secret.GitHubHost)
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
		if callbackURL := chatGPTSubscriptionAutomaticRedirect(s.config.PublicBaseURL); request.AutomaticCallback && callbackURL != "" {
			if _, supported := s.store.(chatGPTSubscriptionCallbackStore); supported {
				secret.AutomaticCallback = true
				session.RedirectURI = callbackURL
				result["flow"], result["callback_mode"], result["poll_interval"] = "auto", "none", 2
				result["redirect_uri"] = callbackURL
			}
		}
		registrations, supported := s.store.(chatGPTSubscriptionRegistrationStore)
		if !supported {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "subscription_oauth_registration_unavailable", "ChatGPT API registration storage is unavailable")
		}
		var registration chatGPTSubscriptionRegistration
		var registrationErr error
		if resourceID := strings.TrimSpace(request.ResourceID); resourceID != "" {
			registration, registrationErr = registrations.chatGPTSubscriptionRegistrationForResource(ctx, invocation.Actor.ID, strings.TrimSpace(request.RegistrationID), resourceID)
		} else {
			registration, registrationErr = registrations.chatGPTSubscriptionRegistration(ctx, invocation.Actor.ID, strings.TrimSpace(request.RegistrationID))
		}
		if registrationErr != nil {
			return pluginmeta.ActionResult{}, registrationErr
		}
		secret.RegistrationID, secret.HostID, secret.ClientID = registration.ID, registration.HostID, registration.ClientID
		result["registration_id"] = registration.ID
		authURL, err = ChatGPTSubscriptionAuthorizeURL(session.RedirectURI, state, nonce, verifier, registration.HostID, registration.ClientID)
	default:
		secret.OrganizationID = firstNonEmpty(strings.TrimSpace(request.Inputs["organization_id"]), strings.TrimSpace(request.OrganizationID))
		secret.ClientID, secret.ClientSecret, err = resolveGoogleSubscriptionOAuthClient(definition.providerType, request.MethodID,
			firstNonEmpty(request.ClientID, request.Inputs["client_id"]), firstNonEmpty(request.ClientSecret, request.Inputs["client_secret"]))
		if err != nil {
			return pluginmeta.ActionResult{}, err
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
	automatic := definition.providerType == ProviderChatGPTSubscription && secret.AutomaticCallback
	if automatic && secret.Callback == nil {
		return subscriptionOAuthPending("pending", 2), nil
	}
	if definition.providerType != ProviderGitHubCopilotSubscription && !automatic && strings.TrimSpace(request.Code) == "" {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "missing_oauth_code", "OAuth authorization code is required")
	}
	if definition.providerType == ProviderChatGPTSubscription && !automatic {
		request.ClientID, err = chatGPTSubscriptionCallbackClientID(secret.ClientID, request.ClientID)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
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
		credentials, status, err = adapter.PollDeviceAuthorizationAt(ctx, secret.DeviceCode, secret.GitHubHost)
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
		if secret.AutomaticCallback {
			if secret.Callback == nil {
				return pluginmeta.ActionResult{}, invalid
			}
			if secret.Callback.Denied {
				return pluginmeta.ActionResult{}, NewHTTPError(409, "subscription_oauth_authorization_denied", "ChatGPT API authorization was cancelled or denied. Start authorization again.")
			}
			request.Code, request.ClientID = secret.Callback.Code, secret.Callback.ClientID
		}
		adapter, ok := resolveTypedAdapter[*ChatGPTSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "provider_adapter_missing", "ChatGPT adapter is unavailable")
		}
		request.ClientID, err = chatGPTSubscriptionCallbackClientID(secret.ClientID, request.ClientID)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		registrations, supported := s.store.(chatGPTSubscriptionRegistrationStore)
		if !supported {
			return pluginmeta.ActionResult{}, NewHTTPError(503, "subscription_oauth_registration_unavailable", "ChatGPT API registration storage is unavailable")
		}
		if err := registrations.bindChatGPTSubscriptionRegistration(ctx, secret.ActorID, secret.RegistrationID, secret.HostID, request.ClientID, ""); err != nil {
			return pluginmeta.ActionResult{}, err
		}
		credentials, err = adapter.ExchangeAuthorization(ctx, ChatGPTSubscriptionAuthorization{Code: request.Code, ClientID: request.ClientID, Verifier: secret.Verifier, Nonce: secret.Nonce, RedirectURI: session.RedirectURI})
		if err != nil && secret.ClientID == "" && AsHTTPError(err).Code == "subscription_oauth_code_rejected" {
			return pluginmeta.ActionResult{}, NewHTTPError(409, "subscription_oauth_registration_retry", "ChatGPT API registration was saved, but its initial authorization code was rejected. Open authorization again to continue with the saved registration.")
		}
		if err == nil {
			err = registrations.bindChatGPTSubscriptionRegistration(ctx, secret.ActorID, secret.RegistrationID, secret.HostID, request.ClientID, credentials.UserID)
		}
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
