package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
      writes:
        - audit
permissions:
  data:
    read:
      - audit
    write:
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

func TestRuntimeLoadIntoRegistersDisabledDescriptorWithoutActivatingHooks(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "privacy")
	writeManifest(t, pluginDir, `
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
	writePackageStateFile(t, pluginDir, `{"status":"disabled","reason":"operator disabled during upgrade"}`)
	plugins := NewRegistry()
	chain := NewGatewayChainRegistry()

	packages, err := NewRuntime(root).LoadInto(plugins, chain)
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 1 || packages[0].State.Status != StatusDisabled || packages[0].State.Reason == "" {
		t.Fatalf("packages = %+v, want disabled package state", packages)
	}
	descriptor, ok := plugins.Describe("tokenhub.privacy")
	if !ok {
		t.Fatal("disabled plugin descriptor was not registered")
	}
	if descriptor.Status != StatusDisabled {
		t.Fatalf("descriptor status = %q, want disabled", descriptor.Status)
	}
	if hooks := chain.Hooks(StagePrivacyPre); len(hooks) != 0 {
		t.Fatalf("disabled plugin hooks were activated: %+v", hooks)
	}
}

func TestRuntimeLoadIntoSkipsNonLoadableLifecycleStates(t *testing.T) {
	root := t.TempDir()
	for _, fixture := range []struct {
		dir   string
		id    string
		state string
	}{
		{
			dir:   "pending",
			id:    "tokenhub.pending",
			state: `{"status":"pending_restart","reason":"installed update","audit_event":"pending_restart"}`,
		},
		{
			dir:   "failed",
			id:    "tokenhub.failed",
			state: `{"status":"failed_validation","health":"unhealthy","last_error_code":"plugin_api_unsupported","audit_event":"validation_failed"}`,
		},
	} {
		pluginDir := filepath.Join(root, fixture.dir)
		writeManifest(t, pluginDir, lifecycleHookManifest(fixture.id))
		writePackageStateFile(t, pluginDir, fixture.state)
	}
	plugins := NewRegistry()
	chain := NewGatewayChainRegistry()

	packages, err := NewRuntime(root).LoadInto(plugins, chain)
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(packages))
	}
	for _, pkg := range packages {
		descriptor, ok := plugins.Describe(pkg.Manifest.ID)
		if !ok {
			t.Fatalf("descriptor for %s was not registered", pkg.Manifest.ID)
		}
		if descriptor.Status != pkg.State.Status {
			t.Fatalf("descriptor status for %s = %q, want %q", pkg.Manifest.ID, descriptor.Status, pkg.State.Status)
		}
		if pkg.State.Loadable() {
			t.Fatalf("package %s is unexpectedly loadable: %+v", pkg.Manifest.ID, pkg.State)
		}
	}
	if hooks := chain.Hooks(StagePrivacyPre); len(hooks) != 0 {
		t.Fatalf("non-loadable lifecycle states activated hooks: %+v", hooks)
	}
}

func TestRuntimeLoadIntoActivatesRollbackAndMandatoryLifecycleStates(t *testing.T) {
	root := t.TempDir()
	for _, fixture := range []struct {
		dir   string
		id    string
		state string
	}{
		{
			dir:   "rollback",
			id:    "tokenhub.rollback",
			state: `{"status":"rollback_available","rollback_version":"1.0.0","audit_event":"rollback_available"}`,
		},
		{
			dir:   "mandatory",
			id:    "tokenhub.mandatory",
			state: `{"status":"mandatory","health":"healthy"}`,
		},
	} {
		pluginDir := filepath.Join(root, fixture.dir)
		writeManifest(t, pluginDir, lifecycleHookManifest(fixture.id))
		writePackageStateFile(t, pluginDir, fixture.state)
	}
	plugins := NewRegistry()
	chain := NewGatewayChainRegistry()

	packages, err := NewRuntime(root).LoadInto(plugins, chain)
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(packages))
	}
	if hooks := chain.Hooks(StagePrivacyPre); len(hooks) != 2 {
		t.Fatalf("loadable lifecycle states activated %d hooks, want 2: %+v", len(hooks), hooks)
	}
	for _, pkg := range packages {
		if !pkg.State.Loadable() {
			t.Fatalf("package %s is not loadable: %+v", pkg.Manifest.ID, pkg.State)
		}
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

func TestRuntimeLoadIntoReflectsAdminUIContributionsInDescriptor(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "sim")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.sim.enterprise
name: Enterprise SIM
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - sim
placement:
  - presentation
entry:
  frontend:
    schema: admin-ui.schema.json
`)
	if err := os.WriteFile(filepath.Join(pluginDir, "admin-ui.schema.json"), []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "enterprise-theme",
				"slot": "theme.tokens",
				"schema": {"tokens": {"accent": "#2563eb"}}
			},
			{
				"id": "ops-layout",
				"slot": "layout.preset",
				"schema": {"preset": {"density": "compact"}}
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()

	if _, err := NewRuntime(root).LoadInto(registry, NewGatewayChainRegistry(), NewAdminUIRegistry()); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	descriptor, ok := registry.Describe("tokenhub.sim.enterprise")
	if !ok {
		t.Fatal("plugin descriptor was not registered")
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "admin_ui", Name: "theme.tokens", Subject: "enterprise-theme"}) {
		t.Fatalf("descriptor does not include theme contribution: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "admin_ui", Name: "layout.preset", Subject: "ops-layout"}) {
		t.Fatalf("descriptor does not include layout contribution: %+v", descriptor.Capabilities)
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

func TestRuntimeLoadIntoWithActionsMarksUnboundAdminUIActionAsStartupFailed(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "ui-action")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.ui-action
name: UI Action Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - admin_ui
placement:
  - presentation
entry:
  frontend:
    schema: admin-ui.schema.json
capabilities:
  admin_ui:
    - provider_form
`)
	if err := os.WriteFile(filepath.Join(pluginDir, "admin-ui.schema.json"), []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "setup",
				"slot": "provider.form.section",
				"action": "oauth.start"
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	adminUI := NewAdminUIRegistry()
	actions := NewActionBroker()

	packages, err := NewRuntime(root).LoadIntoWithActions(NewRegistry(), NewGatewayChainRegistry(), adminUI, actions)
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 1 || !packages[0].State.FailedStartup() ||
		packages[0].State.AuditEvent != PackageLifecycleStartupFailed ||
		packages[0].State.LastErrorCode != "plugin_startup_failed" {
		t.Fatalf("packages = %+v, want startup-failed package", packages)
	}
	state, err := readPackageState(pluginDir)
	if err != nil {
		t.Fatalf("read failed package state: %v", err)
	}
	if !state.FailedStartup() || !strings.Contains(state.Reason, "action oauth.start is not registered for plugin tokenhub.ui-action") {
		t.Fatalf("failed package state = %+v", state)
	}
	if contributions := adminUI.List(); len(contributions) != 0 {
		t.Fatalf("unbound Admin UI contribution was published: %+v", contributions)
	}
}

func TestRuntimeLoadIntoWithActionsAllowsDescriptorOnlyUIAction(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "ui-action")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.ui-action
name: UI Action Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - admin_ui
  - extension
placement:
  - presentation
  - management_action
entry:
  frontend:
    schema: admin-ui.schema.json
capabilities:
  admin_ui:
    - provider_form
  actions:
    - id: oauth.start
      kind: external_redirect
      title: Start OAuth
`)
	if err := os.WriteFile(filepath.Join(pluginDir, "admin-ui.schema.json"), []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "setup",
				"slot": "provider.form.section",
				"schema": {
					"fields": [
						{"name": "connect", "type": "oauth_button", "action": "oauth.start"}
					]
				}
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	adminUI := NewAdminUIRegistry()
	actions := NewActionBroker()

	if _, err := NewRuntime(root).LoadIntoWithActions(NewRegistry(), NewGatewayChainRegistry(), adminUI, actions); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	contributions := adminUI.List()
	if len(contributions) != 1 || contributions[0].ID != "setup" {
		t.Fatalf("Admin UI contributions = %+v, want setup", contributions)
	}
	_, err := actions.Execute(t.Context(), ActionInvocation{PluginID: "tokenhub.ui-action", ActionID: "oauth.start"})
	if !errors.Is(err, ErrPluginActionUnavailable) {
		t.Fatalf("execute descriptor-only UI action error = %v, want ErrPluginActionUnavailable", err)
	}
}

func TestRuntimeLoadIntoWithActionsBindsBackendCommand(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "action")
	writeManifest(t, pluginDir, `
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
entry:
  backend:
    protocol: stdio-json-v1
    command: action.sh
capabilities:
  actions:
    - id: sync.run
      kind: mutate
      title: Run sync
`)
	if err := os.WriteFile(filepath.Join(pluginDir, "action.sh"), []byte(`#!/bin/sh
printf '{"data":{"status":"started"}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	actions := NewActionBroker()

	if _, err := NewRuntime(root).LoadIntoWithActions(NewRegistry(), NewGatewayChainRegistry(), nil, actions); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	result, err := actions.Execute(t.Context(), ActionInvocation{PluginID: "tokenhub.action", ActionID: "sync.run"})
	if err != nil {
		t.Fatalf("execute runtime-bound action: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["status"] != "started" {
		t.Fatalf("action result = %+v, want started", data)
	}
}

func TestRuntimeLoadIntoWithActionsAndBackgroundRegistersJobs(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "jobs")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.jobs
name: Jobs Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - background
entry:
  backend:
    protocol: stdio-json-v1
    command: job.sh
capabilities:
  background_jobs:
    - id: quota.refresh
      title: Refresh quota
      capability: quota.refresh
      subject: openai_codex
      schedule: "*/10 * * * *"
      timeout_millis: 5000
      max_concurrency: 1
      input_schema:
        type: object
        required:
          - resource_id
        properties:
          resource_id:
            type: string
`)
	if err := os.WriteFile(filepath.Join(pluginDir, "job.sh"), []byte(`#!/bin/sh
cat >/dev/null
printf '{"data":{"refreshed":true}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	jobs := NewBackgroundJobBroker()

	if _, err := NewRuntime(root).LoadIntoWithActionsAndBackground(NewRegistry(), NewGatewayChainRegistry(), nil, nil, jobs); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	descriptor, ok := jobs.Describe("tokenhub.jobs", "quota.refresh")
	if !ok {
		t.Fatal("background job descriptor was not registered")
	}
	if descriptor.Schedule != "*/10 * * * *" || descriptor.Capability != "quota.refresh" || descriptor.Subject != "openai_codex" {
		t.Fatalf("background job descriptor = %+v", descriptor)
	}
	result, err := jobs.Execute(t.Context(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
		Trigger:  "manual",
		Payload:  json.RawMessage(`{"resource_id":"res_1"}`),
	})
	if err != nil {
		t.Fatalf("execute background job: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["refreshed"] != true {
		t.Fatalf("background job result = %+v, want refreshed", data)
	}
}

func TestRuntimeLoadIntoWithActionsBindsGatewayHookCommand(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "hook")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.hook
name: Hook Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - gateway_chain
entry:
  backend:
    protocol: stdio-json-v1
    command: hook.sh
capabilities:
  hooks:
    - id: trace
      stage: trace_export
      priority: 2300
      failure_policy: observe_only
      reads:
        - audit
permissions:
 data:
    read:
      - audit
`)
	if err := os.WriteFile(filepath.Join(pluginDir, "hook.sh"), []byte(`#!/bin/sh
cat >/dev/null
printf '{"decision":"continue"}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	chain := NewGatewayChainRegistry()
	runner := NewGatewayHookRunner(chain)

	if _, err := NewRuntime(root).LoadIntoWithActions(NewRegistry(), chain, nil, nil, runner); err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	report, err := runner.RunStage(t.Context(), StageTraceExport, GatewayHookInput{
		RequestID: "req_1",
		Stage:     StageTraceExport,
		Data: GatewayHookData{
			DataAudit: json.RawMessage(`{"request_id":"req_1"}`),
		},
	})
	if err != nil {
		t.Fatalf("run trace export: %v", err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != HookRunSucceeded {
		t.Fatalf("run report = %+v", report)
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

func TestRuntimeLoadIntoMarksDuplicatePluginIDAsFailedValidation(t *testing.T) {
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

	packages, err := NewRuntime(root).LoadInto(NewRegistry(), NewGatewayChainRegistry())
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 2 || packages[0].State.FailedValidation() || !packages[1].State.FailedValidation() {
		t.Fatalf("packages = %+v, want only second duplicate failed validation", packages)
	}
	state, err := readPackageState(filepath.Join(root, "b"))
	if err != nil {
		t.Fatalf("read duplicate package state: %v", err)
	}
	if !state.FailedValidation() || state.AuditEvent != PackageLifecycleValidationFailed ||
		!strings.Contains(state.Reason, "duplicate plugin id tokenhub.duplicate") {
		t.Fatalf("duplicate package state = %+v", state)
	}
}

func TestRuntimeLoadIntoMarksInvalidManifestWithoutActivatingHooks(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "bad-hook")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.bad-hook
name: Bad Hook
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - gateway_chain
capabilities:
  hooks:
    - id: unsafe
      stage: unsupported_stage
      failure_policy: fail_closed
`)
	plugins := NewRegistry()
	chain := NewGatewayChainRegistry()

	packages, err := NewRuntime(root).LoadInto(plugins, chain)
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 1 || !packages[0].State.FailedValidation() ||
		packages[0].State.AuditEvent != PackageLifecycleValidationFailed {
		t.Fatalf("packages = %+v, want failed-validation package", packages)
	}
	if _, ok := plugins.Describe("tokenhub.bad-hook"); ok {
		t.Fatal("invalid manifest registered a plugin descriptor")
	}
	if hooks := chain.Hooks(StagePrivacyPre); len(hooks) != 0 {
		t.Fatalf("invalid manifest activated hooks: %+v", hooks)
	}
}

func TestRuntimeLoadIntoKeepsBuiltInFallbackWhenExternalStartupFails(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "external-codex")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.provider.openai-codex
name: External Codex
version: 2.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
  - admin_ui
placement:
  - presentation
entry:
  frontend:
    schema: admin-ui.schema.json
capabilities:
  provider_types:
    - openai_codex
  admin_ui:
    - provider_form
`)
	if err := os.WriteFile(filepath.Join(pluginDir, "admin-ui.schema.json"), []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "setup",
				"slot": "provider.form.section",
				"action": "codex.oauth.start"
			}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.Register(BuiltInProvider("tokenhub.provider.openai-codex", "OpenAI Codex Subscription", []string{"openai_codex"}, nil)); err != nil {
		t.Fatalf("register built-in fallback: %v", err)
	}
	adminUI := NewAdminUIRegistry()
	actions := NewActionBroker()

	packages, err := NewRuntime(root).LoadIntoWithActions(registry, NewGatewayChainRegistry(), adminUI, actions)
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	if len(packages) != 1 || !packages[0].State.FailedStartup() ||
		!packages[0].State.BuiltInFallbackAvailable() {
		t.Fatalf("packages = %+v, want startup failure with built-in fallback", packages)
	}
	descriptor, ok := registry.Describe("tokenhub.provider.openai-codex")
	if !ok {
		t.Fatal("built-in fallback descriptor is missing")
	}
	if descriptor.Source != SourceBuiltIn || descriptor.Name != "OpenAI Codex Subscription" || descriptor.Version != "built-in" {
		t.Fatalf("descriptor = %+v, want unchanged built-in fallback", descriptor)
	}
	if contributions := adminUI.List(); len(contributions) != 0 {
		t.Fatalf("failed external Admin UI contribution was published: %+v", contributions)
	}
}

func TestRuntimeRejectsUnsupportedPackageState(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "bad-state")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.bad-state
name: Bad State
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	writePackageStateFile(t, pluginDir, `{"status":"paused"}`)

	_, err := NewRuntime(root).Discover()
	if err == nil {
		t.Fatal("runtime discovered plugin with unsupported package state")
	}
}

func TestRuntimeUpdatePackageStateWritesLocalState(t *testing.T) {
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
`)

	pkg, err := NewRuntime(root).UpdatePackageState("tokenhub.privacy", PackageState{
		Status: StatusDisabled,
		Reason: "operator disabled before restart",
	})
	if err != nil {
		t.Fatalf("update package state: %v", err)
	}
	if pkg.State.Status != StatusDisabled || pkg.State.Reason != "operator disabled before restart" {
		t.Fatalf("updated package state = %+v", pkg.State)
	}
	packages, err := NewRuntime(root).Discover()
	if err != nil {
		t.Fatalf("discover after state update: %v", err)
	}
	if len(packages) != 1 || packages[0].State.Status != StatusDisabled {
		t.Fatalf("packages after update = %+v", packages)
	}
}

func TestRuntimeRollbackPackageRestoresRollbackPackage(t *testing.T) {
	root := t.TempDir()
	runtime := NewRuntime(root)
	archive := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest("tokenhub.rollback", "Rollback", "1.0.0"), Mode: 0o644},
		"data.txt":    {Body: "old", Mode: 0o644},
	})
	if _, err := runtime.InstallZipArchive(archive, InstallOptions{}); err != nil {
		t.Fatalf("install first package: %v", err)
	}
	next := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest("tokenhub.rollback", "Rollback", "2.0.0"), Mode: 0o644},
		"data.txt":    {Body: "new", Mode: 0o644},
	})
	if _, err := runtime.InstallZipArchive(next, InstallOptions{
		Replace:          true,
		PreserveRollback: true,
		InitialState: PackageState{
			Status:          StatusEnabled,
			RestartRequired: true,
			RollbackVersion: "1.0.0",
		},
	}); err != nil {
		t.Fatalf("install update package: %v", err)
	}

	pkg, err := runtime.RollbackPackage("tokenhub.rollback", "operator rollback")
	if err != nil {
		t.Fatalf("rollback package: %v", err)
	}
	if pkg.Manifest.Version != "1.0.0" || pkg.State.AuditEvent != PackageLifecycleRollbackStarted ||
		!pkg.State.RestartRequired || pkg.State.RollbackVersion != "" {
		t.Fatalf("rollback package = %+v, want restored version with rollback-started state", pkg)
	}
	data, err := os.ReadFile(filepath.Join(root, "tokenhub.rollback", "data.txt"))
	if err != nil {
		t.Fatalf("read restored package data: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("restored package data = %q, want old", data)
	}
}

