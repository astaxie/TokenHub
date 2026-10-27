package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

func (a *GoogleSubscriptionAdapter) DefaultOAuthRedirectURI() string {
	if a.antigravity() {
		return "http://localhost:51121/oauth-callback"
	}
	return "http://127.0.0.1:8085/oauth2callback"
}

// GoogleSubscriptionAuthorizeURL uses the OAuth client resolved for this session.
// The caller owns one-time state storage and callback validation.
func GoogleSubscriptionAuthorizeURL(providerType, redirectURI, state, verifier, clientID string) (string, error) {
	if (providerType != providerGeminiCLISubscription && providerType != providerAntigravitySubscription) || state == "" || !googleSubscriptionOAuthInput(redirectURI, verifier) {
		return "", NewHTTPError(400, "subscription_oauth_invalid", "Google OAuth session is incomplete")
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || len(clientID) > 4096 {
		return "", googleSubscriptionClientCredentialsRequired()
	}
	a := GoogleSubscriptionAdapter{ProviderType: providerType}
	scopes := []string{"https://www.googleapis.com/auth/cloud-platform", "https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile"}
	if a.antigravity() {
		scopes = append(scopes, "https://www.googleapis.com/auth/cclog", "https://www.googleapis.com/auth/experimentsandconfigs")
	}
	query := url.Values{"client_id": {clientID}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "scope": {strings.Join(scopes, " ")}, "access_type": {"offline"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {openAICodeChallenge(verifier)}, "code_challenge_method": {"S256"}}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + query.Encode(), nil
}

func googleSubscriptionOAuthInput(redirectURI, verifier string) bool {
	target, err := url.Parse(redirectURI)
	if err != nil || target.Scheme != "http" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Hostname() != "127.0.0.1" && target.Hostname() != "localhost" && target.Hostname() != "::1") || len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, char := range verifier {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~", char) {
			continue
		}
		return false
	}
	return true
}

func (a *GoogleSubscriptionAdapter) ExchangeAuthorization(ctx context.Context, code, verifier, redirectURI, clientID, clientSecret string) (ProviderResourceCredentials, error) {
	if strings.TrimSpace(code) == "" || !googleSubscriptionOAuthInput(redirectURI, verifier) {
		return ProviderResourceCredentials{}, NewHTTPError(400, "subscription_oauth_invalid", "Google OAuth callback is incomplete")
	}
	if a.Client == nil {
		return ProviderResourceCredentials{}, NewHTTPError(503, "google_subscription_client_unavailable", "Google subscription HTTP client is unavailable")
	}
	clientID, clientSecret = strings.TrimSpace(clientID), strings.TrimSpace(clientSecret)
	if !googleSubscriptionClientCredentialsValid(clientID, clientSecret) {
		return ProviderResourceCredentials{}, googleSubscriptionClientCredentialsRequired()
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI}, "client_id": {clientID}, "client_secret": {clientSecret}}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, firstNonEmpty(a.TokenURL, googleSubscriptionTokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return ProviderResourceCredentials{}, NewHTTPError(500, "google_subscription_token_url_invalid", "Google subscription token endpoint is invalid")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := subscriptionClient(a.Client).Do(req)
	if err != nil {
		if callCtx.Err() != nil {
			return ProviderResourceCredentials{}, callCtx.Err()
		}
		if guarded := providerEgressFailure(err); guarded != nil {
			return ProviderResourceCredentials{}, guarded
		}
		return ProviderResourceCredentials{}, NewHTTPError(502, "subscription_oauth_exchange_failed", "Google OAuth code exchange failed")
	}
	defer response.Body.Close()
	var token oauthTokenResponse
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
	if response.StatusCode != 200 || readErr != nil || len(data) > 65536 || json.Unmarshal(data, &token) != nil || token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 || token.ExpiresIn > 31*86400 {
		return ProviderResourceCredentials{}, NewHTTPError(502, "subscription_oauth_exchange_failed", "Google did not return usable OAuth credentials")
	}
	credentials := ProviderResourceCredentials{AuthType: "oauth", ClientID: clientID, ClientSecret: clientSecret, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, IDToken: token.IDToken, TokenType: "Bearer", Scopes: token.Scope, ExpiresAt: time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)}
	return a.oauthIdentity(callCtx, credentials)
}

func (a *GoogleSubscriptionAdapter) oauthIdentity(ctx context.Context, credentials ProviderResourceCredentials) (ProviderResourceCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, firstNonEmpty(a.UserInfoURL, "https://www.googleapis.com/oauth2/v2/userinfo?alt=json"), nil)
	if err != nil {
		return ProviderResourceCredentials{}, NewHTTPError(500, "google_subscription_userinfo_url_invalid", "Google identity endpoint is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	response, err := subscriptionClient(a.Client).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ProviderResourceCredentials{}, ctx.Err()
		}
		if guarded := providerEgressFailure(err); guarded != nil {
			return ProviderResourceCredentials{}, guarded
		}
		return ProviderResourceCredentials{}, NewHTTPError(502, "subscription_identity_invalid", "Google account identity could not be verified")
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
	var identity struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Verified bool   `json:"verified_email"`
	}
	if response.StatusCode != 200 || readErr != nil || len(data) > 65536 || json.Unmarshal(data, &identity) != nil || identity.ID == "" || !identity.Verified {
		return ProviderResourceCredentials{}, NewHTTPError(502, "subscription_identity_invalid", "Google did not return a verified account identity")
	}
	address, err := mail.ParseAddress(identity.Email)
	if err != nil || address.Address != identity.Email {
		return ProviderResourceCredentials{}, NewHTTPError(502, "subscription_identity_invalid", "Google did not return a valid account email")
	}
	credentials.Email = identity.Email
	credentials.AccountID = identity.ID
	credentials.UserID = identity.ID
	return credentials, nil
}

func googleSubscriptionClientCredentialsValid(clientID, clientSecret string) bool {
	return strings.TrimSpace(clientID) != "" && len(clientID) <= 4096 && strings.TrimSpace(clientSecret) != "" && len(clientSecret) <= 16384
}

func googleSubscriptionClientCredentialsRequired() error {
	return NewHTTPError(400, "google_subscription_oauth_client_required", "Supply an approved Google OAuth client ID and client secret for this subscription service")
}
