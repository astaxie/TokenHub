# TokenHub Plugin Development Guide

Language: English | [简体中文](zh-CN/plugin-development.md) | [日本語](ja/plugin-development.md)

This guide explains the current TokenHub plugin model and how to develop plugins against it. It is written for plugin authors, platform engineers, and operators who need a practical starting point rather than a theoretical proposal.

TokenHub's plugin design is intentionally conservative:

- Core stays small, auditable, and security-focused.
- Plugins own the behavior that changes often.
- Built-in plugins and external plugins use the same contract.
- Administration, provider integrations, gateway hooks, background jobs, themes, and UI contributions all flow through explicit plugin metadata.

## 1. The Plugin Model

TokenHub treats a plugin as a package with three separate axes:

| Axis | Question it answers | Examples |
| --- | --- | --- |
| Kind | What sort of plugin is this? | `provider`, `admin_ui`, `sim`, `extension` |
| Placement | Where does it run? | `presentation`, `gateway_chain`, `background`, `management_action` |
| Capability | What can it contribute? | `provider_types`, `actions`, `hooks`, `background_jobs`, `theme_tokens` |

A plugin can span more than one kind or placement. For example:

- A Codex subscription plugin is a `provider` that also contributes `gateway_chain` behavior and `management_action` entries.
- A trace exporter is usually an `extension` with `gateway_chain` placement.
- A heartbeat job is usually an `extension` with `background` placement.
- A shell skin is usually a `sim` plugin with `presentation` placement.

The core rule is simple:

> Core owns the pipeline, auth, routing, auditing, and compatibility. Plugins own the variable behavior.

## 2. Built-In vs External Plugins

TokenHub uses one contract for both built-in and external plugins.

| Type | Source | Typical use |
| --- | --- | --- |
| Built-in plugin | Shipped with TokenHub | Core provider adapters, base admin surfaces, core hooks, default themes |
| External plugin | Installed from a marketplace or private repo | Third-party providers, enterprise extensions, partner UI contributions |

The difference is distribution and trust, not capability shape.

Built-in plugins are normally trusted by default because they ship with the product. External plugins must pass manifest validation, permission checks, and signature/trust checks before they become active.

## 3. Manifest Shape

Every plugin package is described by `plugin.yaml`.

Minimal shape:

```yaml
schema_version: 1
id: tokenhub.provider.kimi-go
name: Kimi Subscription Go Provider
version: 1.0.0
description: Reference Go provider plugin for the TokenHub stdio-json-v1 contract kit.
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/provider-kimi-go
capabilities:
  provider_types:
    - kimi_subscription
permissions:
  data:
    read:
      - provider_credentials
distribution:
  repository_url: https://github.com/tokenhub-dev/tokenhub-plugin-marketplace
  homepage_url: https://github.com/tokenhub-dev/tokenhub-plugin-marketplace/tree/main/samples/provider-kimi-go
  license: Apache-2.0
```

### 3.1 Important Fields

- `schema_version`: manifest schema version.
- `tokenhub.plugin_api`: plugin API version. Current value is `v1`.
- `kinds`: one or more of `provider`, `admin_ui`, `sim`, `extension`.
- `placement`: one or more of `presentation`, `gateway_chain`, `background`, `management_action`.
- `entry.backend`: the executable entrypoint. Current samples use `stdio-json-v1`.
- `capabilities`: the actual feature declaration surface.
- `permissions`: the least-privilege declaration surface.
- `distribution`: repository URL, homepage URL, checksum, signature, and license metadata.

The manifest is the first gate. If the manifest is wrong, nothing else should run.

## 4. Runtime Contracts

TokenHub provides four standard runtime entrypoints in the Go SDK:

- `ServeProvider`
- `ServeAction`
- `ServeGatewayHook`
- `ServeBackgroundJob`

Each one wraps a single contract kind.

### 4.1 Provider invocation

Provider plugins receive a provider invocation with:

- operation
- provider projection
- resource projection
- provider model
- request payload
- credentials projection

That means the plugin sees only projected data, not raw core internals.

### 4.2 Action invocation

Action plugins receive:

- plugin ID
- action ID
- actor
- payload

This is the right surface for:

- OAuth start/exchange
- probe
- quota refresh
- custom admin operations

### 4.3 Gateway hook invocation

