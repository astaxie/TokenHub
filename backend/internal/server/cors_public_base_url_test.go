package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSDerivesOriginFromPrefixedPublicBaseURL(t *testing.T) {
	cases := []struct {
		name            string
		config          Config
		origin          string
		wantAllowOrigin string
		wantCredentials string
	}{
		{
			name:            "prefixed public base URL falls back to its origin",
			config:          Config{PublicBaseURL: "https://Console.Example:443/gateway/tokenhub-api"},
			origin:          "https://console.example",
			wantAllowOrigin: "https://console.example",
			wantCredentials: "true",
		},
		{
			name:            "explicit allowlist takes precedence",
			config:          Config{PublicBaseURL: "https://console.example/gateway/tokenhub-api", CORSAllowedOrigins: []string{"https://admin.example"}},
			origin:          "https://admin.example",
			wantAllowOrigin: "https://admin.example",
			wantCredentials: "true",
		},
		{
			name:            "unlisted origin receives no credentials",
			config:          Config{PublicBaseURL: "https://console.example/gateway/tokenhub-api"},
			origin:          "https://other.example",
			wantAllowOrigin: "*",
			wantCredentials: "",
		},
		{
			name:            "unparsable public base URL keeps dev environments restrictive",
			config:          Config{PublicBaseURL: "not-a-url", Environment: "dev"},
			origin:          "https://console.example",
			wantAllowOrigin: "*",
			wantCredentials: "",
		},
		{
			name:            "dev environment without any configured base URL keeps convenience",
			config:          Config{Environment: "dev"},
			origin:          "https://console.example",
			wantAllowOrigin: "https://console.example",
			wantCredentials: "true",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := NewWithConfig(NewMemoryStore(), testCase.config)
			request := httptest.NewRequest(http.MethodGet, "/api/admin/auth/identity-providers", nil)
			request.Header.Set("Origin", testCase.origin)
			recorder := httptest.NewRecorder()
			server.cors(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(recorder, request)
			if got := recorder.Header().Get("access-control-allow-origin"); got != testCase.wantAllowOrigin {
				t.Fatalf("allow-origin = %q, want %q", got, testCase.wantAllowOrigin)
			}
			if got := recorder.Header().Get("access-control-allow-credentials"); got != testCase.wantCredentials {
				t.Fatalf("allow-credentials = %q, want %q", got, testCase.wantCredentials)
			}
		})
	}
}
