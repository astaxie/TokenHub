package server

import "strings"

// These are public installed-application OAuth parameters, not user tokens or
// confidential server credentials. Desktop clients distribute the client secret
// with the application. Keep each application's ID and secret paired; resolved
// values still use encrypted OAuth sessions and account credential storage.
// Source: https://github.com/yetone/magpie/blob/d580537201938b9f510070a213e09b46479e53ad/internal/provider/google.go#L70-L89
// The source's license for this configuration is reproduced below.
/*
MIT License

Copyright (c) 2026 yetone

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/
const (
	googleGeminiDesktopClientID          = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	googleGeminiDesktopClientSecret      = "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"
	googleAntigravityDesktopClientID     = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	googleAntigravityDesktopClientSecret = "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"
)

func resolveGoogleSubscriptionOAuthClient(providerType, methodID, clientID, clientSecret string) (string, string, error) {
	methodID = strings.TrimSpace(methodID)
	if methodID != "" && methodID != "google" && methodID != "google_project" && methodID != "oauth_client" {
		return "", "", NewHTTPError(400, "google_subscription_oauth_method_invalid", "Google subscription login method is invalid")
	}
	clientID, clientSecret = strings.TrimSpace(clientID), strings.TrimSpace(clientSecret)
	if clientID != "" || clientSecret != "" || methodID == "oauth_client" {
		if !googleSubscriptionClientCredentialsValid(clientID, clientSecret) {
			return "", "", googleSubscriptionClientCredentialsRequired()
		}
		return clientID, clientSecret, nil
	}
	switch providerType {
	case providerGeminiCLISubscription:
		return googleGeminiDesktopClientID, googleGeminiDesktopClientSecret, nil
	case providerAntigravitySubscription:
		return googleAntigravityDesktopClientID, googleAntigravityDesktopClientSecret, nil
	default:
		return "", "", NewHTTPError(400, "google_subscription_oauth_provider_invalid", "Google subscription provider is invalid")
	}
}
