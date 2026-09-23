# Plugin Manifest Reference

Language: English | [简体中文](../zh-CN/plugin-development/manifest-reference.md) | [日本語](../ja/plugin-development/manifest-reference.md)

Every package has one `plugin.yaml` at its package root. TokenHub validates it before registering or executing any capability.

| Field | Purpose |
| --- | --- |
| `schema_version` | Manifest schema version; Plugin API v2 uses `2` and the v1 adapter accepts `1` |
| `id` | Stable globally unique plugin identifier |
| `version` | Plugin package version |
| `summary` | Required plain-language value statement for v2 |
| `category` | One primary family: `provider_integration`, `request_pipeline`, `ui_template`, or `automation` |
| `tokenhub.plugin_api` | Manifest/runtime contract major; current is `v2`, with `v1` supported by an adapter |
| `dependencies` | Optional plugin ID and version constraints |
| `settings.scopes` | Declares real editable settings; omit it when the plugin has no settings |
| `kinds` | `provider`, `extension`, `admin_ui`, or `sim` |
| `placement` | `gateway_chain`, `background`, `presentation`, or transitional `management_action` |
| `entry` | Backend command/protocol or frontend schema path |
| `capabilities` | Provider types, hooks, jobs, actions, or UI/template declarations |
| `permissions` | Least-privilege data read/write declaration |
| `distribution` | Repository, homepage, license, release, checksum, and signature metadata |

Capability IDs and the plugin ID are compatibility contracts. Do not rename them in an update unless the migration deliberately preserves existing Provider, route, resource, and configuration references.

Package-relative paths and the `stdio-json-v1` transport define the external backend command contract. The transport name is independent of Plugin API v2. Paths may not escape the package, and packages containing symlinks, ambiguous manifests, or incompatible declarations are rejected. The current runtime cannot enforce the required host-level process, network, and resource isolation, so it fails closed and rejects every runtime-loaded external command before launch. This restriction does not affect in-process built-ins or declarative presentation contributions.

See the [complete guide](guide.md) for schemas, stage policies, and compatibility rules.
