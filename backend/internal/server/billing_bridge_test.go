package server

import (
	"errors"
	"net/http"
	"testing"

	"tokenhub/backend/internal/billing"
)

func TestDomainBillingAdapterErrorRetriesHTTP5xx(t *testing.T) {
	err := domainBillingAdapterError(NewHTTPError(http.StatusBadGateway, "billing_cursor_invalid", "cursor invalid"))
	var failure *billing.AdapterFailure
	if !errors.As(err, &failure) || !failure.Retryable() {
		t.Fatalf("expected HTTP 5xx adapter failure to be retryable: %T %v", err, err)
	}
	if kind, code, message, ok := billing.ErrorInfo(err); !ok || kind != billing.ErrorUpstream || code != "billing_cursor_invalid" || message != "cursor invalid" {
		t.Fatalf("unexpected adapter failure info: kind=%q code=%q message=%q ok=%v", kind, code, message, ok)
	}
}

func TestDomainBillingStoreErrorPreservesHTTPCompatibility(t *testing.T) {
	original := NewHTTPError(http.StatusNotFound, "billing_connector_not_found", "Billing connector not found")
	err := domainBillingStoreError(original)
	if kind, code, message, ok := billing.ErrorInfo(err); !ok || kind != billing.ErrorNotFound || code != original.Code || message != original.Message {
		t.Fatalf("unexpected store error info: kind=%q code=%q message=%q ok=%v", kind, code, message, ok)
	}
	httpErr := billingHTTPError(err)
	if httpErr.Status != original.Status || httpErr.Code != original.Code || httpErr.Message != original.Message {
		t.Fatalf("HTTP compatibility changed: got=%+v want=%+v", httpErr, original)
	}
}

func TestBillingHTTPErrorMapsDomainKinds(t *testing.T) {
	tests := []struct {
		kind   billing.ErrorKind
		status int
	}{
		{kind: billing.ErrorInvalidInput, status: http.StatusBadRequest},
		{kind: billing.ErrorConflict, status: http.StatusConflict},
		{kind: billing.ErrorNotFound, status: http.StatusNotFound},
		{kind: billing.ErrorRateLimited, status: http.StatusTooManyRequests},
		{kind: billing.ErrorUpstream, status: http.StatusBadGateway},
		{kind: billing.ErrorTimeout, status: http.StatusGatewayTimeout},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			err := billingHTTPError(billing.NewError(test.kind, "billing_test_error", "billing test error"))
			if err.Status != test.status || err.Code != "billing_test_error" || err.Message != "billing test error" {
				t.Fatalf("unexpected HTTP mapping: got=%+v want status=%d", err, test.status)
			}
		})
	}
}

func TestBillingHTTPErrorPreservesRateLimitHeaders(t *testing.T) {
	original := NewHTTPError(http.StatusTooManyRequests, "billing_rate_limited", "Billing source rate limited")
	original.Headers = map[string]string{"retry-after": "7"}
	err := billingHTTPError(billing.NewAdapterFailure(original, billing.ErrorRateLimited, original.Code, original.Message, false, 0))
	if err.Status != original.Status || err.Code != original.Code || err.Headers["retry-after"] != "7" {
		t.Fatalf("rate-limit compatibility changed: got=%+v want=%+v", err, original)
	}
}
