package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const copilotSubscriptionClientID = "Iv1.b507a08c87ecfe98"

type CopilotDeviceAuthorization struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// StartDeviceAuthorization starts GitHub's device flow. The host must retain
// DeviceCode server-side and apply expiry and polling intervals to the session.
func (a *CopilotSubscriptionAdapter) StartDeviceAuthorization(ctx context.Context) (CopilotDeviceAuthorization, error) {
	return a.StartDeviceAuthorizationAt(ctx, "")
}

func (a *CopilotSubscriptionAdapter) StartDeviceAuthorizationAt(ctx context.Context, accountHost string) (CopilotDeviceAuthorization, error) {
	host, err := normalizeCopilotSubscriptionHost(accountHost)
	if err != nil {
		return CopilotDeviceAuthorization{}, err
	}
	var authorization CopilotDeviceAuthorization
	err = a.deviceForm(ctx, firstNonEmpty(a.DeviceURL, copilotSubscriptionOrigin(host, "")+"/login/device/code"), url.Values{"client_id": {copilotSubscriptionClientID}, "scope": {"read:user"}}, &authorization)
	if err != nil {
		return authorization, err
	}
	if authorization.DeviceCode == "" || authorization.UserCode == "" || authorization.ExpiresIn <= 0 || authorization.ExpiresIn > 3600 {
		return CopilotDeviceAuthorization{}, NewHTTPError(502, "subscription_device_invalid", "GitHub did not return a valid device authorization")
	}
	// Never ask an administrator to enter a device code on an upstream-supplied
	// arbitrary website. GitHub's device page is fixed for this provider.
	verificationURI := copilotSubscriptionOrigin(host, "") + "/login/device"
	if authorization.VerificationURI != "" && authorization.VerificationURI != verificationURI {
		return CopilotDeviceAuthorization{}, NewHTTPError(502, "subscription_device_invalid", "GitHub returned an unexpected verification page")
	}
	authorization.VerificationURI = verificationURI
	if authorization.Interval < 5 {
		authorization.Interval = 5
	}
	return authorization, nil
}

// PollDeviceAuthorization performs one poll. pending and slow_down are normal
// outcomes; the host must wait Interval (plus five seconds for slow_down).
func (a *CopilotSubscriptionAdapter) PollDeviceAuthorization(ctx context.Context, deviceCode string) (ProviderResourceCredentials, string, error) {
	return a.PollDeviceAuthorizationAt(ctx, deviceCode, "")
}

func (a *CopilotSubscriptionAdapter) PollDeviceAuthorizationAt(ctx context.Context, deviceCode, accountHost string) (ProviderResourceCredentials, string, error) {
	host, err := normalizeCopilotSubscriptionHost(accountHost)
	if err != nil {
		return ProviderResourceCredentials{}, "", err
	}
	if strings.TrimSpace(deviceCode) == "" {
		return ProviderResourceCredentials{}, "", NewHTTPError(400, "subscription_device_missing", "GitHub device authorization is required")
	}
	var token struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	err = a.deviceForm(ctx, firstNonEmpty(a.DeviceTokenURL, copilotSubscriptionOrigin(host, "")+"/login/oauth/access_token"), url.Values{"client_id": {copilotSubscriptionClientID}, "device_code": {deviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &token)
	if err != nil {
		return ProviderResourceCredentials{}, "", err
	}
	switch token.Error {
	case "authorization_pending":
		return ProviderResourceCredentials{}, "pending", nil
	case "slow_down":
		return ProviderResourceCredentials{}, "slow_down", nil
	case "expired_token", "access_denied":
		return ProviderResourceCredentials{}, "", NewHTTPError(400, "subscription_device_expired", "GitHub device authorization expired or was denied")
	case "":
	default:
		return ProviderResourceCredentials{}, "", NewHTTPError(502, "subscription_device_failed", "GitHub device authorization failed")
	}
	if token.AccessToken == "" {
		return ProviderResourceCredentials{}, "", NewHTTPError(502, "subscription_token_missing", "GitHub did not return an access token")
	}
	credentials := ProviderResourceCredentials{AuthType: "oauth", AccessToken: token.AccessToken, ClientID: copilotSubscriptionClientID, TokenType: "github", Scopes: token.Scope}
	if host != "" {
		metadata, _ := json.Marshal(map[string]string{"github_host": host})
		credentials.SessionJSON = string(metadata)
	}
	return credentials, "complete", nil
}

func (a *CopilotSubscriptionAdapter) deviceForm(ctx context.Context, endpoint string, form url.Values, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := subscriptionHTTP(ctx, a.CredentialRefreshClient, req)
	if err != nil {
		return err
	}
	return subscriptionDecode(resp, target)
}
