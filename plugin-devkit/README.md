# TokenHub Plugin Devkit

This directory contains executable development support for external TokenHub plugins. It is not the runtime plugin directory and it is not the hosted plugin marketplace.

## Contents

- `sdk/go/tokenhubplugin/`: Go helpers for the `stdio-json-v1` protocol.
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

Copy an example into a separate plugin workspace, replace its identifiers and behavior, then run the contract command against that directory. Package the resulting `plugin.yaml`, executable, and resources as a ZIP before installing it through TokenHub.

TokenHub loads installed packages from `TOKENHUB_PLUGIN_DIR` (`backend/data/plugins` in local development). A marketplace is a separate HTTPS JSON index that points to released packages, versions, checksums, signatures, and compatibility metadata.
