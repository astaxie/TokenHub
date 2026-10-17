import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const { providers } = JSON.parse(await readFile(new URL("../data/provider-catalog.json", import.meta.url), "utf8"));
const model = (provider, id) => {
  const entry = providers[provider].models.find(item => item.id === id);
  assert.ok(entry, `${provider}/${id}: missing catalog entry`);
  return entry;
};

test("date-only retirement and plan redirection retain distinct scopes", () => {
  for (const provider of ["minimax", "minimax-cn"]) {
    const { metadata } = model(provider, "MiniMax-M2.7");
    assert.equal(metadata.shutdown_date, "2026-11-18");
    assert.equal(metadata.shutdown_at, undefined);
    assert.equal(metadata.new_user_restriction_date, "2026-10-19");
  }
  for (const provider of ["minimax-coding-plan", "minimax-cn-coding-plan"]) {
    const entry = model(provider, "MiniMax-M2.7");
    assert.equal(entry.metadata.lifecycle_status, "redirected");
    assert.equal(entry.metadata.redirect_date, "2026-10-19");
    assert.equal(entry.metadata.shutdown_date, undefined);
    assert.equal(entry.cost, undefined, "subscription quotas are not zero USD token rates");
  }
});

test("new lifecycle notices are exact offers rather than global model-family bans", () => {
  assert.equal(model("openai", "gpt-4.1-2025-04-14").metadata.shutdown_date, "2027-01-14");
  assert.equal(model("openai", "gpt-4o-2024-11-20").metadata.shutdown_at, undefined);
  for (const provider of ["jiekou", "openrouter"]) {
    const rows = providers[provider]?.models.filter(item => item.id.includes("gpt-4.1")) || [];
    for (const entry of rows) assert.notEqual(entry.metadata?.shutdown_date, "2027-01-14");
  }
  const bedrock = model("amazon-bedrock", "meta.llama3-1-70b-instruct-v1:0").metadata;
  assert.equal(bedrock.regional_lifecycle_status, "legacy");
  assert.notEqual(bedrock.lifecycle_status, "deprecated");
  assert.ok(JSON.parse(bedrock.lifecycle_regions).length > 0);
  const sonnet = model("amazon-bedrock", "anthropic.claude-sonnet-4-5-20250929-v1:0").metadata;
  assert.equal(sonnet.regional_legacy_date, "2026-10-08");
  assert.equal(sonnet.regional_eol_date, "2027-04-08");
  assert.equal(model("amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0").metadata?.regional_eol_date, undefined);
  const agent = model("perplexity-agent", "openai/gpt-5.4").metadata;
  assert.equal(agent.shutdown_at, "2026-10-24T00:00:00Z");
});

test("conditional and native-unit prices cannot become universal token rates", () => {
  const argon = model("google", "gemini-4-argon");
  assert.equal(argon.limit.output, 65536);
  assert.equal(argon.limit.context, 1048576);
  assert.equal(argon.cost, undefined);
  assert.equal(argon.metadata.pricing_status, "unverified");
  assert.equal(JSON.parse(argon.metadata.future_pricing).effective_from, "2026-12-01");
  const sol = model("openai", "gpt-6.1-sol");
  assert.equal(sol.cost.input, 2, "Ultrafast must not overwrite Standard reference pricing");
  assert.equal(JSON.parse(sol.metadata.ultrafast_reference_prices).short.input, 12);
  const voice = model("xai", "grok-voice-3.0");
  assert.equal(voice.metadata.billing_unit, "audio_minute,text_input_event");
  assert.equal(voice.cost, undefined);
  assert.equal(voice.metadata.call_support, "unsupported");
  assert.equal(model("anthropic", "claude-mythos-5-1").metadata.availability, "restricted");
});