Gateway hook plugins receive:

- request ID
- stage
- envelope
- optional stage data

This is the right surface for:

- privacy filters
- context optimizers
- route ranking
- cache lookup/write
- request and response transforms
- trace export

### 4.4 Background job invocation

Background job plugins receive:

- plugin ID
- job ID
- trigger
- actor
- payload

This is the right surface for:

- quota sync
- heartbeat
- refresh
- cleanup
- reporting

## 5. How to Build a Plugin

The most reliable workflow is:

1. Pick the kind and placement first.
2. Define the smallest useful capability set.
3. Write the manifest.
4. Implement the runtime handler.
5. Add contract tests.
6. Run the package locally.
7. Publish to the marketplace.
8. Install and verify inside TokenHub.

### 5.1 Start from the question you are solving

Before writing code, answer:

- Is this a provider integration?
- Is this only an admin UI contribution?
- Is this a shell/theme change?
- Is this a gateway-chain extension?
- Is this a background job?
- Is this a management action?

If you cannot answer that, the plugin boundary is still too blurry.

### 5.2 Write the smallest capability first

Do not start with the full wish list.

Examples:

- Provider plugin: start with `provider_types` and `gateway`.
- Action plugin: start with one action ID.
- Hook plugin: start with one stage.
- Background plugin: start with one job.
- SIM plugin: start with one theme or layout contribution.

### 5.3 Implement the handler

Keep the handler small:

- parse the invocation
- perform the plugin's work
- return structured output
- avoid printing secrets

### 5.4 Add contract tests

Every plugin type should have contract tests.

Good tests check:

- manifest parsing
- capability completeness
- input/output shape
- secret redaction
- failure behavior

### 5.5 Run the local contract kit

The marketplace repo includes a local harness:

```bash
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package ./samples/provider-kimi-go
go run ./cmd/tokenhub-plugin-test action --package ./samples/action-echo-go
go run ./cmd/tokenhub-plugin-test hook --package ./samples/hook-trace-go
go run ./cmd/tokenhub-plugin-test background --package ./samples/background-heartbeat-go
```

Replace `--package` with your plugin directory.

### 5.6 Publish and install

When you are ready to publish, include:

- version
- repository URL
- homepage URL
- checksum
- signature
- license
- compatibility metadata

After installation, verify:

1. the manifest passes validation,
2. the permission set is minimal,
3. the plugin becomes active after restart.

## 6. How to Build Each Plugin Kind

### 6.1 Provider plugins

Provider plugins connect TokenHub to a model service or subscription account.

Usually they declare:

- `provider_types`
- `provider_resource_types`
- `provider.route_protocols`
- `provider.default_base_url`
- `provider.model_discovery`
- `provider.error_profile`
- `provider.credentials_scope`
- `provider.api_key_required`
- `provider.supports_custom_headers`

Common responsibilities:

- protocol translation
- model discovery
- quota or account sync
- credential refresh
- provider-specific route behavior
- provider-specific UI metadata

For subscription-style providers, keep quota refresh and account sync in background jobs or actions where possible.

### 6.2 Admin UI plugins

Admin UI plugins contribute configuration and operational surfaces.

Typical content:

- provider form sections
- resource panels
- dashboard cards
- route detail panels
- settings panels
- page templates

Rules:

- UI may be contributed by plugins.
- Execution must still pass through Core.
- Plugins must not bypass RBAC.
- Plugins must not directly use raw admin credentials.

The preferred pattern is declarative:

1. Describe the UI contribution in metadata.
2. Route actions through a core-mediated endpoint.
3. Keep data shaping in frontend domain helpers.
4. Keep React views mostly presentational.

### 6.3 SIM plugins

SIM plugins are for visual identity and layout.

Typical contributions:

- theme tokens
- logo or icon assets
- shell layout presets
- navigation composition
- dashboard composition
- page templates

SIM plugins should only affect `presentation`.

If the change also needs backend behavior, it is not only a SIM plugin anymore.

### 6.4 Extension plugins

Extension plugins provide horizontal capabilities.

Typical features:

- DLP
- prompt firewall
- semantic cache
- context optimizer
- model router
- billing connector
- notification channel
- approval workflow
- export/import
- trace export

Extension plugins can run in multiple placements:

