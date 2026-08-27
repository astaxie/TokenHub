import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  defaultProviderClaudeCodeAttributionPolicy,
} = await importTypeScript(new URL("./provider-attribution.ts", import.meta.url));

test("Claude Code attribution default follows provider metadata", () => {
  const providerTypeOptions = [
    { value: "anthropic", claudeCodeAttributionDefault: "preserve" },
    { value: "vendor_anthropic", claudeCodeAttributionDefault: "strip" },
  ];

  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "anthropic", providerTypeOptions), "preserve");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("vendor_anthropic", "custom", providerTypeOptions), "strip");
});

test("Claude Code attribution default does not infer policy from provider type or catalog", () => {
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "anthropic"), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "custom"), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("openai_compatible", "requesty"), "strip");
});

test("Claude Code attribution default ignores invalid provider metadata", () => {
  const providerTypeOptions = [{ value: "anthropic", claudeCodeAttributionDefault: "normalize" }];

  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "anthropic", providerTypeOptions), "strip");
});
