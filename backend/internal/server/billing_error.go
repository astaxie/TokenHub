package server

import (
	"context"
	"errors"
	"net/http"

	"tokenhub/backend/internal/billing"
)

func billingHTTPError(err error) *HTTPError {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NewHTTPError(http.StatusGatewayTimeout, "billing_sync_timeout", "Billing sync timed out")
	}
	if kind, code, message, ok := billing.ErrorInfo(err); ok {
		status := http.StatusInternalServerError
		switch kind {
		case billing.ErrorInvalidInput:
			status = http.StatusBadRequest
		case billing.ErrorConflict:
			status = http.StatusConflict
		case billing.ErrorNotFound:
			status = http.StatusNotFound
		case billing.ErrorRateLimited:
			status = http.StatusTooManyRequests
		case billing.ErrorUpstream:
			status = http.StatusBadGateway
		case billing.ErrorTimeout:
			status = http.StatusGatewayTimeout
		}
		return NewHTTPError(status, code, message)
	}
	return AsHTTPError(err)
}
