import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const { providers } = JSON.parse(await readFile(new URL("../data/provider-catalog.json", import.meta.url), "utf8"));
const offer = (provider, id) => {
  const model = providers[provider].models.find(item => item.id === id);
  assert.ok(model, `${provider}/${id}: missing offer`);
  return model;
};

test("Gemini 4 reference prices and the supported chat modalities stay provider scoped", () => {
  const neon = offer("google", "gemini-4-neon");
  const carbon = offer("google", "gemini-4-carbon");
  assert.deepEqual(neon.modalities.input, ["text", "image"]);
  assert.equal(carbon.metadata.max_output_tokens_non_streaming, "32768");
  assert.equal(carbon.limit.output, 65536);
  assert.equal(JSON.parse(carbon.metadata.future_pricing).effective_from, "2026-12-01");
  for (const model of [neon, carbon, offer("google", "gemini-4-hydrogen")]) {
    assert.equal(model.cost, undefined);
    assert.equal(model.metadata.pricing_status, "unverified");
  }
  const vertex = offer("google-vertex", "gemini-4-neon");
  assert.equal(vertex.metadata.availability, "restricted");
  assert.equal(vertex.metadata.call_support, "unsupported");
  assert.equal(JSON.parse(vertex.metadata.regional_reference_prices).non_global.input, 3.3);
});

test("native operations and subscription prices cannot become active token-priced chat offers", () => {
  for (const [provider, id] of [
    ["google", "gemini-decision-1"],
    ["google", "gemini-omni-1.2-flash"],
    ["google", "gemini-3.5-transcribe-live"],
    ["perplexity-agent", "google/gemini-4-hydrogen"],
    ["openai", "gpt-6.2-sol"],
    ["alibaba-cn", "pixverse/pixverse-v6-max-t2v"],
  ]) {
    const model = offer(provider, id);
    assert.equal(model.metadata.call_support, "unsupported");
    assert.equal(model.cost, undefined);
  }
  for (const provider of ["alibaba-token-plan-cn", "alibaba-token-plan"]) {
    const preview = offer(provider, "qwen4-max-preview");
    const alias = offer(provider, "qwen4.0-max-preview");
    const ga = offer(provider, "qwen4-max");
    assert.equal(preview.metadata.billing_unit, "credits");
    assert.equal(preview.metadata.lifecycle_status, "deprecated");
    assert.equal(preview.metadata.shutdown_at, "2026-11-26T00:00:00+08:00");
    assert.equal(alias.metadata.shutdown_at, preview.metadata.shutdown_at);
    assert.equal(ga.metadata.shutdown_at, undefined);
    assert.equal(ga.cost, undefined);
  }
});
