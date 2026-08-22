package adapters

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tokenhub/backend/internal/billing"
)

type BillingConnector = billing.Connector
type BillingRecord = billing.Record
type BillingFetchRequest = billing.FetchRequest
type BillingFetchPage = billing.FetchPage

const (
	BillingConnectorAliyun = billing.ConnectorAliyun
	BillingConnectorNewAPI = billing.ConnectorNewAPI
	BillingConnectorOneAPI = billing.ConnectorOneAPI
)

func NewRegistry(client *http.Client) map[string]billing.Adapter {
	return map[string]billing.Adapter{
		billing.ConnectorAliyun: AliyunBillingAdapter{Client: client},
		billing.ConnectorNewAPI: NewAPIBillingAdapter{Client: client},
		billing.ConnectorOneAPI: OneAPIBillingAdapter{Client: client},
	}
}

type billingUpstreamError struct {
	status     int
	code       string
	message    string
	retryable  bool
	retryAfter time.Duration
}

func (e *billingUpstreamError) Error() string                { return e.message }
func (e *billingUpstreamError) Unwrap() error                { return nil }
func (e *billingUpstreamError) ErrorKind() billing.ErrorKind { return billing.ErrorUpstream }
func (e *billingUpstreamError) ErrorCode() string            { return e.code }
func (e *billingUpstreamError) ErrorMessage() string         { return e.message }
func (e *billingUpstreamError) Retryable() bool              { return e.retryable }
func (e *billingUpstreamError) RetryAfter() time.Duration    { return e.retryAfter }

func NewHTTPError(status int, code, message string) error {
	kind := billing.ErrorInvalidInput
	switch {
	case status == http.StatusNotFound:
		kind = billing.ErrorNotFound
	case status == http.StatusConflict:
		kind = billing.ErrorConflict
	case status == http.StatusTooManyRequests:
		kind = billing.ErrorRateLimited
	case status >= 500:
		kind = billing.ErrorUpstream
	}
	return billing.NewAdapterFailure(nil, kind, code, message, status >= 500, 0)
}

func billingConfigInt(config map[string]string, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(config[key]))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func NewID(prefix string) string {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buffer[:])
}
