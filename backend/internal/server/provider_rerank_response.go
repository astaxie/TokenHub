package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
)

const maxRerankResponseBytes = 8 << 20

func (a OpenAICompatibleAdapter) rerankJSON(ctx context.Context, provider Provider, method, path string, payload any) (any, error) {
	response, err := a.doRaw(ctx, provider, method, path, payload, false)
	if err != nil {
		return nil, sanitizeRerankProviderError(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRerankResponseBytes+1))
	if err != nil {
		return nil, sanitizeRerankProviderError(err)
	}
	if len(data) > maxRerankResponseBytes {
		return nil, NewHTTPError(502, "invalid_rerank_response", "Rerank response exceeds the 8 MiB limit")
	}
	var result any
	if json.Unmarshal(data, &result) != nil {
		return nil, NewHTTPError(502, "invalid_rerank_response", "Rerank provider returned invalid JSON")
	}
	return result, nil
}

// Preserve routing decisions without retaining provider messages that may echo input.
func sanitizeRerankProviderError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	safe := NewHTTPError(502, "provider_error", "Rerank provider request failed")
	var original *HTTPError
	if errors.As(err, &original) {
		copySanitizedHTTPErrorMetadata(safe, original)
	}
	var invocation *ProviderInvocationError
	hasInvocation := errors.As(err, &invocation)
	var networkErr net.Error
	isTimeout := errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkErr) && networkErr.Timeout()
	if isTimeout && (!hasInvocation || invocation.Disposition != ProviderErrorEgress) {
		safe = NewHTTPError(504, "provider_upstream_timeout", "Rerank provider request timed out")
	}
	if hasInvocation {
		return &ProviderInvocationError{Err: safe, Disposition: invocation.Disposition, RetryAfter: invocation.RetryAfter}
	}
	if isTimeout {
		return &ProviderInvocationError{Err: safe, Disposition: ProviderErrorTransientSame}
	}
	return safe
}
