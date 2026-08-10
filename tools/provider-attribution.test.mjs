import assert from "node:assert/strict";
import test from "node:test";

import { defaultProviderClaudeCodeAttributionPolicy } from "../frontend/features/admin/domain/provider-attribution.ts";

test("Claude Code attribution defaults favor third-party caches", () => {
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "anthropic"), "preserve");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "custom"), "preserve");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", ""), "preserve");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("anthropic", "google-vertex-anthropic"), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("openai_compatible", "requesty"), "strip");
  assert.equal(defaultProviderClaudeCodeAttributionPolicy("openai", "openai"), "strip");
});
