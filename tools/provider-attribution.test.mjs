import assert from "node:assert/strict";
import test from "node:test";

import { defaultProviderSystemPromptTransformPolicy } from "../frontend/features/admin/domain/provider-attribution.ts";

test("system prompt transform defaults come from provider metadata", () => {
  const providerTypeOptions = [
    { value: "anthropic", systemPromptTransformDefault: "preserve" },
    { value: "vendor_anthropic", systemPromptTransformDefault: "strip" },
    { value: "legacy_anthropic", claudeCodeAttributionDefault: "preserve" },
  ];

  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "anthropic", providerTypeOptions), "preserve");
  assert.equal(defaultProviderSystemPromptTransformPolicy("vendor_anthropic", "custom", providerTypeOptions), "strip");
  assert.equal(defaultProviderSystemPromptTransformPolicy("legacy_anthropic", "custom", providerTypeOptions), "preserve");
  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "anthropic"), "strip");
  assert.equal(defaultProviderSystemPromptTransformPolicy("anthropic", "custom"), "strip");
  assert.equal(defaultProviderSystemPromptTransformPolicy("openai_compatible", "requesty"), "strip");
  assert.equal(defaultProviderSystemPromptTransformPolicy("openai", "openai"), "strip");
});
