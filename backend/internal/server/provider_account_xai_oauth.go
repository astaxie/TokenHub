package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	xaiOAuthIssuer             = "https://auth.x.ai"
	xaiOAuthClientID           = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiOAuthScope              = "openid profile email offline_access grok-cli:access api:access"
	xaiOAuthDeviceGrantType    = "urn:ietf:params:oauth:grant-type:device_code"
	xaiAccountOAuthSessionTTL  = 30 * time.Minute
	xaiAccountOAuthRefreshLead = 5 * time.Minute
	xaiDefaultDevicePoll       = 5 * time.Second
)

var xaiOIDCDiscoveryURL = xaiOAuthIssuer + "/.well-known/openid-configuration"

type xaiOAuthDiscovery struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
}

type xaiDeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	TokenEndpoint           string `json:"-"`
}

type xaiGrokOAuthStartResponse struct {
	SessionID               string `json:"session_id"`
	State                   string `json:"state"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresAt               string `json:"expires_at"`
	IntervalSeconds         int    `json:"interval_seconds"`
}

type xaiGrokOAuthPollRequest struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

type xaiGrokOAuthPollResponse struct {
	Status string `json:"status"`
	providerAccountOAuthTokenInfo
}

func (s *Server) handleAdminXAIAccountOAuthStartDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	device, err := startXAIDeviceFlow(r.Context(), s.upstreamClient)
	if err != nil {
		writeError(w, r, err)
		return
	}
	state, err := randomHex(32)
	if err != nil {
		writeError(w, r, err)
		return
	}
	sessionID, err := randomHex(16)
	if err != nil {
		writeError(w, r, err)
		return
	}
	expiresAt := time.Now().UTC().Add(xaiAccountOAuthSessionTTL)
	if device.ExpiresIn > 0 {
		deviceExpiry := time.Now().UTC().Add(time.Duration(device.ExpiresIn) * time.Second)
		if deviceExpiry.Before(expiresAt) {
			expiresAt = deviceExpiry
		}
	}
	session := providerAccountOAuthSession{
		ID:           sessionID,
		State:        state,
		CodeVerifier: device.DeviceCode,
		ClientID:     xaiOAuthClientID,
		RedirectURI:  device.TokenEndpoint,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.store.SaveProviderAccountOAuthSession(session); err != nil {
		writeError(w, r, err)
		return
	}
	interval := device.Interval
	if interval <= 0 {
		interval = int(xaiDefaultDevicePoll / time.Second)
	}
	verificationURI := firstNonEmpty(device.VerificationURIComplete, device.VerificationURI)
	s.recordAdminAudit(r, user, "start_oauth_device", "provider_account", "xai", "", map[string]any{
		"verification_host": oauthVerificationHost(verificationURI),
	})
	writeJSON(w, http.StatusOK, xaiGrokOAuthStartResponse{
		SessionID:               sessionID,
		State:                   state,
		UserCode:                device.UserCode,
		VerificationURI:         device.VerificationURI,
		VerificationURIComplete: device.VerificationURIComplete,
		ExpiresAt:               expiresAt.Format(time.RFC3339),
		IntervalSeconds:         interval,
	})
}

func (s *Server) handleAdminXAIAccountOAuthPoll(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	var req xaiGrokOAuthPollRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.State) == "" || strings.TrimSpace(req.SessionID) == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_oauth_state", "OAuth state is invalid or expired"))
		return
	}
	session, ok, err := s.store.GetProviderAccountOAuthSessionByState(req.State)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !ok || session.ID != strings.TrimSpace(req.SessionID) {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "oauth_session_not_found", "OAuth session was not found or has expired"))
		return
	}
	token, pending, pollErr := pollXAIDeviceToken(r.Context(), session.CodeVerifier, session.RedirectURI, s.upstreamClient)
	if pollErr != nil {
		writeError(w, r, pollErr)
		return
	}
	if pending != "" {
		writeJSON(w, http.StatusAccepted, xaiGrokOAuthPollResponse{Status: pending})
		return
	}
	if _, consumed, consumeErr := s.store.ConsumeProviderAccountOAuthSession(session.ID, session.State); consumeErr != nil {
		writeError(w, r, consumeErr)
		return
	} else if !consumed {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "oauth_session_not_found", "OAuth session was not found or has expired"))
		return
	}
	info := xaiAccountOAuthTokenInfoFromResponse(token, session.ClientID, ProviderResourceCredentials{})
	s.recordAdminAudit(r, user, "poll_oauth_device", "provider_account", "xai", "", providerAccountCredentialSummary(info.ToCredentials()))
	writeJSON(w, http.StatusOK, xaiGrokOAuthPollResponse{Status: "authorized", providerAccountOAuthTokenInfo: info})
}

func startXAIDeviceFlow(ctx context.Context, clients ...*http.Client) (*xaiDeviceCodeResponse, error) {
	discovery, err := discoverXAIOAuth(ctx, clients...)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"client_id": {xaiOAuthClientID},
		"scope":     {xaiOAuthScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discovery.DeviceAuthorizationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := xaiOAuthHTTPClient(clients...).Do(req)
	if err != nil {
		if providerErrorDisposition(err) == ProviderErrorEgress {
			return nil, err
		}
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_device_failed", fmt.Sprintf("xAI device authorization failed: %v", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_device_failed", fmt.Sprintf("xAI device authorization returned %d", resp.StatusCode))
	}
	var device xaiDeviceCodeResponse
	if err := json.Unmarshal(body, &device); err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_device_failed", "xAI device authorization response was invalid")
	}
	if strings.TrimSpace(device.DeviceCode) == "" || strings.TrimSpace(device.UserCode) == "" {
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_device_failed", "xAI device authorization response was incomplete")
	}
	if strings.TrimSpace(device.VerificationURI) == "" && strings.TrimSpace(device.VerificationURIComplete) == "" {
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_device_failed", "xAI device authorization response was incomplete")
	}
	device.TokenEndpoint = discovery.TokenEndpoint
	return &device, nil
}

func pollXAIDeviceToken(ctx context.Context, deviceCode, tokenEndpoint string, clients ...*http.Client) (oauthTokenResponse, string, error) {
	form := url.Values{}
	form.Set("grant_type", xaiOAuthDeviceGrantType)
	form.Set("device_code", strings.TrimSpace(deviceCode))
	form.Set("client_id", xaiOAuthClientID)
	token, oauthErr, err := requestXAIOAuthToken(ctx, tokenEndpoint, form, clients...)
	if err != nil {
		return oauthTokenResponse{}, "", err
	}
	switch oauthErr {
	case "", "success":
		if strings.TrimSpace(token.AccessToken) == "" {
			return oauthTokenResponse{}, "", NewHTTPError(http.StatusBadGateway, "oauth_token_missing", "OAuth token endpoint did not return an access token")
		}
		return token, "", nil
	case "authorization_pending":
		return oauthTokenResponse{}, "pending", nil
	case "slow_down":
		return oauthTokenResponse{}, "slow_down", nil
	case "expired_token", "expired":
		return oauthTokenResponse{}, "", NewHTTPError(http.StatusBadRequest, "oauth_device_expired", "The Super Grok authorization code expired")
	case "access_denied":
		return oauthTokenResponse{}, "", NewHTTPError(http.StatusBadRequest, "oauth_access_denied", "Super Grok authorization was denied")
	default:
		return oauthTokenResponse{}, "", NewHTTPError(http.StatusBadGateway, "oauth_token_failed", fmt.Sprintf("xAI token endpoint returned %s", oauthErr))
	}
}

func refreshXAIAccountOAuthCredentials(ctx context.Context, current ProviderResourceCredentials, clients ...*http.Client) (ProviderResourceCredentials, error) {
	refreshToken := strings.TrimSpace(current.RefreshToken)
	if refreshToken == "" {
		return current, NewHTTPError(http.StatusBadRequest, "provider_resource_refresh_token_missing", "Provider resource does not have a refresh token")
	}
	discovery, err := discoverXAIOAuth(ctx, clients...)
	if err != nil {
		return current, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", firstNonEmpty(current.ClientID, xaiOAuthClientID))
	token, oauthErr, err := requestXAIOAuthToken(ctx, discovery.TokenEndpoint, form, clients...)
	if err != nil {
		return current, err
	}
	if oauthErr != "" && oauthErr != "success" {
		if oauthErr == "invalid_grant" || oauthErr == "invalid_token" {
			return current, NewHTTPError(http.StatusConflict, "provider_resource_reauthorization_required", "Super Grok account session has ended. Reauthorize the account.")
		}
		return current, NewHTTPError(http.StatusBadGateway, "oauth_token_failed", fmt.Sprintf("xAI token endpoint returned %s", oauthErr))
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return current, NewHTTPError(http.StatusBadGateway, "oauth_token_missing", "OAuth token endpoint did not return an access token")
	}
	info := xaiAccountOAuthTokenInfoFromResponse(token, firstNonEmpty(current.ClientID, xaiOAuthClientID), current)
	creds := info.ToCredentials()
	if strings.TrimSpace(creds.RefreshToken) == "" {
		creds.RefreshToken = current.RefreshToken
	}
	return creds, nil
}

func discoverXAIOAuth(ctx context.Context, clients ...*http.Client) (*xaiOAuthDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, xaiOIDCDiscoveryURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := xaiOAuthHTTPClient(clients...).Do(req)
	if err != nil {
		if providerErrorDisposition(err) == ProviderErrorEgress {
			return nil, err
		}
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", fmt.Sprintf("xAI OAuth discovery failed: %v", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", fmt.Sprintf("xAI OAuth discovery returned %d", resp.StatusCode))
	}
	var discovery xaiOAuthDiscovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", "xAI OAuth discovery response was invalid")
	}
	deviceEndpoint, err := validateXAIOAuthEndpoint(discovery.DeviceAuthorizationEndpoint, "device_authorization_endpoint")
	if err != nil {
		return nil, err
	}
	tokenEndpoint, err := validateXAIOAuthEndpoint(discovery.TokenEndpoint, "token_endpoint")
	if err != nil {
		return nil, err
	}
	discovery.DeviceAuthorizationEndpoint = deviceEndpoint
	discovery.TokenEndpoint = tokenEndpoint
	return &discovery, nil
}

func requestXAIOAuthToken(ctx context.Context, tokenEndpoint string, form url.Values, clients ...*http.Client) (oauthTokenResponse, string, error) {
	endpoint, err := validateXAIOAuthEndpoint(tokenEndpoint, "token_endpoint")
	if err != nil {
		return oauthTokenResponse{}, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := xaiOAuthHTTPClient(clients...).Do(req)
	if err != nil {
		if providerErrorDisposition(err) == ProviderErrorEgress {
			return oauthTokenResponse{}, "", err
		}
		return oauthTokenResponse{}, "", NewHTTPError(http.StatusBadGateway, "oauth_token_failed", fmt.Sprintf("OAuth token request failed: %v", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		oauthTokenResponse
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &payload)
	if oauthErr := strings.ToLower(strings.TrimSpace(payload.Error)); oauthErr != "" {
		return payload.oauthTokenResponse, oauthErr, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oauthTokenResponse{}, "", NewHTTPError(http.StatusBadGateway, "oauth_token_failed", fmt.Sprintf("OAuth token endpoint returned %d", resp.StatusCode))
	}
	return payload.oauthTokenResponse, "", nil
}

func xaiAccountOAuthTokenInfoFromResponse(token oauthTokenResponse, clientID string, current ProviderResourceCredentials) providerAccountOAuthTokenInfo {
	info := openAIAccountOAuthTokenInfoFromResponse(token, firstNonEmpty(clientID, xaiOAuthClientID), current)
	info.ClientID = firstNonEmpty(clientID, xaiOAuthClientID)
	info.Scopes = firstNonEmpty(token.Scope, current.Scopes, xaiOAuthScope)
	email, subject := parseOIDCEmailSubject(info.IDToken)
	info.AccountEmail = firstNonEmpty(email, info.AccountEmail, current.Email)
	info.AccountID = firstNonEmpty(subject, info.AccountID, current.AccountID)
	info.UserID = firstNonEmpty(subject, info.UserID, current.UserID)
	return info
}

func parseOIDCEmailSubject(idToken string) (email string, subject string) {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload := parts[1]
	if padding := len(payload) % 4; padding != 0 {
		payload += strings.Repeat("=", 4-padding)
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(payload)
	}
	if err != nil {
		return "", ""
	}
	var claims struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return "", ""
	}
	return strings.TrimSpace(claims.Email), strings.TrimSpace(claims.Sub)
}

func validateXAIOAuthEndpoint(rawURL, field string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", "xAI OAuth "+field+" is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", "xAI OAuth "+field+" is invalid")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if isLocalProviderHostname(host) {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", "xAI OAuth "+field+" is invalid")
		}
		return parsed.String(), nil
	}
	if parsed.Scheme != "https" {
		return "", NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", "xAI OAuth "+field+" must use HTTPS")
	}
	if host != "x.ai" && host != "auth.x.ai" && !strings.HasSuffix(host, ".x.ai") {
		return "", NewHTTPError(http.StatusBadGateway, "oauth_discovery_failed", "xAI OAuth "+field+" host is not allowed")
	}
	return parsed.String(), nil
}

func xaiOAuthHTTPClient(clients ...*http.Client) *http.Client {
	if len(clients) > 0 && clients[0] != nil {
		return clients[0]
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func oauthVerificationHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