func TestRuntimeRollbackPackageRejectsUnavailableRollback(t *testing.T) {
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
`)

	_, err := NewRuntime(root).RollbackPackage("tokenhub.privacy", "operator rollback")
	if !errors.Is(err, ErrPackageRollbackUnavailable) {
		t.Fatalf("rollback unavailable error = %v, want ErrPackageRollbackUnavailable", err)
	}
}

func TestRuntimeUninstallPackageRemovesInstalledDirectory(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "privacy")
	writeManifest(t, pluginDir, `
schema_version: 1
id: tokenhub.privacy
name: Privacy
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	writePackageStateFile(t, pluginDir, `{"status":"disabled","reason":"operator removed"}`)

	pkg, err := NewRuntime(root).UninstallPackage("tokenhub.privacy")
	if err != nil {
		t.Fatalf("uninstall package: %v", err)
	}
	if pkg.Manifest.ID != "tokenhub.privacy" || pkg.State.Status != StatusDisabled {
		t.Fatalf("uninstalled package = %+v, want disabled tokenhub.privacy package", pkg)
	}
	if _, err := os.Stat(pluginDir); !os.IsNotExist(err) {
		t.Fatalf("plugin directory still exists after uninstall: %v", err)
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

func lifecycleHookManifest(id string) string {
	return `
schema_version: 1
id: ` + id + `
name: Lifecycle
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
`
}

func writePackageStateFile(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, packageStateFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
