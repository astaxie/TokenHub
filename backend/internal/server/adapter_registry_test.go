package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		if len(descriptor.ProviderPolicy.RouteProtocols) == 0 {
			t.Fatalf("provider policy for %q has no route protocols", adapterType)
		}
	}

	listed := server.adapterRegistry.List()
	if len(listed) != len(builtinAdapterCapabilities) {
		t.Fatalf("registry lists %d adapters, want %d", len(listed), len(builtinAdapterCapabilities))
	}
}

func TestAdapterDescriptorsExposeProviderPolicy(t *testing.T) {
	server := New(NewMemoryStore())

	anthropic, ok := server.adapterRegistry.Describe(ProviderAnthropic)
	if !ok {
		t.Fatal("Anthropic adapter descriptor is missing")
	}
	if !reflect.DeepEqual(anthropic.ProviderPolicy.RouteProtocols, []string{"anthropic"}) {
		t.Fatalf("Anthropic route protocols = %v", anthropic.ProviderPolicy.RouteProtocols)
	}
	if anthropic.ProviderPolicy.ErrorProfile != "" {
		t.Fatalf("Anthropic error profile = %q, want generic", anthropic.ProviderPolicy.ErrorProfile)
	}
	if !anthropic.ProviderPolicy.SupportsCustomHeaders {
		t.Fatal("Anthropic should support custom headers")
	}
	if !reflect.DeepEqual(anthropic.ProviderPolicy.AuthModes, []string{anthropicAuthTypeBearer, anthropicAuthTypeAPIKey}) {
		t.Fatalf("Anthropic auth modes = %v", anthropic.ProviderPolicy.AuthModes)
	}
	if anthropic.ProviderPolicy.DefaultBaseURL != "https://api.anthropic.com" {
		t.Fatalf("Anthropic default base URL = %q", anthropic.ProviderPolicy.DefaultBaseURL)
	}
	if anthropic.ProviderPolicy.ModelDiscovery.Path != "/v1/models" ||
		anthropic.ProviderPolicy.ModelDiscovery.Auth != "provider_auth_mode" ||
		anthropic.ProviderPolicy.ModelDiscovery.Headers["anthropic-version"] != "2023-06-01" {
		t.Fatalf("Anthropic model discovery policy = %+v", anthropic.ProviderPolicy.ModelDiscovery)
	}

	azure, ok := server.adapterRegistry.Describe(ProviderAzureOpenAI)
	if !ok {
		t.Fatal("Azure OpenAI adapter descriptor is missing")
	}
	if azure.ProviderPolicy.SupportsCustomHeaders {
		t.Fatal("Azure OpenAI should not support custom headers")
	}

	kronk, ok := server.adapterRegistry.Describe(ProviderKronk)
	if !ok {
		t.Fatal("Kronk adapter descriptor is missing")
	}
	if kronk.ProviderPolicy.ErrorProfile != providerErrorProfileKronk {
		t.Fatalf("Kronk error profile = %q, want %q", kronk.ProviderPolicy.ErrorProfile, providerErrorProfileKronk)
	}
	if kronk.ProviderPolicy.APIKeyRequired {
		t.Fatal("Kronk should declare Provider API keys optional")
	}

	codex, ok := server.adapterRegistry.Describe(ProviderOpenAICodex)
	if !ok {
		t.Fatal("OpenAI Codex adapter descriptor is missing")
	}
	if codex.ProviderPolicy.SupportsCustomHeaders {
		t.Fatal("OpenAI Codex should not support custom headers")
	}
	if !codex.ProviderPolicy.RouteRequiresResource {
		t.Fatal("OpenAI Codex should require route resources through provider policy")
	}
	if codex.ProviderPolicy.CredentialsScope != providerCredentialsScopeResource {
		t.Fatalf("OpenAI Codex credentials scope = %q, want resource", codex.ProviderPolicy.CredentialsScope)
	}
	if codex.ProviderPolicy.SessionAffinityKind != AffinityKindCodexSession {
		t.Fatalf("OpenAI Codex session affinity kind = %q, want codex session", codex.ProviderPolicy.SessionAffinityKind)
	}

	compatible, ok := server.adapterRegistry.Describe(ProviderOpenAICompatible)
	if !ok {
		t.Fatal("OpenAI-compatible adapter descriptor is missing")
	}
	if compatible.ProviderPolicy.RouteRequiresResource {
		t.Fatal("OpenAI-compatible should keep route resources optional")
	}
	if !compatible.ProviderPolicy.APIKeyRequired {
		t.Fatal("OpenAI-compatible should require Provider API keys by default")
	}
	if compatible.ProviderPolicy.CredentialsScope != providerCredentialsScopeProvider {
		t.Fatalf("OpenAI-compatible credentials scope = %q, want provider", compatible.ProviderPolicy.CredentialsScope)
	}
	if compatible.ProviderPolicy.SessionAffinityKind != AffinityKindProviderSession {
		t.Fatalf("OpenAI-compatible session affinity kind = %q, want provider session", compatible.ProviderPolicy.SessionAffinityKind)
	}
	if !compatible.ProviderPolicy.SupportsCustomHeaders {
		t.Fatal("OpenAI-compatible should support custom headers")
	}
	if !reflect.DeepEqual(compatible.ProviderPolicy.RouteProtocols, []string{"chat/completions", "embeddings", "responses"}) {
		t.Fatalf("OpenAI-compatible route protocols = %v", compatible.ProviderPolicy.RouteProtocols)
	}
}

