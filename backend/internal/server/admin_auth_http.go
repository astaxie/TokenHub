package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.Identity) == "" || req.Password == "" {
		writeError(w, r, NewHTTPError(400, "invalid_request", "identity and password are required"))
		return
	}
	user, session, err := s.store.AuthenticateAdminUser(req.Identity, req.Password, 12*time.Hour)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      session.Token,
		"expires_at": session.ExpiresAt,
		"user":       user,
	})
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.Token) == "" || strings.TrimSpace(req.Password) == "" {
		writeError(w, r, NewHTTPError(400, "invalid_reset_request", "token and password are required"))
		return
	}
	if len(req.Password) < 8 {
		writeError(w, r, NewHTTPError(400, "weak_password", "Password must be at least 8 characters"))
		return
	}
	user, err := s.store.ResetAdminUserPassword(req.Token, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token != "" && token != strings.TrimSpace(s.config.AdminToken) {
		s.store.RevokeAdminSession(token)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

type adminAuthIdentityProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	ProviderType string `json:"provider_type"`
	IssuerURL    string `json:"issuer_url,omitempty"`
	IconKey      string `json:"icon_key,omitempty"`
}

type oauthStatePayload struct {
	ProviderID  string `json:"provider_id"`
	ReturnURL   string `json:"return_url"`
	RedirectURI string `json:"redirect_uri"`
	ExpiresAt   int64  `json:"expires_at"`
	Nonce       string `json:"nonce"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope,omitempty"`
}

func (s *Server) handleAdminAuthIdentityProviders(w http.ResponseWriter, r *http.Request) {
	providers := []adminAuthIdentityProvider{}
	for _, item := range s.activeOAuthIdentityProviders() {
		providers = append(providers, adminAuthIdentityProvider{
			ID:           item.ID,
			Name:         item.Name,
			DisplayName:  identityProviderDisplayName(item),
			ProviderType: strings.ToLower(strings.TrimSpace(stringField(item.Fields, "provider_type"))),
			IssuerURL:    strings.TrimSpace(stringField(item.Fields, "issuer_url")),
			IconKey:      identityProviderIconKey(item),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": providers})
}

func (s *Server) handleAdminOAuthStart(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.findActiveOAuthIdentityProvider(r.URL.Query().Get("id"))
	if !ok {
		writeError(w, r, NewHTTPError(404, "identity_provider_not_found", "Identity provider not found"))
		return
	}
	authorizeURL := strings.TrimSpace(stringField(provider.Fields, "authorize_url"))
	clientID := strings.TrimSpace(stringField(provider.Fields, "client_id"))
	if authorizeURL == "" || clientID == "" {
		writeError(w, r, NewHTTPError(400, "identity_provider_incomplete", "Identity provider authorize URL and client ID are required"))
		return
	}
	redirectURI, err := identityProviderRedirectURI(provider, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	returnURL := safeOAuthReturnURL(r.URL.Query().Get("return_url"), r)
	state, err := s.signOAuthState(oauthStatePayload{
		ProviderID:  provider.ID,
		ReturnURL:   returnURL,
		RedirectURI: redirectURI,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute).Unix(),
		Nonce:       NewID("oauth"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	target, err := buildIdentityProviderAuthorizeURL(provider, redirectURI, state)
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleAdminOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state, err := s.verifyOAuthState(r.URL.Query().Get("state"))
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_oauth_state", "OAuth state is invalid or expired"))
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "provider_error"), http.StatusFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		code = strings.TrimSpace(r.URL.Query().Get("authCode"))
	}
	if code == "" {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "missing_code"), http.StatusFound)
		return
	}
	provider, ok := s.findActiveOAuthIdentityProvider(state.ProviderID)
	if !ok {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "identity_provider_not_found"), http.StatusFound)
		return
	}
	token, err := s.exchangeOAuthCode(r.Context(), provider, code, state.RedirectURI)
	if err != nil {
		log.Printf("oauth token exchange failed provider_id=%s redirect_uri=%s error=%v", provider.ID, state.RedirectURI, err)
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, oauthErrorCode("token_exchange_failed", err)), http.StatusFound)
		return
	}
	claims, err := s.fetchOAuthUserInfo(r.Context(), provider, token.AccessToken, code)
	if err != nil {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "userinfo_failed"), http.StatusFound)
		return
	}
	user, err := s.upsertOAuthAdminUser(provider, claims)
	if err != nil {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "user_sync_failed"), http.StatusFound)
		return
	}
	_, session, err := s.store.CreateAdminSession(user.ID, 12*time.Hour)
	if err != nil {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "session_failed"), http.StatusFound)
		return
	}
	http.Redirect(w, r, oauthRedirectWithSession(state.ReturnURL, session), http.StatusFound)
}

