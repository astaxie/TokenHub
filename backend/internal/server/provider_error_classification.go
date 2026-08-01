package server

import (
	"net/http"
	"strings"
)

// An upstream failure has to answer three separate questions, and collapsing them
// into one status code answers none of them well:
//
//   - what the caller is told, which decides whether the caller retries;
//   - whether the router may try another candidate, which decides whether a
//     request that cannot possibly succeed elsewhere burns quota trying;
//   - what the attempt proved about the provider resource, which decides whether
//     it counts toward a breaker.
//
// A malformed request is malformed at every provider, so it must not fail over
// and says nothing about the resource. A broken credential is specific to the
// resource, so it must fail over and does count against it. Both used to arrive
// as 502 provider_error, which reads as a transient gateway fault and got both
// decisions wrong.
type providerErrorClass struct {
	// status is what the caller sees. It is not always the upstream status: an
	// upstream 401 means TokenHub's credential for that provider is broken, and
	// forwarding 401 would tell the caller their own API key was rejected.
	status      int
	code        string
	disposition ProviderErrorDisposition
}

func classifyProviderStatus(upstreamStatus int) providerErrorClass {
	switch upstreamStatus {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// The request itself is wrong, so the caller has to change it. Forwarding
		// the status is what lets them see that rather than retrying a gateway.
		return providerErrorClass{upstreamStatus, "provider_invalid_request", ProviderErrorClient}
	case http.StatusUnauthorized, http.StatusForbidden:
		return providerErrorClass{http.StatusBadGateway, "provider_auth_error", ProviderErrorAuthBroken}
	case http.StatusPaymentRequired:
		return providerErrorClass{http.StatusBadGateway, "provider_payment_required", ProviderErrorQuotaExhausted}
	case http.StatusNotFound:
		// Usually a model, deployment or path this account does not have. Another
		// candidate may well have it, but the account is not thereby broken.
		return providerErrorClass{http.StatusBadGateway, "provider_model_not_found", ProviderErrorModelUnsupported}
	case http.StatusRequestTimeout:
		return providerErrorClass{http.StatusGatewayTimeout, "provider_upstream_timeout", ProviderErrorTransientSame}
	case http.StatusTooManyRequests:
		return providerErrorClass{http.StatusTooManyRequests, "provider_rate_limited", ProviderErrorTransientSame}
	case http.StatusRequestEntityTooLarge:
		return providerErrorClass{http.StatusRequestEntityTooLarge, "provider_invalid_request", ProviderErrorClient}
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		// Forwarded rather than flattened: they already say which kind of upstream
		// fault this was, and every one of them is worth another candidate.
		return providerErrorClass{upstreamStatus, "provider_upstream_unavailable", ProviderErrorTransientSame}
	}
	if upstreamStatus >= http.StatusInternalServerError {
		return providerErrorClass{http.StatusBadGateway, "provider_upstream_error", ProviderErrorTransientSame}
	}
	// An unrecognised 4xx keeps the gateway status, because forwarding a status
	// whose meaning here is unknown could mislead the caller worse than 502 does.
	// It still gets the no-failover, resource-neutral treatment: whatever it
	// means, a 4xx is about the request rather than about the account.
	return providerErrorClass{http.StatusBadGateway, "provider_error", ProviderErrorClient}
}

// newProviderPolicyRefusal reports content the provider refused to process. It
// is the caller's prompt that was rejected, so the account is neither at fault
// nor worth failing over from: another provider would refuse it too, and counting
// it would cool down a working account over content it correctly declined.
func newProviderPolicyRefusal(message string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadRequest, "content_filter", message),
		Disposition: ProviderErrorPolicy,
	}
}

// providerErrorMessage decides how much of the upstream body the caller sees. A
// rejected credential or an unpaid account is TokenHub's problem with its own
// provider account, and the bodies quote it: OpenAI echoes a masked API key,
// Azure names the subscription key. Those go no further than the attempt log,
// where the recorded upstream status already says what happened.
func providerErrorMessage(class providerErrorClass, data []byte) string {
	switch class.code {
	case "provider_auth_error":
		return "The gateway's credential for this provider was rejected"
	case "provider_payment_required":
		return "The gateway's account with this provider cannot be billed"
	}
	return strings.TrimSpace(string(data))
}

// newProviderMisconfigured reports a provider that cannot be called at all. That
// is the resource's own defect, so it counts against it and the router moves on.
func newProviderMisconfigured(message string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusServiceUnavailable, "provider_not_configured", message),
		Disposition: ProviderErrorResourceBroken,
	}
}

// newProviderHTTPError turns one upstream failure into the error the rest of the
// gateway routes, reports and counts on. The upstream body is carried through
// verbatim, as it always has been: callers match on provider-specific field names
// in it, and reducing it to a parsed message would lose them.
func newProviderHTTPError(upstreamStatus int, headers http.Header, data []byte) error {
	class := classifyProviderStatus(upstreamStatus)
	httpErr := NewHTTPError(class.status, class.code, providerErrorMessage(class, data))
	httpErr.UpstreamStatus = upstreamStatus
	return &ProviderInvocationError{
		Err:         httpErr,
		Disposition: class.disposition,
		RetryAfter:  parseRetryAfter(headers.Get("retry-after")),
	}
}
