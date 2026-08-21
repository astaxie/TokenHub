package server

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type adminOAuthTestSession struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      AdminUser `json:"user"`
}

const testAdminOAuthCodeVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

func testAdminOAuthCodeChallenge(t *testing.T) string {
	t.Helper()
	challenge, ok := adminOAuthCodeChallenge(testAdminOAuthCodeVerifier)
	if !ok {
		t.Fatal("test OAuth code verifier is invalid")
	}
	return challenge
}

func adminOAuthStartURLForTest(t *testing.T, rawURL string) string {
	t.Helper()
	target, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	query.Set("code_challenge", testAdminOAuthCodeChallenge(t))
	query.Set("code_challenge_method", "S256")
	target.RawQuery = query.Encode()
	return target.String()
}

func requireResponseCookieWithPrefix(t *testing.T, response *httptest.ResponseRecorder, prefix string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if strings.HasPrefix(cookie.Name, prefix) && cookie.Value != "" && cookie.MaxAge >= 0 {
			return cookie
		}
	}
	t.Fatalf("response did not set cookie with prefix %q: %v", prefix, response.Header().Values("Set-Cookie"))
	return nil
}

func exchangeAdminOAuthCodeForTest(t *testing.T, handler http.Handler, code string, codeVerifier string) adminOAuthTestSession {
	t.Helper()
	body, err := json.Marshal(map[string]string{"code": code, "code_verifier": codeVerifier})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/oauth/exchange", bytes.NewReader(body))
	request.Header.Set("content-type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("OAuth code exchange failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var session adminOAuthTestSession
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Token == "" || session.User.ID == "" || session.ExpiresAt.IsZero() {
		t.Fatalf("incomplete OAuth session response: %s", response.Body.String())
	}
	return session
}

func TestIdentityProviderMutationsRequirePlatformAdmin(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.CreateAdminUser(AdminUser{
		Username: "platform-admin", Email: "platform-admin@example.test", Role: "admin", Status: StatusActive,
	}, "platform-password")
	if err != nil {
		t.Fatal(err)
	}
	securityUser, err := store.CreateAdminUser(AdminUser{
		Username: "security-admin", Email: "security-admin@example.test", Role: "security_admin", Status: StatusActive,
	}, "security-password")
	if err != nil {
		t.Fatal(err)
	}
	_, securitySession, err := store.CreateAdminSession(securityUser.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	provider := store.CreateResource("identity-providers", AdminResource{
		ID: "idp_existing", Name: "Existing IdP", Status: StatusActive,
	})
	app := NewWithConfig(store, Config{AdminToken: "platform-token"}).Handler()

	read := doJSON(t, app, http.MethodGet, "/api/admin/resources/identity-providers", nil, securitySession.Token)
	if read.Code != http.StatusOK || !strings.Contains(read.Body, provider.ID) {
		t.Fatalf("security admin should retain IdP read access: status=%d body=%s", read.Code, read.Body)
	}
	mutations := []struct {
		method  string
		path    string
		payload any
	}{
		{http.MethodPost, "/api/admin/resources/identity-providers", map[string]any{"name": "Malicious IdP"}},
		{http.MethodPatch, "/api/admin/resources/identity-providers/" + provider.ID, map[string]any{"name": "Hijacked IdP"}},
		{http.MethodDelete, "/api/admin/resources/identity-providers/" + provider.ID, nil},
	}
	for _, mutation := range mutations {
		response := doJSON(t, app, mutation.method, mutation.path, mutation.payload, securitySession.Token)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body, "admin_forbidden") {
			t.Fatalf("security admin %s should be forbidden: status=%d body=%s", mutation.method, response.Code, response.Body)
		}
	}
	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/identity-providers", map[string]any{"name": "Platform IdP"}, "platform-token")
	if created.Code != http.StatusCreated {
		t.Fatalf("platform admin should manage IdPs: status=%d body=%s", created.Code, created.Body)
	}
}

func TestOAuthProvisioningDoesNotBindByUsername(t *testing.T) {
	store := NewMemoryStore()
	existing, err := store.CreateAdminUser(AdminUser{
		Username: "platform-admin", Email: "admin@example.test", Role: "admin", Status: StatusActive,
	}, "platform-password")
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	provider := AdminResource{
		ID: "idp_untrusted_username", Name: "Enterprise SSO", Status: StatusActive,
		Fields: map[string]any{"username_claim": "preferred_username", "email_claim": "email", "default_role": "user"},
	}

	provisioned, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"preferred_username": existing.Username,
		"email":              "attacker@example.test",
		"name":               "Unrelated IdP User",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provisioned.ID == existing.ID || provisioned.Role != "user" || provisioned.Email != "attacker@example.test" {
		t.Fatalf("same-name IdP account bound existing administrator: %+v", provisioned)
	}
	if provisioned.Username == existing.Username {
		t.Fatalf("same-name IdP account retained conflicting username: %q", provisioned.Username)
	}
	var unchanged AdminUser
	for _, candidate := range store.ListAdminUsers() {
		if candidate.ID == existing.ID {
			unchanged = candidate
			break
		}
	}
	if unchanged.ID == "" || unchanged.Email != existing.Email || unchanged.Role != "admin" {
		t.Fatalf("existing administrator was modified by same-name IdP login: %+v", unchanged)
	}
}

func TestOAuthProvisioningRejectsExplicitlyUnverifiedEmail(t *testing.T) {
	store := NewMemoryStore()
	existing, err := store.CreateAdminUser(AdminUser{
		Username: "platform-admin", Email: "admin@example.test", Role: "admin", Status: StatusActive,
	}, "platform-password")
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	provider := AdminResource{
		ID: "idp_unverified_email", Name: "Enterprise SSO", Status: StatusActive,
		Fields: map[string]any{"username_claim": "preferred_username", "email_claim": "email", "default_role": "user"},
	}

	for _, unverified := range []any{false, nil, "false", "unexpected", float64(1)} {
		_, err = server.upsertOAuthAdminUser(provider, map[string]any{
			"preferred_username": "unrelated-user",
			"email":              existing.Email,
			"email_verified":     unverified,
		})
		if httpErr := AsHTTPError(err); httpErr.Code != "oauth_email_unverified" || httpErr.Status != http.StatusForbidden {
			t.Fatalf("unverified email value %#v was accepted: err=%v", unverified, err)
		}
	}
	users := store.ListAdminUsers()
	if len(users) != 1 || users[0].ID != existing.ID || users[0].Email != existing.Email || users[0].Role != "admin" {
		t.Fatalf("unverified email changed administrator records: %+v", users)
	}
	_, err = server.upsertOAuthAdminUser(provider, map[string]any{
		"preferred_username": "new-user",
		"email":              "new@example.test",
		"email_verified":     false,
	})
	if httpErr := AsHTTPError(err); httpErr.Code != "oauth_email_unverified" || len(store.ListAdminUsers()) != 1 {
		t.Fatalf("unverified email created a user: err=%v users=%+v", err, store.ListAdminUsers())
	}
	verified, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"preferred_username": existing.Username,
		"email":              existing.Email,
		"email_verified":     true,
	})
	if err != nil || verified.ID != existing.ID {
		t.Fatalf("verified email did not bind existing account: user=%+v err=%v", verified, err)
	}
}

