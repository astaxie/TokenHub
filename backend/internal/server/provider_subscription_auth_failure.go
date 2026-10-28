package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Authorization diagnostics contain fixed stages and numeric status codes only.
// Vendor messages, callback URLs, response bodies, and credentials stay private.
func subscriptionAuthorizationFailure(raw json.RawMessage) error {
	var result struct {
		Error *subscriptionWorkerFailure `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil {
		result.Error = nil
	}
	return subscriptionAuthorizationFailureDetails(result.Error)
}

func subscriptionAuthorizationFailureDetails(failure *subscriptionWorkerFailure) error {
	if failure != nil && failure.Code == "workbuddy_authorization_failed" {
		stage := map[string]string{"state": "starting browser sign-in", "token": "waiting for the browser token", "account": "loading the signed-in account"}[failure.Stage]
		if stage != "" {
			var details []string
			if status := failure.UpstreamStatus; status >= 100 && status <= 599 {
				details = append(details, fmt.Sprintf("HTTP %d", status))
			}
			if code := failure.UpstreamCode; code != nil && *code >= -9007199254740991 && *code <= 9007199254740991 {
				details = append(details, fmt.Sprintf("provider code %d", *code))
			}
			suffix := ""
			if len(details) > 0 {
				suffix = " (" + strings.Join(details, ", ") + ")"
			}
			return NewHTTPError(400, "workbuddy_authorization_failed", "WorkBuddy authorization failed while "+stage+suffix+"; start again")
		}
	}
	if failure != nil {
		messages := map[string]string{
			"upstream_host_not_allowed": "Subscription authorization returned an unsupported account domain; check the selected provider",
			"invalid_credentials":       "Subscription authorization returned incomplete account credentials; sign in again",
			"credential_save_failed":    "Subscription authorization credentials could not be saved; start again",
		}
		if message := messages[failure.Code]; message != "" {
			return NewHTTPError(400, "subscription_authorization_failed", message)
		}
	}
	return NewHTTPError(400, "subscription_authorization_failed", "Subscription authorization failed or expired; start again")
}
