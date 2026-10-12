package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTypeSafeDiscoveryValidatesCustomHeaders(t *testing.T) {
	tooMany := map[string]string{}
	for index := 0; index <= providerHeaderMaxCount; index++ {
		tooMany["X-Test-"+strconv.Itoa(index)] = "value"
	}
	for _, tt := range []struct {
		name    string
		headers map[string]string
		valid   bool
	}{
		{"normalized name", map[string]string{" x-tenant ": "synthetic-tenant"}, true},
		{"invalid name", map[string]string{"Bad Header": "value"}, false},
		{"invalid value", map[string]string{"X-Tenant": "value\r\nInjected: true"}, false},
		{"duplicate names", map[string]string{"X-Tenant": "one", "x-tenant": "two"}, false},
		{"reserved header", map[string]string{"Cookie": "synthetic=value"}, false},
		{"count limit", tooMany, false},
		{"value limit", map[string]string{"X-Tenant": strings.Repeat("x", providerHeaderValueMaxBytes+1)}, false},
		{"name limit", map[string]string{strings.Repeat("x", providerHeaderNameMaxBytes+1): "value"}, false},
		{"total limit", map[string]string{"X-One": strings.Repeat("x", providerHeaderValueMaxBytes), "X-Two": strings.Repeat("x", providerHeaderValueMaxBytes), "X-Three": strings.Repeat("x", providerHeaderValueMaxBytes), "X-Four": strings.Repeat("x", providerHeaderValueMaxBytes)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("X-Tenant") != "synthetic-tenant" {
					t.Errorf("normalized custom header missing: %v", r.Header)
				}
				writeFixture(t, w, `{"models":[{"name":"jev-latest"}]}`)
			}))
			defer upstream.Close()
			_, err := (TypeSafeAdapter{Client: upstream.Client()}).DiscoverModels(context.Background(), ProviderCreateRequest{Type: providerTypeSafe, BaseURL: upstream.URL + "/v1", APIKey: "synthetic-key", Headers: tt.headers})
			if tt.valid {
				if err != nil || calls.Load() != 1 {
					t.Fatalf("calls=%d error=%v", calls.Load(), err)
				}
			} else if err == nil || AsHTTPError(err).Status != http.StatusBadRequest || calls.Load() != 0 {
				t.Fatalf("invalid headers reached upstream or bypassed validation: calls=%d error=%v", calls.Load(), err)
			}
		})
	}
}
