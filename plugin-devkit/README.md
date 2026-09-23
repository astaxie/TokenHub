# TokenHub Plugin Devkit

This directory contains contract development and local executable-test support for external TokenHub plugin packages. The examples use Plugin API v2 manifests; the Devkit also validates legacy Plugin API v1 manifests through the compatibility adapter. This is not the runtime plugin directory or the hosted plugin marketplace.

The current TokenHub runtime does not execute external package commands. The SDK, examples, and `tokenhub-plugin-test` define and verify the future `stdio-json-v1` contract locally; installing a command-bearing package in TokenHub leaves it inspectable with a `failed_startup` lifecycle state.

## Contents

- `sdk/go/tokenhubplugin/`: Go helpers for the `stdio-json-v1` process transport used by both manifest API versions.
- `cmd/tokenhub-plugin-test/`: local contract-test command.
- `contract-tests/`: protocol fixtures and contract coverage.
- `examples/`: reference packages for Providers, gateway hooks, background jobs, and transitional management actions.

The Go SDK import path is `github.com/astaxie/TokenHub/plugin-devkit/sdk/go/tokenhubplugin`. Pin a reviewed Devkit version or commit in production plugin repositories.

Start with the [Plugin Development documentation](../docs/plugin-development/README.md).

## Run the Devkit

```bash
cd plugin-devkit
go test ./...
go run ./cmd/tokenhub-plugin-test provider --package "$PWD/examples/provider-mock-go"
```

Copy an example into a separate plugin workspace, replace its identifiers and behavior, then run the contract command against that directory. Package the resulting `plugin.yaml`, executable, and resources as a ZIP before using TokenHub to validate package acceptance and inspect its contents.

TokenHub inspects installed packages from `TOKENHUB_PLUGIN_DIR` (`backend/data/plugins` in local development). Purely declarative presentation packages can be activated; packages with external backend commands fail closed before publishing runtime capabilities. A marketplace is a separate HTTPS JSON index that points to released packages, versions, checksums, signatures, and compatibility metadata.