func TestSafeOAuthReturnURLIgnoresOriginAndReferer(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{
		PublicBaseURL:      "https://api.tokenhub.example",
		CORSAllowedOrigins: []string{"not-an-origin", "https://console.tokenhub.example:8443"},
	})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	request := httptest.NewRequest(http.MethodGet, "http://internal:8080/api/admin/auth/oauth/start", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Referer", "https://attacker.example/login")

	if got := server.safeOAuthReturnURL("https://attacker.example/steal", request); got != "https://console.tokenhub.example:8443/overview" {
		t.Fatalf("untrusted return URL fell back to %q", got)
	}
	if got := server.safeOAuthReturnURL("", request); got != "https://console.tokenhub.example:8443/overview" {
		t.Fatalf("empty return URL trusted request headers: %q", got)
	}
	if got := server.safeOAuthReturnURL("https://console.tokenhub.example:8443/settings?tab=sso", request); got != "https://console.tokenhub.example:8443/settings?tab=sso" {
		t.Fatalf("configured CORS origin should be allowed: %q", got)
	}
	if got := server.safeOAuthReturnURL("https://api.tokenhub.example/settings", request); got != "https://api.tokenhub.example/settings" {
		t.Fatalf("public base origin should be allowed: %q", got)
	}
	if got := server.safeOAuthReturnURL("https://api.tokenhub.example/overview?language=zh&oauth_token=attacker#oauth_code=stale", request); got != "https://api.tokenhub.example/overview?language=zh" {
		t.Fatalf("reserved OAuth parameters were retained: %q", got)
	}
	for _, candidate := range []string{
		"https://console.tokenhub.example/settings",
		"http://console.tokenhub.example:8443/settings",
		"https://console.tokenhub.example:9443/settings",
		"http://localhost:3000/overview",
	} {
		if got := server.safeOAuthReturnURL(candidate, request); got != "https://console.tokenhub.example:8443/overview" {
			t.Fatalf("return URL %q bypassed exact-origin validation: %q", candidate, got)
		}
	}

	request.Host = "admin.internal.test:8080"
	unconfigured := &Server{}
	if got := unconfigured.safeOAuthReturnURL("https://attacker.example/steal", request); got != "http://admin.internal.test:8080/overview" {
		t.Fatalf("request Host fallback = %q", got)
	}

	loopbackRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/admin/auth/oauth/start", nil)
	if got := unconfigured.safeOAuthReturnURL("http://localhost:3000/settings", loopbackRequest); got != "http://127.0.0.1:8080/overview" {
		t.Fatalf("unconfigured loopback return URL = %q", got)
	}
}

