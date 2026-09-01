# TokenHub Plugin Architecture and Development Guide

Language: English | [简体中文](zh-CN/plugin-development.md) | [日本語](ja/plugin-development.md)

This guide describes the current TokenHub plugin direction and how to build against it. It is written for plugin authors, platform engineers, and operators.

TokenHub keeps the core small:

- the core owns auth, routing, billing, auditing, compatibility, and upgrade safety
- plugins own the parts that change more often
- built-in plugins and external plugins use the same contract
- UI templates, provider integrations, chain injection, background jobs, and admin UI contributions all come from explicit plugin metadata

## 1. Plugin Families

TokenHub now organizes plugins into a few clear families.

| Family | What it owns | Examples |
| --- | --- | --- |
| UI template | Whole-shell look, layout, and template package | shell theme, page template, dashboard composition |
| Provider | Upstream model access, auth, discovery, and quotas | Codex, Kimi, Gemini, Anthropic, OpenAI-compatible providers |
| Chain injection | Request-to-upstream pipeline behavior | privacy filters, routing, cache, context optimization, trace export |
| Background job | Scheduled or operator-triggered work | quota refresh, sync, cleanup, reporting |

Admin UI contributions are a capability surface, not a top-level family. They are usually attached to Provider, Chain injection, or Background job plugins.

The current repository still uses the internal kind name `sim` in compatibility payloads. In user-facing language, read that as `UI template`.

A single plugin may span more than one family, but each family should stay focused. For example:

- a Codex subscription plugin is a Provider plugin that also contributes admin UI, chain hooks, and background jobs
- a trace exporter is usually a Chain injection plugin with a narrow `observe_only` hook
- a quota sync worker is usually a Background job plugin
- a shell replacement is usually a UI template plugin

## 2. Manifest Contract

Every plugin package is described by `plugin.yaml`.

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

Important fields:

- `schema_version`: manifest schema version
- `tokenhub.plugin_api`: plugin API version
- `kinds`: one or more of `provider`, `admin_ui`, `sim`, `extension`
- `placement`: one or more of `presentation`, `gateway_chain`, `background`, `management_action`
- `capabilities`: the actual feature surface
- `permissions`: the least-privilege declaration surface
- `distribution`: repository URL, homepage URL, checksum, signature, and license metadata

`management_action` is a transitional surface for operator-only actions. New request-path behavior should move into `gateway_chain`, and recurring work should move into `background`.

## 3. Runtime Surfaces

TokenHub uses three core runtime surfaces, plus one transitional compatibility surface.

- `ServeProvider`
- `ServeGatewayHook`
- `ServeBackgroundJob`
- `ServeAction` for compatibility-only admin operations

### 3.1 Provider invocation

Provider plugins receive:

- operation
- provider projection
- resource projection
- provider model
- request payload
- credentials projection

This keeps provider plugins on projected data, not raw core internals.

### 3.2 Gateway hook invocation

Gateway hook plugins receive:

- request ID
- stage
- envelope
- optional stage data

This is the right surface for:

- privacy controls
- route candidate generation and ranking
- cache lookup and cache write
- context optimization
- request and response transforms
- trace export

### 3.3 Background job invocation

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

### 3.4 Admin UI contributions

Admin UI contributions are not a separate runtime surface. They are declarative entries that render into panels, tabs, cards, and setting sections, and they should still route execution through Core.

## 4. How to Build a Plugin

The safest workflow is:

1. Pick the family first.
2. Define the smallest useful capability set.
3. Write the manifest.
4. Implement the runtime handler or UI contribution.
5. Add contract tests.
6. Run the package locally.
7. Publish to the marketplace.
8. Install and verify inside TokenHub.

Before writing code, answer:

- Is this a Provider integration?
- Is this a UI template or admin UI contribution?
- Is this a chain-injection concern?
- Is this a Background job?
- Is this only a transitional admin action?

If you cannot answer that, the boundary is still too blurry.

### 4.1 Start small

Do not start with the full wish list.

- Provider plugin: start with one provider type and one resource or route contract
- Chain injection plugin: start with one hook stage
- Background job plugin: start with one job
- UI template plugin: start with one template, shell, or layout contribution

### 4.2 Implement the handler

Keep the handler small:

- parse the invocation
- perform the plugin's work
- return structured output
- avoid printing secrets

### 4.3 Add contract tests

Every plugin family should have contract tests.

Good tests check:

- manifest parsing
- capability completeness
- input/output shape
- secret redaction
- failure behavior

### 4.4 Run the local contract kit

The marketplace repo includes a local harness:

```bash
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package ./samples/provider-kimi-go
go run ./cmd/tokenhub-plugin-test hook --package ./samples/hook-trace-go
go run ./cmd/tokenhub-plugin-test background --package ./samples/background-heartbeat-go
```

Replace `--package` with your plugin directory.

## 5. Build Each Family

### 5.1 Provider plugins

Provider plugins connect TokenHub to a model service or subscription account.

They usually declare:

- `provider_types`
- `provider_resource_types`
- provider policies
- `provider.default_base_url`
- `provider.model_discovery`
- `provider.credentials_scope`

Common responsibilities:

- protocol translation
- model discovery
- quota or account sync
- credential refresh
- provider-specific route behavior
- provider-specific UI metadata

For subscription-style providers, keep quota refresh and account sync in background jobs when possible.

### 5.2 Chain injection plugins

Chain injection plugins shape the request path from the user token request to the upstream response.

