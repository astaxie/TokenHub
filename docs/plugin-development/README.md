# TokenHub Plugin Development

Language: English | [简体中文](../zh-CN/plugin-development/README.md) | [日本語](../ja/plugin-development/README.md)

This directory is the single documentation entry point for building TokenHub plugins. The executable SDK, contract harness, and reference packages live in [`plugin-devkit`](../../plugin-devkit/README.md); installed packages live in `TOKENHUB_PLUGIN_DIR`. The hosted marketplace is a separate distribution index, not this devkit.

## Start Here

| Goal | Document |
| --- | --- |
| Build and test the first package | [Getting Started](getting-started.md) |
| Understand `plugin.yaml` | [Manifest Reference](manifest-reference.md) |
| Connect an upstream model service | [Provider Plugins](provider-plugins.md) |
| Participate in the request pipeline | [Gateway Hooks](gateway-hooks.md) |
| Add themes, layouts, and declarative panels | [UI Templates](ui-templates.md) |
| Run scheduled or operational work | [Background Jobs](background-jobs.md) |
| Create a ZIP and publish a release | [Packaging and Release](packaging-and-release.md) |
| Read every contract and migration detail | [Complete Architecture and Development Guide](guide.md) |

## Directory Boundaries

```text
docs/plugin-development/     documentation and navigation
plugin-devkit/               SDK, contract harness, fixtures, examples
your-plugin-repository/      real plugin source and release workflow
TOKENHUB_PLUGIN_DIR/         packages installed into a TokenHub deployment
marketplace index            released versions, URLs, checksums and signatures
```

Use the devkit to develop a plugin. Do not run its examples as production integrations without replacing their fixture behavior and adding provider-specific tests.
