# Getting Started With a TokenHub Plugin

Language: English | [简体中文](../zh-CN/plugin-development/getting-started.md) | [日本語](../ja/plugin-development/getting-started.md)

Use [`plugin-devkit`](../../plugin-devkit/README.md) to learn and verify Plugin API v1. Its `examples/` are fixtures, not production integrations.

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

## 4. Package and Install

Build the executable, create a ZIP containing exactly one `plugin.yaml`, calculate its SHA-256 checksum, and install it from TokenHub's **Install Plugin** page. TokenHub writes installed packages to `TOKENHUB_PLUGIN_DIR`; it never loads a package merely because it exists under `plugin-devkit/examples/`.

Continue with the [Manifest Reference](manifest-reference.md) and [Packaging and Release](packaging-and-release.md).
