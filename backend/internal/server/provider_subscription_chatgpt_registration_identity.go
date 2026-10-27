package server

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
)

// Older admin clients retained the protected ID token but omitted user_id when
// saving an account. Recover only an expected identity for the next OAuth flow.
// This value cannot authenticate a request: the new token must independently
// pass the exchange's issuer, audience, expiry and nonce checks and match it.
// The retained token may be expired, as it is only an account-binding hint.
func chatGPTSubscriptionStoredSubject(credentials ProviderResourceCredentials) string {
	if credentials.UserID != "" {
		return credentials.UserID
	}
	if credentials.ClientID == "" || credentials.ClientID == "dynamic_agent_client" || len(credentials.IDToken) > 64<<10 {
		return ""
	}
	parts := strings.Split(credentials.IDToken, ".")
	if len(parts) != 3 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil || claims["iss"] != "https://auth.openai.com" {
		return ""
	}
	audience := claims["aud"] == credentials.ClientID
	if values, ok := claims["aud"].([]any); ok {
		audience = slices.Contains(values, any(credentials.ClientID))
	}
	subject, _ := claims["sub"].(string)
	if !audience || strings.TrimSpace(subject) == "" {
		return ""
	}
	return subject
}
