# Packaging and Release

Language: English | [简体中文](../zh-CN/plugin-development/packaging-and-release.md) | [日本語](../ja/plugin-development/packaging-and-release.md)

A production plugin should live in its own source directory or repository. `plugin-devkit/examples/` contains fixtures for development and contract testing; it is neither an installation source nor the TokenHub marketplace.

## Build the Package

A release ZIP contains exactly one discoverable `plugin.yaml`, the runtime entrypoint when required, and optional package-relative schemas or assets. The manifest may be at the archive root or inside one top-level directory.

Do not include symlinks, credentials, `.env` files, local databases, logs, source-control metadata, or build caches. Keep `entry.backend.command` relative to the package root and preserve executable permissions.

Example:

```bash
cd plugin-devkit
go build -o examples/background-heartbeat-go/bin/background-heartbeat-go \
  ./examples/background-heartbeat-go
cd examples/background-heartbeat-go
zip -r ../../../background-heartbeat-go.zip plugin.yaml bin
cd ../../..
shasum -a 256 background-heartbeat-go.zip
```

Run the matching Devkit contract command against the package before creating the ZIP. Test the final archive through TokenHub as well, because archive layout and executable permissions are part of the release contract.

## Version and Publish

Use a new semantic plugin version for every changed artifact. Keep the plugin ID and established capability IDs stable. Record the required TokenHub and Plugin API compatibility, permission changes, checksum, release notes, and download URL with the release.

TokenHub's Marketplace is a remote HTTPS JSON index. It describes plugins and released versions and points to immutable ZIP artifacts. It is separate from `plugin-devkit` and from `TOKENHUB_PLUGIN_DIR`. A Marketplace record should provide at least an artifact URL and lowercase SHA-256 checksum; signed releases can also provide an Ed25519 signature URL and key ID.

Never reuse a release URL for different bytes. Checksums and signatures protect the exact archive, so rebuilding a published version requires a new version and artifact.

## Install and Verify

From **Plugin Management > Install Plugin**, operators can select a Marketplace release, provide a direct URL, or upload a ZIP. Review the compatibility, checksum, trust information, and permission diff before installation. Direct URL installation requires the package checksum.

TokenHub extracts accepted packages into `TOKENHUB_PLUGIN_DIR`. A package that adds or changes runtime capabilities can enter `pending_restart`; restart the backend before judging whether the new version is active. Then verify:

- version, compatibility, and trust state on the Details page
- file inventory and expected package contents on the Files page
- permissions, jobs, hooks, and UI declarations on the Settings page
- the real Provider or gateway behavior with a non-production credential first

For updates, repeat the same review and verification. Back up relevant TokenHub state before an update that changes persisted data, and keep the previous immutable ZIP available for a controlled rollback.
