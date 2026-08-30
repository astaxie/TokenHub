package server

import (
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
