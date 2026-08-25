package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

// builtinAdapterCapabilities pins the capability set every built-in provider
// type advertises. Capabilities gate routing and the admin adapter listing, so
// an unintended change here is a product behaviour change, not a refactor.
var builtinAdapterCapabilities = map[string][]AdapterCapability{
	ProviderMock: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityResponses,
	},
	ProviderOpenAI: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityImageGenerate,
		AdapterCapabilityProbe, AdapterCapabilityResponses,
		AdapterCapabilityResponseStream,
	},
	ProviderOpenAICompatible: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
	ProviderOpenAICodex: {
		AdapterCapabilityImageGenerate, AdapterCapabilityModels,
		AdapterCapabilityOAuth, AdapterCapabilityProbe,
		AdapterCapabilityQuota, AdapterCapabilityResponses,
		AdapterCapabilityCompact, AdapterCapabilityResponseStream,
		AdapterCapabilityAffinity,
	},
	ProviderAzureOpenAI: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
	},
	ProviderAnthropic: {
		AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityProbe,
	},
	ProviderGemini: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
	},
	ProviderKronk: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityModels,
		AdapterCapabilityProbe, AdapterCapabilityResponses,
		AdapterCapabilityResponseStream,
	},
	"deepseek": {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
	"qwen": {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
	"local": {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
}

var builtinAdapterPlugins = map[string]string{
	ProviderMock:             "tokenhub.provider.mock",
	ProviderOpenAI:           "tokenhub.provider.openai",
	ProviderOpenAICompatible: "tokenhub.provider.openai-compatible",
	ProviderOpenAICodex:      "tokenhub.provider.openai-codex",
	ProviderAzureOpenAI:      "tokenhub.provider.azure-openai",
	ProviderAnthropic:        "tokenhub.provider.anthropic",
	ProviderGemini:           "tokenhub.provider.gemini",
	ProviderKronk:            "tokenhub.provider.kronk",
	"deepseek":               "tokenhub.provider.deepseek",
	"qwen":                   "tokenhub.provider.qwen",
	"local":                  "tokenhub.provider.local",
}

func TestBuiltinAdaptersResolveWithUnchangedCapabilities(t *testing.T) {
	server := New(NewMemoryStore())

	for adapterType, want := range builtinAdapterCapabilities {
		adapter, err := server.adapterRegistry.Resolve(adapterType)
		if err != nil {
			t.Fatalf("resolve %q: %v", adapterType, err)
		}
		if adapter == nil {
			t.Fatalf("resolve %q returned a nil adapter", adapterType)
		}
		descriptor, ok := server.adapterRegistry.Describe(adapterType)
		if !ok {
			t.Fatalf("describe %q: no descriptor", adapterType)
		}
		if !reflect.DeepEqual(descriptor.Capabilities, want) {
			t.Fatalf("capabilities for %q = %v, want %v", adapterType, descriptor.Capabilities, want)
		}
		if descriptor.PluginID != builtinAdapterPlugins[adapterType] {
			t.Fatalf("plugin id for %q = %q, want %q", adapterType, descriptor.PluginID, builtinAdapterPlugins[adapterType])
		}
	}

	listed := server.adapterRegistry.List()
	if len(listed) != len(builtinAdapterCapabilities) {
		t.Fatalf("registry lists %d adapters, want %d", len(listed), len(builtinAdapterCapabilities))
	}
}

func TestBuiltinProviderPluginsExposeAdapterCapabilities(t *testing.T) {
	server := New(NewMemoryStore())

	plugins := server.adapterRegistry.ListPlugins()
	if len(plugins) < len(builtinAdapterPlugins) {
		t.Fatalf("registry lists %d plugins, want at least %d", len(plugins), len(builtinAdapterPlugins))
	}
	for adapterType, pluginID := range builtinAdapterPlugins {
		descriptor, ok := server.adapterRegistry.plugins.Describe(pluginID)
		if !ok {
			t.Fatalf("plugin %q for adapter %q is missing", pluginID, adapterType)
		}
		capabilities := map[string]bool{}
		for _, capability := range descriptor.Capabilities {
			if capability.Kind == "provider" && capability.Subject == adapterType {
				capabilities[capability.Name] = true
			}
		}
		for _, capability := range builtinAdapterCapabilities[adapterType] {
			if !capabilities[string(capability)] {
				t.Fatalf("plugin %q does not expose %q for adapter %q", pluginID, capability, adapterType)
			}
		}
	}
}

func TestBuiltinGatewayChainPluginPlansCoreHooks(t *testing.T) {
	server := New(NewMemoryStore())

	descriptor, ok := server.pluginRegistry.Describe("tokenhub.chain.core")
	if !ok {
		t.Fatal("core gateway chain plugin is missing")
	}
	if len(descriptor.Capabilities) == 0 {
		t.Fatal("core gateway chain plugin exposes no capabilities")
	}
	plan := server.gatewayChain.Plan()
	if len(plan.Hooks) == 0 {
		t.Fatal("core gateway chain plan has no hooks")
	}
	if plan.Hooks[0].HookID != "decode_normalize" {
		t.Fatalf("first hook = %q, want decode_normalize", plan.Hooks[0].HookID)
	}
	for _, hook := range plan.Hooks {
		if !hook.Mandatory {
			t.Fatalf("builtin core hook %q is not marked mandatory", hook.HookID)
		}
		if hook.PluginID != "tokenhub.chain.core" {
			t.Fatalf("hook %q plugin id = %q, want tokenhub.chain.core", hook.HookID, hook.PluginID)
		}
	}
	report, err := server.gatewayHooks.RunStage(context.Background(), pluginmeta.StageDecodeNormalize, pluginmeta.GatewayHookInput{RequestID: "req_builtin_chain"})
	if err != nil {
		t.Fatalf("run builtin decode_normalize hook: %v", err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != pluginmeta.HookRunSucceeded {
		t.Fatalf("builtin hook report = %+v", report)
	}
}

func TestServerLoadsLocalPluginManifestsIntoRegistries(t *testing.T) {
	pluginDir := t.TempDir()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "privacy"), `
schema_version: 1
id: tokenhub.local-privacy
name: Local Privacy
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - gateway_chain
capabilities:
  hooks:
    - id: mask
      stage: privacy_pre
      priority: 2300
      failure_policy: fail_closed
      reads:
        - request_body
      writes:
        - request_body
permissions:
  data:
    read:
      - request_body
    write:
      - request_body
`)

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	descriptor, ok := server.pluginRegistry.Describe("tokenhub.local-privacy")
	if !ok {
		t.Fatal("local plugin descriptor was not loaded")
	}
	if descriptor.Source != pluginmeta.SourceLocalFile {
		t.Fatalf("local plugin source = %q, want %q", descriptor.Source, pluginmeta.SourceLocalFile)
	}
	hooks := server.gatewayChain.Hooks(pluginmeta.StagePrivacyPre)
	if len(hooks) != 1 || hooks[0].PluginID != "tokenhub.local-privacy" || hooks[0].HookID != "mask" {
		t.Fatalf("privacy hooks = %+v", hooks)
	}
}

func TestServerLoadsLocalPluginAdminUIManifestsIntoRegistry(t *testing.T) {
	pluginDir := t.TempDir()
	uiPluginDir := filepath.Join(pluginDir, "ui")
	writeServerPluginManifest(t, uiPluginDir, `
schema_version: 1
id: tokenhub.local-ui
name: Local UI
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - admin_ui
placement:
  - presentation
entry:
  frontend:
    schema: ui/admin-ui.schema.json
capabilities:
  admin_ui:
    - provider_resource_panel
`)
	if err := os.MkdirAll(filepath.Join(uiPluginDir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiPluginDir, "ui", "admin-ui.schema.json"), []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "health-panel",
				"slot": "provider.resource.panel",
				"title": "Provider health",
				"provider_types": ["openai"]
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	contributions := server.adminUI.List()
	var found bool
	for _, contribution := range contributions {
		if contribution.PluginID == "tokenhub.local-ui" && contribution.ID == "health-panel" {
			found = true
			if contribution.Slot != pluginmeta.SlotProviderResourcePanel {
				t.Fatalf("slot = %q, want %q", contribution.Slot, pluginmeta.SlotProviderResourcePanel)
			}
		}
	}
	if !found {
		t.Fatalf("local admin UI contribution was not loaded: %+v", contributions)
	}
}

func TestResolveReportsUnregisteredAdapterType(t *testing.T) {
	server := New(NewMemoryStore())

	if _, err := server.adapterRegistry.Resolve("not_a_provider"); AsHTTPError(err).Code != "provider_adapter_missing" {
		t.Fatalf("resolving an unknown type returned %v, want provider_adapter_missing", err)
	}
	if _, ok := server.adapterRegistry.Describe("not_a_provider"); ok {
		t.Fatal("an unknown type reported a capability descriptor")
	}
}

// The gateway resolves the concrete adapter types for the Anthropic native path
// and OpenAI image generation, so a wrong registration would only surface as a
// runtime downgrade rather than a compile error.
func TestRegistryResolvesConcreteAdapterTypes(t *testing.T) {
	server := New(NewMemoryStore())

	anthropic, ok := resolveTypedAdapter[AnthropicAdapter](server.adapterRegistry, ProviderAnthropic)
	if !ok {
		t.Fatal("anthropic type did not resolve to an AnthropicAdapter")
	}
	if anthropic.Client == nil {
		t.Fatal("resolved AnthropicAdapter carries no HTTP client")
	}
	if _, ok := resolveTypedAdapter[OpenAICompatibleAdapter](server.adapterRegistry, ProviderOpenAI); !ok {
		t.Fatal("openai type did not resolve to an OpenAICompatibleAdapter")
	}
	if _, ok := resolveTypedAdapter[AnthropicAdapter](server.adapterRegistry, ProviderOpenAI); ok {
		t.Fatal("openai type resolved to an AnthropicAdapter")
	}
}

func TestRegisterTestAdapterInjectsAndOverridesWithoutTouchingCapabilities(t *testing.T) {
	server := New(NewMemoryStore())
	injected := MockAdapter{}

	registerTestAdapter(server, "injected_type", injected)
	resolved, err := server.adapterRegistry.Resolve("injected_type")
	if err != nil {
		t.Fatalf("resolve injected type: %v", err)
	}
	if _, ok := resolved.(MockAdapter); !ok {
		t.Fatalf("injected type resolved to %T, want MockAdapter", resolved)
	}
	if _, ok := server.adapterRegistry.Describe("injected_type"); ok {
		t.Fatal("injecting an adapter declared capabilities it does not have")
	}

	// Overriding a built-in must take effect, which is what the gateway tests
	// that swap in a failing or blocking upstream depend on.
	registerTestAdapter(server, ProviderOpenAI, injected)
	overridden, err := server.adapterRegistry.Resolve(ProviderOpenAI)
	if err != nil {
		t.Fatalf("resolve overridden built-in: %v", err)
	}
	if _, ok := overridden.(MockAdapter); !ok {
		t.Fatalf("override of %q resolved to %T, want MockAdapter", ProviderOpenAI, overridden)
	}
	descriptor, ok := server.adapterRegistry.Describe(ProviderOpenAI)
	if !ok || !reflect.DeepEqual(descriptor.Capabilities, builtinAdapterCapabilities[ProviderOpenAI]) {
		t.Fatalf("overriding an adapter changed its capabilities to %v", descriptor.Capabilities)
	}
}

func writeServerPluginManifest(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
