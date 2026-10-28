package server

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// Transport read failures can contain upstream-controlled diagnostics, such as
// HTTP/2 GOAWAY debug data. Keep them outside public errors and audit records.
// Wrap only upstream bodies, so caller writer failures retain their identity.
type subscriptionStreamReadCloser struct{ io.ReadCloser }

func (r *subscriptionStreamReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}
	for _, canceled := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, canceled) {
			return n, canceled
		}
	}
	var failure *HTTPError
	if errors.As(err, &failure) {
		return n, failure
	}
	return n, NewHTTPError(http.StatusBadGateway, "subscription_stream_failed", "Subscription upstream stream could not be read")
}
