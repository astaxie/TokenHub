package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	identityProviderDingTalk = "dingtalk"
	identityProviderFeishu   = "feishu"
	identityProviderWeCom    = "wecom"
)

func identityProviderTemplateKey(provider AdminResource) string {
	configured := strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "provider_template")))
	switch configured {
	case identityProviderDingTalk, identityProviderFeishu, identityProviderWeCom:
		return configured
	}
	fingerprint := strings.ToLower(strings.Join([]string{
		stringField(provider.Fields, "icon_key"),
		provider.Name,
		stringField(provider.Fields, "issuer_url"),
		stringField(provider.Fields, "authorize_url"),
	}, " "))
	for _, key := range []string{identityProviderDingTalk, identityProviderFeishu, identityProviderWeCom} {
		if strings.Contains(fingerprint, key) {
			return key
		}
	}
	if strings.Contains(fingerprint, "work.weixin") || strings.Contains(fingerprint, "qyapi.weixin") {
		return identityProviderWeCom
	}
	return ""
}

func identityProviderPlatformConfigurationComplete(provider AdminResource) bool {
	switch identityProviderTemplateKey(provider) {
	case identityProviderDingTalk, identityProviderFeishu:
		return strings.TrimSpace(stringField(provider.Fields, "client_secret")) != ""
	case identityProviderWeCom:
		return strings.TrimSpace(stringField(provider.Fields, "client_secret")) != "" &&
			strings.TrimSpace(stringField(provider.Fields, "agent_id")) != ""
	default:
		return true
	}
}