func TestAdapterProviderPolicyDefaultsAreGenericWithoutPluginPolicy(t *testing.T) {
	registry := NewAdapterRegistry()
	registry.Register(ProviderOpenAICodex, MockAdapter{}, AdapterCapabilityResponses)

	descriptor, ok := registry.Describe(ProviderOpenAICodex)
	if !ok {
		t.Fatal("adapter descriptor is missing")
	}
	if descriptor.ProviderPolicy.RouteRequiresResource {
		t.Fatal("bare adapter registration should not imply route resource policy")
	}
	if descriptor.ProviderPolicy.CredentialsScope != providerCredentialsScopeProvider {
		t.Fatalf("bare adapter credentials scope = %q, want provider", descriptor.ProviderPolicy.CredentialsScope)
	}
	if descriptor.ProviderPolicy.SessionAffinityKind != AffinityKindProviderSession {
		t.Fatalf("bare adapter session affinity kind = %q, want provider session", descriptor.ProviderPolicy.SessionAffinityKind)
	}
	if !descriptor.ProviderPolicy.SupportsCustomHeaders {
		t.Fatal("bare adapter registration should keep custom headers enabled by default")
	}
	if descriptor.ProviderPolicy.ErrorProfile != "" {
		t.Fatalf("bare adapter error profile = %q, want generic", descriptor.ProviderPolicy.ErrorProfile)
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

func TestPluginAdapterDescriptorExposesRouteResourcePolicy(t *testing.T) {
	registry := NewAdapterRegistry()
	providerType := "subscription_plugin"
	if err := registry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.subscription-plugin",
		Name:    "Subscription Plugin",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider_policy", Name: "route_requires_resource", Subject: providerType, Value: "true"},
			{Kind: "provider_policy", Name: providerAPIKeyRequiredOption, Subject: providerType, Value: "false"},
			{Kind: "provider_policy", Name: "auth_mode", Subject: providerType, Value: "oauth"},
			{Kind: "provider_policy", Name: "auth_mode", Subject: providerType, Value: "personal_access_token"},
			{Kind: "provider_policy", Name: "credentials_scope", Subject: providerType, Value: providerCredentialsScopeResource},
			{Kind: "provider_policy", Name: "session_affinity_kind", Subject: providerType, Value: AffinityKindCodexSession},
			{Kind: "provider_policy", Name: claudeCodeAttributionDefaultPolicy, Subject: providerType, Value: claudeCodeAttributionStrip},
			{Kind: "provider_policy", Name: "default_base_url", Subject: providerType, Value: "https://subscription.example/v1"},
			{Kind: "provider_policy", Name: "error_profile", Subject: providerType, Value: providerErrorProfileKronk},
			{Kind: "provider_policy", Name: "model_discovery_path", Subject: providerType, Value: "/subscription/models"},
			{Kind: "provider_policy", Name: "model_discovery_auth", Subject: providerType, Value: "query_param"},
			{Kind: "provider_policy", Name: "model_discovery_api_key_query_param", Subject: providerType, Value: "access_token"},
			{Kind: "provider_policy", Name: "model_discovery_headers", Subject: providerType, Value: `{"x-subscription-version":"2026-01-01"}`},
			{Kind: "provider_resource_type", Name: "subscription_account", Subject: providerType, Value: pluginmeta.ManifestProviderResourceType{
				Type:        "subscription_account",
				DisplayName: "Subscription Account",
				AuthModes:   []string{"oauth", "personal_access_token"},
				Default:     true,
				Defaults: map[string]string{
					"auth_type": "oauth",
					"base_url":  "https://subscription.example/v1",
				},
			}.CapabilityValue()},
		},
	}, AdapterRegistration{Type: providerType, Adapter: struct{}{}}); err != nil {
		t.Fatalf("register plugin adapter: %v", err)
	}

	descriptor, ok := registry.Describe(providerType)
	if !ok {
		t.Fatal("plugin adapter descriptor is missing")
	}
	if !descriptor.ProviderPolicy.RouteRequiresResource {
		t.Fatalf("plugin provider policy = %+v, want route resource required", descriptor.ProviderPolicy)
	}
	if descriptor.ProviderPolicy.APIKeyRequired {
		t.Fatalf("plugin provider API key policy = %+v, want optional", descriptor.ProviderPolicy)
	}
	if descriptor.ProviderPolicy.CredentialsScope != providerCredentialsScopeResource {
		t.Fatalf("plugin provider credentials scope = %+v, want resource", descriptor.ProviderPolicy)
	}
	if !reflect.DeepEqual(descriptor.ProviderPolicy.AuthModes, []string{"oauth", "personal_access_token"}) {
		t.Fatalf("plugin provider auth modes = %+v", descriptor.ProviderPolicy.AuthModes)
	}
	if descriptor.ProviderPolicy.SessionAffinityKind != AffinityKindCodexSession {
		t.Fatalf("plugin provider session affinity kind = %+v, want codex session", descriptor.ProviderPolicy)
	}
	if descriptor.ProviderPolicy.ClaudeCodeAttributionDefault != claudeCodeAttributionStrip {
		t.Fatalf("plugin provider Claude Code attribution default = %+v, want strip", descriptor.ProviderPolicy)
	}
	if descriptor.ProviderPolicy.DefaultBaseURL != "https://subscription.example/v1" {
		t.Fatalf("plugin provider default base URL = %+v, want subscription URL", descriptor.ProviderPolicy)
	}
	if descriptor.ProviderPolicy.ErrorProfile != providerErrorProfileKronk {
		t.Fatalf("plugin provider error profile = %+v, want Kronk profile", descriptor.ProviderPolicy)
	}
	if descriptor.ProviderPolicy.ModelDiscovery.Path != "/subscription/models" ||
		descriptor.ProviderPolicy.ModelDiscovery.Auth != "query_param" ||
		descriptor.ProviderPolicy.ModelDiscovery.APIKeyQueryParam != "access_token" ||
		descriptor.ProviderPolicy.ModelDiscovery.Headers["x-subscription-version"] != "2026-01-01" {
		t.Fatalf("plugin provider model discovery policy = %+v", descriptor.ProviderPolicy.ModelDiscovery)
	}
	if len(descriptor.ResourceTypes) != 1 {
		t.Fatalf("plugin provider resource types = %+v, want one resource type", descriptor.ResourceTypes)
	}
	resourceType := descriptor.ResourceTypes[0]
	if resourceType.Type != "subscription_account" || resourceType.DisplayName != "Subscription Account" || !resourceType.Default {
		t.Fatalf("plugin provider resource type = %+v, want subscription account metadata", resourceType)
	}
	if !reflect.DeepEqual(resourceType.AuthModes, []string{"oauth", "personal_access_token"}) {
		t.Fatalf("plugin provider resource auth modes = %+v", resourceType.AuthModes)
	}
	if resourceType.Defaults["auth_type"] != "oauth" || resourceType.Defaults["base_url"] != "https://subscription.example/v1" {
		t.Fatalf("plugin provider resource defaults = %+v", resourceType.Defaults)
	}
}

