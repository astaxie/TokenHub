package server

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

var copilotEnterpriseName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeCopilotSubscriptionHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	invalid := NewHTTPError(400, "copilot_host_invalid", "Copilot requires github.com or an enterprise's single-label ghe.com host")
	if strings.Contains(host, "://") {
		parsed, err := url.Parse(host)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
			return "", invalid
		}
		host = parsed.Hostname()
	}
	if host == "" || host == "github.com" {
		return "", nil
	}
	if name, ok := strings.CutSuffix(host, ".ghe.com"); ok && copilotEnterpriseName.MatchString(name) {
		return host, nil
	}
	return "", invalid
}

func copilotSubscriptionCredentialHost(credentials ProviderResourceCredentials) (string, error) {
	if strings.TrimSpace(credentials.SessionJSON) == "" {
		return "", nil
	}
	var session struct {
		Host string `json:"github_host"`
	}
	if json.Unmarshal([]byte(credentials.SessionJSON), &session) != nil {
		return "", NewHTTPError(400, "copilot_session_invalid", "Copilot account metadata is invalid")
	}
	return normalizeCopilotSubscriptionHost(session.Host)
}

func copilotSubscriptionOrigin(host, subdomain string) string {
	if host == "" {
		if subdomain == "api" {
			return "https://api.github.com"
		}
		if subdomain == "copilot-api" {
			return copilotSubscriptionBaseURL
		}
		return "https://github.com"
	}
	if subdomain != "" {
		host = subdomain + "." + host
	}
	return "https://" + host
}

func copilotSubscriptionAPIHost(accountHost, candidate string) bool {
	if accountHost != "" {
		return candidate == "copilot-api."+accountHost
	}
	return copilotSubscriptionHost(candidate)
}