Typical stages include:

- `decode_normalize`
- `admission`
- `privacy_pre`
- `guardrail_pre`
- `cache_lookup`
- `route_candidates`
- `route_rank`
- `provider_call`
- `guardrail_post`
- `usage_attribution`
- `cache_write`
- `settlement`
- `trace_export`

Typical policies:

- `fail_closed` for admission, privacy, guardrails, and routing
- `fail_open` for cache lookup and cache write
- `skip_route` for provider call wrappers
- `observe_only` for settlement and trace export

Good chain plugins are deterministic, narrow, and explicit about what they read and write.

### 5.3 UI template plugins

UI template plugins are for visual identity and layout.

Typical contributions:

- theme tokens
- shell layout presets
- navigation composition
- dashboard composition
- page templates

UI template plugins should only affect `presentation`.

If the change also needs backend behavior, it is no longer only a UI template plugin.

### 5.4 Background job plugins

Background job plugins handle recurring or operator-triggered work.

Typical features:

- quota refresh
- heartbeat
- sync
- cleanup
- reporting

Background job plugins should expose small inputs, predictable retries, and sanitized results.

### 5.5 Admin UI contributions

Admin UI contributions are the declarative panels, tabs, cards, and route sections used to surface plugin state and operator controls.

Rules:

- execution still passes through Core
- plugins must not bypass RBAC
- plugins must not directly use raw admin credentials
- plugin-managed actions should stay permission-scoped and auditable

## 6. Packaging and Distribution

TokenHub uses one package shape for built-in and external plugins.

Typical package contents:

- `plugin.yaml`
- one runtime entrypoint
- optional assets
- contract tests

Distribution metadata should include:

- repository URL
- homepage URL
- download URL
- checksum
- signature
- license
- compatibility metadata

The plugin marketplace URL defaults to `https://plugins.betokenhub.com`. Operators can install a package from that marketplace or from a direct ZIP URL, validate the checksum, and restart the backend to activate it.

## 7. Versioning and Compatibility

Treat versioning as three separate concerns:

| Version | Meaning |
| --- | --- |
| Core version | TokenHub product version |
| Plugin API version | Plugin protocol and envelope contract version |
| Plugin package version | The plugin package's own version |

Compatibility rules:

1. plugin API changes should be additive within a major version
2. manifest schema changes should stay forward compatible when possible
3. stage names should stay stable inside the same API major
4. envelope fields may be added, but existing semantics should not change silently
5. new sensitive permissions require re-approval
6. new placements or capabilities require Core validation
7. the `sim` compatibility alias may remain internally until the UI template rename is complete

The migration principle is simple:

- preserve old provider IDs
- preserve old routes
- preserve old resources and quotas
- preserve old admin payload aliases until the new contract is ready

## 8. Testing and Release Flow

Recommended order:

1. Local unit tests
2. Manifest parsing tests
3. Contract tests
4. Package-level tests
5. TokenHub integration tests
6. Marketplace and compatibility checks
7. Install and restart validation

What to emphasize by family:

- Provider plugin: route protocols, discovery, credentials projection, response shape, secret redaction
- Chain injection plugin: stage order, mutation limits, failure policy, retry and cancel behavior, permission enforcement
- UI template plugin: theme selection, layout selection, template rendering, dashboard composition
- Background job plugin: schedules, retry rules, concurrency, result sanitization
- Admin UI contribution: schema parsing, action binding, payload redaction, no arbitrary admin API calls

## 9. Migration From the Current Built-Ins

The practical migration order is:

1. Normalize current built-in descriptors and registries to the plugin worldview.
2. Move provider adapters, quota, OAuth, and model discovery behind provider plugins.
3. Split gateway enhancements into explicit chain hooks.
4. Turn recurring jobs into background plugins.
5. Turn admin pages, panels, and buttons into declarative contributions.
6. Keep the old action surface only as a compatibility bridge while the request path is extracted.
7. Expand the marketplace for external authors.

This approach keeps upgrades safe because each step can ship independently and be validated with contract tests.

## 10. A Simple Decision Tree

```text
Does it connect TokenHub to a model or subscription account?
  -> Provider plugin

Does it affect the path from user token request to provider response?
  -> Chain injection plugin

Does it only change admin pages, panels, cards, or shell appearance?
  -> Admin UI contribution or UI template plugin

Does it run on a schedule or after startup?
  -> Background job plugin

Does it expose an operator-triggered action?
  -> Transitional management_action capability only if it cannot yet move into a hook or job
```

Then ask:

```text
What is the smallest permission set that makes this safe?
```

If you cannot answer that, shrink the plugin boundary again.

## 11. Migration Checklist

- [ ] Map the current built-in modules to Provider, chain, UI template, and Background job packages.
- [ ] Extract provider-specific model discovery and quota logic into provider plugins.
- [ ] Move request-path logic into explicit chain hooks.
- [ ] Keep admin UI contributions declarative and permission-scoped.
- [ ] Remove the old action-runner surface from the main plugin manager.
- [ ] Keep `sim` as an internal compatibility alias until the rename is complete.
- [ ] Publish external plugin samples in the marketplace repo.
- [ ] Add contract tests for every plugin family before release.

## 12. Final Rule of Thumb

When you build a plugin, optimize for:

1. smaller
2. safer
3. easier to upgrade
4. easier to separate from Core

If a behavior can live in a plugin, keep it there.
If it must stay in Core, let Core make the final decision and keep the implementation path stable.
