import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

for (const [provider, model] of [["alibaba", "qwen3-rerank"], ["alibaba-cn", "gte-rerank-v2"]]) {
  test(`canonical ${provider} retrieval model matches the generated package`, async () => {
    const source = JSON.parse(await readFile(new URL("../data/provider-catalog.json", import.meta.url), "utf8"));
    const entry = source.providers[provider];
    const generated = JSON.parse(await readFile(new URL(`../data/builtin-plugins/providers/${provider}/catalog.json`, import.meta.url), "utf8"));
    const manifest = await readFile(new URL(`../data/builtin-plugins/providers/${provider}/plugin.yaml`, import.meta.url), "utf8");
    assert.ok(entry.models.some(item => item.id === model && item.type === "rerank"));
    assert.deepEqual(generated.models, entry.models);
    assert.match(manifest, new RegExp(`models_count: ${entry.models.length}\\b`));
  });
}