func TestOAuthLoopbackReturnURLRequiresExactConfiguredOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/admin/auth/oauth/start", nil)
	unconfigured := NewWithConfig(NewMemoryStore(), Config{})
	t.Cleanup(func() { _ = unconfigured.Shutdown(t.Context()) })
	for _, candidate := range []string{
		"http://127.0.0.1:8080/settings",
		"http://localhost:3000/settings",
		"https://localhost:3000/settings",
		"http://[::1]:3000/settings",
	} {
		if got := unconfigured.safeOAuthReturnURL(candidate, request); got != "http://127.0.0.1:8080/overview" {
			t.Fatalf("unconfigured loopback return URL %q was accepted: %q", candidate, got)
		}
	}

	corsConfigured := NewWithConfig(NewMemoryStore(), Config{CORSAllowedOrigins: []string{"http://localhost:3000"}})
	t.Cleanup(func() { _ = corsConfigured.Shutdown(t.Context()) })
	if got := corsConfigured.safeOAuthReturnURL("http://localhost:3000/settings?tab=sso", request); got != "http://localhost:3000/settings?tab=sso" {
		t.Fatalf("configured loopback CORS origin was rejected: %q", got)
	}
	for _, candidate := range []string{
		"http://localhost:3001/settings",
		"https://localhost:3000/settings",
		"http://127.0.0.1:3000/settings",
		"http://[::1]:3000/settings",
	} {
		if got := corsConfigured.safeOAuthReturnURL(candidate, request); got != "http://localhost:3000/overview" {
			t.Fatalf("loopback return URL %q bypassed configured CORS origin: %q", candidate, got)
		}
	}

	publicConfigured := NewWithConfig(NewMemoryStore(), Config{PublicBaseURL: "http://127.0.0.1:4000/base"})
	t.Cleanup(func() { _ = publicConfigured.Shutdown(t.Context()) })
	if got := publicConfigured.safeOAuthReturnURL("http://127.0.0.1:4000/settings", request); got != "http://127.0.0.1:4000/settings" {
		t.Fatalf("configured loopback public origin was rejected: %q", got)
	}
	if got := publicConfigured.safeOAuthReturnURL("http://localhost:4000/settings", request); got != "http://127.0.0.1:4000/overview" {
		t.Fatalf("loopback host alias bypassed configured public origin: %q", got)
	}
}

func TestAdminOAuthStartRequiresS256PKCE(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID: "idp_pkce", Name: "PKCE IdP", Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2", "client_id": "client", "authorize_url": "https://idp.example/authorize",
			"token_url": "https://idp.example/token", "userinfo_url": "https://idp.example/userinfo",
			"redirect_uri": "https://tokenhub.example/api/admin/auth/oauth/callback",
		},
	})
	app := New(store).Handler()

	for _, rawURL := range []string{
		"/api/admin/auth/oauth/start?id=idp_pkce",
		"/api/admin/auth/oauth/start?id=idp_pkce&code_challenge=invalid&code_challenge_method=S256",
		"/api/admin/auth/oauth/start?id=idp_pkce&code_challenge=" + url.QueryEscape(testAdminOAuthCodeChallenge(t)) + "&code_challenge_method=plain",
	} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, rawURL, nil))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_oauth_code_challenge") {
			t.Fatalf("OAuth start without valid PKCE = %d: %s", response.Code, response.Body.String())
		}
	}

	validResponse := httptest.NewRecorder()
	app.ServeHTTP(validResponse, httptest.NewRequest(http.MethodGet, adminOAuthStartURLForTest(t, "/api/admin/auth/oauth/start?id=idp_pkce"), nil))
	if validResponse.Code != http.StatusFound {
		t.Fatalf("OAuth start with S256 PKCE = %d: %s", validResponse.Code, validResponse.Body.String())
	}
}

func TestOAuthCallbackURLUsesOnlyConfiguredOrTrustedOrigins(t *testing.T) {
	untrusted := NewWithConfig(NewMemoryStore(), Config{})
	request := httptest.NewRequest(http.MethodGet, "https://api.tokenhub.example/api/admin/auth/oauth/start", nil)
	request.RemoteAddr = "198.51.100.7:4321"
	request.Header.Set("X-Forwarded-Proto", "http")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	got, err := untrusted.oauthCallbackURL(request)
	if err != nil || got != "https://api.tokenhub.example/api/admin/auth/oauth/callback" {
		t.Fatalf("untrusted forwarded headers changed callback: callback=%q err=%v", got, err)
	}

	configured := NewWithConfig(NewMemoryStore(), Config{PublicBaseURL: "https://public.tokenhub.example/base"})
	got, err = configured.oauthCallbackURL(request)
	if err != nil || got != "https://public.tokenhub.example/api/admin/auth/oauth/callback" {
		t.Fatalf("public base callback = %q, err=%v", got, err)
	}

	trusted := NewWithConfig(NewMemoryStore(), Config{TrustedProxyCIDRs: []string{"10.0.0.0/8"}})
	trustedRequest := httptest.NewRequest(http.MethodGet, "http://tokenhub-backend:8080/api/admin/auth/oauth/start", nil)
	trustedRequest.RemoteAddr = "10.0.0.8:4321"
	trustedRequest.Header.Set("X-Forwarded-Proto", "https")
	trustedRequest.Header.Set("X-Forwarded-Host", "gateway.tokenhub.example")
	got, err = trusted.oauthCallbackURL(trustedRequest)
	if err != nil || got != "https://gateway.tokenhub.example/api/admin/auth/oauth/callback" {
		t.Fatalf("trusted proxy callback = %q, err=%v", got, err)
	}
}

func TestIdentityProviderRedirectURIRejectsInsecureExternalHTTP(t *testing.T) {
	server := New(NewMemoryStore())
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/admin/auth/oauth/start", nil)
	provider := AdminResource{Fields: map[string]any{"redirect_uri": "http://oauth.example/api/admin/auth/oauth/callback"}}
	if _, err := server.identityProviderRedirectURI(provider, request); AsHTTPError(err).Code != "insecure_redirect_uri" {
		t.Fatalf("external HTTP callback error = %v", err)
	}
	provider.Fields["redirect_uri"] = "http://localhost:8080/api/admin/auth/oauth/callback"
	if got, err := server.identityProviderRedirectURI(provider, request); err != nil || got != provider.Fields["redirect_uri"] {
		t.Fatalf("loopback callback = %q, err=%v", got, err)
	}
}

