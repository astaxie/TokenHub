package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGenericIdentityProviderAuthorizeURLIncludesPKCE(t *testing.T) {
	provider := AdminResource{Fields: map[string]any{
		"client_id":     "generic-client",
		"authorize_url": "https://idp.example.test/authorize",
		"scopes":        "openid profile",
	}}
	challenge := testAdminOAuthCodeChallenge(t)
	target, err := buildIdentityProviderAuthorizeURL(
		provider, "https://tokenhub.example.test/api/admin/auth/oauth/callback", "signed-state", challenge)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("code_challenge") != challenge || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("generic OAuth authorize URL must retain provider PKCE: %s", parsed.RawQuery)
	}
}

func TestDingTalkIdentityProviderLoginProtocol(t *testing.T) {
	var tokenRequested bool
	var userInfoRequested bool
	providerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/userAccessToken":
			tokenRequested = true
			if r.Method != http.MethodPost || r.Header.Get("content-type") != "application/json" {
				t.Fatalf("unexpected token request: method=%s content-type=%s", r.Method, r.Header.Get("content-type"))
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["clientId"] != "ding-app-key" || payload["clientSecret"] != "ding-app-secret" ||
				payload["code"] != "ding-code" || payload["grantType"] != "authorization_code" {
				t.Fatalf("unexpected token payload: %+v", payload)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"accessToken":  "ding-access-token",
				"refreshToken": "ding-refresh-token",
				"expireIn":     7200,
			})
		case "/contact/users/me":
			userInfoRequested = true
			if r.Header.Get("x-acs-dingtalk-access-token") != "ding-access-token" || r.Header.Get("authorization") != "" {
				t.Fatalf("unexpected userinfo headers: %+v", r.Header)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"nick":    "Ding User",
				"unionId": "ding-union-id",
				"openId":  "ding-open-id",
			})
		default:
			t.Fatalf("unexpected provider API path: %s", r.URL.Path)
		}
	}))
	defer providerAPI.Close()

	provider := AdminResource{
		ID:     "idp_dingtalk",
		Name:   "DingTalk",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_template": "dingtalk",
			"provider_type":     "oauth2",
			"client_id":         "ding-app-key",
			"client_secret":     "ding-app-secret",
			"authorize_url":     providerAPI.URL + "/oauth2/auth",
			"token_url":         providerAPI.URL + "/oauth2/userAccessToken",
			"userinfo_url":      providerAPI.URL + "/contact/users/me",
			"scopes":            "openid",
			"username_claim":    "unionId",
			"email_claim":       "email",
			"subject_claim":     "unionId",
		},
	}

	authorizeChallenge := testAdminOAuthCodeChallenge(t)
	authorizeTarget, err := buildIdentityProviderAuthorizeURL(provider, "https://tokenhub.example.test/api/admin/auth/oauth/callback", "signed-state", authorizeChallenge)
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorizeTarget, err := url.Parse(authorizeTarget)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedAuthorizeTarget.Query()
	if query.Get("client_id") != "ding-app-key" || query.Get("response_type") != "code" ||
		query.Get("scope") != "openid" || query.Get("state") != "signed-state" || query.Get("prompt") != "consent" ||
		query.Has("code_challenge") || query.Has("code_challenge_method") {
		t.Fatalf("unexpected authorize query: %s", parsedAuthorizeTarget.RawQuery)
	}

	store := NewMemoryStore()
	existing, err := store.CreateAdminUser(AdminUser{
		Username: "ding-union-id",
		Name:     "Local User",
		Email:    "local.user@example.test",
		Role:     "user",
		Status:   StatusActive,
	}, "local-password")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("identity-providers", provider)
	app := NewWithConfig(store, Config{
		AdminToken: "dev_admin_token", SecretKey: "test-secret",
		CORSAllowedOrigins: []string{"http://localhost:3000"},
	}).Handler()
	startReq := httptest.NewRequest(http.MethodGet, adminOAuthStartURLForTest(t, "/api/admin/auth/oauth/start?id=idp_dingtalk&return_url=http%3A%2F%2Flocalhost%3A3000%2Foverview"), nil)
	startReq.Host = "localhost:8080"
	startResp := httptest.NewRecorder()
	app.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusFound {
		t.Fatalf("expected start redirect, got %d: %s", startResp.Code, startResp.Body.String())
	}
	startLocation, err := url.Parse(startResp.Header().Get("location"))
	if err != nil {
		t.Fatal(err)
	}
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/admin/auth/oauth/callback?authCode=ding-code&state="+url.QueryEscape(startLocation.Query().Get("state")), nil)
	callbackReq.Host = "localhost:8080"
	callbackReq.AddCookie(requireResponseCookieWithPrefix(t, startResp, adminOAuthStateCookiePrefix))
	callbackResp := httptest.NewRecorder()
	app.ServeHTTP(callbackResp, callbackReq)
	callbackLocation, parseErr := url.Parse(callbackResp.Header().Get("location"))
	if callbackResp.Code != http.StatusFound || parseErr != nil || callbackLocation.Query().Has("oauth_code") || !strings.Contains(callbackLocation.Fragment, "oauth_code=") {
		t.Fatalf("unexpected callback response: status=%d location=%s", callbackResp.Code, callbackResp.Header().Get("location"))
	}
	var user AdminUser
	for _, candidate := range store.ListAdminUsers() {
		if strings.HasSuffix(candidate.Email, "@dingtalk.tokenhub.local") {
			user = candidate
			break
		}
	}
	if user.ID == "" || user.ID == existing.ID || user.Name != "Ding User" || user.Username == "ding-union-id" {
		t.Fatalf("unexpected DingTalk user: %+v", user)
	}
	if !tokenRequested || !userInfoRequested {
		t.Fatalf("expected token and userinfo requests, token=%v userinfo=%v", tokenRequested, userInfoRequested)
	}
}

