import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const toolsDir = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(toolsDir, "..");
const catalog = JSON.parse(await readFile(path.join(repositoryRoot, "data", "provider-catalog.json"), "utf8"));
const packagesRoot = path.join(repositoryRoot, "data", "builtin-plugins", "providers");

test("every Provider catalog entry has one real built-in plugin package", async () => {
  const catalogIDs = Object.keys(catalog.providers ?? {}).sort();
  const packageIDs = (await readdir(packagesRoot, { withFileTypes: true }))
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name)
    .sort();
  assert.equal(catalogIDs.length, 158);
  assert.deepEqual(packageIDs, catalogIDs);

  const pluginIDs = new Set();
  for (const catalogID of catalogIDs) {
    const directory = path.join(packagesRoot, catalogID);
    const [manifest, metadata, readme, license] = await Promise.all([
      readFile(path.join(directory, "plugin.yaml"), "utf8"),
      readFile(path.join(directory, "catalog.json"), "utf8"),
      readFile(path.join(directory, "README.md"), "utf8"),
      readFile(path.join(directory, "LICENSE"), "utf8"),
    ]);
    const pluginID = manifest.match(/^id: "([^"]+)"$/m)?.[1];
    assert.ok(pluginID, `${catalogID} has no plugin ID`);
    assert.equal(pluginIDs.has(pluginID), false, `duplicate plugin ID ${pluginID}`);
    pluginIDs.add(pluginID);
    assert.match(manifest, /^schema_version: 2$/m);
    assert.match(manifest, /^version: 1\.0\.0$/m);
    assert.match(manifest, /^category: provider_integration$/m);
    assert.match(manifest, /^host_adapter: "[^"]+"$/m);
    assert.equal(JSON.parse(metadata).id, catalogID);
    assert.match(readme, /Built-in Provider Integration/);
    assert.match(license, /Apache License/);
  }
});

test("templated Provider URLs remain catalog metadata instead of runtime defaults", async () => {
  for (const catalogID of ["cloudflare-workers-ai", "databricks", "neon", "snowflake-cortex"]) {
    const directory = path.join(packagesRoot, catalogID);
    const [manifest, metadata] = await Promise.all([
      readFile(path.join(directory, "plugin.yaml"), "utf8"),
      readFile(path.join(directory, "catalog.json"), "utf8"),
    ]);
    assert.doesNotMatch(manifest, /^    default_base_url:/m);
    const catalogEntry = JSON.parse(metadata);
    assert.match(catalogEntry.base_url || catalogEntry.api, /\$\{/);
  }
});

test("OpenAI Astra inventory stays synchronized with its built-in plugin", async () => {
  const directory = path.join(packagesRoot, "openai");
  const packaged = JSON.parse(await readFile(path.join(directory, "catalog.json"), "utf8"));
  assert.deepEqual(packaged.models, catalog.providers.openai.models);
  const manifest = await readFile(path.join(directory, "plugin.yaml"), "utf8");
  assert.equal(Number(manifest.match(/models_count: (\d+)/)?.[1]), packaged.models.length);
  const astra = packaged.models.find((model) => model.id === "gpt-6-astra");
  assert.ok(astra);
  assert.deepEqual(astra.reasoning_options[0].values, ["low", "medium", "high", "xhigh", "max"]);
  assert.deepEqual(astra.cost.tiers, [{ input: 20, output: 75, cache_read: 2, cache_write: 25, tier: { type: "context", size: 272000 } }]);
});
