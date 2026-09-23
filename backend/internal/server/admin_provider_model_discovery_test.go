package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminCustomProviderCatalogUsesPluginModelDiscoveryPolicy(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	providerType := "catalog_discovery_plugin"
	descriptor := pluginmeta.BuiltInProvider("tokenhub.provider.catalog-discovery", "Catalog Discovery", []string{providerType}, []string{string(AdapterCapabilityChat)})
	descriptor.Capabilities = append(descriptor.Capabilities,
		pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_path", Subject: providerType, Value: "/catalog/models"},
		pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_auth", Subject: providerType, Value: "query_param"},
		pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_api_key_query_param", Subject: providerType, Value: "access_token"},
		pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_headers", Subject: providerType, Value: `{"x-provider-version":"2026-01-01"}`},
	)
	if err := server.adapterRegistry.RegisterPlugin(descriptor, AdapterRegistration{Type: providerType, Adapter: MockAdapter{}, Capabilities: []AdapterCapability{AdapterCapabilityChat}}); err != nil {
		t.Fatalf("register catalog discovery plugin: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/catalog/models" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("access_token"); got != "plugin-secret" {
			t.Fatalf("unexpected query credential %q", got)
		}
		if got := r.Header.Get("authorization"); got != "" {
			t.Fatalf("unexpected bearer credential %q", got)
		}
		if got := r.Header.Get("x-provider-version"); got != "2026-02-02" {
			t.Fatalf("unexpected discovery header %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": "plugin-discovered-model"}},
		})
	}))
	defer upstream.Close()

	resp := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name":     "Catalog Discovery",
		"type":     providerType,
		"base_url": upstream.URL + "/api",
		"api_key":  "plugin-secret",
		"options":  map[string]string{"x_provider_version": "2026-02-02"},
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected plugin discovery catalog, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"plugin-discovered-model"`) {
		t.Fatalf("expected plugin-discovered model, got %s", resp.Body)
	}
}

func TestAdminCustomProviderCatalogUsesPluginProviderAuthModeDiscovery(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		requestedMode     string
		wantAuthorization string
		wantAPIKey        string
	}{
		{name: "default api key header", wantAPIKey: "plugin-secret"},
		{name: "requested bearer", requestedMode: providerAuthModeBearer, wantAuthorization: "Bearer plugin-secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := NewMemoryStore()
			server := New(store)
			providerType := "catalog_auth_mode_plugin"
			descriptor := pluginmeta.BuiltInProvider("tokenhub.provider.catalog-auth-mode", "Catalog Auth Mode", []string{providerType}, []string{string(AdapterCapabilityChat)})
			descriptor.Capabilities = append(descriptor.Capabilities,
				pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: providerAuthModeOption, Subject: providerType, Value: providerAuthModeAPIKeyHeader},
				pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: providerAuthModeOption, Subject: providerType, Value: providerAuthModeBearer},
				pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_auth", Subject: providerType, Value: providerModelDiscoveryAuthProviderAuthMode},
			)
			if err := server.adapterRegistry.RegisterPlugin(descriptor, AdapterRegistration{Type: providerType, Adapter: MockAdapter{}, Capabilities: []AdapterCapability{AdapterCapabilityChat}}); err != nil {
				t.Fatalf("register catalog auth mode plugin: %v", err)
			}

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("authorization"); got != testCase.wantAuthorization {
					t.Errorf("authorization = %q, want %q", got, testCase.wantAuthorization)
				}
				if got := r.Header.Get("x-api-key"); got != testCase.wantAPIKey {
					t.Errorf("x-api-key = %q, want %q", got, testCase.wantAPIKey)
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"data": []map[string]any{{"id": "auth-mode-discovered-model"}},
				})
			}))
			defer upstream.Close()

			body := map[string]any{
				"name":     "Catalog Auth Mode",
				"type":     providerType,
				"base_url": upstream.URL,
				"api_key":  "plugin-secret",
			}
			if testCase.requestedMode != "" {
				body["provider_auth_mode"] = testCase.requestedMode
			}
			resp := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", body, "")
			if resp.Code != http.StatusOK {
				t.Fatalf("expected plugin auth mode discovery catalog, got %d: %s", resp.Code, resp.Body)
			}
			if !strings.Contains(resp.Body, `"auth-mode-discovered-model"`) {
				t.Fatalf("expected auth-mode-discovered model, got %s", resp.Body)
			}
		})
	}
}
