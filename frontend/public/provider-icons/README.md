# Provider icons

These SVG and PNG assets are served locally. Provider cards use a brand asset when the catalog identity is known and fall back to the shared icon for custom or unknown providers. Regional subscription variants share their product icon.

## Sources and attribution

- The original catalog SVGs are vendored from the MIT-licensed `@lobehub/icons-static-svg@1.94.0` package, using pinned URLs under `https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/`.
- Additional product icons are copied unchanged from [Magpie](https://github.com/yetone/magpie), commit `d580537201938b9f510070a213e09b46479e53ad`, under `internal/gui/assets/icons/`. Its MIT license is preserved in [LICENSE-upstream-icons](LICENSE-upstream-icons).
- AutoClaw, CodeArts, Google Cloud AI, JoyCode, Kimi Work, and Raccoon icons are decoded from the `magpie.icon` data URIs in [community plugin manifests](https://github.com/magpie-community/plugins), commit `5c9b7a413756c2b4f139290047085075c83eb3e8`. Artwork is unchanged. The repository's MIT license is preserved in [LICENSE-community-icons](LICENSE-community-icons).
- The OpenDesign icon is copied from the [official favicon](https://open-design.ai/favicon.svg) referenced by its community plugin. The asset URL does not declare a separate license; it is not represented as MIT-licensed artwork.

[subscription-icons.provenance.json](subscription-icons.provenance.json) records the complete mapping for all 35 subscription and account provider identities, exact source paths, pinned revisions, and SHA-256 digests. Codex and Claude Code use the product icons declared by the upstream agent integrations. QwenWork uses the Qwen brand icon because its plugin does not declare a separate icon.

Provider names and logos remain trademarks of their respective owners. Repository licenses do not grant trademark rights.
