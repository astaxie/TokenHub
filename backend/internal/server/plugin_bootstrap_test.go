package server

import (
	"encoding/json"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestBootstrapServerPluginsBuildsRegistriesAndLoadsLocalPlugins(t *testing.T) {
	pluginDir := t.TempDir()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "trace"), `
schema_version: 1
id: tokenhub.local-bootstrap-trace
name: Local Bootstrap Trace
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - gateway_chain
capabilities:
  hooks:
    - id: export
      stage: trace_export
      priority: 9000
      failure_policy: observe_only
      reads:
        - audit
permissions:
  data:
    read:
      - audit
`)

	bootstrap, err := bootstrapServerPlugins(NewMemoryStore(), Config{PluginDir: pluginDir}, map[string]any{
		ProviderMock:        MockAdapter{},
		ProviderOpenAICodex: &CodexSubscriptionAdapter{},
	})
	if err != nil {
		t.Fatalf("bootstrap plugins: %v", err)
	}
	if bootstrap.pluginRegistry == nil || bootstrap.gatewayChain == nil || bootstrap.gatewayHooks == nil ||
		bootstrap.adminUI == nil || bootstrap.pluginActions == nil || bootstrap.pluginBackgroundJobs == nil ||
		bootstrap.pluginBackgroundRunner == nil || bootstrap.adapterRegistry == nil {
		t.Fatalf("bootstrap registries were not initialized: %+v", bootstrap)
	}
	if _, ok := bootstrap.pluginRegistry.Describe("tokenhub.provider.mock"); !ok {
		t.Fatal("built-in mock provider plugin was not registered")
	}
	if _, ok := bootstrap.pluginRegistry.Describe("tokenhub.local-bootstrap-trace"); !ok {
		t.Fatal("local plugin manifest was not loaded")
	}
	if !gatewayHookExists(bootstrap.gatewayChain.Hooks(pluginmeta.StageTraceExport), "tokenhub.local-bootstrap-trace", "export") {
		t.Fatalf("trace export hooks = %+v", bootstrap.gatewayChain.Hooks(pluginmeta.StageTraceExport))
	}
	defaultTemplate, ok := bootstrap.pluginRegistry.Describe("tokenhub.sim.default")
	if !ok {
		t.Fatal("built-in default interface template plugin was not registered")
	}
	if !descriptorHasPluginCapability(defaultTemplate, pluginmeta.CapabilityDescriptor{
		Kind:    pluginmeta.CapabilityKindSIM,
		Name:    pluginmeta.SIMCapabilityShellLayout,
		Subject: "default-sidebar",
		Value:   simCapabilityValueForTest(t, defaultTemplate, pluginmeta.SIMCapabilityShellLayout, "default-sidebar"),
	}) {
		t.Fatalf("default template plugin is missing shell layout capability: %+v", defaultTemplate.Capabilities)
	}
	if value := simCapabilityValueForTest(t, defaultTemplate, pluginmeta.SIMCapabilityThemeTokens, "default-light"); !simCapabilityPayloadBool(t, value, "default") {
		t.Fatalf("default light theme payload = %s, want default=true", value)
	}
	if value := simCapabilityValueForTest(t, defaultTemplate, pluginmeta.SIMCapabilityThemeTokens, "default-dark"); !simCapabilityPayloadBool(t, value, "default") {
		t.Fatalf("default dark theme payload = %s, want default=true", value)
	}
	if value := simCapabilityValueForTest(t, defaultTemplate, pluginmeta.SIMCapabilityDashboardComposition, "default-dashboard"); !simCapabilityPayloadBool(t, value, "default") {
		t.Fatalf("default dashboard payload = %s, want default=true", value)
	}
	descriptor, ok := bootstrap.adapterRegistry.Describe(ProviderMock)
	if !ok || descriptor.PluginID != "tokenhub.provider.mock" {
		t.Fatalf("mock provider descriptor = %+v found=%t", descriptor, ok)
	}
}

func TestInstallServerPluginHandlersRegistersBuiltinActionAndBackgroundCallbacks(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token"})

	if _, ok := server.pluginActions.Describe("tokenhub.provider.openai-codex", "openai_codex.oauth.start"); !ok {
		t.Fatal("built-in Codex OAuth action was not registered")
	}
	if _, ok := server.providerPluginBackgroundJobDescriptor(ProviderOpenAICodex, AdapterCapabilityOAuth, providerCredentialRefreshDueJobCapability); !ok {
		t.Fatal("built-in Codex credential refresh background job was not registered")
	}
	if server.credentialRefresh.pluginRefresh == nil || server.credentialRefresh.pluginJob == nil {
		t.Fatal("credential refresh plugin callbacks were not installed")
	}
}

func simCapabilityValueForTest(t *testing.T, descriptor pluginmeta.Descriptor, name string, subject string) string {
	t.Helper()
	for _, capability := range descriptor.Capabilities {
		if capability.Kind == pluginmeta.CapabilityKindSIM && capability.Name == name && capability.Subject == subject {
			if capability.Value == "" {
				t.Fatalf("SIM capability %s/%s has empty value", name, subject)
			}
			return capability.Value
		}
	}
	t.Fatalf("SIM capability %s/%s is missing from %+v", name, subject, descriptor.Capabilities)
	return ""
}

func simCapabilityPayloadBool(t *testing.T, value string, key string) bool {
	t.Helper()
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		t.Fatalf("decode SIM capability payload: %v", err)
	}
	return payload[key] == true
}
