package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeDiscoversPluginManifestsInInstallDirectory(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "b-plugin"), `
schema_version: 1
id: tokenhub.b
name: B Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - sim
placement:
  - presentation
capabilities:
  admin_ui:
    - dashboard_panel
`)
	writeManifest(t, filepath.Join(root, "a-plugin"), `
schema_version: 1
id: tokenhub.a
name: A Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - gateway_chain
capabilities:
  hooks:
    - id: trace
      stage: trace_export
      priority: 2500
      failure_policy: observe_only
      reads:
        - audit
permissions:
  data:
    read:
      - audit
`)

	runtime := NewRuntime(root)
	packages, err := runtime.Discover()
	if err != nil {
		t.Fatalf("discover plugins: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(packages))
	}
	if packages[0].Manifest.ID != "tokenhub.a" || packages[1].Manifest.ID != "tokenhub.b" {
		t.Fatalf("packages are not sorted by directory name: %q, %q", packages[0].Manifest.ID, packages[1].Manifest.ID)
	}
}

func TestRuntimeLoadIntoRegistersDescriptorsAndHooks(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "privacy"), `
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
	plugins := NewRegistry()
	chain := NewGatewayChainRegistry()

	packages, err := NewRuntime(root).LoadInto(plugins, chain)
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("packages = %d, want 1", len(packages))
	}
	if _, ok := plugins.Describe("tokenhub.privacy"); !ok {
		t.Fatal("plugin descriptor was not registered")
	}
	hooks := chain.Hooks(StagePrivacyPre)
	if len(hooks) != 1 || hooks[0].HookID != "mask" {
		t.Fatalf("privacy hooks = %+v", hooks)
	}
}

func TestRuntimeLoadIntoRegistersAdminUIContributions(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "codex")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.codex
name: Codex
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
  - admin_ui
placement:
  - presentation
entry:
  frontend:
    schema: ui/admin-ui.schema.json
capabilities:
  provider_types:
    - openai_codex
  admin_ui:
    - provider_form
`)
	if err := os.MkdirAll(filepath.Join(pluginDir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "ui", "admin-ui.schema.json"), []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "quota",
				"slot": "provider.resource.panel",
				"title": "Quota",
				"provider_types": ["openai_codex"],
				"action": "codex.quota.read"
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	plugins := NewRegistry()
	chain := NewGatewayChainRegistry()
	adminUI := NewAdminUIRegistry()

	if _, err := NewRuntime(root).LoadInto(plugins, chain, adminUI); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	contributions := adminUI.List()
	if len(contributions) != 1 {
		t.Fatalf("admin UI contributions = %d, want 1", len(contributions))
	}
	if contributions[0].PluginID != "tokenhub.codex" || contributions[0].Slot != SlotProviderResourcePanel {
		t.Fatalf("admin UI contribution = %+v", contributions[0])
	}
}

func TestRuntimeLoadIntoWithActionsRegistersActionDescriptors(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "action"), `
schema_version: 1
id: tokenhub.action
name: Action Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - management_action
capabilities:
  actions:
    - id: sync.run
      kind: mutate
      title: Run sync
`)
	actions := NewActionBroker()

	if _, err := NewRuntime(root).LoadIntoWithActions(NewRegistry(), NewGatewayChainRegistry(), nil, actions); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	descriptor, ok := actions.Describe("tokenhub.action", "sync.run")
	if !ok {
		t.Fatal("action descriptor was not registered")
	}
	if descriptor.Kind != ActionKindMutate || descriptor.Title != "Run sync" {
		t.Fatalf("action descriptor = %+v", descriptor)
	}
	_, err := actions.Execute(t.Context(), ActionInvocation{PluginID: "tokenhub.action", ActionID: "sync.run"})
	if !errors.Is(err, ErrPluginActionUnavailable) {
		t.Fatalf("execute descriptor-only action error = %v, want ErrPluginActionUnavailable", err)
	}
}

func TestRuntimeRejectsAdminUISchemaPathOutsidePluginDirectory(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "bad-ui"), `
schema_version: 1
id: tokenhub.bad-ui
name: Bad UI
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - admin_ui
placement:
  - presentation
entry:
  frontend:
    schema: ../outside.json
`)
	_, err := NewRuntime(root).Discover()
	if err == nil {
		t.Fatal("runtime discovered plugin with escaping admin UI schema path")
	}
}

func TestRuntimeLoadIntoRejectsDuplicatePluginIDs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a", "b"} {
		writeManifest(t, filepath.Join(root, dir), `
schema_version: 1
id: tokenhub.duplicate
name: Duplicate
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	}

	_, err := NewRuntime(root).LoadInto(NewRegistry(), NewGatewayChainRegistry())
	if err == nil {
		t.Fatal("runtime loaded duplicate plugin IDs successfully")
	}
}

func TestRuntimeIgnoresMissingInstallDirectory(t *testing.T) {
	packages, err := NewRuntime(filepath.Join(t.TempDir(), "missing")).Discover()
	if err != nil {
		t.Fatalf("discover missing directory: %v", err)
	}
	if len(packages) != 0 {
		t.Fatalf("packages = %d, want 0", len(packages))
	}
}

func writeManifest(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
