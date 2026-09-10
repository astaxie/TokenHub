package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type failingAdminResourceSecretStore struct {
	*GormStore
}

func (store *failingAdminResourceSecretStore) protectAdminResourceSecret(secret string) (string, error) {
	return secret, errors.New("injected secret protection failure")
}

func (store *failingAdminResourceSecretStore) revealAdminResourceSecret(secret string) string {
	return store.GormStore.revealAdminResourceSecret(secret)
}

func TestGatewaySettingsProtectConfiguredProxyCredential(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Name:   "Gateway Base Settings",
		Status: StatusActive,
		Fields: map[string]any{},
	})
	server := NewWithConfig(store, Config{AdminToken: "proxy-settings-admin", SecretKey: "proxy-settings-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	const password = "proxy-password-value"
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, map[string]any{
		"name":   "Gateway Base Settings",
		"status": StatusActive,
		"fields": map[string]any{
			"provider_egress_mode":        "configured_proxy",
			"provider_proxy_protocol":     "http",
			"provider_proxy_host":         "proxy.internal",
			"provider_proxy_port":         8080,
			"provider_proxy_auth_enabled": true,
			"provider_proxy_username":     "tokenhub",
			"provider_proxy_password":     password,
		},
	}, "proxy-settings-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("update proxy settings = %d: %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body, password) {
		t.Fatalf("proxy settings response exposed the password: %s", response.Body)
	}
	var updated AdminResource
	if err := json.Unmarshal([]byte(response.Body), &updated); err != nil {
		t.Fatal(err)
	}
	if got := stringField(updated.Fields, "provider_proxy_password"); got != providerHeaderMask {
		t.Fatalf("proxy password response = %q, want mask", got)
	}

	settings := store.ListResources("settings")
	if len(settings) != 1 {
		t.Fatalf("stored settings = %d, want 1", len(settings))
	}
	stored := stringField(settings[0].Fields, "provider_proxy_password")
	if stored == "" || stored == password || !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("stored proxy password is not protected: %q", stored)
	}
}

func TestGatewaySettingsFailClosedWhenProxyCredentialProtectionFails(t *testing.T) {
	base := NewMemoryStoreWithConfig(Config{SecretKey: "proxy-settings-failure-secret-key"})
	base.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Name:   "Gateway Base Settings",
		Status: StatusActive,
		Fields: map[string]any{providerEgressModeField: providerEgressModeDirect},
	})
	store := &failingAdminResourceSecretStore{GormStore: base}
	server := NewWithConfig(store, Config{AdminToken: "proxy-settings-failure-admin"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	const password = "must-never-reach-storage"
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, map[string]any{
		"name":   "Gateway Base Settings",
		"status": StatusActive,
		"fields": map[string]any{
			providerEgressModeField:       providerEgressModeConfigured,
			providerProxyProtocolField:    "http",
			providerProxyHostField:        "proxy.internal",
			providerProxyPortField:        8080,
			providerProxyAuthEnabledField: true,
			providerProxyUsernameField:    "tokenhub",
			providerProxyPasswordField:    password,
		},
	}, "proxy-settings-failure-admin")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body, "admin_resource_secret_protection_failed") {
		t.Fatalf("failed proxy password protection = %d: %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body, password) {
		t.Fatalf("secret protection error exposed the password: %s", response.Body)
	}

	settings := base.ListResources("settings")
	if len(settings) != 1 {
		t.Fatalf("stored settings = %d, want 1", len(settings))
	}
	if _, exists := settings[0].Fields[providerProxyPasswordField]; exists {
		t.Fatalf("failed secret protection persisted proxy password: %+v", settings[0].Fields)
	}
	if got := stringField(settings[0].Fields, providerEgressModeField); got != providerEgressModeDirect {
		t.Fatalf("failed secret protection changed Provider egress mode to %q", got)
	}
}

