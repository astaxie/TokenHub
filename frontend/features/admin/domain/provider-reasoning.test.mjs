import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  providerTypeOptionsSupportAnthropicReasoning,
} = await importTypeScript(new URL("./provider-reasoning-policy.ts", import.meta.url));

test("provider reasoning compatibility does not infer support from provider type names", () => {
  assert.equal(providerTypeOptionsSupportAnthropicReasoning([], "openai_compatible"), false);
  assert.equal(providerTypeOptionsSupportAnthropicReasoning([{ value: "openai_compatible" }], "openai_compatible"), false);
  assert.equal(providerTypeOptionsSupportAnthropicReasoning([], "openai_codex"), false);
});

test("provider reasoning compatibility follows adapter route protocols when descriptors are loaded", () => {
  const providerTypes = [
    { value: "openai_compatible", supportsCustomHeaders: true, routeProtocols: ["chat/completions", "responses"] },
    { value: "codex_subscription", supportsCustomHeaders: false, routeProtocols: ["codex/responses", "responses"] },
    { value: "native_chat_plugin", supportsCustomHeaders: true, routeProtocols: ["chat/completions"] },
  ];

  assert.equal(providerTypeOptionsSupportAnthropicReasoning(providerTypes, "openai_compatible"), true);
  assert.equal(providerTypeOptionsSupportAnthropicReasoning(providerTypes, "codex_subscription"), false);
  assert.equal(providerTypeOptionsSupportAnthropicReasoning(providerTypes, "native_chat_plugin"), true);
});