func buildIdentityProviderAuthorizeURL(provider AdminResource, redirectURI string, state string, codeChallenge string) (string, error) {
	authorizeURL := strings.TrimSpace(stringField(provider.Fields, "authorize_url"))
	clientID := strings.TrimSpace(stringField(provider.Fields, "client_id"))
	templateKey := identityProviderTemplateKey(provider)
	if templateKey == identityProviderDingTalk || templateKey == identityProviderFeishu || templateKey == identityProviderWeCom {
		// These vendor-specific exchanges do not accept a PKCE verifier. Do not
		// advertise a challenge that the token exchange cannot bind. The separate
		// browser-to-TokenHub exchange remains protected by mandatory S256 PKCE.
		codeChallenge = ""
	}
	target, err := buildOAuthAuthorizeURL(authorizeURL, clientID, redirectURI, identityProviderScopes(provider), state, codeChallenge)
	if err != nil {
		return "", err
	}
	if templateKey != identityProviderDingTalk && templateKey != identityProviderFeishu && templateKey != identityProviderWeCom {
		return target, nil
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if templateKey == identityProviderDingTalk {
		query.Set("prompt", "consent")
	} else if templateKey == identityProviderFeishu {
		query.Del("client_id")
		query.Del("response_type")
		query.Del("scope")
		query.Set("app_id", clientID)
		if scopes := strings.TrimSpace(stringField(provider.Fields, "scopes")); scopes != "" {
			query.Set("scope", identityProviderScopes(provider))
		}
	} else {
		query.Del("client_id")
		query.Del("response_type")
		query.Del("scope")
		query.Set("login_type", "CorpApp")
		query.Set("appid", clientID)
		query.Set("agentid", strings.TrimSpace(stringField(provider.Fields, "agent_id")))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func exchangeConfiguredIdentityProviderOAuthCode(ctx context.Context, provider AdminResource, code string, redirectURI string) (oauthTokenResponse, bool, error) {
	templateKey := identityProviderTemplateKey(provider)
	if templateKey == identityProviderWeCom {
		tokenURL, err := identityProviderURLWithQuery(strings.TrimSpace(stringField(provider.Fields, "token_url")), map[string]string{
			"corpid":     strings.TrimSpace(stringField(provider.Fields, "client_id")),
			"corpsecret": strings.TrimSpace(stringField(provider.Fields, "client_secret")),
		})
		if err != nil {
			return oauthTokenResponse{}, true, err
		}
		token, err := requestIdentityProviderGETToken(ctx, tokenURL)
		return token, true, err
	}
	if templateKey != identityProviderDingTalk && templateKey != identityProviderFeishu {
		return oauthTokenResponse{}, false, nil
	}
	clientID := strings.TrimSpace(stringField(provider.Fields, "client_id"))
	clientSecret := strings.TrimSpace(stringField(provider.Fields, "client_secret"))
	var payload map[string]any
	if templateKey == identityProviderDingTalk {
		payload = map[string]any{
			"clientId":     clientID,
			"clientSecret": clientSecret,
			"code":         code,
			"grantType":    "authorization_code",
		}
	} else {
		payload = map[string]any{
			"client_id":     clientID,
			"client_secret": clientSecret,
			"code":          code,
			"grant_type":    "authorization_code",
			"redirect_uri":  redirectURI,
		}
	}
	token, err := requestIdentityProviderJSONToken(ctx, strings.TrimSpace(stringField(provider.Fields, "token_url")), payload)
	return token, true, err
}

func fetchConfiguredIdentityProviderUserInfo(ctx context.Context, provider AdminResource, accessToken string, code string) (map[string]any, bool, error) {
	templateKey := identityProviderTemplateKey(provider)
	if templateKey == identityProviderWeCom {
		claims, err := fetchWeComIdentityProviderUserInfo(ctx, provider, accessToken, code)
		return claims, true, err
	}
	if templateKey != identityProviderDingTalk && templateKey != identityProviderFeishu {
		return nil, false, nil
	}
	headers := map[string]string{}
	if templateKey == identityProviderDingTalk {
		headers["x-acs-dingtalk-access-token"] = accessToken
	} else {
		headers["authorization"] = "Bearer " + accessToken
	}
	claims, err := requestIdentityProviderClaims(ctx, strings.TrimSpace(stringField(provider.Fields, "userinfo_url")), headers)
	if err == nil && templateKey == identityProviderFeishu {
		claims, err = unwrapIdentityProviderClaims(claims)
	}
	return claims, true, err
}

func requestIdentityProviderGETToken(ctx context.Context, endpoint string) (oauthTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("accept", "application/json")
	body, err := executeIdentityProviderJSONRequest(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	return decodeIdentityProviderToken(body)
}

func fetchWeComIdentityProviderUserInfo(ctx context.Context, provider AdminResource, accessToken string, code string) (map[string]any, error) {
	identityURL, err := identityProviderURLWithQuery(strings.TrimSpace(stringField(provider.Fields, "userinfo_url")), map[string]string{
		"access_token": accessToken,
		"code":         code,
	})
	if err != nil {
		return nil, err
	}
	identity, err := requestIdentityProviderClaims(ctx, identityURL, nil)
	if err != nil {
		return nil, err
	}
	userID := firstOAuthClaim(identity, "userid", "UserId")
	if userID == "" {
		return nil, NewHTTPError(502, "oauth_userinfo_failed", "WeCom identity response did not include a UserId")
	}
	userDetailURL := strings.TrimSpace(stringField(provider.Fields, "userdetail_url"))
	if userDetailURL == "" {
		userDetailURL = "https://qyapi.weixin.qq.com/cgi-bin/user/get"
	}
	detailURL, err := identityProviderURLWithQuery(userDetailURL, map[string]string{
		"access_token": accessToken,
		"userid":       userID,
	})
	if err != nil {
		return nil, err
	}
	return requestIdentityProviderClaims(ctx, detailURL, nil)
}

func identityProviderURLWithQuery(endpoint string, values map[string]string) (string, error) {
	target, err := url.Parse(endpoint)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", NewHTTPError(400, "identity_provider_incomplete", "Identity provider endpoint URL is invalid")
	}
	query := target.Query()
	for key, value := range values {
		query.Set(key, value)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func unwrapIdentityProviderClaims(claims map[string]any) (map[string]any, error) {
	data, ok := claims["data"].(map[string]any)
	if !ok {
		return nil, NewHTTPError(502, "oauth_userinfo_failed", "Identity provider userinfo response did not include data")
	}
	return data, nil
}

func requestIdentityProviderJSONToken(ctx context.Context, endpoint string, payload map[string]any) (oauthTokenResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	responseBody, err := executeIdentityProviderJSONRequest(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	return decodeIdentityProviderToken(responseBody)
}

func requestIdentityProviderClaims(ctx context.Context, endpoint string, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	body, err := executeIdentityProviderJSONRequest(req)
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, err
	}
	if detail := identityProviderResponseError(claims); detail != "" {
		return nil, NewHTTPError(502, "oauth_userinfo_failed", detail)
	}
	return claims, nil
}

func executeIdentityProviderJSONRequest(req *http.Request) ([]byte, error) {
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, NewHTTPError(502, "oauth_provider_failed", "Identity provider request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := sanitizeOAuthErrorDetail(body)
		if detail != "" {
			return nil, NewHTTPError(502, "oauth_provider_failed", fmt.Sprintf("Identity provider returned %d: %s", resp.StatusCode, detail))
		}
		return nil, NewHTTPError(502, "oauth_provider_failed", fmt.Sprintf("Identity provider returned %d", resp.StatusCode))
	}
	return body, nil
}

func decodeIdentityProviderToken(body []byte) (oauthTokenResponse, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return oauthTokenResponse{}, err
	}
	if detail := identityProviderResponseError(payload); detail != "" {
		return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_failed", detail)
	}
	if nested, ok := payload["data"].(map[string]any); ok {
		payload = nested
	}
	token := oauthTokenResponse{
		AccessToken:  firstOAuthClaim(payload, "access_token", "accessToken"),
		RefreshToken: firstOAuthClaim(payload, "refresh_token", "refreshToken"),
		TokenType:    firstOAuthClaim(payload, "token_type", "tokenType"),
		IDToken:      firstOAuthClaim(payload, "id_token", "idToken"),
		Scope:        firstOAuthClaim(payload, "scope"),
	}
	token.ExpiresIn = identityProviderInt64(payload, "expires_in", "expireIn")
	if token.AccessToken == "" {
		return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_missing", "OAuth token endpoint did not return an access token")
	}
	return token, nil
}

func identityProviderInt64(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case float64:
			return int64(value)
		case json.Number:
			parsed, _ := value.Int64()
			return parsed
		case string:
			parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			return parsed
		}
	}
	return 0
}

func identityProviderResponseError(payload map[string]any) string {
	if errCode := firstOAuthClaim(payload, "errcode"); errCode != "" && errCode != "0" {
		return identityProviderErrorDetail(payload, errCode, "errmsg", "message", "msg", "error")
	}
	if code := firstOAuthClaim(payload, "code"); code != "" && code != "0" {
		return identityProviderErrorDetail(payload, code, "message", "msg", "errmsg", "error")
	}
	if errorCode := firstOAuthClaim(payload, "error"); errorCode != "" {
		return identityProviderErrorDetail(payload, errorCode, "error_description", "message", "msg", "error")
	}
	return ""
}

func identityProviderErrorDetail(payload map[string]any, fallback string, keys ...string) string {
	if detail := firstOAuthClaim(payload, keys...); detail != "" {
		return detail
	}
	return "Identity provider error " + fallback
}

func identityProviderFallbackEmail(provider AdminResource, claims map[string]any) string {
	templateKey := identityProviderTemplateKey(provider)
	switch templateKey {
	case identityProviderDingTalk, identityProviderFeishu, identityProviderWeCom:
	default:
		return ""
	}
	subjectClaim := strings.TrimSpace(stringField(provider.Fields, "subject_claim"))
	subject := firstOAuthClaim(claims, subjectClaim, "sub", "unionId", "union_id", "openId", "open_id", "user_id", "userid", "UserId")
	if subject == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(provider.ID + "\x00" + subject))
	return fmt.Sprintf("oauth-%x@%s.tokenhub.local", sum[:12], templateKey)
}
