package plugin

import "testing"

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
capabilities:
  provider_types:
    - openai_codex
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
	if len(descriptor.Capabilities) != 6 {
		t.Fatalf("descriptor capabilities = %v, want 6 entries", descriptor.Capabilities)
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
