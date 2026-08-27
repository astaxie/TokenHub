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