func TestAdminOAuthStateCookieIsBrowserBoundAndSingleUse(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID: "idp_browser_bound", Name: "Browser-bound IdP", Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2", "client_id": "client", "authorize_url": "https://idp.example/authorize",
			"token_url": "https://idp.example/token", "userinfo_url": "https://idp.example/userinfo",
			"redirect_uri": "https://tokenhub.example/api/admin/auth/oauth/callback",
		},
	})
	app := NewWithConfig(store, Config{PublicBaseURL: "https://tokenhub.example", SecretKey: "test-secret"}).Handler()
	startRequest := httptest.NewRequest(http.MethodGet, adminOAuthStartURLForTest(t, "https://tokenhub.example/api/admin/auth/oauth/start?id=idp_browser_bound"), nil)
	startResponse := httptest.NewRecorder()
	app.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusFound {
		t.Fatalf("OAuth start failed: status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	stateCookie := requireResponseCookieWithPrefix(t, startResponse, adminOAuthStateCookiePrefix)
	if !stateCookie.HttpOnly || !stateCookie.Secure || stateCookie.SameSite != http.SameSiteLaxMode || stateCookie.Path != adminOAuthStateCookiePath {
		t.Fatalf("unsafe OAuth state cookie: %+v", stateCookie)
	}
	authorizeURL, err := url.Parse(startResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	callbackURL := "https://tokenhub.example/api/admin/auth/oauth/callback?error=access_denied&state=" + url.QueryEscape(state)

	withoutCookie := httptest.NewRecorder()
	app.ServeHTTP(withoutCookie, httptest.NewRequest(http.MethodGet, callbackURL, nil))
	if withoutCookie.Code != http.StatusBadRequest || !strings.Contains(withoutCookie.Body.String(), "invalid_oauth_state") {
		t.Fatalf("callback without browser cookie should fail: status=%d body=%s", withoutCookie.Code, withoutCookie.Body.String())
	}

	callbackRequest := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callbackRequest.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	app.ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusFound {
		t.Fatalf("bound callback failed: status=%d body=%s", callbackResponse.Code, callbackResponse.Body.String())
	}
	location, err := url.Parse(callbackResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Has("oauth_error") || !strings.Contains(location.Fragment, "oauth_error=provider_error") {
		t.Fatalf("OAuth error must only be returned in fragment: %s", location.String())
	}

	replayRequest := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	replayRequest.AddCookie(stateCookie)
	replayResponse := httptest.NewRecorder()
	app.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusBadRequest || !strings.Contains(replayResponse.Body.String(), "invalid_oauth_state") {
		t.Fatalf("consumed OAuth state was reusable: status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
}

func TestAdminOAuthTokenExchangeErrorsDoNotLeakIntoRedirects(t *testing.T) {
	const authorizationCode = "sensitive-authorization-code"
	providerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code=` + authorizationCode + `&client_secret=server-echo-secret"}`))
	}))
	t.Cleanup(providerAPI.Close)

	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID: "idp_error_redaction", Name: "Error Redaction IdP", Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2", "client_id": "client", "authorize_url": "https://idp.example/authorize",
			"token_url": providerAPI.URL, "userinfo_url": "https://idp.example/userinfo",
			"redirect_uri": "https://tokenhub.example/api/admin/auth/oauth/callback",
		},
	})
	app := NewWithConfig(store, Config{PublicBaseURL: "https://tokenhub.example", SecretKey: "test-secret"}).Handler()
	startRequest := httptest.NewRequest(http.MethodGet, adminOAuthStartURLForTest(t, "https://tokenhub.example/api/admin/auth/oauth/start?id=idp_error_redaction"), nil)
	startResponse := httptest.NewRecorder()
	app.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusFound {
		t.Fatalf("OAuth start failed: status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	stateCookie := requireResponseCookieWithPrefix(t, startResponse, adminOAuthStateCookiePrefix)
	authorizeURL, err := url.Parse(startResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callbackURL := "https://tokenhub.example/api/admin/auth/oauth/callback?code=" + url.QueryEscape(authorizationCode) + "&state=" + url.QueryEscape(authorizeURL.Query().Get("state"))
	callbackRequest := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callbackRequest.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	app.ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusFound {
		t.Fatalf("OAuth callback failed: status=%d body=%s", callbackResponse.Code, callbackResponse.Body.String())
	}
	location, err := url.Parse(callbackResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(location.Fragment)
	if err != nil || fragment.Get("oauth_error") != "token_exchange_failed" {
		t.Fatalf("OAuth redirect error = %q, err=%v", location.Fragment, err)
	}
	if location.Query().Has("oauth_error") {
		t.Fatalf("OAuth error leaked into query: %s", location.String())
	}
	for _, secret := range []string{authorizationCode, "server-echo-secret", "invalid_grant", "error_description"} {
		if strings.Contains(location.String(), secret) {
			t.Fatalf("OAuth redirect leaked token endpoint detail %q: %s", secret, location.String())
		}
	}
}

func TestAdminOAuthAuthorizationIsPKCEBoundToIdentityProvider(t *testing.T) {
	var tokenForm url.Values
	providerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			tokenForm = r.PostForm
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"provider-access-token","token_type":"Bearer","expires_in":3600}`))
		case "/userinfo":
			if r.Header.Get("authorization") != "Bearer provider-access-token" {
				t.Fatalf("unexpected userinfo authorization header: %q", r.Header.Get("authorization"))
			}
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"sub":"pkce-bound-subject","preferred_username":"pkce-user","name":"PKCE User","email":"pkce-user@example.test","email_verified":true}`))
		default:
			t.Fatalf("unexpected provider API path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(providerAPI.Close)

	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID: "idp_pkce_bound", Name: "PKCE Bound IdP", Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2", "client_id": "client", "client_secret": "secret",
			"authorize_url":  "https://idp.example/authorize",
			"token_url":      providerAPI.URL + "/token",
			"userinfo_url":   providerAPI.URL + "/userinfo",
			"redirect_uri":   "https://tokenhub.example/api/admin/auth/oauth/callback",
			"username_claim": "preferred_username", "email_claim": "email",
		},
	})
	app := NewWithConfig(store, Config{PublicBaseURL: "https://tokenhub.example", SecretKey: "test-secret"}).Handler()

	startResponse := httptest.NewRecorder()
	app.ServeHTTP(startResponse, httptest.NewRequest(http.MethodGet, adminOAuthStartURLForTest(t, "https://tokenhub.example/api/admin/auth/oauth/start?id=idp_pkce_bound"), nil))
	if startResponse.Code != http.StatusFound {
		t.Fatalf("OAuth start failed: status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	authorizeTarget, err := url.Parse(startResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	providerChallenge := authorizeTarget.Query().Get("code_challenge")
	if !validAdminOAuthCodeChallenge(providerChallenge) {
		t.Fatalf("authorize redirect is missing a valid S256 code challenge: %s", authorizeTarget.String())
	}
	if authorizeTarget.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize redirect must declare code_challenge_method=S256: %s", authorizeTarget.String())
	}
	if providerChallenge == testAdminOAuthCodeChallenge(t) {
		t.Fatalf("identity provider challenge must be independent of the console PKCE challenge: %s", authorizeTarget.String())
	}
	stateCookie := requireResponseCookieWithPrefix(t, startResponse, adminOAuthStateCookiePrefix)

	callbackRequest := httptest.NewRequest(http.MethodGet, "https://tokenhub.example/api/admin/auth/oauth/callback?code=provider-authorization-code&state="+url.QueryEscape(authorizeTarget.Query().Get("state")), nil)
	callbackRequest.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	app.ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusFound {
		t.Fatalf("OAuth callback failed: status=%d body=%s", callbackResponse.Code, callbackResponse.Body.String())
	}
	if tokenForm == nil {
		t.Fatal("identity provider token endpoint was not called")
	}
	if tokenForm.Get("code_verifier") == "" {
		t.Fatalf("token exchange omitted the PKCE code verifier: %v", tokenForm)
	}
	derivedChallenge, ok := adminOAuthCodeChallenge(tokenForm.Get("code_verifier"))
	if !ok || derivedChallenge != providerChallenge {
		t.Fatalf("token exchange verifier does not match the authorize challenge: verifier=%q challenge=%q", tokenForm.Get("code_verifier"), providerChallenge)
	}
	if tokenForm.Get("grant_type") != "authorization_code" || tokenForm.Get("code") != "provider-authorization-code" ||
		tokenForm.Get("redirect_uri") != "https://tokenhub.example/api/admin/auth/oauth/callback" {
		t.Fatalf("unexpected token exchange form: %v", tokenForm)
	}

	callbackLocation, err := url.Parse(callbackResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(callbackLocation.Fragment)
	if err != nil || fragment.Get("oauth_code") == "" {
		t.Fatalf("callback did not return an exchange code in the fragment: %s", callbackLocation.String())
	}
	session := exchangeAdminOAuthCodeForTest(t, app, fragment.Get("oauth_code"), testAdminOAuthCodeVerifier)
	if session.User.Email != "pkce-user@example.test" {
		t.Fatalf("unexpected provisioned administrator: %+v", session.User)
	}
}

func TestAdminOAuthExchangeIsPKCEBoundAndSingleUse(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "oauth-user", Email: "oauth-user@example.test", Role: "user", Status: StatusActive,
	}, "oauth-password")
	if err != nil {
		t.Fatal(err)
	}
	code := "single-use-exchange-code"
	challenge := testAdminOAuthCodeChallenge(t)
	if err := store.SaveAdminOAuthExchange(adminOAuthExchange{Code: code, CodeChallenge: challenge, UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	requestBody := []byte(`{"code":"single-use-exchange-code","code_verifier":"wrong-verifier-that-is-still-long-enough-1234567890"}`)
	wrongVerifier := httptest.NewRequest(http.MethodPost, "/api/admin/auth/oauth/exchange", bytes.NewReader(requestBody))
	wrongVerifier.Header.Set("content-type", "application/json")
	wrongVerifierResponse := httptest.NewRecorder()
	app.ServeHTTP(wrongVerifierResponse, wrongVerifier)
	if wrongVerifierResponse.Code != http.StatusBadRequest {
		t.Fatalf("exchange with the wrong PKCE verifier should fail: %d", wrongVerifierResponse.Code)
	}

	session := exchangeAdminOAuthCodeForTest(t, app, code, testAdminOAuthCodeVerifier)
	if session.User.ID != user.ID {
		t.Fatalf("exchange returned wrong user: %+v", session.User)
	}
	replayBody := []byte(`{"code":"single-use-exchange-code","code_verifier":"` + testAdminOAuthCodeVerifier + `"}`)
	replay := httptest.NewRequest(http.MethodPost, "/api/admin/auth/oauth/exchange", bytes.NewReader(replayBody))
	replay.Header.Set("content-type", "application/json")
	replayResponse := httptest.NewRecorder()
	app.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusBadRequest || !strings.Contains(replayResponse.Body.String(), "invalid_oauth_code") {
		t.Fatalf("consumed OAuth code was reusable: status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
}

func TestAdminOAuthFlowPersistsAcrossInstances(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "oauth-flow.db")
	config := Config{SecretKey: "shared-oauth-secret"}
	storeA, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*GormStore{storeA, storeB} {
		sqlDB, dbErr := store.db.DB()
		if dbErr != nil {
			t.Fatal(dbErr)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	assertAdminOAuthFlowUsesDatabaseClockAcrossInstances(t, storeA, storeB)
	assertAdminOAuthExchangeUsesDatabaseClockAcrossInstances(t, storeA, storeB)
}

func TestAdminOAuthPersistentRecordConsumptionUsesDatabaseClock(t *testing.T) {
	databaseTime := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	storeA, storeB := openAdminOAuthClockSkewSQLiteStores(t, databaseTime)
	flow := adminOAuthFlow{
		State: "database-clock-flow", BrowserNonce: "database-clock-browser", ProviderID: "idp_database_clock",
		ReturnURL: "https://tokenhub.example/overview", RedirectURI: "https://tokenhub.example/api/admin/auth/oauth/callback",
		CodeChallenge: testAdminOAuthCodeChallenge(t),
	}
	flowRecord := adminOAuthFlowRecord{
		ID:               NewID("oauth_flow"),
		StateHash:        HashSecret(flow.State),
		BrowserNonceHash: HashSecret(flow.BrowserNonce),
		ProviderID:       flow.ProviderID,
		ReturnURL:        flow.ReturnURL,
		RedirectURI:      flow.RedirectURI,
		CodeChallenge:    flow.CodeChallenge,
		CreatedAt:        databaseTime.Add(-30 * time.Second),
		ExpiresAt:        databaseTime.Add(30 * time.Second),
	}
	if err := storeA.db.Create(&flowRecord).Error; err != nil {
		t.Fatal(err)
	}
	consumed, ok, err := storeB.ConsumeAdminOAuthFlow(flow.State, flow.BrowserNonce)
	if err != nil || !ok || consumed.ProviderID != flow.ProviderID {
		t.Fatalf("database-active OAuth flow was rejected by application clock skew: flow=%+v ok=%v err=%v", consumed, ok, err)
	}

	exchange := adminOAuthExchangeRecord{
		ID:            NewID("oauth_exchange"),
		CodeHash:      HashSecret("database-clock-consume-code"),
		CodeChallenge: testAdminOAuthCodeChallenge(t),
		UserID:        "usr_database_clock",
		CreatedAt:     databaseTime.Add(-30 * time.Second),
		ExpiresAt:     databaseTime.Add(30 * time.Second),
	}
	if err := storeA.db.Create(&exchange).Error; err != nil {
		t.Fatal(err)
	}

	exchangeConsumed, exchangeOK, err := storeB.ConsumeAdminOAuthExchange("database-clock-consume-code", testAdminOAuthCodeVerifier)
	if err != nil || !exchangeOK || exchangeConsumed.UserID != exchange.UserID {
		t.Fatalf("database-active OAuth exchange was rejected by application clock skew: exchange=%+v ok=%v err=%v", exchangeConsumed, exchangeOK, err)
	}
}

func assertAdminOAuthFlowUsesDatabaseClockAcrossInstances(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	databaseBefore, err := storeA.databaseNow(storeA.db)
	if err != nil {
		t.Fatal(err)
	}
	flow := adminOAuthFlow{
		State: NewID("cross-instance-state"), BrowserNonce: NewID("cross-instance-browser"), Source: "198.51.100.10", ProviderID: "idp_shared",
		ReturnURL: "https://tokenhub.example/overview", RedirectURI: "https://tokenhub.example/api/admin/auth/oauth/callback",
		CodeChallenge: testAdminOAuthCodeChallenge(t), ProviderCodeVerifier: testAdminOAuthCodeVerifier, CreatedAt: databaseBefore.Add(-24 * time.Hour),
	}
	if err := storeA.SaveAdminOAuthFlow(flow); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = storeA.db.Where("state_hash = ?", HashSecret(flow.State)).Delete(&adminOAuthFlowRecord{}).Error
	})
	databaseAfter, err := storeA.databaseNow(storeA.db)
	if err != nil {
		t.Fatal(err)
	}
	var persisted adminOAuthFlowRecord
	if err := storeA.db.First(&persisted, "state_hash = ?", HashSecret(flow.State)).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.CreatedAt.Before(databaseBefore) || persisted.CreatedAt.After(databaseAfter) {
		t.Fatalf("OAuth flow creation used application clock: created_at=%s database_window=[%s,%s]", persisted.CreatedAt, databaseBefore, databaseAfter)
	}
	if persisted.ExpiresAt.Sub(persisted.CreatedAt) != adminOAuthFlowTTL {
		t.Fatalf("OAuth flow TTL = %s, want %s", persisted.ExpiresAt.Sub(persisted.CreatedAt), adminOAuthFlowTTL)
	}

	consumed, ok, err := storeB.ConsumeAdminOAuthFlow(flow.State, flow.BrowserNonce)
	if err != nil || !ok || consumed.ProviderID != flow.ProviderID {
		t.Fatalf("second instance did not consume database-timed OAuth flow: flow=%+v ok=%v err=%v", consumed, ok, err)
	}
	if _, ok, err := storeA.ConsumeAdminOAuthFlow(flow.State, flow.BrowserNonce); err != nil || ok {
		t.Fatalf("first instance replay result: ok=%v err=%v", ok, err)
	}
}

func assertAdminOAuthExchangeUsesDatabaseClockAcrossInstances(t *testing.T, storeA *GormStore, storeB *GormStore) {
	t.Helper()
	databaseBefore, err := storeA.databaseNow(storeA.db)
	if err != nil {
		t.Fatal(err)
	}
	exchange := adminOAuthExchange{
		Code:          NewID("cross-instance-code"),
		CodeChallenge: testAdminOAuthCodeChallenge(t),
		UserID:        NewID("usr_shared"),
		CreatedAt:     databaseBefore.Add(-24 * time.Hour),
	}
	if err := storeA.SaveAdminOAuthExchange(exchange); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = storeA.db.Where("code_hash = ?", HashSecret(exchange.Code)).Delete(&adminOAuthExchangeRecord{}).Error
	})
	databaseAfter, err := storeA.databaseNow(storeA.db)
	if err != nil {
		t.Fatal(err)
	}
	var persisted adminOAuthExchangeRecord
	if err := storeA.db.First(&persisted, "code_hash = ?", HashSecret(exchange.Code)).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.CreatedAt.Before(databaseBefore) || persisted.CreatedAt.After(databaseAfter) {
		t.Fatalf("OAuth exchange creation used application clock: created_at=%s database_window=[%s,%s]", persisted.CreatedAt, databaseBefore, databaseAfter)
	}
	if persisted.ExpiresAt.Sub(persisted.CreatedAt) != adminOAuthExchangeTTL {
		t.Fatalf("OAuth exchange TTL = %s, want %s", persisted.ExpiresAt.Sub(persisted.CreatedAt), adminOAuthExchangeTTL)
	}

	consumed, ok, err := storeB.ConsumeAdminOAuthExchange(exchange.Code, testAdminOAuthCodeVerifier)
	if err != nil || !ok || consumed.UserID != exchange.UserID {
		t.Fatalf("second instance did not consume database-timed OAuth exchange: exchange=%+v ok=%v err=%v", consumed, ok, err)
	}
}

