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
distribution:
  marketplace_url: https://plugins.example/tokenhub.openai-codex
  repository_url: https://github.com/example/tokenhub-openai-codex
  download_url: https://plugins.example/tokenhub.openai-codex/1.0.0/plugin.tar.gz
  checksum_sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  signature_url: https://plugins.example/tokenhub.openai-codex/1.0.0/plugin.tar.gz.sig
  homepage_url: https://example.com/tokenhub-openai-codex
  license: Apache-2.0
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
    auth_modes:
      - x-api-key
      - bearer
    supports_custom_headers: false
    api_key_required: false
    route_requires_resource: true
    credentials_scope: resource
    session_affinity_kind: codex_session
    claude_code_attribution_default: strip
    preserve_reasoning_content: true
    default_base_url: https://chatgpt.example/backend-api/codex
    error_profile: kronk
    model_discovery:
      path: /codex/models
      auth: query_param
      api_key_query_param: token
      headers:
        x-codex-version: "2026-01-01"
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
      metadata:
        default_payload_json: '{"refresh":true}'
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
      subject: openai_codex
      metadata:
        protocol: codex/responses
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
	if len(descriptor.Capabilities) != 24 {
		t.Fatalf("descriptor capabilities = %v, want 24 entries", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_resource_type", Name: "openai_subscription", Subject: "openai_codex"}) {
		t.Fatalf("descriptor is missing provider resource type capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "supports_custom_headers", Subject: "openai_codex", Value: "false"}) {
		t.Fatalf("descriptor is missing provider policy capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "api_key_required", Subject: "openai_codex", Value: "false"}) {
		t.Fatalf("descriptor is missing provider API key policy capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "route_requires_resource", Subject: "openai_codex", Value: "true"}) {
		t.Fatalf("descriptor is missing provider route resource policy capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "credentials_scope", Subject: "openai_codex", Value: "resource"}) {
		t.Fatalf("descriptor is missing provider credentials scope policy capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "session_affinity_kind", Subject: "openai_codex", Value: "codex_session"}) {
		t.Fatalf("descriptor is missing provider session affinity policy capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "claude_code_attribution_default", Subject: "openai_codex", Value: "strip"}) {
		t.Fatalf("descriptor is missing provider Claude Code attribution default capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "preserve_reasoning_content", Subject: "openai_codex", Value: "true"}) {
		t.Fatalf("descriptor is missing provider reasoning content default capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "default_base_url", Subject: "openai_codex", Value: "https://chatgpt.example/backend-api/codex"}) {
		t.Fatalf("descriptor is missing provider default base URL capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "error_profile", Subject: "openai_codex", Value: "kronk"}) {
		t.Fatalf("descriptor is missing provider error profile capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_path", Subject: "openai_codex", Value: "/codex/models"}) {
		t.Fatalf("descriptor is missing provider model discovery path capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_auth", Subject: "openai_codex", Value: "query_param"}) {
		t.Fatalf("descriptor is missing provider model discovery auth capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_api_key_query_param", Subject: "openai_codex", Value: "token"}) {
		t.Fatalf("descriptor is missing provider model discovery query param capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_headers", Subject: "openai_codex", Value: `{"x-codex-version":"2026-01-01"}`}) {
		t.Fatalf("descriptor is missing provider model discovery headers capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "route_protocol", Subject: "openai_codex", Value: "codex/responses"}) {
		t.Fatalf("descriptor is missing provider route protocol capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "auth_mode", Subject: "openai_codex", Value: "x-api-key"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "auth_mode", Subject: "openai_codex", Value: "bearer"}) {
		t.Fatalf("descriptor is missing provider auth mode policy capabilities: %+v", descriptor.Capabilities)
	}
	if len(manifest.Capabilities.Provider.RouteProtocols) != 1 || manifest.Capabilities.Provider.RouteProtocols[0] != "codex/responses" {
		t.Fatalf("provider route protocols = %+v", manifest.Capabilities.Provider.RouteProtocols)
	}
	if len(manifest.Capabilities.Provider.AuthModes) != 2 || manifest.Capabilities.Provider.AuthModes[0] != "x-api-key" || manifest.Capabilities.Provider.AuthModes[1] != "bearer" {
		t.Fatalf("provider auth modes = %+v", manifest.Capabilities.Provider.AuthModes)
	}
	if manifest.Capabilities.Provider.SupportsCustomHeaders == nil || *manifest.Capabilities.Provider.SupportsCustomHeaders {
		t.Fatalf("provider custom header policy = %+v", manifest.Capabilities.Provider.SupportsCustomHeaders)
	}
	if manifest.Capabilities.Provider.APIKeyRequired == nil || *manifest.Capabilities.Provider.APIKeyRequired {
		t.Fatalf("provider API key policy = %+v", manifest.Capabilities.Provider.APIKeyRequired)
	}
	if manifest.Capabilities.Provider.RouteRequiresResource == nil || !*manifest.Capabilities.Provider.RouteRequiresResource {
		t.Fatalf("provider route resource policy = %+v", manifest.Capabilities.Provider.RouteRequiresResource)
	}
	if manifest.Capabilities.Provider.CredentialsScope != "resource" {
		t.Fatalf("provider credentials scope = %q", manifest.Capabilities.Provider.CredentialsScope)
	}
	if manifest.Capabilities.Provider.SessionAffinityKind != "codex_session" {
		t.Fatalf("provider session affinity kind = %q", manifest.Capabilities.Provider.SessionAffinityKind)
	}
	if manifest.Capabilities.Provider.ModelDiscovery.Path != "/codex/models" ||
		manifest.Capabilities.Provider.ModelDiscovery.Auth != "query_param" ||
		manifest.Capabilities.Provider.ModelDiscovery.APIKeyQueryParam != "token" ||
		manifest.Capabilities.Provider.ModelDiscovery.Headers["x-codex-version"] != "2026-01-01" {
		t.Fatalf("provider model discovery policy = %+v", manifest.Capabilities.Provider.ModelDiscovery)
	}
	hooks := manifest.GatewayHooks()
	if len(hooks) != 1 {
		t.Fatalf("gateway hooks = %d, want 1", len(hooks))
	}
	if hooks[0].PluginID != manifest.ID || hooks[0].Stage != StageRouteRank {
		t.Fatalf("gateway hook = %+v", hooks[0])
	}
	if hooks[0].Subject != "openai_codex" || hooks[0].Metadata["protocol"] != "codex/responses" {
		t.Fatalf("gateway hook metadata = %+v", hooks[0])
	}
	actions := manifest.Actions()
	if len(actions) != 1 || actions[0].ActionID != "codex.quota.read" || actions[0].Kind != ActionKindRead {
		t.Fatalf("actions = %+v", actions)
	}
	if actions[0].Capability != "quota.read" || actions[0].Subject != "openai_codex" || actions[0].Metadata["default_payload_json"] != `{"refresh":true}` {
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
	if descriptor.Distribution == nil ||
		descriptor.Distribution.MarketplaceURL != "https://plugins.example/tokenhub.openai-codex" ||
		descriptor.Distribution.DownloadURL != "https://plugins.example/tokenhub.openai-codex/1.0.0/plugin.tar.gz" ||
		descriptor.Distribution.ChecksumSHA256 != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" ||
		descriptor.Distribution.License != "Apache-2.0" {
		t.Fatalf("descriptor distribution = %+v", descriptor.Distribution)
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

func TestParseManifestBuildsLegacyProviderResourceTypeCapability(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.legacy-resource
name: Legacy Resource
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - legacy_provider
  provider_resource_types:
    - openai_subscription
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	descriptor := manifest.Descriptor()
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_resource_type", Name: "openai_subscription", Subject: "legacy_provider"}) {
		t.Fatalf("descriptor is missing legacy provider resource type capability: %+v", descriptor.Capabilities)
	}
}

func TestParseManifestBuildsDefaultCatalogProviderTypeCapability(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.default-compatible
name: Default Compatible
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - default_compatible
  provider:
    default_catalog_provider_type: true
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	descriptor := manifest.Descriptor()
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "default_catalog_provider_type", Subject: "default_compatible", Value: "true"}) {
		t.Fatalf("descriptor is missing default catalog provider type capability: %+v", descriptor.Capabilities)
	}
}

func TestParseManifestBuildsResponsesModelAllowlistCapabilities(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.responses-model-scoped
name: Responses Model Scoped
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
capabilities:
  provider_types:
    - model_scoped_provider
  provider:
    responses_model_allowlist:
      - model-a
      - model-b
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	descriptor := manifest.Descriptor()
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "responses_model_allowlist", Subject: "model_scoped_provider", Value: "model-a"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "responses_model_allowlist", Subject: "model_scoped_provider", Value: "model-b"}) {
		t.Fatalf("descriptor is missing Responses model allowlist capabilities: %+v", descriptor.Capabilities)
	}
}

func TestParseManifestBuildsProviderResourceTypeMetadataCapability(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.kimi
name: Kimi Provider
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - kimi_subscription
  provider_resource_types:
    - type: kimi_oauth_account
      display_name: Kimi OAuth Account
      auth_modes:
        - oauth
        - personal_access_token
      credential_identity_profile: openai_account_id_token
      credential_input_optional: true
      default: true
      defaults:
        auth_type: oauth
        base_url: https://api.moonshot.cn/v1
        max_concurrency: "3"
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	descriptor := manifest.Descriptor()
	var resourceValue string
	for _, capability := range descriptor.Capabilities {
		if capability.Kind == "provider_resource_type" && capability.Name == "kimi_oauth_account" && capability.Subject == "kimi_subscription" {
			resourceValue = capability.Value
			break
		}
	}
	if resourceValue == "" {
		t.Fatalf("descriptor is missing provider resource type metadata: %+v", descriptor.Capabilities)
	}
	var resourceType ManifestProviderResourceType
	if err := json.Unmarshal([]byte(resourceValue), &resourceType); err != nil {
		t.Fatalf("decode provider resource type capability: %v", err)
	}
	if resourceType.Type != "kimi_oauth_account" || resourceType.DisplayName != "Kimi OAuth Account" || !resourceType.Default {
		t.Fatalf("provider resource type capability = %+v", resourceType)
	}
	if len(resourceType.AuthModes) != 2 || resourceType.AuthModes[0] != "oauth" || resourceType.AuthModes[1] != "personal_access_token" {
		t.Fatalf("provider resource type auth modes = %+v", resourceType.AuthModes)
	}
	if resourceType.Defaults["auth_type"] != "oauth" || resourceType.Defaults["base_url"] != "https://api.moonshot.cn/v1" || resourceType.Defaults["max_concurrency"] != "3" {
		t.Fatalf("provider resource type defaults = %+v", resourceType.Defaults)
	}
	if resourceType.CredentialIdentityProfile != "openai_account_id_token" {
		t.Fatalf("provider resource type credential identity profile = %q", resourceType.CredentialIdentityProfile)
	}
	if !resourceType.CredentialInputOptional {
		t.Fatalf("provider resource type credential input optional = false")
	}
}

func TestParseManifestRejectsProviderResourceTypeWithoutType(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.bad-resource
name: Bad Resource
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - bad_provider
  provider_resource_types:
    - display_name: Missing Type
`))
	if err == nil {
		t.Fatal("manifest with missing provider resource type parsed successfully")
	}
}

func TestParseManifestRejectsInvalidProviderCredentialsScope(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.bad-credentials-scope
name: Bad Credentials Scope
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - bad_provider
  provider:
    credentials_scope: tenant
`))
	if err == nil {
		t.Fatal("manifest with invalid provider credentials scope parsed successfully")
	}
}

func TestParseManifestRejectsInvalidProviderSessionAffinityKind(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.bad-affinity-kind
name: Bad Affinity Kind
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - bad_provider
  provider:
    session_affinity_kind: sticky_session
`))
	if err == nil {
		t.Fatal("manifest with invalid provider session affinity kind parsed successfully")
	}
}

func TestParseManifestRejectsUnsafeDistributionURL(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.provider.unsafe-distribution
name: Unsafe Distribution
version: 1.0.0
tokenhub:
  plugin_api: v1
distribution:
  download_url: http://plugins.example/plugin.tar.gz
kinds:
  - provider
placement:
  - gateway_chain
capabilities:
  provider_types:
    - unsafe_provider
`))
	if err == nil {
		t.Fatal("manifest with insecure distribution URL parsed successfully")
	}
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
