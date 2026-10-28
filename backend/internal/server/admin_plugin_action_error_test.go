package server

import (
	"net/http"
	"testing"
)

func TestPluginActionProviderUnauthorizedDoesNotInvalidateAdminSession(t *testing.T) {
	for _, test := range []struct {
		name       string
		code       string
		status     int
		wantStatus int
	}{
		{name: "reauthorization", code: "provider_resource_reauthorization_required", status: http.StatusUnauthorized, wantStatus: http.StatusConflict},
		{name: "provider auth", code: "copilot_session_refused", status: http.StatusUnauthorized, wantStatus: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := pluginActionHTTPError(NewHTTPError(test.status, test.code, "synthetic account error"))
			if got := AsHTTPError(err).Status; got != test.wantStatus {
				t.Fatalf("plugin action status = %d, want %d", got, test.wantStatus)
			}
			if got := AsHTTPError(err).Code; got != test.code {
				t.Fatalf("plugin action code = %q, want %q", got, test.code)
			}
		})
	}
}
