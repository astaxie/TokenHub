package server

import (
	"errors"
	"time"
)

type ProviderErrorDisposition string

const (
	ProviderErrorClient           ProviderErrorDisposition = "client_error"
	ProviderErrorPolicy           ProviderErrorDisposition = "policy_rejection"
	ProviderErrorTransientSame    ProviderErrorDisposition = "transient_retry_same"
	ProviderErrorQuotaExhausted   ProviderErrorDisposition = "quota_exhausted"
	ProviderErrorAuthBroken       ProviderErrorDisposition = "auth_broken"
	ProviderErrorModelUnsupported ProviderErrorDisposition = "model_unsupported"
	ProviderErrorResourceBroken   ProviderErrorDisposition = "resource_broken"
	ProviderErrorStreamCommitted  ProviderErrorDisposition = "stream_failed_committed"
)

type ProviderInvocationError struct {
	Err         error
	Disposition ProviderErrorDisposition
	RetryAfter  time.Duration
}

func (e *ProviderInvocationError) Error() string {
	if e == nil || e.Err == nil {
		return "provider invocation failed"
	}
	return e.Err.Error()
}

func (e *ProviderInvocationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func providerErrorDisposition(err error) ProviderErrorDisposition {
	var invocationErr *ProviderInvocationError
	if errors.As(err, &invocationErr) {
		return invocationErr.Disposition
	}
	return ""
}

func providerErrorRetryAfter(err error) time.Duration {
	var invocationErr *ProviderInvocationError
	if errors.As(err, &invocationErr) {
		return invocationErr.RetryAfter
	}
	return 0
}

// AttemptOutcome grades what an attempt proved about the provider resource.
//
// The distinction that matters is between AttemptSucceeded and AttemptNeutral. Both
// leave the resource's failure count alone, but only AttemptSucceeded is evidence the
// upstream is actually working: a mid-stream client disconnect or a policy refusal is
// "not the resource's fault" without saying anything about its availability. Only
// AttemptSucceeded may clear a breaker, so a half-open trial cannot be closed by a
// client that hung up.
type AttemptOutcome int

const (
	// AttemptFailed counts against the resource and can trip the breaker.
	AttemptFailed AttemptOutcome = iota
	// AttemptNeutral does not count against the resource and is not proof it works.
	AttemptNeutral
	// AttemptSucceeded is a confirmed upstream success.
	AttemptSucceeded
)

// CountsAsHealthy reports whether the outcome should leave the failure count intact.
func (o AttemptOutcome) CountsAsHealthy() bool {
	return o == AttemptSucceeded || o == AttemptNeutral
}

func providerAttemptOutcome(err error) AttemptOutcome {
	if err == nil {
		return AttemptSucceeded
	}
	switch providerErrorDisposition(err) {
	case ProviderErrorClient, ProviderErrorPolicy, ProviderErrorModelUnsupported:
		return AttemptNeutral
	default:
		if isCodexModelUnsupportedError(err) {
			return AttemptNeutral
		}
		return AttemptFailed
	}
}
