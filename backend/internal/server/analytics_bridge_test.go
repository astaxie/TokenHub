package server

import (
	"errors"
	"testing"

	"tokenhub/backend/internal/analytics"
)

func TestAnalyticsHTTPErrorPreservesAdapterFailures(t *testing.T) {
	adapterErr := errors.New("database unavailable")
	if got := analyticsHTTPError(adapterErr); !errors.Is(got, adapterErr) {
		t.Fatalf("analyticsHTTPError() changed adapter error: got %v", got)
	}

	validationErr := analyticsHTTPError(analytics.ErrInvalidLimit)
	if got := AsHTTPError(validationErr); got.Status != 400 || got.Code != "invalid_analytics_limit" {
		t.Fatalf("analyticsHTTPError() = %#v, want invalid limit HTTP error", got)
	}
}