func openAdminOAuthClockSkewSQLiteStores(t *testing.T, databaseTime time.Time) (*GormStore, *GormStore) {
	t.Helper()
	driverName := NewID("sqlite_oauth_clock")
	julianDay := float64(databaseTime.UnixNano())/float64(time.Second)/(24*60*60) + 2440587.5
	sql.Register(driverName, &sqlite3.SQLiteDriver{ConnectHook: func(connection *sqlite3.SQLiteConn) error {
		return connection.RegisterFunc("julianday", func(value string) (float64, error) {
			if value != "now" {
				return 0, fmt.Errorf("unsupported julianday argument %q", value)
			}
			return julianDay, nil
		}, true)
	}})
	databasePath := filepath.Join(t.TempDir(), "oauth-exchange-clock.db")
	openStore := func() *GormStore {
		database, err := gorm.Open(gormsqlite.New(gormsqlite.Config{DriverName: driverName, DSN: databasePath}), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if err := database.AutoMigrate(&adminOAuthFlowRecord{}, &adminOAuthExchangeRecord{}); err != nil {
			t.Fatal(err)
		}
		sqlDB, err := database.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		return &GormStore{db: database, dbDriver: "sqlite"}
	}
	return openStore(), openStore()
}

func TestAdminOAuthStartBoundsPendingFlowsPerSourceAndProvider(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID: "idp_flow_limit", Name: "Flow Limit IdP", Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2", "client_id": "client", "authorize_url": "https://idp.example/authorize",
			"token_url": "https://idp.example/token", "userinfo_url": "https://idp.example/userinfo",
			"redirect_uri": "https://tokenhub.example/api/admin/auth/oauth/callback",
		},
	})
	server := NewWithConfig(store, Config{PublicBaseURL: "https://tokenhub.example", SecretKey: "test-secret"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()
	startURL := adminOAuthStartURLForTest(t, "https://tokenhub.example/api/admin/auth/oauth/start?id=idp_flow_limit")

	for index := 0; index < adminOAuthFlowsPerClientScopeProviderLimit; index++ {
		request := httptest.NewRequest(http.MethodGet, startURL, nil)
		request.RemoteAddr = "198.51.100.20:4321"
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", index+1))
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusFound {
			t.Fatalf("OAuth start %d = %d: %s", index+1, response.Code, response.Body.String())
		}
	}

	limitedRequest := httptest.NewRequest(http.MethodGet, startURL, nil)
	limitedRequest.RemoteAddr = "198.51.100.20:9876"
	limitedResponse := httptest.NewRecorder()
	app.ServeHTTP(limitedResponse, limitedRequest)
	if limitedResponse.Code != http.StatusTooManyRequests || !strings.Contains(limitedResponse.Body.String(), "oauth_start_rate_limited") {
		t.Fatalf("OAuth flow abuse was not limited: status=%d body=%s", limitedResponse.Code, limitedResponse.Body.String())
	}
	if limitedResponse.Header().Get("Retry-After") == "" || limitedResponse.Header().Get("Set-Cookie") != "" || limitedResponse.Header().Get("Location") != "" {
		t.Fatalf("limited OAuth start returned unsafe headers: %v", limitedResponse.Header())
	}

	differentSource := httptest.NewRequest(http.MethodGet, startURL, nil)
	differentSource.RemoteAddr = "203.0.113.21:4321"
	differentSourceResponse := httptest.NewRecorder()
	app.ServeHTTP(differentSourceResponse, differentSource)
	if differentSourceResponse.Code != http.StatusFound {
		t.Fatalf("independent source was blocked: status=%d body=%s", differentSourceResponse.Code, differentSourceResponse.Body.String())
	}

	var liveFlows int64
	if err := store.db.Model(&adminOAuthFlowRecord{}).Where("expires_at > ?", time.Now().UTC()).Count(&liveFlows).Error; err != nil {
		t.Fatal(err)
	}
	if liveFlows != adminOAuthFlowsPerClientScopeProviderLimit+1 {
		t.Fatalf("live OAuth flows = %d, want %d", liveFlows, adminOAuthFlowsPerClientScopeProviderLimit+1)
	}
}

func TestAdminOAuthFlowClientScopeLimitSpansProviders(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "oauth-client-scope-secret"})
	limits := adminOAuthFlowLimits{ClientScopeProvider: 2, ClientScope: 2, Global: 10}
	flow := func(state string, source string, providerID string) adminOAuthFlow {
		return adminOAuthFlow{
			State: state, BrowserNonce: "browser-" + state, Source: source, ProviderID: providerID,
			ReturnURL: "https://tokenhub.example/overview", RedirectURI: "https://tokenhub.example/api/admin/auth/oauth/callback",
			CodeChallenge: testAdminOAuthCodeChallenge(t),
		}
	}
	if err := store.saveAdminOAuthFlow(flow("first", "198.51.100.10", "idp_a"), limits); err != nil {
		t.Fatal(err)
	}
	if err := store.saveAdminOAuthFlow(flow("second", "198.51.100.20", "idp_b"), limits); err != nil {
		t.Fatal(err)
	}
	err := store.saveAdminOAuthFlow(flow("limited", "198.51.100.30", "idp_c"), limits)
	if httpErr := AsHTTPError(err); httpErr.Status != http.StatusTooManyRequests || httpErr.Code != "oauth_start_rate_limited" {
		t.Fatalf("client scope bypassed the cross-provider limit: %v", err)
	}
	if err := store.saveAdminOAuthFlow(flow("independent", "203.0.113.10", "idp_c"), limits); err != nil {
		t.Fatalf("independent client scope was blocked: %v", err)
	}
}