func TestConfiguredProxyRoutesProviderModelDiscovery(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_PROXY_LOCAL", "true")
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyRequests.Add(1)
		if request.URL.Host != "127.0.0.1:1" || request.URL.Path != "/v1/models" {
			t.Errorf("proxy request target = %s, want http://127.0.0.1:1/v1/models", request.URL.String())
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": "proxied-model", "object": "model"}},
		})
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	settings := store.ListResources("settings")[0]
	settings.Fields["provider_egress_mode"] = "configured_proxy"
	settings.Fields["provider_proxy_protocol"] = proxyURL.Scheme
	settings.Fields["provider_proxy_host"] = proxyURL.Hostname()
	settings.Fields["provider_proxy_port"] = proxyURL.Port()
	settings.Fields["provider_proxy_auth_enabled"] = false
	if _, err := store.UpdateResource("settings", gatewaySettingsID, settings); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "proxy-discovery-admin", SecretKey: "proxy-discovery-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name":     "Proxied Models",
		"type":     ProviderOpenAICompatible,
		"base_url": "http://127.0.0.1:1/v1",
		"api_key":  "provider-secret",
	}, "proxy-discovery-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("proxied model discovery = %d: %s", response.Code, response.Body)
	}
	if proxyRequests.Load() != 1 || !strings.Contains(response.Body, "proxied-model") {
		t.Fatalf("provider request did not use the configured proxy: requests=%d body=%s", proxyRequests.Load(), response.Body)
	}
}

func TestGatewaySettingsSwitchConfiguredProxyForNewRequests(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_PROXY_LOCAL", "true")
	proxyOne := newProviderModelsProxy(t, "proxy-one-model")
	defer proxyOne.Close()
	proxyTwo := newProviderModelsProxy(t, "proxy-two-model")
	defer proxyTwo.Close()

	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	settings := store.ListResources("settings")[0]
	setConfiguredProxyFields(t, settings.Fields, proxyOne.URL)
	if _, err := store.UpdateResource("settings", gatewaySettingsID, settings); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "proxy-switch-admin", SecretKey: "proxy-switch-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	discover := func() string {
		response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
			"name": "Proxy Switch", "type": ProviderOpenAICompatible,
			"base_url": "http://127.0.0.1:1/v1", "api_key": "provider-secret",
		}, "proxy-switch-admin")
		if response.Code != http.StatusOK {
			t.Fatalf("model discovery = %d: %s", response.Code, response.Body)
		}
		return response.Body
	}
	if body := discover(); !strings.Contains(body, "proxy-one-model") {
		t.Fatalf("initial proxy response = %s", body)
	}

	fields := map[string]any{}
	setConfiguredProxyFields(t, fields, proxyTwo.URL)
	updated := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, map[string]any{
		"name": "Gateway Base Settings", "status": StatusActive, "fields": fields,
	}, "proxy-switch-admin")
	if updated.Code != http.StatusOK {
		t.Fatalf("switch proxy settings = %d: %s", updated.Code, updated.Body)
	}
	if body := discover(); !strings.Contains(body, "proxy-two-model") {
		t.Fatalf("new request did not use the updated proxy: %s", body)
	}
}

func TestConfiguredProxyDoesNotRelaxProviderTargetValidation(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyRequests.Add(1)
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": "unexpected-model", "object": "model"}},
		})
	}))
	defer proxy.Close()

	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	settings := store.ListResources("settings")[0]
	setConfiguredProxyFields(t, settings.Fields, proxy.URL)
	if _, err := store.UpdateResource("settings", gatewaySettingsID, settings); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "proxy-trust-admin", SecretKey: "proxy-trust-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name": "Rejected metadata target", "type": ProviderOpenAICompatible,
		"base_url": "http://169.254.169.254/v1", "api_key": "provider-secret",
	}, "proxy-trust-admin")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "provider_base_url_not_allowed") {
		t.Fatalf("configured proxy relaxed Provider catalog validation = %d: %s", response.Code, response.Body)
	}
	if proxyRequests.Load() != 0 {
		t.Fatalf("rejected Provider target reached configured proxy: requests=%d", proxyRequests.Load())
	}
}