func TestReconcileProviderPluginPoliciesPersistsBuiltinProviderPolicy(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_legacy_codex_policy",
		Name:    "Legacy Codex Policy",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{"catalog_id": "openai-codex"},
	})
	delete(provider.Options, providerRouteRequiresResourceOption)
	delete(provider.Options, providerCredentialsScopeOption)
	if err := store.db.Model(&provider).Select("Options").Updates(provider).Error; err != nil {
		t.Fatal(err)
	}

	NewWithConfig(store, Config{AdminToken: "dev_admin_token"})

	stored, ok := store.GetProvider(provider.ID)
	if !ok {
		t.Fatal("provider disappeared")
	}
	if stored.Options["catalog_id"] != "openai-codex" {
		t.Fatalf("non-policy options were not preserved: %+v", stored.Options)
	}
	if stored.Options[providerRouteRequiresResourceOption] != "true" || stored.Options[providerCredentialsScopeOption] != providerCredentialsScopeResource {
		t.Fatalf("builtin provider policy was not reconciled: %+v", stored.Options)
	}
}

func TestReconcileProviderPluginPoliciesPersistsExternalProviderPolicy(t *testing.T) {
	store := NewMemoryStore()
	providerType := "external_policy_provider"
	provider := store.AddProvider(Provider{
		ID:      "prv_external_policy",
		Name:    "External Policy",
		Type:    providerType,
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{"custom": "preserved"},
	})
	registry := NewAdapterRegistry()
	if err := registry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.external-policy",
		Name:    "External Policy Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider_policy", Name: "route_requires_resource", Subject: providerType, Value: "true"},
			{Kind: "provider_policy", Name: "credentials_scope", Subject: providerType, Value: providerCredentialsScopeResource},
			{Kind: "provider_policy", Name: "error_profile", Subject: providerType, Value: providerErrorProfileKronk},
		},
	}, AdapterRegistration{Type: providerType, Adapter: struct{}{}}); err != nil {
		t.Fatalf("register plugin adapter: %v", err)
	}

	updated, err := store.ReconcileProviderPluginPolicies(registry)
	if err != nil {
		t.Fatalf("reconcile provider policies: %v", err)
	}
	if updated != 1 {
		t.Fatalf("reconciled providers = %d, want 1", updated)
	}
	stored, ok := store.GetProvider(provider.ID)
	if !ok {
		t.Fatal("provider disappeared")
	}
	if stored.Options["custom"] != "preserved" ||
		stored.Options[providerRouteRequiresResourceOption] != "true" ||
		stored.Options[providerCredentialsScopeOption] != providerCredentialsScopeResource ||
		stored.Options[providerErrorProfileOption] != providerErrorProfileKronk {
		t.Fatalf("external provider policy was not reconciled: %+v", stored.Options)
	}
}

