package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTypeSafeDiscoveryAndProbesRedactNormalizedSensitiveHeaders(t *testing.T) {
	for _, mode := range []string{"discovery", "provider probe", "resource models", "resource probe", "catalog HTTP", "connection HTTP"} {
		t.Run(mode, func(t *testing.T) {
			const providerSecret = "synthetic-provider-header-secret"
			const resourceSecret = "synthetic-resource-header-secret"
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				secret := r.Header.Get("X-Secret")
				if secret == "" {
					t.Error("custom header was not sent")
				}
				w.WriteHeader(http.StatusUnprocessableEntity)
				writeFixture(t, w, fmt.Sprintf(`{"error":{"message":"rejected %s"}}`, secret))
			}))
			defer upstream.Close()
			adapter := TypeSafeAdapter{Client: upstream.Client()}
			provider := Provider{Type: providerTypeSafe, BaseURL: upstream.URL, APIKey: "synthetic-key", Headers: map[string]string{" x-secret ": providerSecret}, SensitiveHeaders: []string{" x-secret "}}
			resource := ProviderResource{Headers: map[string]string{" X-SECRET ": resourceSecret}, SensitiveHeaders: []string{" X-SECRET "}}
			var err error
			switch mode {
			case "catalog HTTP", "connection HTTP":
				path := "/api/admin/provider-catalog/custom"
				if mode == "connection HTTP" {
					path = "/api/admin/providers/test-connection"
				}
				response := doJSON(t, newTestServer(), http.MethodPost, path, ProviderCreateRequest{Name: "Synthetic Header Preview", Type: provider.Type, BaseURL: provider.BaseURL, APIKey: provider.APIKey, Headers: provider.Headers, SensitiveHeaders: provider.SensitiveHeaders}, "")
				if response.Code != http.StatusUnprocessableEntity || calls != 1 {
					t.Fatalf("status=%d calls=%d", response.Code, calls)
				}
				if strings.Contains(response.Body, providerSecret) {
					t.Fatal("marked sensitive header leaked through an admin preview endpoint")
				}
				return
			case "discovery":
				_, err = adapter.DiscoverModels(context.Background(), ProviderCreateRequest{Type: provider.Type, BaseURL: provider.BaseURL, APIKey: provider.APIKey, Headers: provider.Headers, SensitiveHeaders: provider.SensitiveHeaders})
			case "provider probe":
				_, err = adapter.ProbeProvider(context.Background(), provider)
			case "resource models":
				_, _, err = adapter.ResourceModels(context.Background(), provider, resource, "")
			case "resource probe":
				_, err = adapter.Probe(context.Background(), provider, resource, adapter.DefaultProbeRequest())
			}
			if err == nil || calls != 1 || AsHTTPError(err).Status != http.StatusUnprocessableEntity {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if strings.Contains(err.Error(), providerSecret) || strings.Contains(err.Error(), resourceSecret) {
				t.Fatal("marked sensitive custom header was exposed in the upstream error")
			}
		})
	}
}

func TestTypeSafeResourceDiscoveryRejectsInvalidHeaderConfigBeforeMerge(t *testing.T) {
	for _, field := range []string{"provider sensitive name", "resource sensitive name", "provider header", "resource header", "combined size"} {
		t.Run(field, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				writeFixture(t, w, `{"models":[{"name":"jev-latest"}]}`)
			}))
			defer upstream.Close()
			provider := Provider{Type: providerTypeSafe, BaseURL: upstream.URL, APIKey: "synthetic-key"}
			resource := ProviderResource{}
			switch field {
			case "provider sensitive name":
				provider.SensitiveHeaders = []string{"missing"}
			case "resource sensitive name":
				resource.SensitiveHeaders = []string{"missing"}
			case "provider header":
				provider.Headers = map[string]string{"Bad Header": "value"}
			case "resource header":
				resource.Headers = map[string]string{"Bad Header": "value"}
			case "combined size":
				provider.Headers = map[string]string{"X-One": strings.Repeat("a", providerHeaderValueMaxBytes), "X-Two": strings.Repeat("a", providerHeaderValueMaxBytes)}
				resource.Headers = map[string]string{"X-Three": strings.Repeat("b", providerHeaderValueMaxBytes), "X-Four": strings.Repeat("b", providerHeaderValueMaxBytes)}
			}
			_, _, err := (TypeSafeAdapter{Client: upstream.Client()}).ResourceModels(context.Background(), provider, resource, "")
			if err == nil || AsHTTPError(err).Status != http.StatusBadRequest || calls != 0 {
				t.Fatalf("invalid headers were discarded during merge: calls=%d error=%v", calls, err)
			}
		})
	}
}
