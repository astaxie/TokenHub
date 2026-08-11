package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	openAIAccountOAuthClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIAccountOAuthAuthorize    = "https://auth.openai.com/oauth/authorize"
	openAIAccountOAuthTokenURL     = "https://auth.openai.com/oauth/token"
	openAIAccountOAuthRedirectURI  = "http://localhost:1455/auth/callback"
	openAIAccountOAuthScopes       = "openid profile email offline_access"
	openAIAccountOAuthRefreshScope = "openid profile email"
	openAIAccountOAuthSessionTTL   = 30 * time.Minute
	openAIAccountOAuthRefreshLead  = 5 * time.Minute
)

var openAIAccountOAuthTokenEndpoint = openAIAccountOAuthTokenURL

type providerAccountOAuthSession struct {
	ID           string
	State        string
	CodeVerifier string
	ClientID     string
	RedirectURI  string
	ReturnURL    string
	CreatedAt    time.Time
}

type providerAccountOAuthSessionRecord struct {
	ID                    string `gorm:"primaryKey"`
	StateHash             string `gorm:"uniqueIndex"`
	CodeVerifierEncrypted string
	ClientID              string
	RedirectURI           string
	ReturnURL             string
	CreatedAt             time.Time
	ExpiresAt             time.Time `gorm:"index"`
}

func (s *GormStore) SaveProviderAccountOAuthSession(session providerAccountOAuthSession) error {
	if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.State) == "" || strings.TrimSpace(session.CodeVerifier) == "" {
		return fmt.Errorf("provider account OAuth session is incomplete")
	}
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	record := providerAccountOAuthSessionRecord{
		ID:                    session.ID,
		StateHash:             HashSecret(session.State),
		CodeVerifierEncrypted: s.encryptSecret(session.CodeVerifier),
		ClientID:              session.ClientID,
		RedirectURI:           session.RedirectURI,
		ReturnURL:             session.ReturnURL,
		CreatedAt:             session.CreatedAt,
		ExpiresAt:             session.CreatedAt.Add(openAIAccountOAuthSessionTTL),
	}
	_ = s.db.Where("expires_at <= ?", now).Delete(&providerAccountOAuthSessionRecord{}).Error
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&record).Error
}

func (s *GormStore) GetProviderAccountOAuthSessionByState(state string) (providerAccountOAuthSession, bool, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return providerAccountOAuthSession{}, false, nil
	}
	var record providerAccountOAuthSessionRecord
	if err := s.db.First(&record, "state_hash = ? AND expires_at > ?", HashSecret(state), time.Now().UTC()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return providerAccountOAuthSession{}, false, nil
		}
		return providerAccountOAuthSession{}, false, err
	}
	session, err := s.providerAccountOAuthSessionFromRecord(record, state)
	if err != nil {
		return providerAccountOAuthSession{}, false, err
	}
	return session, true, nil
}

func (s *GormStore) ConsumeProviderAccountOAuthSession(id string, state string) (providerAccountOAuthSession, bool, error) {
	id = strings.TrimSpace(id)
	state = strings.TrimSpace(state)
	if id == "" || state == "" {
		return providerAccountOAuthSession{}, false, nil
	}
	var session providerAccountOAuthSession
	consumed := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_account_oauth", id); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var record providerAccountOAuthSessionRecord
		if err := query.First(&record, "id = ? AND state_hash = ? AND expires_at > ?", id, HashSecret(state), time.Now().UTC()).Error; err != nil {
			return err
		}
		decoded, err := s.providerAccountOAuthSessionFromRecord(record, state)
		if err != nil {
			return err
		}
		if err := tx.Delete(&record).Error; err != nil {
			return err
		}
		session = decoded
		consumed = true
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return providerAccountOAuthSession{}, false, nil
		}
		return providerAccountOAuthSession{}, false, err
	}
	return session, consumed, nil
}

func (s *GormStore) providerAccountOAuthSessionFromRecord(record providerAccountOAuthSessionRecord, state string) (providerAccountOAuthSession, error) {
	codeVerifier := s.decryptSecret(record.CodeVerifierEncrypted)
	if strings.TrimSpace(codeVerifier) == "" {
		return providerAccountOAuthSession{}, fmt.Errorf("provider account OAuth verifier could not be decrypted")
	}
	return providerAccountOAuthSession{
		ID:           record.ID,
		State:        state,
		CodeVerifier: codeVerifier,
		ClientID:     record.ClientID,
		RedirectURI:  record.RedirectURI,
		ReturnURL:    record.ReturnURL,
		CreatedAt:    record.CreatedAt,
	}, nil
}