func TestBuiltinCodexProviderPluginExposesResourceTypeMetadata(t *testing.T) {
	server := New(NewMemoryStore())
	descriptor, ok := server.adapterRegistry.plugins.Describe("tokenhub.provider.openai-codex")
	if !ok {
		t.Fatal("Codex provider plugin is missing")
	}
	var value string
	for _, capability := range descriptor.Capabilities {
		if capability.Kind == "provider_resource_type" && capability.Name == ProviderResourceOpenAISubscription && capability.Subject == ProviderOpenAICodex {
			value = capability.Value
			break
		}
	}
	if value == "" {
		t.Fatalf("Codex provider plugin resource type metadata is missing: %+v", descriptor.Capabilities)
	}
	if !descriptorHasPluginCapability(descriptor, pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "route_requires_resource", Subject: ProviderOpenAICodex, Value: "true"}) {
		t.Fatalf("Codex provider plugin route resource policy is missing: %+v", descriptor.Capabilities)
	}
	if !descriptorHasPluginCapability(descriptor, pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "credentials_scope", Subject: ProviderOpenAICodex, Value: providerCredentialsScopeResource}) {
		t.Fatalf("Codex provider plugin credentials scope policy is missing: %+v", descriptor.Capabilities)
	}
	if !descriptorHasPluginCapability(descriptor, pluginmeta.CapabilityDescriptor{Kind: "provider_policy", Name: "session_affinity_kind", Subject: ProviderOpenAICodex, Value: AffinityKindCodexSession}) {
		t.Fatalf("Codex provider plugin session affinity policy is missing: %+v", descriptor.Capabilities)
	}
	if catalog, ok := providerCatalogEntryFromPluginCapability(descriptor, AdapterDescriptor{Type: ProviderOpenAICodex}); !ok ||
		catalog.ID != codexProviderCatalogID || catalog.Type != ProviderOpenAICodex || catalog.BaseURL != openAICodexBaseURL {
		t.Fatalf("Codex provider plugin catalog entry = %+v found=%t", catalog, ok)
	}
	var resourceType pluginmeta.ManifestProviderResourceType
	if err := json.Unmarshal([]byte(value), &resourceType); err != nil {
		t.Fatalf("decode Codex resource type metadata: %v", err)
	}
	if resourceType.Type != ProviderResourceOpenAISubscription || !resourceType.Default || resourceType.Defaults["base_url"] != openAICodexBaseURL || resourceType.Defaults["max_concurrency"] != "3" {
		t.Fatalf("Codex resource type metadata = %+v", resourceType)
	}
	if !reflect.DeepEqual(resourceType.AuthModes, []string{"oauth", "personal_access_token"}) {
		t.Fatalf("Codex resource type auth modes = %+v", resourceType.AuthModes)
	}
}

