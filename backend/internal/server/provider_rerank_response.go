package server

import (
	"bytes"
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
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxRerankResponseBytes+1))
	// A complete leading response can prove usage even when its transport or
	// trailing bytes fail validation. Incomplete JSON cannot prove any usage.
	var result any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decodeErr := decoder.Decode(&result)
	if decodeErr != nil {
		result = nil
	}
	if readErr != nil {
		return result, sanitizeRerankProviderError(readErr)
	}
	if len(data) > maxRerankResponseBytes {
		return result, NewHTTPError(502, "invalid_rerank_response", "Rerank response exceeds the 8 MiB limit")
	}
	var extra any
	if decodeErr != nil || decoder.Decode(&extra) != io.EOF {
		return result, NewHTTPError(502, "invalid_rerank_response", "Rerank provider returned invalid JSON")
	}
	return result, nil
}

// Preserve routing decisions without retaining provider messages that may echo input.
func sanitizeRerankProviderError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return &ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorClient}
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