func (s *Server) activeOAuthIdentityProviders() []AdminResource {
	items := []AdminResource{}
	for _, item := range s.store.ListResources("identity-providers") {
		if item.Status != StatusActive {
			continue
		}
		providerType := strings.ToLower(strings.TrimSpace(stringField(item.Fields, "provider_type")))
		if providerType != "oidc" && providerType != "oauth2" {
			continue
		}
		if strings.TrimSpace(stringField(item.Fields, "authorize_url")) == "" ||
			strings.TrimSpace(stringField(item.Fields, "token_url")) == "" ||
			strings.TrimSpace(stringField(item.Fields, "userinfo_url")) == "" ||
			strings.TrimSpace(stringField(item.Fields, "client_id")) == "" {
			continue
		}
		if !identityProviderPlatformConfigurationComplete(item) {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (s *Server) findActiveOAuthIdentityProvider(id string) (AdminResource, bool) {
	id = strings.TrimSpace(id)
	items := s.activeOAuthIdentityProviders()
	if id == "" && len(items) == 1 {
		return items[0], true
	}
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return AdminResource{}, false
}

func identityProviderScopes(provider AdminResource) string {
	raw := strings.TrimSpace(stringField(provider.Fields, "scopes"))
	if raw == "" {
		return "openid profile email"
	}
	if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := strings.TrimSpace(part); value != "" {
				out = append(out, value)
			}
		}
		return strings.Join(out, " ")
	}
	return raw
}

func identityProviderIconKey(provider AdminResource) string {
	configured := strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "icon_key")))
	if configured != "" && configured != "auto" {
		return configured
	}
	providerType := strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "provider_type")))
	fingerprint := strings.ToLower(strings.Join([]string{
		provider.Name,
		stringField(provider.Fields, "issuer_url"),
		stringField(provider.Fields, "authorize_url"),
		providerType,
	}, " "))
	for _, key := range []string{"dingtalk", "feishu", "wecom", "gitlab", "github", "google", "microsoft", "azure", "entra", "okta", "keycloak"} {
		if strings.Contains(fingerprint, key) {
			if key == "azure" || key == "entra" {
				return "microsoft"
			}
			return key
		}
	}
	switch providerType {
	case "oidc", "oauth2", "saml", "ldap":
		return providerType
	default:
		return "sso"
	}
}

func identityProviderDisplayName(provider AdminResource) string {
	if label := strings.TrimSpace(stringField(provider.Fields, "login_label")); label != "" {
		return label
	}
	iconKey := identityProviderIconKey(provider)
	if label := identityProviderIconDisplayName(iconKey); label != "" {
		return label
	}
	if provider.Name != "" {
		return provider.Name
	}
	return identityProviderTypeLabel(strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "provider_type"))))
}

func identityProviderIconDisplayName(iconKey string) string {
	switch strings.ToLower(strings.TrimSpace(iconKey)) {
	case "gitlab":
		return "GitLab"
	case "github":
		return "GitHub"
	case "google":
		return "Google"
	case "microsoft":
		return "Microsoft"
	case "okta":
		return "Okta"
	case "keycloak":
		return "Keycloak"
	case "dingtalk":
		return "DingTalk"
	case "feishu":
		return "Feishu"
	case "wecom":
		return "WeCom"
	default:
		return ""
	}
}