func TestBuiltinCodexAdminUIContributesFingerprintResourceForm(t *testing.T) {
	server := New(NewMemoryStore())
	var found bool
	for _, contribution := range server.adminUI.List() {
		if contribution.PluginID != "tokenhub.provider.openai-codex" || contribution.ID != "fingerprint" {
			continue
		}
		found = true
		if contribution.Slot != pluginmeta.SlotProviderResourceFormSection {
			t.Fatalf("Codex fingerprint slot = %q", contribution.Slot)
		}
		if !reflect.DeepEqual(contribution.ProviderTypes, []string{ProviderOpenAICodex}) {
			t.Fatalf("Codex fingerprint provider types = %+v", contribution.ProviderTypes)
		}
		if !reflect.DeepEqual(contribution.ResourceTypes, []string{ProviderResourceOpenAISubscription}) {
			t.Fatalf("Codex fingerprint resource types = %+v", contribution.ResourceTypes)
		}
		fields, ok := contribution.Schema["fields"].([]any)
		if !ok || len(fields) != 1 {
			t.Fatalf("Codex fingerprint fields = %#v", contribution.Schema["fields"])
		}
		field, ok := fields[0].(map[string]any)
		if !ok || field["name"] != "codex_fingerprint_mode" || field["type"] != "select" || field["default"] != "session" {
			t.Fatalf("Codex fingerprint field = %#v", fields[0])
		}
	}
	if !found {
		t.Fatal("Codex fingerprint resource form contribution is missing")
	}
}

