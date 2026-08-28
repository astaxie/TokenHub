package plugin

import "testing"

func TestSupportedPluginAPICompatibilityDescribesV1Contract(t *testing.T) {
	compatibility := SupportedPluginAPICompatibility()
	if len(compatibility) != 1 {
		t.Fatalf("compatibility entries = %d, want 1", len(compatibility))
	}
	v1 := compatibility[0]
	if v1.PluginAPI != PluginAPIV1 {
		t.Fatalf("plugin API = %q, want %q", v1.PluginAPI, PluginAPIV1)
	}
	if v1.ManifestSchemaVersion != PluginManifestSchemaVersion {
		t.Fatalf("manifest schema version = %d, want %d", v1.ManifestSchemaVersion, PluginManifestSchemaVersion)
	}
	if v1.MinCore != CurrentCoreVersion {
		t.Fatalf("minimum core version = %q, want %q", v1.MinCore, CurrentCoreVersion)
	}
	for _, kind := range []string{
		CapabilityKindProviderType,
		CapabilityKindProvider,
		CapabilityKindProviderPolicy,
		CapabilityKindProviderResourceType,
		CapabilityKindProviderCatalog,
		CapabilityKindAdminUI,
		CapabilityKindSIM,
		CapabilityKindGatewayChain,
		CapabilityKindManagementAction,
		CapabilityKindBackgroundJob,
	} {
		if !containsString(v1.CapabilityKinds, kind) {
			t.Fatalf("v1 compatibility is missing capability kind %q: %+v", kind, v1.CapabilityKinds)
		}
	}
	for _, feature := range []string{
		PluginFeatureProviderPlugins,
		PluginFeatureAdminUIContributions,
		PluginFeatureSIMPresentation,
		PluginFeatureGatewayChainHooks,
		PluginFeatureManagementActions,
		PluginFeatureBackgroundJobs,
		PluginFeatureStdioJSONV1,
		PluginFeatureMarketplaceDistribution,
	} {
		if !containsString(v1.FeatureFlags, feature) {
			t.Fatalf("v1 compatibility is missing feature %q: %+v", feature, v1.FeatureFlags)
		}
	}
}

func TestParseManifestValidatesPluginAPIAndCoreVersion(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.compatible
name: Compatible
version: 1.0.0
tokenhub:
  plugin_api: v1
  min_core: 0.6.0
  max_core: 0.8.0
kinds:
  - extension
`))
	if err != nil {
		t.Fatalf("parse compatible manifest: %v", err)
	}
	if manifest.TokenHub.PluginAPI != PluginAPIV1 {
		t.Fatalf("plugin API = %q, want %q", manifest.TokenHub.PluginAPI, PluginAPIV1)
	}

	for _, tc := range []struct {
		name string
		body string
		code PluginErrorCode
	}{
		{
			name: "unsupported plugin API",
			body: `
schema_version: 1
id: tokenhub.unsupported-api
name: Unsupported API
version: 1.0.0
tokenhub:
  plugin_api: v2
kinds:
  - extension
`,
			code: PluginErrorAPIUnsupported,
		},
		{
			name: "future minimum core",
			body: `
schema_version: 1
id: tokenhub.future-core
name: Future Core
version: 1.0.0
tokenhub:
  plugin_api: v1
  min_core: 99.0.0
kinds:
  - extension
`,
			code: PluginErrorCoreVersionUnsupported,
		},
		{
			name: "invalid max core",
			body: `
schema_version: 1
id: tokenhub.invalid-core
name: Invalid Core
version: 1.0.0
tokenhub:
  plugin_api: v1
  max_core: latest
kinds:
  - extension
`,
			code: PluginErrorCoreVersionInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.body))
			if err == nil {
				t.Fatal("manifest parsed successfully")
			}
			if got, ok := PluginErrorCodeOf(err); !ok || got != tc.code {
				t.Fatalf("error code = %q, %t; want %q for error %v", got, ok, tc.code, err)
			}
		})
	}
}

func TestManifestRejectsUnsupportedCapabilityNamesWithStableErrorCode(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.bad-capability
name: Bad Capability
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - admin_ui
placement:
  - presentation
capabilities:
  admin_ui:
    - arbitrary.script
`))
	if err == nil {
		t.Fatal("manifest with unsupported admin UI capability parsed successfully")
	}
	if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorCapabilityUnsupported {
		t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorCapabilityUnsupported, err)
	}
}

func TestRegistryRejectsUnsupportedCapabilityNamesWithStableErrorCode(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(Descriptor{
		ID:         "tokenhub.bad-registry-capability",
		Name:       "Bad Registry Capability",
		Version:    "1.0.0",
		Kinds:      []Kind{KindExtension},
		Placements: []Placement{PlacementManagementAction},
		Capabilities: []CapabilityDescriptor{{
			Kind: CapabilityKindProviderPolicy,
			Name: "unknown_policy",
		}},
	})
	if err == nil {
		t.Fatal("descriptor with unsupported capability registered successfully")
	}
	if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorCapabilityUnsupported {
		t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorCapabilityUnsupported, err)
	}
}

func TestRegistryAcceptsStoreProbeFallbackProviderPolicy(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(Descriptor{
		ID:         "tokenhub.store-probe-provider",
		Name:       "Store Probe Provider",
		Version:    "1.0.0",
		Kinds:      []Kind{KindProvider},
		Placements: []Placement{PlacementGatewayChain},
		Capabilities: []CapabilityDescriptor{{
			Kind:    CapabilityKindProviderPolicy,
			Name:    ProviderPolicyStoreProbeFallback,
			Subject: "store_probe_provider",
			Value:   "true",
		}},
	})
	if err != nil {
		t.Fatalf("descriptor with store probe fallback policy should register: %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
