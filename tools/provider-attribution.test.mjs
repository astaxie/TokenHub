import assert from "node:assert/strict";
import test from "node:test";

import { defaultProviderClaudeCodeAttributionPolicy } from "../frontend/features/admin/domain/provider-attribution.ts";

test("Claude Code attribution defaults come from provider metadata", () => {
  const providerTypeOptions = [
    { value: "anthropic", claudeCodeAttributionDefault: "preserve" },
    { value: "vendor_anthropic", claudeCodeAttributionDefault: "strip" },
  ];

  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "anthropic", providerTypeOptions), "preserve");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("vendor_anthropic", "custom", providerTypeOptions), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "anthropic"), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "custom"), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("openai_compatible", "requesty"), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("openai", "openai"), "strip");
});
