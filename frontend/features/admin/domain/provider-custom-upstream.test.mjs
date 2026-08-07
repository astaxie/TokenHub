import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  customUpstreamConnectionKey,
  customUpstreamDiscoveryPayload,
  customUpstreamModelsAreCurrent,
  customUpstreamModelsVisible,
  providerAnthropicAuthType,
} = await importTypeScript(new URL("./provider-custom-upstream.ts", import.meta.url));

const bearerAnthropicValues = {
  name: "Bearer Anthropic",
  type: "anthropic",
  base_url: "https://anthropic.example.test/v1",
  api_key: "test-api-key",
  anthropic_auth_type: "bearer",
};

test("create discovery sends the selected Anthropic auth mode", () => {
  assert.deepEqual(customUpstreamDiscoveryPayload(bearerAnthropicValues, "", "chat"), {
    provider_id: "",
    name: "Bearer Anthropic",
    type: "anthropic",
    base_url: "https://anthropic.example.test/v1",
    api_key: "test-api-key",
    anthropic_auth_type: "bearer",
    model_category: "chat",
  });
});

test("edit discovery sends the provider ID and selected Anthropic auth mode", () => {
  const values = { ...bearerAnthropicValues, api_key: "" };

  assert.deepEqual(customUpstreamDiscoveryPayload(values, "provider-anthropic", "all"), {
    provider_id: "provider-anthropic",
    name: "Bearer Anthropic",
    type: "anthropic",
    base_url: "https://anthropic.example.test/v1",
    api_key: "",
    anthropic_auth_type: "bearer",
    model_category: "all",
  });
});

test("Anthropic discovery defaults to x-api-key while other provider types omit the mode", () => {
  assert.equal(providerAnthropicAuthType({ type: "anthropic" }), "x-api-key");
  assert.equal(providerAnthropicAuthType({ type: "openai_compatible", anthropic_auth_type: "bearer" }), "");
});

test("changing the Anthropic auth selector invalidates the custom model cache key", () => {
  const apiKeyConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    anthropic_auth_type: "x-api-key",
  });
  const bearerConnection = customUpstreamConnectionKey(bearerAnthropicValues);

  assert.notEqual(apiKeyConnection, bearerConnection);
  assert.equal(customUpstreamModelsAreCurrent(1, apiKeyConnection, bearerConnection), false);
});

test("changing the provider type invalidates the custom model cache key", () => {
  const openAIConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    type: "openai_compatible",
  });
  const anthropicConnection = customUpstreamConnectionKey(bearerAnthropicValues);

  assert.notEqual(openAIConnection, anthropicConnection);
});

test("custom model discovery becomes visible in both create and edit flows", () => {
  assert.equal(customUpstreamModelsVisible("create", "connect", true, "models", 1), true);
  assert.equal(customUpstreamModelsVisible("edit", "models", false, "connect", 0), true);
  assert.equal(customUpstreamModelsVisible("edit", "advanced", false, "connect", 0), false);
});
