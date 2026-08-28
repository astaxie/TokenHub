import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  defaultProviderSystemPromptTransformPolicy,
} = await importTypeScript(new URL("./provider-attribution.ts", import.meta.url));

test("system prompt transform default follows provider metadata", () => {
  const providerTypeOptions = [
    { value: "anthropic", systemPromptTransformDefault: "preserve" },
    { value: "vendor_anthropic", systemPromptTransformDefault: "strip" },
  ];

  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "anthropic", providerTypeOptions), "preserve");
  assert.equal(defaultProviderSystemPromptTransformPolicy("vendor_anthropic", "custom", providerTypeOptions), "strip");
});

test("system prompt transform default falls back to legacy provider metadata", () => {
  const providerTypeOptions = [{ value: "anthropic", claudeCodeAttributionDefault: "preserve" }];

  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "anthropic", providerTypeOptions), "preserve");
});

test("system prompt transform default does not infer policy from provider type or catalog", () => {
  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "anthropic"), "strip");
  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "custom"), "strip");
  assert.equal(defaultProviderSystemPromptTransformPolicy("openai_compatible", "requesty"), "strip");
});

test("system prompt transform default ignores invalid provider metadata", () => {
  const providerTypeOptions = [{ value: "anthropic", systemPromptTransformDefault: "normalize" }];

  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "anthropic", providerTypeOptions), "strip");
});
