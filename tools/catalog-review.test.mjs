import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const catalog = JSON.parse(await readFile(new URL("../data/provider-catalog.json", import.meta.url), "utf8"));

test("reviewed model annotations and generated packages retain their contracts", async () => {
  for (const [providerID, provider] of Object.entries(catalog.providers)) {
    const reviewed = provider.models.filter(model => model.metadata?.catalog_reviewed_at);
    if (!reviewed.length) continue;
    const packaged = JSON.parse(await readFile(new URL(`../data/builtin-plugins/providers/${providerID}/catalog.json`, import.meta.url), "utf8"));
    assert.deepEqual(packaged.models, provider.models, `${providerID}: stale package`);
    const seen = new Set();
    for (const model of reviewed) {
      const label = `${providerID}/${model.id}`;
      assert.equal(seen.has(model.id), false, `${label}: duplicate reviewed ID`);
      seen.add(model.id);
      const metadata = model.metadata;
      for (const [key, value] of Object.entries(metadata)) assert.equal(typeof value, "string", `${label}: non-string ${key}`);
      assert.ok([metadata.upstream_source, metadata.lifecycle_source].some(source => /^https:\/\//.test(source || "")), `${label}: missing primary source`);
      assert.match(metadata.catalog_reviewed_at, /^\d{4}-\d{2}-\d{2}$/, `${label}: invalid review date`);
      if (metadata.shutdown_at) {
        assert.match(metadata.shutdown_at, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$/, `${label}: ambiguous shutdown timezone`);
        assert.ok(Number.isFinite(Date.parse(metadata.shutdown_at)), `${label}: invalid shutdown timestamp`);
      }
      if (metadata.pricing_status === "unverified") {
        assert.notEqual(metadata.retrieval_pricing_confirmed, "true", `${label}: unknown catalog price marked free`);
      }
    }
  }
});

test("subscription-only previews do not leak into ordinary API catalogs", () => {
  const id = "MiniMax-M3.1-Flash-Preview";
  assert.ok(catalog.providers["minimax-coding-plan"].models.some(model => model.id === id));
  for (const providerID of ["minimax", "minimax-cn"]) {
    assert.equal(catalog.providers[providerID].models.some(model => model.id === id), false);
  }
  assert.ok(catalog.providers.voyage.models.some(model => model.id === "rerank-3"));
});

test("new media offers remain catalog-only until their adapters are verified", () => {
  for (const [provider, id] of [
    ["openai", "gpt-image-2.5-sunburst"], ["openai", "gpt-live-1"],
    ["google", "gemini-nano-banana-2.1"], ["google", "gemini-3.8-live"],
    ["xai", "grok-imagine-video-1.5"], ["alibaba-cn", "qwen-image-2.1-pro"],
    ["stepfun-global", "stepaudio-3-tts"],
    ["amazon-bedrock", "amazon.nova-2-multimodal-embeddings-v1:0"],
  ]) {
    const model = catalog.providers[provider].models.find(model => model.id === id);
    assert.ok(model, `${provider}/${id}: missing`);
    assert.equal(model.metadata.call_support, "unsupported", `${provider}/${id}: unverified call support`);
  }
});