func TestFeishuIdentityProviderLoginProtocol(t *testing.T) {
	providerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authen/v2/oauth/token":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["client_id"] != "feishu-app-id" || payload["client_secret"] != "feishu-app-secret" ||
				payload["code"] != "feishu-code" || payload["grant_type"] != "authorization_code" ||
				payload["redirect_uri"] != "https://tokenhub.example.test/api/admin/auth/oauth/callback" {
				t.Fatalf("unexpected token payload: %+v", payload)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"access_token":  "feishu-access-token",
				"refresh_token": "feishu-refresh-token",
				"expires_in":    7200,
			})
		case "/authen/v1/user_info":
			if r.Header.Get("authorization") != "Bearer feishu-access-token" {
				t.Fatalf("unexpected userinfo authorization: %s", r.Header.Get("authorization"))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{
					"name":             "Feishu User",
					"union_id":         "feishu-union-id",
					"enterprise_email": "feishu.user@example.test",
				},
			})
		default:
			t.Fatalf("unexpected provider API path: %s", r.URL.Path)
		}
	}))
	defer providerAPI.Close()

	provider := AdminResource{
		ID:     "idp_feishu",
		Name:   "Feishu",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_template": "feishu",
			"provider_type":     "oauth2",
			"client_id":         "feishu-app-id",
			"client_secret":     "feishu-app-secret",
			"authorize_url":     providerAPI.URL + "/authen/v1/authorize",
			"token_url":         providerAPI.URL + "/authen/v2/oauth/token",
			"userinfo_url":      providerAPI.URL + "/authen/v1/user_info",
			"scopes":            "contact:user.base:readonly",
			"username_claim":    "union_id",
			"email_claim":       "enterprise_email",
			"subject_claim":     "union_id",
		},
	}

	authorizeChallenge := testAdminOAuthCodeChallenge(t)
	authorizeTarget, err := buildIdentityProviderAuthorizeURL(provider, "https://tokenhub.example.test/api/admin/auth/oauth/callback", "signed-state", authorizeChallenge)
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorizeTarget, err := url.Parse(authorizeTarget)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedAuthorizeTarget.Query()
	if query.Get("app_id") != "feishu-app-id" || query.Get("redirect_uri") == "" || query.Get("state") != "signed-state" ||
		query.Get("scope") != "contact:user.base:readonly" ||
		query.Has("client_id") || query.Has("response_type") ||
		query.Has("code_challenge") || query.Has("code_challenge_method") {
		t.Fatalf("unexpected authorize query: %s", parsedAuthorizeTarget.RawQuery)
	}

	server := New(NewMemoryStore())
	token, err := server.exchangeOAuthCode(context.Background(), provider, "feishu-code", "https://tokenhub.example.test/api/admin/auth/oauth/callback", "")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := server.fetchOAuthUserInfo(context.Background(), provider, token.AccessToken, "feishu-code")
	if err != nil {
		t.Fatal(err)
	}
	if firstOAuthClaim(claims, "name") != "Feishu User" || firstOAuthClaim(claims, "enterprise_email") != "feishu.user@example.test" {
		t.Fatalf("unexpected Feishu claims: %+v", claims)
	}
}