func identityProviderTypeLabel(providerType string) string {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "oidc":
		return "OIDC"
	case "oauth2":
		return "OAuth2"
	case "saml":
		return "SAML"
	case "ldap":
		return "LDAP"
	default:
		return "SSO"
	}
}

func buildOAuthAuthorizeURL(authorizeURL string, clientID string, redirectURI string, scope string, state string) (string, error) {
	target, err := url.Parse(authorizeURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", NewHTTPError(400, "invalid_authorize_url", "Authorize URL is invalid")
	}
	query := target.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(scope) != "" {
		query.Set("scope", scope)
	}
	query.Set("state", state)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func oauthCallbackURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := firstForwardedValue(r.Header.Get("x-forwarded-proto")); forwarded != "" {
		scheme = forwarded
	}
	host := r.Host
	if forwarded := firstForwardedValue(r.Header.Get("x-forwarded-host")); forwarded != "" {
		host = forwarded
	}
	return fmt.Sprintf("%s://%s/api/admin/auth/oauth/callback", scheme, host)
}

func identityProviderRedirectURI(provider AdminResource, r *http.Request) (string, error) {
	configured := strings.TrimSpace(stringField(provider.Fields, "redirect_uri"))
	if configured == "" {
		return oauthCallbackURL(r), nil
	}
	target, err := url.Parse(configured)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", NewHTTPError(400, "invalid_redirect_uri", "OAuth callback URL must be an absolute URL")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", NewHTTPError(400, "invalid_redirect_uri", "OAuth callback URL must use http or https")
	}
	if target.Fragment != "" {
		return "", NewHTTPError(400, "invalid_redirect_uri", "OAuth callback URL must not contain a fragment")
	}
	return configured, nil
}

func firstForwardedValue(value string) string {
	if value == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func safeOAuthReturnURL(raw string, r *http.Request) string {
	fallback := "http://localhost:3000/overview"
	if origin := strings.TrimSpace(r.Header.Get("origin")); origin != "" {
		fallback = strings.TrimRight(origin, "/") + "/overview"
	} else if referer := strings.TrimSpace(r.Header.Get("referer")); referer != "" {
		if parsed, err := url.Parse(referer); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			fallback = parsed.Scheme + "://" + parsed.Host + "/overview"
		}
	}
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return fallback
	}
	parsed, err := url.Parse(candidate)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fallback
	}
	if isAllowedOAuthReturnHost(parsed.Hostname(), r.Host) {
		return parsed.String()
	}
	return fallback
}

