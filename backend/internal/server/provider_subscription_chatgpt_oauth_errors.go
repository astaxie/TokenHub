package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func chatGPTSubscriptionTokenRejection(status int, headers http.Header, data []byte, grant string) error {
	if status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= http.StatusInternalServerError {
		return newProviderHTTPError(status, headers, []byte("ChatGPT API token endpoint is temporarily unavailable"))
	}
	reason, description := chatGPTSubscriptionOAuthFailure(data)
	failure := NewHTTPError(http.StatusBadGateway, "subscription_token_request_failed", "ChatGPT API token endpoint refused the request")
	stage := "unknown"
	if grant == "authorization_code" {
		stage = grant
		switch reason {
		case "invalid_grant":
			failure = NewHTTPError(http.StatusConflict, "subscription_oauth_code_rejected", "ChatGPT API rejected the authorization code. It may have expired, already been used, or not match this authorization request. Start authorization again and paste the new callback URL.")
		case "invalid_client":
			failure = NewHTTPError(http.StatusConflict, "subscription_oauth_client_rejected", "ChatGPT API rejected the registered OAuth client. Start authorization again and use the new callback URL.")
		default:
			failure = NewHTTPError(http.StatusBadGateway, "subscription_oauth_exchange_failed", "ChatGPT API refused the authorization code exchange. Start authorization again; if it still fails, check the account's access eligibility and server connection.")
		}
	} else if grant == "refresh_token" {
		stage = grant
		switch reason {
		case "invalid_grant", "invalid_client", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
			failure = NewHTTPError(http.StatusConflict, "provider_resource_reauthorization_required", "ChatGPT API account must be authorized again")
		}
	}
	failure.UpstreamStatus = status
	details := map[string]any{"stage": stage, "reason": reason}
	if stage == "authorization_code" && reason == "invalid_grant" {
		if subtype := chatGPTSubscriptionGrantFailureReason(description); subtype != "" {
			details["oauth_reason"] = subtype
		}
	}
	failure.Details = details
	return failure
}

// Only allowlisted reasons and fixed subtypes leave the adapter. The provider's
// error descriptions can contain account identities, authorization codes or tokens.
func chatGPTSubscriptionOAuthFailure(data []byte) (string, string) {
	var body struct {
		Error       json.RawMessage `json:"error"`
		Code        string          `json:"code"`
		Description string          `json:"error_description"`
	}
	if json.Unmarshal(data, &body) != nil {
		return "unknown", ""
	}
	var plain string
	if json.Unmarshal(body.Error, &plain) == nil {
		body.Code = firstNonEmpty(body.Code, plain)
	} else {
		var detail struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body.Error, &detail) == nil {
			body.Code = firstNonEmpty(body.Code, detail.Code)
			body.Description = firstNonEmpty(body.Description, detail.Message)
		}
	}
	switch body.Code {
	case "invalid_grant", "invalid_client", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "invalid_request", "unauthorized_client", "unsupported_grant_type", "invalid_scope", "access_denied":
		return body.Code, body.Description
	default:
		return "unknown", ""
	}
}

func chatGPTSubscriptionGrantFailureReason(description string) string {
	description = strings.ToLower(strings.Join(strings.Fields(description), " "))
	for _, candidate := range []struct {
		reason   string
		patterns []string
	}{
		{"code_expired", []string{"authorization code has expired", "authorization code expired", "authorization code is expired"}},
		{"code_already_used", []string{"authorization code has already been used", "authorization code already used", "authorization code has already been redeemed", "authorization code already redeemed"}},
		{"pkce_verifier_mismatch", []string{"pkce verification failed", "code verifier mismatch", "code_verifier mismatch", "code verifier does not match", "code_verifier does not match"}},
		{"redirect_uri_mismatch", []string{"redirect_uri mismatch", "redirect uri mismatch", "redirect_uri does not match", "redirect uri does not match"}},
	} {
		for _, pattern := range candidate.patterns {
			if strings.Contains(description, pattern) {
				return candidate.reason
			}
		}
	}
	return ""
}
