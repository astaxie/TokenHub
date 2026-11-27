package server

import (
	"net/http"
	"net/url"
	"strings"
)

// The public API prefix is operator configuration, never a forwarded header.
// Normalize the origin while retaining the escaped path used by the proxy.
func publicOAuthCallbackURL(configured string) (string, error) {
	origin, ok := normalizedOAuthOrigin(configured, false)
	if !ok {
		return "", NewHTTPError(http.StatusInternalServerError, "invalid_public_base_url", "Public base URL is invalid")
	}
	base, _ := url.Parse(strings.TrimSpace(configured))
	return validateOAuthCallbackURL(origin + strings.TrimRight(base.EscapedPath(), "/") + adminOAuthStateCookiePath)
}

// The stored redirect URI fixes both the browser-visible cookie scope and its
// eventual deletion scope, even if configuration changes during the login.
func oauthBindingCookiePath(redirectURI string) string {
	if target, err := url.Parse(redirectURI); err == nil && target.EscapedPath() != "" {
		return target.EscapedPath()
	}
	return adminOAuthStateCookiePath
}
