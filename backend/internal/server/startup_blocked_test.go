package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartupBlockedHandlerSeparatesLivenessFromReadiness(t *testing.T) {
	handler := NewStartupBlockedHandler()
	tests := []struct {
		path string
		want int
	}{
		{path: "/livez", want: http.StatusOK},
		{path: "/readyz", want: http.StatusServiceUnavailable},
		{path: "/healthz", want: http.StatusServiceUnavailable},
		{path: "/v1/models", want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
			if !strings.Contains(response.Body.String(), "configuration_required") {
				t.Fatalf("missing redacted configuration state: %s", response.Body.String())
			}
		})
	}
}

func TestStartupBlockedHandlerRejectsApplicationWritesAsUnavailable(t *testing.T) {
	response := httptest.NewRecorder()
	NewStartupBlockedHandler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
