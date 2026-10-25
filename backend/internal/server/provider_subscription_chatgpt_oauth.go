package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// ChatGPTSubscriptionAuthorizeURL builds the dynamic-client authorization URL.
// The caller owns one-time state, PKCE verifier and nonce storage and validation.
func ChatGPTSubscriptionAuthorizeURL(redirectURI, state, nonce, verifier, hostID string) (string, error) {
	if !chatGPTSubscriptionRedirect(redirectURI) || state == "" || nonce == "" || len(verifier) < 43 || !strings.HasPrefix(hostID, "urn:uuid:") {
		return "", NewHTTPError(400, "subscription_oauth_invalid", "ChatGPT API OAuth session is incomplete")
	}
	query := url.Values{"client_id": {"dynamic_agent_client"}, "agent_name_hint": {"TokenHub"}, "ext_agent_host_id": {hostID}, "response_type": {"code"},
		"redirect_uri": {redirectURI}, "resource": {chatGPTSubscriptionBaseURL}, "scope": {"openid profile email offline_access resource.invoke " + chatGPTSubscriptionDirectScope},
		"state": {state}, "nonce": {nonce}, "code_challenge": {openAICodeChallenge(verifier)}, "code_challenge_method": {"S256"}}
	return "https://auth.openai.com/api/accounts/authorize?" + query.Encode(), nil
}

func chatGPTSubscriptionRedirect(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "http" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")
}

type ChatGPTSubscriptionAuthorization struct {
	Code        string
	ClientID    string
	Verifier    string
	RedirectURI string
	Nonce       string
}

func (a *ChatGPTSubscriptionAdapter) ExchangeAuthorization(ctx context.Context, authorization ChatGPTSubscriptionAuthorization) (ProviderResourceCredentials, error) {
	if authorization.Code == "" || authorization.ClientID == "" || authorization.ClientID == "dynamic_agent_client" || len(authorization.Verifier) < 43 || authorization.Nonce == "" || !chatGPTSubscriptionRedirect(authorization.RedirectURI) {
		return ProviderResourceCredentials{}, NewHTTPError(400, "subscription_oauth_invalid", "ChatGPT API OAuth callback is incomplete")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {authorization.Code}, "client_id": {authorization.ClientID},
		"code_verifier": {authorization.Verifier}, "redirect_uri": {authorization.RedirectURI}, "resource": {chatGPTSubscriptionBaseURL}}
	token, err := a.token(ctx, form)
	if err != nil {
		return ProviderResourceCredentials{}, err
	}
	credentials, err := chatGPTSubscriptionTokenCredentials(token, ProviderResourceCredentials{ClientID: authorization.ClientID})
	if err != nil {
		return ProviderResourceCredentials{}, err
	}
	claims, err := chatGPTSubscriptionIDClaims(token.IDToken, authorization.ClientID, authorization.Nonce)
	if err != nil {
		return ProviderResourceCredentials{}, err
	}
	credentials.Email, _ = claims["email"].(string)
	credentials.UserID, _ = claims["sub"].(string)
	if credentials.UserID == "" {
		return ProviderResourceCredentials{}, NewHTTPError(502, "subscription_identity_invalid", "ChatGPT API did not return an account identity")
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		credentials.PlanType, _ = auth["chatgpt_plan_type"].(string)
	}
	return credentials, nil
}

func (a *ChatGPTSubscriptionAdapter) Refresh(ctx context.Context, current ProviderResourceCredentials) (ProviderResourceCredentials, error) {
	if current.ClientID == "" || current.ClientID == "dynamic_agent_client" || current.RefreshToken == "" {
		return current, NewHTTPError(400, "subscription_refresh_credentials_missing", "ChatGPT API refresh token and registered client ID are required")
	}
	token, err := a.token(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {current.ClientID}, "refresh_token": {current.RefreshToken}, "resource": {chatGPTSubscriptionBaseURL}})
	if err != nil {
		return current, subscriptionCredentialError(err)
	}
	return chatGPTSubscriptionTokenCredentials(token, current)
}

func (a *ChatGPTSubscriptionAdapter) token(ctx context.Context, form url.Values) (oauthTokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, firstNonEmpty(a.TokenURL, chatGPTSubscriptionTokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := subscriptionClient(a.CredentialRefreshClient).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return oauthTokenResponse{}, ctx.Err()
		}
		if guarded := providerEgressFailure(err); guarded != nil {
			return oauthTokenResponse{}, guarded
		}
		return oauthTokenResponse{}, NewHTTPError(502, "subscription_token_request_failed", "ChatGPT API token request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return oauthTokenResponse{}, NewHTTPError(502, "subscription_token_invalid", "ChatGPT API token response is invalid")
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if slices.Contains([]string{"invalid_grant", "invalid_client", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused"}, failure.Error) {
			return oauthTokenResponse{}, NewHTTPError(409, "provider_resource_reauthorization_required", "ChatGPT API account must be authorized again")
		}
		return oauthTokenResponse{}, NewHTTPError(502, "subscription_token_request_failed", "ChatGPT API token endpoint refused the request")
	}
	var token oauthTokenResponse
	if json.Unmarshal(data, &token) != nil {
		return token, NewHTTPError(502, "subscription_token_invalid", "ChatGPT API token response is invalid")
	}
	return token, nil
}

func chatGPTSubscriptionTokenCredentials(token oauthTokenResponse, current ProviderResourceCredentials) (ProviderResourceCredentials, error) {
	if token.AccessToken == "" || token.ExpiresIn <= 0 || token.ExpiresIn > 365*24*60*60 || firstNonEmpty(token.RefreshToken, current.RefreshToken) == "" {
		return current, NewHTTPError(502, "subscription_token_invalid", "ChatGPT API did not return usable tokens")
	}
	scope := firstNonEmpty(token.Scope, current.Scopes)
	if !slices.Contains(strings.Fields(scope), chatGPTSubscriptionDirectScope) {
		return current, NewHTTPError(403, "subscription_scope_missing", "ChatGPT account is not authorized for direct API subscription access")
	}
	current.AuthType, current.TokenType = "oauth", "Bearer"
	current.AccessToken, current.RefreshToken = token.AccessToken, firstNonEmpty(token.RefreshToken, current.RefreshToken)
	current.IDToken = firstNonEmpty(token.IDToken, current.IDToken)
	current.Scopes = scope
	current.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	return current, nil
}

// The ID token comes directly from the authenticated TLS token exchange. These
// checks bind that response to this app and this OAuth session; it is never used
// as a TokenHub login or as an authorization credential for TokenHub itself.
func chatGPTSubscriptionIDClaims(token, clientID, nonce string) (map[string]any, error) {
	invalid := NewHTTPError(502, "subscription_identity_invalid", "ChatGPT API returned an invalid OAuth identity")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, invalid
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return nil, invalid
	}
	issuer, _ := claims["iss"].(string)
	actualNonce, _ := claims["nonce"].(string)
	expires, _ := claims["exp"].(float64)
	if issuer != "https://auth.openai.com" || expires <= float64(time.Now().Unix()) || nonce == "" || subtle.ConstantTimeCompare([]byte(actualNonce), []byte(nonce)) != 1 {
		return nil, invalid
	}
	audience := claims["aud"] == clientID
	if audiences, ok := claims["aud"].([]any); ok {
		audience = slices.Contains(audiences, any(clientID))
	}
	if !audience {
		return nil, invalid
	}
	return claims, nil
}