func isAllowedOAuthReturnHost(hostname string, requestHost string) bool {
	hostname = strings.ToLower(strings.Trim(hostname, "[]"))
	requestHostname := strings.ToLower(strings.Trim(strings.Split(requestHost, ":")[0], "[]"))
	switch hostname {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return hostname != "" && hostname == requestHostname
}

func (s *Server) signOAuthState(payload oauthStatePayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(s.oauthStateSecret()))
	_, _ = mac.Write([]byte(body))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

func (s *Server) verifyOAuthState(state string) (oauthStatePayload, error) {
	parts := strings.Split(strings.TrimSpace(state), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return oauthStatePayload{}, fmt.Errorf("invalid oauth state")
	}
	mac := hmac.New(sha256.New, []byte(s.oauthStateSecret()))
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(got, expected) {
		return oauthStatePayload{}, fmt.Errorf("invalid oauth state")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oauthStatePayload{}, err
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return oauthStatePayload{}, err
	}
	if payload.ProviderID == "" || payload.ReturnURL == "" || payload.RedirectURI == "" || time.Now().UTC().Unix() > payload.ExpiresAt {
		return oauthStatePayload{}, fmt.Errorf("invalid oauth state")
	}
	return payload, nil
}

func (s *Server) oauthStateSecret() string {
	if secret := strings.TrimSpace(s.config.SecretKey); secret != "" {
		return secret
	}
	if secret := strings.TrimSpace(s.config.AdminToken); secret != "" {
		return secret
	}
	return "tokenhub-oauth-state"
}

func (s *Server) exchangeOAuthCode(ctx context.Context, provider AdminResource, code string, redirectURI string) (oauthTokenResponse, error) {
	if token, handled, err := exchangeConfiguredIdentityProviderOAuthCode(ctx, provider, code, redirectURI); handled {
		return token, err
	}
	tokenURL := strings.TrimSpace(stringField(provider.Fields, "token_url"))
	clientID := strings.TrimSpace(stringField(provider.Fields, "client_id"))
	clientSecret := strings.TrimSpace(stringField(provider.Fields, "client_secret"))
	if tokenURL == "" || clientID == "" {
		return oauthTokenResponse{}, NewHTTPError(400, "identity_provider_incomplete", "Identity provider token URL and client ID are required")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	token, detail, err := requestOAuthToken(ctx, tokenURL, form, "", "")
	if err == nil {
		if strings.TrimSpace(token.AccessToken) == "" {
			return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_missing", "OAuth token endpoint did not return an access token")
		}
		return token, nil
	}
	if clientSecret == "" || !strings.Contains(detail, "invalid_client") {
		return oauthTokenResponse{}, err
	}
	log.Printf("oauth token exchange retrying with client_secret_basic after invalid_client")
	basicForm := url.Values{}
	basicForm.Set("grant_type", "authorization_code")
	basicForm.Set("code", code)
	basicForm.Set("redirect_uri", redirectURI)
	token, _, err = requestOAuthToken(ctx, tokenURL, basicForm, clientID, clientSecret)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_missing", "OAuth token endpoint did not return an access token")
	}
	return token, nil
}

func requestOAuthToken(ctx context.Context, tokenURL string, form url.Values, basicClientID string, basicSecret string) (oauthTokenResponse, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, "", err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	if basicClientID != "" || basicSecret != "" {
		req.SetBasicAuth(basicClientID, basicSecret)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return oauthTokenResponse{}, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := sanitizeOAuthErrorDetail(body)
		if detail != "" {
			return oauthTokenResponse{}, detail, NewHTTPError(502, "oauth_token_failed", fmt.Sprintf("OAuth token endpoint returned %d: %s", resp.StatusCode, detail))
		}
		return oauthTokenResponse{}, detail, NewHTTPError(502, "oauth_token_failed", fmt.Sprintf("OAuth token endpoint returned %d", resp.StatusCode))
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthTokenResponse{}, "", err
	}
	return token, "", nil
}

func (s *Server) fetchOAuthUserInfo(ctx context.Context, provider AdminResource, accessToken string, code string) (map[string]any, error) {
	if claims, handled, err := fetchConfiguredIdentityProviderUserInfo(ctx, provider, accessToken, code); handled {
		return claims, err
	}
	userinfoURL := strings.TrimSpace(stringField(provider.Fields, "userinfo_url"))
	if userinfoURL == "" {
		return nil, NewHTTPError(400, "identity_provider_incomplete", "Identity provider userinfo URL is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+accessToken)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewHTTPError(502, "oauth_userinfo_failed", fmt.Sprintf("OAuth userinfo endpoint returned %d", resp.StatusCode))
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *Server) upsertOAuthAdminUser(provider AdminResource, claims map[string]any) (AdminUser, error) {
	usernameClaim := strings.TrimSpace(stringField(provider.Fields, "username_claim"))
	emailClaim := strings.TrimSpace(stringField(provider.Fields, "email_claim"))
	teamClaim := strings.TrimSpace(stringField(provider.Fields, "team_claim"))
	email := firstOAuthClaim(claims, emailClaim, "email", "enterprise_email", "biz_mail", "public_email")
	allowUsernameMatch := true
	if email == "" {
		email = identityProviderFallbackEmail(provider, claims)
		allowUsernameMatch = email == ""
	}
	if email == "" {
		return AdminUser{}, NewHTTPError(400, "oauth_email_missing", "OAuth userinfo did not include an email")
	}
	username := firstOAuthClaim(claims, usernameClaim, "preferred_username", "username", "nickname", "name")
	if username == "" {
		username = strings.Split(email, "@")[0]
	}
	name := firstOAuthClaim(claims, "name", "nick", "display_name", "en_name", usernameClaim, "username")
	if name == "" {
		name = username
	}
	claimedTeamID := s.oauthTeamID(firstOAuthClaim(claims, teamClaim))
	defaultTeamID := s.oauthDefaultTeamID(provider)
	teamID := claimedTeamID
	if teamID == "" {
		teamID = defaultTeamID
	}
	users := s.store.ListAdminUsers()
	if existing, ok := findOAuthAdminUser(users, email, username, allowUsernameMatch); ok {
		if existing.Status != StatusActive {
			return AdminUser{}, NewHTTPError(403, "admin_user_disabled", "Admin user is disabled")
		}
		patch := existing
		if name != "" {
			patch.Name = name
		}
		patch.Email = email
		if username != "" && !adminUsernameTaken(users, username, existing.ID) {
			patch.Username = username
		}
		if claimedTeamID != "" {
			patch.TeamID = claimedTeamID
		} else if strings.TrimSpace(patch.TeamID) == "" && defaultTeamID != "" {
			patch.TeamID = defaultTeamID
		}
		updated, err := s.store.UpdateAdminUser(existing.ID, patch, "")
		if err != nil {
			return AdminUser{}, err
		}
		s.assignOAuthDefaultProject(provider, updated)
		return updated, nil
	}
	username = uniqueOAuthUsername(users, username, email)
	user, err := s.store.CreateAdminUser(AdminUser{
		Username: username,
		Name:     name,
		Email:    email,
		Role:     oauthDefaultRole(provider),
		TeamID:   teamID,
		Status:   StatusActive,
	}, GenerateAdminSessionToken())
	if err != nil {
		return AdminUser{}, err
	}
	s.assignOAuthDefaultProject(provider, user)
	return user, nil
}

func firstOAuthClaim(claims map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := oauthClaimString(claims, key); value != "" {
			return value
		}
	}
	return ""
}

func oauthClaimString(claims map[string]any, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || claims == nil {
		return ""
	}
	var value any = claims
	for _, part := range strings.Split(key, ".") {
		fields, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value, ok = fields[part]
		if !ok || value == nil {
			return ""
		}
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func findOAuthAdminUser(users []AdminUser, email string, username string, allowUsernameMatch bool) (AdminUser, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.ToLower(strings.TrimSpace(username))
	for _, user := range users {
		if email != "" && strings.ToLower(strings.TrimSpace(user.Email)) == email {
			return user, true
		}
	}
	if !allowUsernameMatch {
		return AdminUser{}, false
	}
	for _, user := range users {
		if username != "" && strings.ToLower(strings.TrimSpace(user.Username)) == username {
			return user, true
		}
	}
	return AdminUser{}, false
}

func adminUsernameTaken(users []AdminUser, username string, allowedUserID string) bool {
	username = strings.ToLower(strings.TrimSpace(username))
	for _, user := range users {
		if user.ID != allowedUserID && strings.ToLower(strings.TrimSpace(user.Username)) == username {
			return true
		}
	}
	return false
}

func uniqueOAuthUsername(users []AdminUser, preferred string, email string) string {
	base := strings.TrimSpace(preferred)
	if base == "" {
		base = strings.Split(strings.TrimSpace(email), "@")[0]
	}
	if base == "" {
		base = "oauth-user"
	}
	if !adminUsernameTaken(users, base, "") {
		return base
	}
	for index := 2; index < 1000; index++ {
		candidate := fmt.Sprintf("%s-%d", base, index)
		if !adminUsernameTaken(users, candidate, "") {
			return candidate
		}
	}
	return base + "-" + NewID("oauth")
}

func (s *Server) oauthTeamID(claimValue string) string {
	normalized := normalizeScopeValue(claimValue)
	if normalized == "" {
		return ""
	}
	for _, team := range s.store.ListResources("teams") {
		for _, value := range []string{
			team.ID,
			team.Name,
			stringField(team.Fields, "name"),
			stringField(team.Fields, "code"),
			stringField(team.Fields, "team_id"),
			stringField(team.Fields, "team_name"),
		} {
			if normalizeScopeValue(value) == normalized {
				return team.ID
			}
		}
	}
	return ""
}

func oauthDefaultRole(provider AdminResource) string {
	role := normalizeAdminRole(stringField(provider.Fields, "default_role"))
	switch role {
	case "team_leader":
		return "team_leader"
	default:
		return "user"
	}
}

func (s *Server) oauthDefaultTeamID(provider AdminResource) string {
	return s.oauthTeamID(firstStringField(provider.Fields, "default_team_id", "default_team", "default_team_name"))
}

func (s *Server) oauthDefaultProject(provider AdminResource) (Project, bool) {
	return s.oauthProject(firstStringField(provider.Fields, "default_project_id", "default_project", "default_project_name"))
}

func (s *Server) oauthProject(value string) (Project, bool) {
	normalized := normalizeScopeValue(value)
	if normalized == "" {
		return Project{}, false
	}
	for _, project := range s.store.ListProjects() {
		if project.Status != "" && project.Status != StatusActive {
			continue
		}
		for _, candidate := range []string{project.ID, project.Name} {
			if normalizeScopeValue(candidate) == normalized {
				return project, true
			}
		}
	}
	return Project{}, false
}

func oauthDefaultProjectRole(provider AdminResource) string {
	role := strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "default_project_role")))
	switch role {
	case "viewer", "developer", "maintainer":
		return role
	default:
		return "developer"
	}
}

func (s *Server) assignOAuthDefaultProject(provider AdminResource, user AdminUser) {
	project, ok := s.oauthDefaultProject(provider)
	if !ok || strings.TrimSpace(user.ID) == "" {
		return
	}
	for _, item := range s.store.ListResources("project-members") {
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == project.ID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == user.ID {
			return
		}
	}
	role := oauthDefaultProjectRole(provider)
	displayName := user.Name
	if strings.TrimSpace(displayName) == "" {
		displayName = user.Username
	}
	s.store.CreateResource("project-members", AdminResource{
		Name:   fmt.Sprintf("%s / %s", project.Name, displayName),
		Status: StatusActive,
		Fields: map[string]any{
			"project_id":      project.ID,
			"user_id":         user.ID,
			"role":            role,
			"can_issue_keys":  projectMemberRoleCanIssueKey(role),
			"provisioned_by":  "oauth_default_project",
			"identity_source": provider.ID,
		},
	})
}

func oauthRedirectWithSession(returnURL string, session AdminSession) string {
	values := url.Values{}
	values.Set("oauth_token", session.Token)
	values.Set("oauth_expires_at", session.ExpiresAt.Format(time.RFC3339))
	return oauthRedirectWithFragment(returnURL, values)
}

func oauthRedirectWithError(returnURL string, code string) string {
	values := url.Values{}
	values.Set("oauth_error", code)
	return oauthRedirectWithFragment(returnURL, values)
}

func oauthErrorCode(code string, err error) string {
	if err == nil {
		return code
	}
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		return code
	}
	detail = strings.ReplaceAll(detail, "\n", " ")
	detail = strings.ReplaceAll(detail, "\r", " ")
	if len(detail) > 160 {
		detail = detail[:160]
	}
	return code + ": " + detail
}

func sanitizeOAuthErrorDetail(body []byte) string {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		parts := []string{}
		for _, key := range []string{"error", "error_description", "error_uri", "message"} {
			if value, ok := parsed[key].(string); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", key, strings.TrimSpace(value)))
			}
		}
		if len(parts) > 0 {
			raw = strings.Join(parts, "; ")
		}
	}
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.ReplaceAll(raw, "\r", " ")
	if len(raw) > 240 {
		raw = raw[:240]
	}
	return raw
}

func oauthRedirectWithFragment(returnURL string, values url.Values) string {
	target, err := url.Parse(returnURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		target, _ = url.Parse("http://localhost:3000/overview")
	}
	query := target.Query()
	for key, items := range values {
		query.Del(key)
		for _, item := range items {
			query.Add(key, item)
		}
	}
	target.RawQuery = query.Encode()
	target.Fragment = values.Encode()
	return target.String()
}
