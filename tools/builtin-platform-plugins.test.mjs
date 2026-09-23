import assert from "node:assert/strict";
import { access, readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const packageRoot = path.join(root, "data", "builtin-plugins", "platform");
const expected = [
  "tokenhub.provider.mock", "tokenhub.provider.openai-compatible", "tokenhub.provider.openai-codex",
  "tokenhub.provider.azure-openai", "tokenhub.provider.qwen", "tokenhub.provider.local",
  "tokenhub.admin.plugin-ecosystem", "tokenhub.admin.core-provider",
  "tokenhub.sim.default", "tokenhub.sim.antd", "tokenhub.sim.knowledge-sidebar",
];

test("every non-catalog built-in plugin has an inspectable package", async () => {
  for (const id of expected) {
    const directory = path.join(packageRoot, id);
    await Promise.all(["plugin.yaml", "README.md", "LICENSE"].map((name) => access(path.join(directory, name))));
    const manifest = await readFile(path.join(directory, "plugin.yaml"), "utf8");
    assert.match(manifest, new RegExp(`^id: ${JSON.stringify(id).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`, "m"));
    assert.match(manifest, /^schema_version: 2$/m);
    assert.match(manifest, /^  plugin_api: v2$/m);
  }
});