func TestAdminOAuthFlowGlobalLimitIsSharedAcrossInstances(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "oauth-flow-limit.db")
	config := Config{SecretKey: "shared-oauth-limit-secret"}
	storeA, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*GormStore{storeA, storeB} {
		sqlDB, dbErr := store.db.DB()
		if dbErr != nil {
			t.Fatal(dbErr)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	flow := func(state string, source string) adminOAuthFlow {
		return adminOAuthFlow{
			State: state, BrowserNonce: "browser-" + state, Source: source, ProviderID: "idp_shared_limit",
			ReturnURL: "https://tokenhub.example/overview", RedirectURI: "https://tokenhub.example/api/admin/auth/oauth/callback",
			CodeChallenge: testAdminOAuthCodeChallenge(t),
		}
	}
	limits := adminOAuthFlowLimits{ClientScopeProvider: 2, ClientScope: 2, Global: 1}
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, store := range []*GormStore{storeA, storeB} {
		go func(index int, store *GormStore) {
			<-start
			results <- store.saveAdminOAuthFlow(flow(fmt.Sprintf("flow-%d", index), fmt.Sprintf("198.51.%d.30", index+100)), limits)
		}(index, store)
	}
	close(start)
	successes := 0
	limited := 0
	for range 2 {
		resultErr := <-results
		if resultErr == nil {
			successes++
			continue
		}
		if httpErr := AsHTTPError(resultErr); httpErr.Status == http.StatusTooManyRequests && httpErr.Code == "oauth_start_rate_limited" {
			limited++
			continue
		}
		t.Fatalf("unexpected OAuth admission error: %v", resultErr)
	}
	if successes != 1 || limited != 1 {
		t.Fatalf("cross-instance admission results: successes=%d limited=%d", successes, limited)
	}
	var count int64
	if err := storeB.db.Model(&adminOAuthFlowRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted OAuth flows = %d, want 1", count)
	}

	expired := adminOAuthFlowRecord{
		ID: "oauth_flow_expired", StateHash: HashSecret("expired-state"), ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}
	if err := storeA.db.Create(&expired).Error; err != nil {
		t.Fatal(err)
	}
	err = storeB.saveAdminOAuthFlow(flow("cleanup", "203.0.113.40"), limits)
	if httpErr := AsHTTPError(err); httpErr.Status != http.StatusTooManyRequests || httpErr.Code != "oauth_start_rate_limited" {
		t.Fatalf("active global limit was bypassed after cleanup: %v", err)
	}
	if err := storeA.db.Model(&adminOAuthFlowRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rejected admission rolled back expired-flow cleanup: count=%d", count)
	}
}

func TestOAuthRedirectValuesAreFragmentOnly(t *testing.T) {
	redirect := oauthRedirectWithFragment("https://tokenhub.example/overview?language=zh#old", url.Values{"oauth_code": {"short-code"}})
	target, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if target.Query().Get("language") != "zh" || target.Query().Has("oauth_code") {
		t.Fatalf("redirect query was changed or contains OAuth data: %s", redirect)
	}
	if target.Fragment != "oauth_code=short-code" {
		t.Fatalf("redirect fragment = %q", target.Fragment)
	}
}

func TestSecureCookieDetectionUsesCallbackScheme(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://tokenhub.example/api/admin/auth/oauth/start", nil)
	if request.TLS == nil {
		request.TLS = &tls.ConnectionState{}
	}
	if got := canonicalOAuthReturnURL(Config{}, request); got != "https://tokenhub.example/overview" {
		t.Fatalf("TLS request fallback = %q", got)
	}
}
