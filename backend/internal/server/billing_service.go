package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tokenhub/backend/internal/billing"
)

// BillingService is a compatibility composition wrapper. Domain behavior and
// scheduler lifecycle are implemented by billing.Service until the remaining
// server adapters move in W04.
type BillingService struct {
	*billing.Service
}

type billingUpstreamError struct {
	status     int
	code       string
	message    string
	retryable  bool
	retryAfter time.Duration
}

func (e *billingUpstreamError) Error() string             { return e.message }
func (e *billingUpstreamError) Retryable() bool           { return e.retryable }
func (e *billingUpstreamError) RetryAfter() time.Duration { return e.retryAfter }
func (e *billingUpstreamError) StatusCode() int           { return e.status }
func (e *billingUpstreamError) ErrorCode() string         { return e.code }
func (e *billingUpstreamError) ErrorMessage() string      { return e.message }

func newBillingService(store BillingStore) *BillingService {
	client := &http.Client{Timeout: 30 * time.Second}
	return &BillingService{Service: billing.NewService(&billingStoreBridge{store: store}, map[string]billing.Adapter{
		BillingConnectorAliyun: &billingAdapterBridge{adapter: AliyunBillingAdapter{Client: client}},
		BillingConnectorNewAPI: &billingAdapterBridge{adapter: NewAPIBillingAdapter{Client: client}},
		BillingConnectorOneAPI: &billingAdapterBridge{adapter: OneAPIBillingAdapter{Client: client}},
	})}
}

func billingConfigInt(config map[string]string, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(config[key]))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

// billingHTTPError remains available to existing adapter tests and maps
// upstream errors into the server's response error type.
func billingHTTPError(err error) *HTTPError {
	var upstream *billingUpstreamError
	if errors.As(err, &upstream) {
		status := upstream.status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		return NewHTTPError(status, defaultString(upstream.code, "billing_upstream_error"), defaultString(upstream.message, "Billing source request failed"))
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	if kind, code, message, ok := billing.ErrorInfo(err); ok {
		return NewHTTPError(billingErrorStatus(kind), code, message)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NewHTTPError(http.StatusGatewayTimeout, "billing_sync_timeout", "Billing sync timed out")
	}
	return AsHTTPError(err)
}

func billingErrorStatus(kind billing.ErrorKind) int {
	switch kind {
	case billing.ErrorConflict:
		return http.StatusConflict
	case billing.ErrorNotFound:
		return http.StatusNotFound
	case billing.ErrorRateLimited:
		return http.StatusTooManyRequests
	case billing.ErrorUpstream:
		return http.StatusBadGateway
	case billing.ErrorTimeout:
		return http.StatusGatewayTimeout
	case billing.ErrorInvalidInput:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
