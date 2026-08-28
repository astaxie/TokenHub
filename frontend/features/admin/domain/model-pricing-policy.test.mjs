import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  cacheReadPriceHelpText,
} = await importTypeScript(new URL("./model-pricing-policy.ts", import.meta.url));

test("cache-read price help describes metadata policy instead of provider names", () => {
  assert.match(cacheReadPriceHelpText, /cache_read_estimate_ratio/);
  assert.doesNotMatch(cacheReadPriceHelpText, /DeepSeek|Kimi|OpenAI|Anthropic|Codex/i);
});