func descriptorHasPluginCapability(descriptor pluginmeta.Descriptor, capability pluginmeta.CapabilityDescriptor) bool {
	for _, candidate := range descriptor.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
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
	capabilities := map[string]bool{}
	for _, capability := range descriptor.Capabilities {
		if capability.Kind == "gateway_chain" {
			capabilities[capability.Name] = true
		}
	}
	for _, stage := range []pluginmeta.GatewayHookStage{
		pluginmeta.StageAuthContext,
		pluginmeta.StageContextOptimize,
		pluginmeta.StageRequestTransform,
		pluginmeta.StageStreamTransform,
		pluginmeta.StageResponsePost,
	} {
		if !capabilities[string(stage)] {
			t.Fatalf("core gateway chain plugin does not expose stage %q", stage)
		}
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
	if !gatewayHookExists(hooks, "tokenhub.local-privacy", "mask") {
		t.Fatalf("privacy hooks = %+v", hooks)
	}
}

func TestServerListsDisabledLocalPluginWithoutActivatingHooks(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "privacy")
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.local-disabled-privacy
name: Local Disabled Privacy
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
	if err := os.WriteFile(filepath.Join(localPluginDir, "plugin.state.json"), []byte(`{"status":"disabled"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	descriptor, ok := server.pluginRegistry.Describe("tokenhub.local-disabled-privacy")
	if !ok {
		t.Fatal("disabled local plugin descriptor was not loaded")
	}
	if descriptor.Status != pluginmeta.StatusDisabled {
		t.Fatalf("disabled plugin status = %q, want disabled", descriptor.Status)
	}
	if gatewayHookExists(server.gatewayChain.Hooks(pluginmeta.StagePrivacyPre), "tokenhub.local-disabled-privacy", "mask") {
		t.Fatal("disabled local plugin hook was activated")
	}
	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugins: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"id":"tokenhub.local-disabled-privacy"`) || !strings.Contains(response.Body, `"status":"disabled"`) {
		t.Fatalf("GET /api/admin/plugins did not include disabled plugin status: %s", response.Body)
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

func TestServerExposesLocalProviderPluginsInCatalog(t *testing.T) {
	pluginDir := t.TempDir()
	providerPluginDir := filepath.Join(pluginDir, "provider")
	writeServerPluginManifest(t, providerPluginDir, `
schema_version: 1
id: tokenhub.provider.catalog-stdio
name: Catalog stdio Provider
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
entry:
  backend:
    protocol: stdio-json-v1
    command: provider.sh
capabilities:
  provider_types:
    - catalog_stdio
  provider:
    catalog:
      display_name: Catalog Stdio
      base_url: https://stdio.example/v1
      doc_url: https://stdio.example/docs
      categories:
        - custom
      models:
        - id: plugin-model
          display_name: Plugin Model
          category: custom
          type: chat
          context_window: 128000
  gateway:
    - chat
permissions:
  data:
    read:
      - provider_credentials
`)
	if err := os.WriteFile(filepath.Join(providerPluginDir, "provider.sh"), []byte(`#!/bin/sh
printf '{"response":{},"usage":{}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	catalog := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog", nil, "dev_admin_token")
	if catalog.Code != http.StatusOK {
		t.Fatalf("provider catalog status = %d body=%s", catalog.Code, catalog.Body)
	}
	if !strings.Contains(catalog.Body, `"id":"catalog_stdio"`) || !strings.Contains(catalog.Body, `"source":"plugin:local_file"`) {
		t.Fatalf("provider catalog did not include local provider plugin: %s", catalog.Body)
	}
	item := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/catalog_stdio", nil, "dev_admin_token")
	if item.Code != http.StatusOK || !strings.Contains(item.Body, `"type":"catalog_stdio"`) {
		t.Fatalf("provider catalog item status = %d body=%s", item.Code, item.Body)
	}
	var itemPayload struct {
		Data ProviderCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal([]byte(item.Body), &itemPayload); err != nil {
		t.Fatalf("decode plugin catalog item: %v", err)
	}
	if itemPayload.Data.DisplayName != "Catalog Stdio" || itemPayload.Data.BaseURL != "https://stdio.example/v1" || itemPayload.Data.DocURL != "https://stdio.example/docs" {
		t.Fatalf("plugin catalog metadata = %+v", itemPayload.Data)
	}
	if itemPayload.Data.ModelsCount != 1 || len(itemPayload.Data.Models) != 1 || itemPayload.Data.Models[0].ID != "plugin-model" || itemPayload.Data.Models[0].ContextWindow != 128000 {
		t.Fatalf("plugin catalog models = %+v", itemPayload.Data.Models)
	}
	created := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":        "catalog_stdio",
		"api_key":           "provider-secret",
		"selected_models":   []string{"plugin-model"},
		"model_category":    "custom",
		"model_access_mode": "inherit",
	}, "dev_admin_token")
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider from plugin catalog status = %d body=%s", created.Code, created.Body)
	}
	if !strings.Contains(created.Body, `"type":"catalog_stdio"`) || !strings.Contains(created.Body, `"catalog_source":"plugin:local_file"`) {
		t.Fatalf("created provider did not use plugin catalog: %s", created.Body)
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

func gatewayHookExists(hooks []pluginmeta.GatewayHookDescriptor, pluginID string, hookID string) bool {
	for _, hook := range hooks {
		if hook.PluginID == pluginID && hook.HookID == hookID {
			return true
		}
	}
	return false
}
