# Getting Started With a TokenHub Plugin

Language: English | [简体中文](../zh-CN/plugin-development/getting-started.md) | [日本語](../ja/plugin-development/getting-started.md)

Use [`plugin-devkit`](../../plugin-devkit/README.md) to learn and verify Plugin API v2. The Devkit also recognizes v1 manifests for compatibility; its `examples/` are fixtures, not production integrations.

External command execution is not available in the current TokenHub runtime. The workflow below develops and tests the `stdio-json-v1` contract for a future runtime; installing a command-bearing package only validates, stores, and exposes it for inspection before recording a `failed_startup` lifecycle state.

## 1. Verify the Devkit

```bash
cd plugin-devkit
go test ./...
go run ./cmd/tokenhub-plugin-test provider \
  --package "$PWD/examples/provider-mock-go"
```

## 2. Start a Real Plugin

Create the real plugin in its own directory or repository. Copy the closest example, then change the plugin ID, capability IDs, implementation, fixtures, and distribution metadata together. Keep only the permissions the implementation actually needs.

For a Go plugin, initialize its own module and pin an approved Devkit version or commit:

```bash
go mod init example.com/your-plugin
go get github.com/astaxie/TokenHub/plugin-devkit@<approved-version>
```

Import the SDK as `github.com/astaxie/TokenHub/plugin-devkit/sdk/go/tokenhubplugin`.

A backend package normally contains:

```text
your-plugin/
├── plugin.yaml
├── go.mod              # Go module and pinned Devkit dependency
├── bin/your-plugin
├── ui/                 # optional schemas or assets
└── contract-tests/     # plugin-owned tests
```

## 3. Validate the Contract

Choose the command matching the capability:

```bash
go run ./cmd/tokenhub-plugin-test provider --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test hook --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test background --package /path/to/your-plugin
go run ./cmd/tokenhub-plugin-test action --package /path/to/your-plugin
```

## 4. Package and Inspect

Build the executable, create a ZIP containing exactly one `plugin.yaml`, and calculate its SHA-256 checksum. You may install it from **Plugin Management > Browse Plugins > Manual Install** to verify package acceptance and inspect its manifest and files. TokenHub writes accepted packages to `TOKENHUB_PLUGIN_DIR` and reloads lifecycle state after a successful operation; a package with `entry.backend.command` is shown as **Startup Failed** and none of its Provider, hook, job, or action capabilities are registered. Validate command behavior with the Devkit contract command, not through a live TokenHub Provider or gateway request. TokenHub never discovers a package merely because it exists under `plugin-devkit/examples/`.

Continue with the [Manifest Reference](manifest-reference.md) and [Packaging and Release](packaging-and-release.md).
