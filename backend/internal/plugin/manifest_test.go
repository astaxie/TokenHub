package plugin

import (
	"encoding/json"
	"testing"
)

func TestParseManifestBuildsDescriptorAndGatewayHooks(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.openai-codex
name: OpenAI Codex Subscription
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
  - admin_ui
  - extension
placement:
  - presentation
  - gateway_chain
  - management_action
  - background
capabilities:
  provider_types:
    - openai_codex
  provider_resource_types:
    - openai_subscription
  provider:
    route_protocols:
      - codex/responses
    supports_custom_headers: false
  gateway:
    - responses
    - responses_stream
  admin_ui:
    - provider_form
  actions:
    - id: codex.quota.read
      kind: read
      title: Read quota
      capability: quota.read
      subject: openai_codex
      input_schema:
        type: object
        required:
          - resource_id
        properties:
          resource_id:
            type: string
  hooks:
    - id: codex_affinity
      stage: route_rank
      priority: 1200
      failure_policy: fail_open
      reads:
        - route_candidates
      writes:
        - route_candidates
  background_jobs:
    - id: codex.quota.refresh
      title: Refresh quota
      capability: quota.refresh
      subject: openai_codex
      schedule: "*/10 * * * *"
      timeout_millis: 5000
      max_concurrency: 1
      retry:
        max_attempts: 2
        backoff_millis: 1000
permissions:
  data:
    read:
      - route_candidates
    write:
      - route_candidates
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	descriptor := manifest.Descriptor()
	if descriptor.ID != "tokenhub.openai-codex" {
		t.Fatalf("descriptor id = %q", descriptor.ID)
	}
	if len(descriptor.Kinds) != 3 {
		t.Fatalf("descriptor kinds = %v, want 3 entries", descriptor.Kinds)
	}
	if len(descriptor.Capabilities) != 10 {
		t.Fatalf("descriptor capabilities = %v, want 10 entries", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_resource_type", Name: "openai_subscription", Subject: "openai_codex"}) {
		t.Fatalf("descriptor is missing provider resource type capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "supports_custom_headers", Subject: "openai_codex", Value: "false"}) {
		t.Fatalf("descriptor is missing provider policy capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "route_protocol", Subject: "openai_codex", Value: "codex/responses"}) {
		t.Fatalf("descriptor is missing provider route protocol capability: %+v", descriptor.Capabilities)
	}
	if len(manifest.Capabilities.Provider.RouteProtocols) != 1 || manifest.Capabilities.Provider.RouteProtocols[0] != "codex/responses" {
		t.Fatalf("provider route protocols = %+v", manifest.Capabilities.Provider.RouteProtocols)
	}
	if manifest.Capabilities.Provider.SupportsCustomHeaders == nil || *manifest.Capabilities.Provider.SupportsCustomHeaders {
		t.Fatalf("provider custom header policy = %+v", manifest.Capabilities.Provider.SupportsCustomHeaders)
	}
	hooks := manifest.GatewayHooks()
	if len(hooks) != 1 {
		t.Fatalf("gateway hooks = %d, want 1", len(hooks))
	}
	if hooks[0].PluginID != manifest.ID || hooks[0].Stage != StageRouteRank {
		t.Fatalf("gateway hook = %+v", hooks[0])
	}
	actions := manifest.Actions()
	if len(actions) != 1 || actions[0].ActionID != "codex.quota.read" || actions[0].Kind != ActionKindRead {
		t.Fatalf("actions = %+v", actions)
	}
	if actions[0].Capability != "quota.read" || actions[0].Subject != "openai_codex" {
		t.Fatalf("action capability metadata = %+v", actions[0])
	}
	if actions[0].InputSchema["type"] != "object" {
		t.Fatalf("action input schema = %+v", actions[0].InputSchema)
	}
	jobs := manifest.BackgroundJobs()
	if len(jobs) != 1 || jobs[0].JobID != "codex.quota.refresh" || jobs[0].Schedule != "*/10 * * * *" {
		t.Fatalf("background jobs = %+v", jobs)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "background_job", Name: "codex.quota.refresh", Subject: "openai_codex", Value: "quota.refresh"}) {
		t.Fatalf("descriptor is missing background job capability: %+v", descriptor.Capabilities)
	}
}

func descriptorHasCapability(descriptor Descriptor, capability CapabilityDescriptor) bool {
	for _, candidate := range descriptor.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func TestParseManifestBuildsProviderCatalogCapability(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.custom-stdio
name: Custom Stdio Provider
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - custom_stdio
  provider:
    catalog:
      display_name: Custom Stdio
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
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	descriptor := manifest.Descriptor()
	var catalogValue string
	for _, capability := range descriptor.Capabilities {
		if capability.Kind == "provider_catalog" && capability.Name == "entry" && capability.Subject == "custom_stdio" {
			catalogValue = capability.Value
			break
		}
	}
	if catalogValue == "" {
		t.Fatalf("descriptor is missing provider catalog capability: %+v", descriptor.Capabilities)
	}
	var catalog ManifestProviderCatalog
	if err := json.Unmarshal([]byte(catalogValue), &catalog); err != nil {
		t.Fatalf("decode provider catalog capability: %v", err)
	}
	if catalog.ID != "custom_stdio" || catalog.Type != "custom_stdio" || catalog.DisplayName != "Custom Stdio" || len(catalog.Models) != 1 {
		t.Fatalf("provider catalog capability = %+v", catalog)
	}
}

func TestParseManifestRejectsInvalidProviderCatalog(t *testing.T) {
	for _, manifest := range []string{
		`
schema_version: 1
id: tokenhub.provider.catalog
name: Catalog Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider:
    catalog:
      display_name: Missing Type
`,
		`
schema_version: 1
id: tokenhub.provider.catalog
name: Catalog Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - custom_stdio
  provider:
    catalog:
      id: bad/id
`,
		`
schema_version: 1
id: tokenhub.provider.catalog
name: Catalog Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - custom_stdio
  provider:
    catalog:
      models:
        - display_name: Missing ID
`,
		`
schema_version: 1
id: tokenhub.provider.catalog
name: Catalog Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - gateway_chain
capabilities:
  provider_types:
    - custom_stdio
  provider:
    catalog:
      display_name: Wrong Kind
`,
	} {
		_, err := ParseManifest([]byte(manifest))
		if err == nil {
			t.Fatal("manifest with invalid provider catalog parsed successfully")
		}
	}
}

func TestParseManifestRejectsUnsupportedHookStage(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.bad
name: Bad Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - gateway_chain
capabilities:
  hooks:
    - id: bad
      stage: unknown_stage
`))
	if err == nil {
		t.Fatal("manifest with unsupported hook stage parsed successfully")
	}
}

func TestParseManifestRejectsGatewayChainAsProductKind(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.bad-kind
name: Bad Kind
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - gateway_chain
placement:
  - gateway_chain
`))
	if err == nil {
		t.Fatal("manifest accepted gateway_chain as a product kind")
	}
}

func TestParseManifestRejectsHookDataWithoutPermission(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.privacy
name: Privacy
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
      reads:
        - request_body
`))
	if err == nil {
		t.Fatal("manifest with undeclared hook data permissions parsed successfully")
	}
}

func TestParseManifestRejectsAdminUIWithoutPresentationPlacement(t *testing.T) {
	for _, manifest := range []string{
		`
schema_version: 1
id: tokenhub.ui
name: UI Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - admin_ui
placement:
  - gateway_chain
capabilities:
  admin_ui:
    - settings_panel
`,
		`
schema_version: 1
id: tokenhub.ui-schema
name: UI Schema Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - sim
placement:
  - gateway_chain
entry:
  frontend:
    schema: admin-ui.schema.json
`,
	} {
		_, err := ParseManifest([]byte(manifest))
		if err == nil {
			t.Fatal("manifest with admin UI surface but no presentation placement parsed successfully")
		}
	}
}

func TestParseManifestRejectsGatewayHookWithoutGatewayChainPlacement(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.router
name: Router
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - presentation
capabilities:
  hooks:
    - id: rank
      stage: route_rank
      reads:
        - route_candidates
permissions:
  data:
    read:
      - route_candidates
`))
	if err == nil {
		t.Fatal("manifest with gateway hook but no gateway_chain placement parsed successfully")
	}
}

func TestParseManifestRejectsActionWithoutManagementPlacement(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.action
name: Action Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - admin_ui
placement:
  - presentation
capabilities:
  actions:
    - id: run
      kind: mutate
`))
	if err == nil {
		t.Fatal("manifest with action but no management_action placement parsed successfully")
	}
}

func TestParseManifestRejectsBackgroundJobWithoutBackgroundPlacement(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.job
name: Job Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - management_action
capabilities:
  background_jobs:
    - id: quota.refresh
      schedule: "*/10 * * * *"
      max_concurrency: 1
`))
	if err == nil {
		t.Fatal("manifest with background job but no background placement parsed successfully")
	}
}

func TestParseManifestRejectsUnsupportedHookDataClass(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.bad-data
name: Bad Data
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
      reads:
        - request_soul
permissions:
  data:
    read:
      - request_soul
`))
	if err == nil {
		t.Fatal("manifest with unsupported hook data class parsed successfully")
	}
}

func TestParseManifestRequiresPluginAPI(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.bad
name: Bad Plugin
version: 1.0.0
kinds:
  - provider
`))
	if err == nil {
		t.Fatal("manifest without tokenhub.plugin_api parsed successfully")
	}
}
