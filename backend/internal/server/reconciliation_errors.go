package server

import (
	"errors"
	"net/http"

	"tokenhub/backend/internal/reconciliation"
)

// reconciliationHTTPError is the composition-layer mapping from public domain
// errors to the established admin HTTP error contract.
func reconciliationHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	if kind, code, message, ok := reconciliation.ErrorInfo(err); ok {
		status := http.StatusInternalServerError
		switch kind {
		case reconciliation.ErrorInvalidInput:
			status = http.StatusBadRequest
		case reconciliation.ErrorConflict:
			status = http.StatusConflict
		case reconciliation.ErrorNotFound:
			status = http.StatusNotFound
		case reconciliation.ErrorUnavailable:
			status = http.StatusServiceUnavailable
		}
		return NewHTTPError(status, code, message)
	}
	return err
}
