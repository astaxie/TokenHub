package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthSubpathCallbackAndBrowserBinding(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "default-callback"
		if explicit {
			name = "explicit-callback"
		}
		t.Run(name, func(t *testing.T) {
			store := NewMemoryStore()
			const publicBase = "https://public.example/gateway/tokenhub-api"
			callback := publicBase + adminOAuthStateCookiePath
			fields := map[string]any{
				"provider_type": "oauth2", "client_id": "synthetic-client", "client_secret": "synthetic-secret",
				"authorize_url": "https://idp.example/authorize", "token_url": "https://idp.example/token", "userinfo_url": "https://idp.example/userinfo",
			}
			if explicit {
				callback = "https://public.example/another-proxy/oauth/callback?tenant=synthetic"
				fields["redirect_uri"] = callback
			}
			provider := store.CreateResource("identity-providers", AdminResource{ID: "subpath-idp", Name: "Synthetic IdP", Status: StatusActive, Fields: fields})
			app := NewWithConfig(store, Config{PublicBaseURL: publicBase + "/", SecretKey: "synthetic-subpath-secret-key-material-32"})
			handler := app.Handler()
			t.Cleanup(func() {
				if err := app.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			startURL, err := url.Parse(adminOAuthStartURLForTest(t, publicBase+"/api/admin/auth/oauth/start?id="+provider.ID))
			if err != nil {
				t.Fatal(err)
			}
			start := httptest.NewRecorder()
			// Model a proxy that strips only its external prefix, leaving
			// browser cookie matching to a real RFC 6265 cookie jar below.
			request := httptest.NewRequest(http.MethodGet, startURL.String(), nil)
			request.URL.Path = "/api/admin/auth/oauth/start"
			request.Header.Set("X-Forwarded-Prefix", "/untrusted-prefix")
			handler.ServeHTTP(start, request)
			if start.Code != http.StatusFound {
				t.Fatalf("start: %d %s", start.Code, start.Body)
			}
			authorize, err := url.Parse(start.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if got := authorize.Query().Get("redirect_uri"); got != callback {
				t.Fatalf("callback = %q, want %q", got, callback)
			}
			callbackURL, err := url.Parse(callback)
			if err != nil {
				t.Fatal(err)
			}
			query := callbackURL.Query()
			query.Set("state", authorize.Query().Get("state"))
			query.Set("error", "access_denied")
			callbackURL.RawQuery = query.Encode()
			cookie := requireResponseCookieWithPrefix(t, start, adminOAuthStateCookiePrefix)
			if cookie.Path != callbackURL.EscapedPath() || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("unsafe or incorrectly scoped binding cookie: %+v", cookie)
			}
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			jar.SetCookies(startURL, start.Result().Cookies())
			if len(jar.Cookies(callbackURL)) != 1 || len(jar.Cookies(startURL)) != 0 {
				t.Fatal("browser jar did not scope the binding to the public callback")
			}
			callbackRequest := func(withBinding bool) *http.Request {
				r := httptest.NewRequest(http.MethodGet, callbackURL.String(), nil)
				if withBinding {
					for _, c := range jar.Cookies(callbackURL) {
						r.AddCookie(c)
					}
				}
				r.URL.Path = adminOAuthStateCookiePath
				return r
			}
			missing := httptest.NewRecorder()
			handler.ServeHTTP(missing, callbackRequest(false))
			if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "invalid_oauth_state") {
				t.Fatalf("missing browser binding was accepted: %d %s", missing.Code, missing.Body)
			}
			// A pending flow must retain its original cookie deletion scope.
			app.config.PublicBaseURL = "https://public.example/changed-prefix"
			fields["redirect_uri"] = "https://public.example/changed-prefix/callback"
			if _, err := store.UpdateResource("identity-providers", provider.ID, AdminResource{Fields: fields}); err != nil {
				t.Fatal(err)
			}
			finished := httptest.NewRecorder()
			handler.ServeHTTP(finished, callbackRequest(true))
			if finished.Code != http.StatusFound || !strings.Contains(finished.Header().Get("Location"), "oauth_error=provider_error") {
				t.Fatalf("bound callback: %d %s", finished.Code, finished.Body)
			}
			deleted := finished.Result().Cookies()
			if len(deleted) != 1 || deleted[0].Path != cookie.Path || deleted[0].MaxAge >= 0 || !deleted[0].HttpOnly || !deleted[0].Secure {
				t.Fatalf("binding was not cleared at its saved callback path: %+v", deleted)
			}
			jar.SetCookies(callbackURL, deleted)
			if len(jar.Cookies(callbackURL)) != 0 {
				t.Fatal("completed flow retained its browser binding")
			}
		})
	}
}

func TestOAuthSubpathPreservesEscapingAndOriginValidation(t *testing.T) {
	got, err := publicOAuthCallbackURL("https://PUBLIC.example:443/encoded%20prefix/")
	if err != nil || got != "https://public.example/encoded%20prefix/api/admin/auth/oauth/callback" {
		t.Fatalf("escaped public callback = %q, err=%v", got, err)
	}
	if oauthBindingCookiePath(got+"?tenant=test") != "/encoded%20prefix/api/admin/auth/oauth/callback" {
		t.Fatal("cookie scope lost callback escaping or included its query")
	}
	for _, raw := range []string{"https://user:password@public.example/prefix", "ftp://public.example/prefix", "http://public.example/prefix", "//public.example/prefix"} {
		if _, err := publicOAuthCallbackURL(raw); err == nil {
			t.Errorf("invalid public callback base accepted: %q", raw)
		}
	}
}