func TestConfiguredProxyRejectsMetadataProviderTarget(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	setting := store.ListResources("settings")[0]
	setConfiguredProxyFields(t, setting.Fields, "http://proxy.internal:8080")
	if _, err := store.UpdateResource("settings", gatewaySettingsID, setting); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "proxy-private-admin", SecretKey: "proxy-private-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Metadata provider through proxy", "type": ProviderOpenAICompatible,
		"base_url": "http://169.254.169.254/v1", "status": StatusActive,
	}, "proxy-private-admin")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "provider_base_url_not_allowed") {
		t.Fatalf("configured proxy relaxed Provider save validation = %d: %s", response.Code, response.Body)
	}
}

func TestConfiguredProxyUsesEncryptedBasicCredential(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_PROXY_LOCAL", "true")
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Proxy-Authorization") != "Basic cHJveHktdXNlcjpwcm94eS1wYXNzd29yZA==" {
			writer.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": "authenticated-proxy-model", "object": "model"}},
		})
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "proxy-auth-admin", SecretKey: "proxy-auth-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	updated := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, map[string]any{
		"name": "Gateway Base Settings", "status": StatusActive,
		"fields": map[string]any{
			"provider_egress_mode":        "configured_proxy",
			"provider_proxy_protocol":     proxyURL.Scheme,
			"provider_proxy_host":         proxyURL.Hostname(),
			"provider_proxy_port":         proxyURL.Port(),
			"provider_proxy_auth_enabled": true,
			"provider_proxy_username":     "proxy-user",
			"provider_proxy_password":     "proxy-password",
		},
	}, "proxy-auth-admin")
	if updated.Code != http.StatusOK || strings.Contains(updated.Body, "proxy-password") {
		t.Fatalf("save authenticated proxy = %d: %s", updated.Code, updated.Body)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name": "Authenticated Proxy", "type": ProviderOpenAICompatible,
		"base_url": "http://127.0.0.1:1/v1", "api_key": "provider-secret",
	}, "proxy-auth-admin")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, "authenticated-proxy-model") {
		t.Fatalf("authenticated proxy request = %d: %s", response.Code, response.Body)
	}
}

func TestGatewaySettingsRetainMaskedProxyPasswordAndClearDisabledAuthentication(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{ID: gatewaySettingsID, Name: "Gateway Base Settings", Status: StatusActive, Fields: map[string]any{}})
	server := NewWithConfig(store, Config{AdminToken: "proxy-password-admin", SecretKey: "proxy-password-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	update := func(authEnabled bool, password any) responseBody {
		return doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, map[string]any{
			"name": "Gateway Base Settings", "status": StatusActive,
			"fields": map[string]any{
				"provider_egress_mode":        "configured_proxy",
				"provider_proxy_protocol":     "http",
				"provider_proxy_host":         "proxy.internal",
				"provider_proxy_port":         8080,
				"provider_proxy_auth_enabled": authEnabled,
				"provider_proxy_username":     "proxy-user",
				"provider_proxy_password":     password,
			},
		}, "proxy-password-admin")
	}
	created := update(true, "original-password")
	if created.Code != http.StatusOK {
		t.Fatalf("save proxy password = %d: %s", created.Code, created.Body)
	}
	before := stringField(store.ListResources("settings")[0].Fields, providerProxyPasswordField)
	retained := update(true, providerHeaderMask)
	if retained.Code != http.StatusOK {
		t.Fatalf("retain proxy password = %d: %s", retained.Code, retained.Body)
	}
	after := stringField(store.ListResources("settings")[0].Fields, providerProxyPasswordField)
	if before == "" || after != before {
		t.Fatalf("masked password was not retained: before=%q after=%q", before, after)
	}

	cleared := update(false, providerHeaderMask)
	if cleared.Code != http.StatusOK {
		t.Fatalf("disable proxy authentication = %d: %s", cleared.Code, cleared.Body)
	}
	fields := store.ListResources("settings")[0].Fields
	if _, ok := fields[providerProxyPasswordField]; ok {
		t.Fatalf("disabled proxy authentication retained password: %+v", fields)
	}
	if _, ok := fields[providerProxyUsernameField]; ok {
		t.Fatalf("disabled proxy authentication retained username: %+v", fields)
	}
}