- `gateway_chain` for request-path logic
- `background` for sync and recurring jobs
- `management_action` for operator-triggered actions
- `presentation` for related UI surfaces

Use `observe_only` or `read_only` defaults whenever possible. Mutation should be explicit and narrow.

## 7. Recommended Repository Layout

The current marketplace repo uses one SDK and multiple sample packages.

```text
tokenhub-plugin-marketplace/
  go.mod
  sdk/go/tokenhubplugin/
  contract-tests/
    provider/
    gateway-hook/
    management-action/
    background-job/
    protocol/stdio-json-v1/
  samples/
    provider-mock-go/
    provider-kimi-go/
    provider-glm-go/
    action-echo-go/
    hook-trace-go/
    background-heartbeat-go/
  cmd/tokenhub-plugin-test/
```

The smallest practical package usually contains:

- `plugin.yaml`
- `main.go`
- one fixture file
- one contract test set

Naming convention:

- `provider-xxx-go`
- `action-xxx-go`
- `hook-xxx-go`
- `background-xxx-go`

## 8. Versioning and Compatibility

TokenHub currently treats versioning as three separate concepts:

| Version | Meaning |
| --- | --- |
| Core Version | TokenHub product version |
| Plugin API Version | Plugin protocol and envelope contract version |
| Plugin Package Version | The plugin package's own version |

Compatibility rules:

1. `plugin_api` changes should be additive within a major version.
2. Manifest schema changes should remain forward compatible when possible.
3. Stage names should stay stable inside the same API major.
4. Envelope fields may be added, but existing semantics should not change silently.
5. New sensitive permissions require re-approval.
6. New placements or capabilities require Core validation.

The migration principle is simple:

- preserve old provider IDs,
- preserve old routes,
- preserve old resources and quotas,
- preserve old admin payload aliases until the new contract is ready.

## 9. Security and Trust

TokenHub assumes least privilege by default.

Plugins must not:

- access core DB directly
- bypass RBAC
- take raw admin tokens
- silently expand privileges
- redefine public `/v1` endpoints

Plugins must declare:

- what they read
- what they write
- what network access they need
- what stage/job/action they bind to
- whether restart is required

For marketplace distribution, include at least:

- checksum
- signature
- key ID
- repository URL
- homepage URL
- license
- compatibility verdict
- advisories
- release notes

## 10. Testing and Release Flow

Recommended order:

1. Local unit tests
2. Manifest parsing tests
3. Contract tests
4. Package-level tests
5. TokenHub integration tests
6. Marketplace/signature/compatibility checks
7. Install and restart validation

What to emphasize by kind:

- Provider plugin: route protocols, discovery, credentials projection, response shape, secret redaction.
- Admin UI plugin: schema parsing, action binding, payload redaction, no arbitrary admin API calls.
- SIM plugin: theme selection, layout selection, template rendering, dashboard composition.
- Extension plugin: stage order, mutation limits, failure policy, retry/cancel behavior, permission enforcement.

## 11. Migration Path From Built-Ins

The practical migration order is:

1. Normalize current built-in descriptors and registries to the plugin worldview.
2. Move provider adapters, quota, OAuth, and model discovery behind plugin contracts.
3. Turn admin pages, panels, and buttons into declarative contributions.
4. Split gateway enhancements into explicit hooks.
5. Turn recurring jobs into background plugins.
6. Expand the marketplace for external authors.

This approach keeps upgrades safe because every step can be released independently and validated with contract tests.

## 12. A Simple Decision Tree

Ask these questions:

```text
Does it connect TokenHub to a model or subscription account?
  -> Provider plugin

Does it only change admin pages, panels, or shell appearance?
  -> Admin UI or SIM plugin

Does it affect the path from user token request to provider response?
  -> Extension plugin with gateway_chain placement

Does it run on a schedule or after startup?
  -> Background plugin

Does it expose an operator-triggered action?
  -> Management action capability
```

Then ask:

```text
What is the smallest permission set that makes this safe?
```

If you cannot answer that, shrink the plugin boundary again.

## 13. Final Rule of Thumb

When you build a plugin, optimize for:

1. smaller,
2. safer,
3. easier to upgrade,
4. easier to separate from Core.

If a behavior can live in a plugin, keep it there.  
If it must stay in Core, let Core make the final decision and keep the implementation path as stable as possible.
