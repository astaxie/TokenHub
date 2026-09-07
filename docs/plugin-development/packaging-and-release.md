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

Run the matching Devkit contract command against the package before creating the ZIP. Install the final archive in TokenHub to test archive acceptance and file inspection, but use the Devkit to test executable behavior: the current TokenHub runtime does not launch external commands.

## Version and Publish

Use a new semantic plugin version for every changed artifact. Keep the plugin ID and established capability IDs stable. Record the required TokenHub and Plugin API compatibility, permission changes, checksum, release notes, and download URL with the release.

TokenHub's Marketplace is a remote HTTPS JSON index. It describes plugins and released versions and points to immutable ZIP artifacts. It is separate from `plugin-devkit` and from `TOKENHUB_PLUGIN_DIR`. A Marketplace record should provide at least an artifact URL and lowercase SHA-256 checksum; signed releases can also provide an Ed25519 signature URL and key ID.

Never reuse a release URL for different bytes. Checksums and signatures protect the exact archive, so rebuilding a published version requires a new version and artifact.

## Install and Inspect

From **Plugin Management > Browse Plugins**, operators can select an available release or use **Manual Install** with a direct URL or ZIP. Review compatibility, checksum, trust information, and the permission diff before installation. Direct URL installation requires the package checksum.

TokenHub extracts accepted packages into `TOKENHUB_PLUGIN_DIR` and reevaluates them after install, update, enable, disable, or rollback. Declarative presentation changes normally take effect without restarting the TokenHub service. A package with `entry.backend.command` cannot become operational in this release: an enable attempt records **Startup Failed**, keeps the package installed and inspectable, and registers none of its Provider, hook, job, or action capabilities. Lifecycle facts remain separate, so Installed must not be interpreted as executable or active. Then verify:

- version, compatibility, and trust state on the Details page
- file inventory and expected package contents on the Files page
- the declared jobs, hooks, actions, and permissions in `plugin.yaml`, plus any supported declarative UI contributions on the Details page
- executable behavior with the matching Devkit contract command; real Provider and gateway execution is unavailable through TokenHub

For updates, repeat the same review and verification. Back up relevant TokenHub state before an update that changes persisted data, and keep the previous immutable ZIP available for a controlled rollback.
