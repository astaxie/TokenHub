import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  providerTypeSupportsReasoningConfig,
} = await importTypeScript(new URL("./provider-reasoning-policy.ts", import.meta.url));

test("provider reasoning compatibility does not infer support from provider type names", () => {
  assert.equal(providerTypeSupportsReasoningConfig([], "openai_compatible"), false);
  assert.equal(providerTypeSupportsReasoningConfig([{ value: "openai_compatible" }], "openai_compatible"), false);
  assert.equal(providerTypeSupportsReasoningConfig([], "openai_codex"), false);
});

test("provider reasoning compatibility uses route protocols as a legacy fallback", () => {
  const providerTypes = [
    { value: "openai_compatible", supportsCustomHeaders: true, routeProtocols: ["chat/completions", "responses"] },
    { value: "codex_subscription", supportsCustomHeaders: false, routeProtocols: ["codex/responses", "responses"] },
    { value: "native_chat_plugin", supportsCustomHeaders: true, routeProtocols: ["chat/completions"] },
  ];

  assert.equal(providerTypeSupportsReasoningConfig(providerTypes, "openai_compatible"), true);
  assert.equal(providerTypeSupportsReasoningConfig(providerTypes, "codex_subscription"), false);
  assert.equal(providerTypeSupportsReasoningConfig(providerTypes, "native_chat_plugin"), true);
});

test("provider reasoning compatibility follows explicit plugin policy first", () => {
  const providerTypes = [
    { value: "chat_disabled", routeProtocols: ["chat/completions"], reasoningConfigurable: false },
    { value: "native_reasoning", routeProtocols: ["responses"], reasoningConfigurable: true },
  ];

  assert.equal(providerTypeSupportsReasoningConfig(providerTypes, "chat_disabled"), false);
  assert.equal(providerTypeSupportsReasoningConfig(providerTypes, "native_reasoning"), true);
});
