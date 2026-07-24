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

func providerAttemptCountsAsHealthy(err error) bool {
	if err == nil {
		return true
	}
	switch providerErrorDisposition(err) {
	case ProviderErrorClient, ProviderErrorPolicy, ProviderErrorModelUnsupported:
		return true
	default:
		return isCodexModelUnsupportedError(err)
	}
}