func TestWeComIdentityProviderLoginProtocol(t *testing.T) {
	providerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.Method != http.MethodGet || r.URL.Query().Get("corpid") != "ww-corp-id" || r.URL.Query().Get("corpsecret") != "wecom-secret" {
				t.Fatalf("unexpected app token request: %s %s", r.Method, r.URL.String())
			}
			writeJSON(w, http.StatusOK, map[string]any{"errcode": 0, "errmsg": "ok", "access_token": "wecom-access-token", "expires_in": 7200})
		case "/cgi-bin/auth/getuserinfo":
			if r.URL.Query().Get("access_token") != "wecom-access-token" || r.URL.Query().Get("code") != "wecom-code" {
				t.Fatalf("unexpected identity request: %s", r.URL.String())
			}
			writeJSON(w, http.StatusOK, map[string]any{"errcode": 0, "errmsg": "ok", "userid": "zhangsan"})
		case "/cgi-bin/user/get":
			if r.URL.Query().Get("access_token") != "wecom-access-token" || r.URL.Query().Get("userid") != "zhangsan" {
				t.Fatalf("unexpected user detail request: %s", r.URL.String())
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"errcode":         0,
				"errmsg":          "ok",
				"userid":          "zhangsan",
				"name":            "WeCom User",
				"biz_mail":        "zhangsan@example.test",
				"main_department": 42,
			})
		default:
			t.Fatalf("unexpected provider API path: %s", r.URL.Path)
		}
	}))
	defer providerAPI.Close()

	provider := AdminResource{
		ID:     "idp_wecom",
		Name:   "WeCom",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_template": "wecom",
			"provider_type":     "oauth2",
			"client_id":         "ww-corp-id",
			"client_secret":     "wecom-secret",
			"agent_id":          "1000002",
			"authorize_url":     providerAPI.URL + "/wwlogin/sso/login",
			"token_url":         providerAPI.URL + "/cgi-bin/gettoken",
			"userinfo_url":      providerAPI.URL + "/cgi-bin/auth/getuserinfo",
			"userdetail_url":    providerAPI.URL + "/cgi-bin/user/get",
			"username_claim":    "userid",
			"email_claim":       "biz_mail",
			"team_claim":        "main_department",
			"subject_claim":     "userid",
		},
	}

	authorizeChallenge := testAdminOAuthCodeChallenge(t)
	authorizeTarget, err := buildIdentityProviderAuthorizeURL(provider, "https://tokenhub.example.test/api/admin/auth/oauth/callback", "signed-state", authorizeChallenge)
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorizeTarget, err := url.Parse(authorizeTarget)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedAuthorizeTarget.Query()
	if query.Get("login_type") != "CorpApp" || query.Get("appid") != "ww-corp-id" || query.Get("agentid") != "1000002" ||
		query.Get("redirect_uri") == "" || query.Get("state") != "signed-state" || query.Has("client_id") || query.Has("scope") ||
		query.Has("code_challenge") || query.Has("code_challenge_method") {
		t.Fatalf("unexpected authorize query: %s", parsedAuthorizeTarget.RawQuery)
	}

	server := New(NewMemoryStore())
	token, err := server.exchangeOAuthCode(context.Background(), provider, "wecom-code", "https://tokenhub.example.test/api/admin/auth/oauth/callback", "")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := server.fetchOAuthUserInfo(context.Background(), provider, token.AccessToken, "wecom-code")
	if err != nil {
		t.Fatal(err)
	}
	if firstOAuthClaim(claims, "userid") != "zhangsan" || firstOAuthClaim(claims, "biz_mail") != "zhangsan@example.test" ||
		firstOAuthClaim(claims, "main_department") != "42" {
		t.Fatalf("unexpected WeCom claims: %+v", claims)
	}
}