type providerAccountOAuthGenerateRequest struct {
	RedirectURI string `json:"redirect_uri"`
	ReturnURL   string `json:"return_url"`
}

type providerAccountOAuthGenerateResponse struct {
	AuthURL     string `json:"auth_url"`
	SessionID   string `json:"session_id"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
	ExpiresAt   string `json:"expires_at"`
}

type providerAccountOAuthExchangeRequest struct {
	SessionID   string `json:"session_id"`
	Code        string `json:"code"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
}

type providerAccountOAuthTokenInfo struct {
	AccessToken    string `json:"access_token,omitempty"`
	RefreshToken   string `json:"refresh_token,omitempty"`
	IDToken        string `json:"id_token,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
	Scopes         string `json:"scopes,omitempty"`
	TokenType      string `json:"token_type,omitempty"`
	ExpiresIn      int64  `json:"expires_in,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	AccountEmail   string `json:"account_email,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	UserID         string `json:"user_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	PlanType       string `json:"plan_type,omitempty"`
}

func (s *Server) handleAdminOpenAIAccountOAuthGenerateAuthURL(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	var req providerAccountOAuthGenerateRequest
	if err := s.decodeJSONOptional(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	redirectURI := strings.TrimSpace(req.RedirectURI)
	if redirectURI == "" {
		redirectURI = openAIAccountOAuthRedirectURI
	}
	if err := validateAbsoluteHTTPURL(redirectURI, "invalid_redirect_uri", "OAuth callback URL must be an absolute http or https URL"); err != nil {
		writeError(w, r, err)
		return
	}
	returnURL := safeOAuthReturnURL(req.ReturnURL, r)
	state, err := randomHex(32)
	if err != nil {
		writeError(w, r, err)
		return
	}
	codeVerifier, err := openAICodeVerifier()
	if err != nil {
		writeError(w, r, err)
		return
	}
	sessionID, err := randomHex(16)
	if err != nil {
		writeError(w, r, err)
		return
	}
	expiresAt := time.Now().UTC().Add(openAIAccountOAuthSessionTTL)
	session := providerAccountOAuthSession{
		ID:           sessionID,
		State:        state,
		CodeVerifier: codeVerifier,
		ClientID:     openAIAccountOAuthClientID,
		RedirectURI:  redirectURI,
		ReturnURL:    returnURL,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.store.SaveProviderAccountOAuthSession(session); err != nil {
		writeError(w, r, err)
		return
	}
	authURL, err := buildOpenAIAccountOAuthAuthorizeURL(state, openAICodeChallenge(codeVerifier), redirectURI)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "generate_oauth_url", "provider_account", "openai", "", map[string]any{"redirect_uri": redirectURI})
	writeJSON(w, http.StatusOK, providerAccountOAuthGenerateResponse{
		AuthURL:     authURL,
		SessionID:   sessionID,
		State:       state,
		RedirectURI: redirectURI,
		ExpiresAt:   expiresAt.Format(time.RFC3339),
	})
}

func (s *Server) handleOpenAIAccountOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	session, ok, err := s.store.GetProviderAccountOAuthSessionByState(state)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !ok {
		writeError(w, r, NewHTTPError(400, "invalid_oauth_state", "OAuth state is invalid or expired"))
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		http.Redirect(w, r, providerAccountOAuthRedirectWithError(session.ReturnURL, "provider_error"), http.StatusFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Redirect(w, r, providerAccountOAuthRedirectWithError(session.ReturnURL, "missing_code"), http.StatusFound)
		return
	}
	target, err := url.Parse(session.ReturnURL)
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_return_url", "OAuth return URL is invalid"))
		return
	}
	values := target.Query()
	values.Set("provider_account_oauth", "1")
	values.Set("provider_account_oauth_session_id", session.ID)
	values.Set("provider_account_oauth_state", session.State)
	values.Set("code", code)
	target.RawQuery = values.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) handleAdminOpenAIAccountOAuthExchangeCode(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	var req providerAccountOAuthExchangeRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.State) == "" {
		writeError(w, r, NewHTTPError(400, "invalid_oauth_state", "OAuth state is invalid or expired"))
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		writeError(w, r, NewHTTPError(400, "missing_oauth_code", "OAuth authorization code is required"))
		return
	}
	session, ok, err := s.store.ConsumeProviderAccountOAuthSession(req.SessionID, req.State)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !ok {
		writeError(w, r, NewHTTPError(400, "oauth_session_not_found", "OAuth session was not found or has expired"))
		return
	}
	redirectURI := session.RedirectURI
	if strings.TrimSpace(req.RedirectURI) != "" {
		redirectURI = strings.TrimSpace(req.RedirectURI)
	}
	token, err := exchangeOpenAIAccountOAuthCode(r.Context(), code, session.CodeVerifier, redirectURI, session.ClientID)
	if err != nil {
		// Preserve retryability when the token endpoint fails before consuming
		// the authorization code. Concurrent exchanges are still serialized by
		// the atomic session consume operation.
		if restoreErr := s.store.SaveProviderAccountOAuthSession(session); restoreErr != nil {
			writeError(w, r, fmt.Errorf("restore OAuth session after token exchange failure: %w", restoreErr))
			return
		}
		writeError(w, r, err)
		return
	}
	info := openAIAccountOAuthTokenInfoFromResponse(token, session.ClientID, ProviderResourceCredentials{})
	s.recordAdminAudit(r, user, "exchange_oauth_code", "provider_account", "openai", "", providerAccountCredentialSummary(info.ToCredentials()))
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) prepareRouteForUpstream(ctx context.Context, route RouteSelection) (RouteSelection, error) {
	if route.Resource == nil || !isOpenAIAccountResource(route.Resource.ResourceType) {
		return route, nil
	}
	creds, err := s.store.RefreshProviderResourceCredentials(ctx, routeResourceID(route), false)
	if err != nil {
		return route, err
	}
	if strings.TrimSpace(creds.AccessToken) != "" {
		route.Provider.APIKey = creds.AccessToken
	}
	if route.Provider.Options == nil {
		route.Provider.Options = map[string]string{}
	}
	route.Provider.Options["resource_id"] = routeResourceID(route)
	applyOpenAIAccountOptions(route.Provider.Options, creds)
	return route, nil
}

func buildOpenAIAccountOAuthAuthorizeURL(state, codeChallenge, redirectURI string) (string, error) {
	if err := validateAbsoluteHTTPURL(redirectURI, "invalid_redirect_uri", "OAuth callback URL must be an absolute http or https URL"); err != nil {
		return "", err
	}
	target, err := url.Parse(openAIAccountOAuthAuthorize)
	if err != nil {
		return "", err
	}
	query := target.Query()
	query.Set("response_type", "code")
	query.Set("client_id", openAIAccountOAuthClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", openAIAccountOAuthScopes)
	query.Set("state", state)
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func exchangeOpenAIAccountOAuthCode(ctx context.Context, code, codeVerifier, redirectURI, clientID string) (oauthTokenResponse, error) {
	clientID = firstNonEmpty(clientID, openAIAccountOAuthClientID)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID)
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", strings.TrimSpace(redirectURI))
	form.Set("code_verifier", strings.TrimSpace(codeVerifier))
	token, err := requestOpenAIAccountOAuthToken(ctx, form)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_missing", "OAuth token endpoint did not return an access token")
	}
	return token, nil
}

func refreshOpenAIAccountOAuthCredentials(ctx context.Context, current ProviderResourceCredentials) (ProviderResourceCredentials, error) {
	refreshToken := strings.TrimSpace(current.RefreshToken)
	if refreshToken == "" {
		return current, NewHTTPError(400, "provider_resource_refresh_token_missing", "Provider resource does not have a refresh token")
	}
	clientID := firstNonEmpty(current.ClientID, openAIAccountOAuthClientID)
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	form.Set("scope", openAIAccountOAuthRefreshScope)
	token, err := requestOpenAIAccountOAuthToken(ctx, form)
	if err != nil {
		return current, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return current, NewHTTPError(502, "oauth_token_missing", "OAuth token endpoint did not return an access token")
	}
	info := openAIAccountOAuthTokenInfoFromResponse(token, clientID, current)
	creds := info.ToCredentials()
	if strings.TrimSpace(creds.RefreshToken) == "" {
		creds.RefreshToken = current.RefreshToken
	}
	return creds, nil
}

func requestOpenAIAccountOAuthToken(ctx context.Context, form url.Values) (oauthTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIAccountOAuthTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("user-agent", "codex-cli/0.91.0")
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_failed", fmt.Sprintf("OAuth token request failed: %v", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := sanitizeOAuthErrorDetail(body)
		if detail != "" {
			return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_failed", fmt.Sprintf("OAuth token endpoint returned %d: %s", resp.StatusCode, detail))
		}
		return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_failed", fmt.Sprintf("OAuth token endpoint returned %d", resp.StatusCode))
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthTokenResponse{}, err
	}
	return token, nil
}

func openAIAccountOAuthTokenInfoFromResponse(token oauthTokenResponse, clientID string, current ProviderResourceCredentials) providerAccountOAuthTokenInfo {
	expiresAt := current.ExpiresAt
	if token.ExpiresIn > 0 {
		expiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	info := providerAccountOAuthTokenInfo{
		AccessToken:    firstNonEmpty(token.AccessToken, current.AccessToken),
		RefreshToken:   firstNonEmpty(token.RefreshToken, current.RefreshToken),
		IDToken:        firstNonEmpty(token.IDToken, current.IDToken),
		ClientID:       firstNonEmpty(clientID, current.ClientID, openAIAccountOAuthClientID),
		Scopes:         firstNonEmpty(token.Scope, current.Scopes, openAIAccountOAuthScopes),
		TokenType:      firstNonEmpty(token.TokenType, current.TokenType, "Bearer"),
		ExpiresIn:      token.ExpiresIn,
		ExpiresAt:      expiresAt,
		AccountEmail:   current.Email,
		AccountID:      current.AccountID,
		UserID:         current.UserID,
		OrganizationID: current.OrganizationID,
		PlanType:       current.PlanType,
	}
	if claims := decodeOpenAIIDTokenClaims(info.IDToken); claims != nil {
		info.AccountEmail = firstNonEmpty(claims.Email, info.AccountEmail)
		if claims.OpenAIAuth != nil {
			info.AccountID = firstNonEmpty(claims.OpenAIAuth.ChatGPTAccountID, info.AccountID)
			info.UserID = firstNonEmpty(claims.OpenAIAuth.UserID, claims.OpenAIAuth.ChatGPTUserID, info.UserID)
			info.OrganizationID = firstNonEmpty(defaultOpenAIOrganizationID(claims.OpenAIAuth.Organizations), info.OrganizationID)
			info.PlanType = firstNonEmpty(claims.OpenAIAuth.ChatGPTPlanType, info.PlanType)
		}
	}
	return info
}

func (info providerAccountOAuthTokenInfo) ToCredentials() ProviderResourceCredentials {
	return ProviderResourceCredentials{
		AuthType:       "oauth",
		AccessToken:    info.AccessToken,
		RefreshToken:   info.RefreshToken,
		IDToken:        info.IDToken,
		ClientID:       info.ClientID,
		Scopes:         info.Scopes,
		TokenType:      info.TokenType,
		ExpiresAt:      info.ExpiresAt,
		AccountID:      info.AccountID,
		UserID:         info.UserID,
		Email:          info.AccountEmail,
		OrganizationID: info.OrganizationID,
		PlanType:       info.PlanType,
	}
}

func providerAccountCredentialSummary(creds ProviderResourceCredentials) map[string]string {
	options := map[string]string{}
	applyOpenAIAccountOptions(options, creds)
	for _, key := range []string{"access_token", "refresh_token", "id_token", "api_key", "credential_blob"} {
		delete(options, key)
	}
	return options
}

func providerAccountOAuthRedirectWithError(returnURL string, code string) string {
	target, err := url.Parse(returnURL)
	if err != nil {
		return returnURL
	}
	values := target.Query()
	values.Set("provider_account_oauth", "1")
	values.Set("provider_account_oauth_error", code)
	target.RawQuery = values.Encode()
	return target.String()
}

func openAICodeVerifier() (string, error) {
	return randomHex(64)
}

func openAICodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func validateAbsoluteHTTPURL(value, code, message string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return NewHTTPError(400, code, message)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return NewHTTPError(400, code, message)
	}
	return nil
}