func TestGatewaySettingsRejectInvalidConfiguredProxyWithoutTestingReachability(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{ID: gatewaySettingsID, Name: "Gateway Base Settings", Status: StatusActive, Fields: map[string]any{}})
	server := NewWithConfig(store, Config{AdminToken: "proxy-validation-admin", SecretKey: "proxy-validation-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	invalid := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, map[string]any{
		"name": "Gateway Base Settings", "status": StatusActive,
		"fields": map[string]any{
			"provider_egress_mode": "configured_proxy", "provider_proxy_protocol": "socks5",
			"provider_proxy_host": "proxy.internal", "provider_proxy_port": 70000,
		},
	}, "proxy-validation-admin")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, "provider_proxy_settings_invalid") {
		t.Fatalf("invalid proxy settings = %d: %s", invalid.Code, invalid.Body)
	}

	validButUnreachable := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, map[string]any{
		"name": "Gateway Base Settings", "status": StatusActive,
		"fields": map[string]any{
			"provider_egress_mode": "configured_proxy", "provider_proxy_protocol": "http",
			"provider_proxy_host": "127.0.0.1", "provider_proxy_port": 1,
		},
	}, "proxy-validation-admin")
	if validButUnreachable.Code != http.StatusOK {
		t.Fatalf("unreachable but valid proxy settings = %d: %s", validButUnreachable.Code, validButUnreachable.Body)
	}
}

func TestProviderEgressTestUsesUnsavedProxyWithoutProviderCredentials(t *testing.T) {
	var connectTarget string
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect {
			t.Errorf("proxy method = %s", request.Method)
		}
		connectTarget = request.Host
		if request.Header.Get("Proxy-Authorization") != "Basic cHJveHktdXNlcjpwcm94eS1wYXNzd29yZA==" {
			t.Errorf("Proxy-Authorization = %q", request.Header.Get("Proxy-Authorization"))
		}
		if request.Header.Get("Authorization") != "" {
			t.Errorf("Provider authorization leaked to proxy test: %q", request.Header.Get("Authorization"))
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	store.AddProvider(Provider{ID: "prv_proxy_test", Name: "Proxy test provider", Type: ProviderOpenAICompatible, BaseURL: "http://127.0.0.1:8080/v1", APIKey: "provider-secret", Status: StatusActive})
	server := NewWithConfig(store, Config{AdminToken: "proxy-test-admin", SecretKey: "proxy-test-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	proxyURL, _ := url.Parse(proxy.URL)

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-egress/test", map[string]any{
		"provider_id": "prv_proxy_test",
		"fields": map[string]any{
			"provider_egress_mode": "configured_proxy", "provider_proxy_protocol": "http",
			"provider_proxy_host": proxyURL.Hostname(), "provider_proxy_port": proxyURL.Port(),
			"provider_proxy_auth_enabled": true, "provider_proxy_username": "proxy-user",
			"provider_proxy_password": "proxy-password",
		},
	}, "proxy-test-admin")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"ok":true`) {
		t.Fatalf("test unsaved proxy = %d: %s", response.Code, response.Body)
	}
	if connectTarget != "127.0.0.1:8080" {
		t.Fatalf("CONNECT target = %q", connectTarget)
	}
}

func newProviderModelsProxy(t *testing.T, model string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Host != "127.0.0.1:1" || request.URL.Path != "/v1/models" {
			t.Errorf("proxy request target = %s", request.URL.String())
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": model, "object": "model"}},
		})
	}))
}

func setConfiguredProxyFields(t *testing.T, fields map[string]any, rawURL string) {
	t.Helper()
	proxyURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	fields["provider_egress_mode"] = "configured_proxy"
	fields["provider_proxy_protocol"] = proxyURL.Scheme
	fields["provider_proxy_host"] = proxyURL.Hostname()
	fields["provider_proxy_port"] = proxyURL.Port()
	fields["provider_proxy_auth_enabled"] = false
}
