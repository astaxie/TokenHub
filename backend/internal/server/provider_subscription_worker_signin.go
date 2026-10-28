package server

import "net/http"

// The Node relay removes upstream copies before the bundled adapter declares
// its account state. This enum is private worker metadata, never a public or
// provider-controlled HTTP header.
func subscriptionWorkerSignInError(status int, signIn string, headers http.Header) error {
	if signIn != "expired" && ((signIn != "kept" && signIn != "renewed") || (status != http.StatusUnauthorized && status != http.StatusForbidden)) {
		return nil
	}
	class := classifyProviderStatus(http.StatusServiceUnavailable)
	message := "Subscription request was rejected temporarily; retry with the saved account credentials"
	if signIn == "expired" {
		class = classifyProviderStatus(http.StatusUnauthorized)
		message = "Subscription authorization expired or was rejected; sign in again"
	}
	err := NewHTTPError(class.status, class.code, message)
	err.UpstreamStatus = status
	return &ProviderInvocationError{Err: err, Disposition: class.disposition, RetryAfter: parseRetryAfter(headers.Get("Retry-After"))}
}
