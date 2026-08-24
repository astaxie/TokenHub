import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  customUpstreamConnectionKey,
  customUpstreamDiscoveryPayload,
  customUpstreamModelsAreCurrent,
  customUpstreamModelsVisible,
  providerAnthropicAuthType,
  providerConnectionTestRunAfterUpdate,
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

test("discovery includes custom header payloads", () => {
  const headers = {
    headers: { "X-Tenant": "tenant-one" },
    sensitive_headers: ["X-Tenant"],
  };

  assert.deepEqual(customUpstreamDiscoveryPayload(bearerAnthropicValues, "", "chat", headers), {
    provider_id: "",
    name: "Bearer Anthropic",
    type: "anthropic",
    base_url: "https://anthropic.example.test/v1",
    api_key: "test-api-key",
    ...headers,
    anthropic_auth_type: "bearer",
    model_category: "chat",
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

test("changing custom headers invalidates the custom model cache key", () => {
  const firstConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    custom_headers: `[{"name":"X-Tenant","value":"one"}]`,
  });
  const secondConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    custom_headers: `[{"name":"X-Tenant","value":"two"}]`,
  });

  assert.notEqual(firstConnection, secondConnection);
});

test("connection inputs invalidate completed and in-flight connection tests", () => {
  const testedRun = 7;

  for (const key of ["base_url", "api_key", "type", "anthropic_auth_type", "custom_headers"]) {
    assert.equal(providerConnectionTestRunAfterUpdate(testedRun, key), testedRun + 1, key);
  }
  for (const key of ["name", "priority"]) {
    assert.equal(providerConnectionTestRunAfterUpdate(testedRun, key), testedRun, key);
  }
});

test("custom model discovery becomes visible in both create and edit flows", () => {
  assert.equal(customUpstreamModelsVisible("create", "connect", true, "models", 1), true);
  assert.equal(customUpstreamModelsVisible("edit", "models", false, "connect", 0), true);
  assert.equal(customUpstreamModelsVisible("edit", "advanced", false, "connect", 0), false);
});
