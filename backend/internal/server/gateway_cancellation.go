package server

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
)

type gatewayCallerContextKey struct{}

// classifyCallerCancellation uses the original caller, not just the resource
// lease: a lease can be canceled by coordination or other internal work.
func classifyCallerCancellation(caller, attempt context.Context, err error) error {
	if err == nil {
		return nil
	}
	if caller != nil {
		if original, ok := caller.Value(gatewayCallerContextKey{}).(context.Context); ok {
			caller = original
		}
	}
	if leaseErr := coordinationLeaseError(attempt); leaseErr != nil {
		return leaseErr
	}
	// Typed upstream and policy errors remain actionable even when the caller
	// cancels concurrently. Stream classification wrappers alone are not proof
	// of a provider failure, so inspect their underlying error as well.
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return err
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		if invocation, ok := current.(*ProviderInvocationError); ok &&
			invocation.Disposition != ProviderErrorClient && invocation.Disposition != ProviderErrorStreamCommitted {
			return err
		}
	}
	if attempt != nil && context.Cause(attempt) != nil && context.Cause(attempt) != context.Canceled && errors.Is(err, context.Canceled) {
		// Preserve internal provenance for early completions that no longer have
		// the derived context. Raw cancellation must still never retry.
		return &ProviderInvocationError{Err: err, Disposition: ProviderErrorOutcomeUnknown}
	}
	if caller == nil || caller.Err() != context.Canceled ||
		context.Cause(caller) != context.Canceled ||
		(attempt != nil && context.Cause(attempt) != nil && context.Cause(attempt) != context.Canceled) ||
		errors.Is(err, ErrCoordinationLeaseLost) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, syscall.EPIPE) &&
		!errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return &ProviderInvocationError{
		Err:         NewHTTPError(499, "request_cancelled", "Request canceled by the client"),
		Disposition: ProviderErrorClient,
	}
}

func isCallerCancellation(status int, code string) bool {
	return status == 499 && code == "request_cancelled"
}

func isOperationalError(status int, code string) bool {
	return status >= 400 && !isCallerCancellation(status, code)
}

// columnPrefix is an internal SQL table alias, never user input. COALESCE keeps
// legacy NULL codes actionable, including otherwise unclassified 499 rows.
func operationalErrorSQL(columnPrefix string) string {
	return columnPrefix + "status_code >= 400 AND NOT (" + columnPrefix +
		"status_code = 499 AND COALESCE(" + columnPrefix + "error_code, '') = 'request_cancelled')"
}
