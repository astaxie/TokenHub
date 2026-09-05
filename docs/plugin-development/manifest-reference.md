# Plugin Manifest Reference

Language: English | [简体中文](../zh-CN/plugin-development/manifest-reference.md) | [日本語](../ja/plugin-development/manifest-reference.md)

Every package has one `plugin.yaml` at its package root. TokenHub validates it before registering or executing any capability.

| Field | Purpose |
| --- | --- |
| `schema_version` | Manifest schema version; Plugin API v1 uses `1` |
| `id` | Stable globally unique plugin identifier |
| `version` | Plugin package version |
| `tokenhub.plugin_api` | Runtime contract major, currently `v1` |
| `kinds` | `provider`, `extension`, `admin_ui`, or `sim` |
| `placement` | `gateway_chain`, `background`, `presentation`, or transitional `management_action` |
| `entry` | Backend command/protocol or frontend schema path |
| `capabilities` | Provider types, hooks, jobs, actions, or UI/template declarations |
| `permissions` | Least-privilege data read/write declaration |
| `distribution` | Repository, homepage, license, release, checksum, and signature metadata |

Capability IDs and the plugin ID are compatibility contracts. Do not rename them in an update unless the migration deliberately preserves existing Provider, route, resource, and configuration references.

Backend commands use package-relative paths and the `stdio-json-v1` protocol. Paths may not escape the package. Packages containing symlinks, ambiguous manifests, or incompatible declarations are rejected.

See the [complete guide](guide.md) for schemas, stage policies, and compatibility rules.
