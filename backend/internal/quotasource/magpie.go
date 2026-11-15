package quotasource

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// DefaultBalanceExpression matches Magpie's defaults for explicitly named relay URLs.
func DefaultBalanceExpression(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	switch path := strings.TrimRight(u.Path, "/"); {
	case path == "/api/user/self":
		return "$data.quota / 500000"
	case path == "/api/usage/token":
		return "$data.total_available / 500000"
	case path == "/api/v1/user/profile":
		return "$data.balance"
	case path == "/v1/usage":
		return "$remaining"
	case strings.HasSuffix(path, "/dashboard/billing/credit_grants"):
		return "$total_available"
	}
	return ""
}

var magpieKeyNames = regexp.MustCompile(`(?i)\$?\{\{?\s*(?:key|api[_-]?key|token)\s*\}?\}`)

// PrepareMagpie preserves the separate meanings of the inference key and account
// balance token. Placeholders always name the inference key, including {token}.
func PrepareMagpie(c Config, key, token string) (Config, error) {
	if c.Auth != "magpie" {
		return c, nil
	}
	token = strings.TrimSpace(token)
	plain := token
	if len(plain) > 7 && strings.EqualFold(plain[:7], "bearer ") {
		plain = strings.TrimSpace(plain[7:])
	}
	if plain == strings.TrimSpace(key) {
		token = ""
	}
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return c, errors.New("invalid balance URL")
	}
	path := strings.TrimRight(u.Path, "/")
	if token != "" && (path == "/v1/usage" || path == "/api/usage/token") {
		return c, errors.New("this balance URL takes the API key; remove the account balance token or use the account balance URL")
	}
	explicitAuth := false
	for name, value := range c.Headers {
		if strings.TrimSpace(value) != "" && (strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Cookie")) {
			explicitAuth = true
		}
	}
	if token == "" && !explicitAuth {
		fallback := map[string]string{"/api/user/self": "/api/usage/token", "/api/v1/user/profile": "/v1/usage"}
		if target := fallback[path]; target != "" {
			if strings.TrimSpace(c.Expression) == DefaultBalanceExpression(c.URL) {
				c.Expression = ""
			}
			u.Path, u.RawPath = target, ""
			c.URL = u.String()
		}
	}
	if strings.TrimSpace(c.Expression) == "" && len(c.Windows) == 0 {
		c.Expression = DefaultBalanceExpression(c.URL)
	}
	c.URL = magpieKeyNames.ReplaceAllLiteralString(c.URL, url.QueryEscape(key))
	headers := make(map[string]string, len(c.Headers)+1)
	for name, value := range c.Headers {
		headers[name] = magpieKeyNames.ReplaceAllLiteralString(value, key)
	}
	c.Auth = "bearer"
	if token != "" {
		// A scheme is kept verbatim. JWTs use Bearer; opaque panel tokens are raw.
		if !strings.Contains(token, " ") && strings.HasPrefix(token, "eyJ") && strings.Count(token, ".") == 2 {
			token = "Bearer " + token
		}
		for name := range headers {
			if strings.EqualFold(name, "Authorization") {
				delete(headers, name)
			}
		}
		headers["Authorization"] = token
	}
	c.Headers = headers
	return c, c.Validate()
}
